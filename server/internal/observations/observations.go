// Package observations computes PRD §11's 「관찰」 table for one workspace
// (openapi getWorkspaceObservations, S14 「대시보드」 below the ten indicators,
// K-18). It is `metrics` for the five rows that carry no target: a
// distribution is shown, nothing is judged.
//
// The rules are metrics' rules. A row with no sample is `n: 0` with every
// number null — never 0, which would look like a measurement. Each row is one
// SQL statement (the depth row loads hops and reuses the router's own depth
// reading, see chainDepth) that returns its numbers; `Defs` carries the
// label and the definition sentence (`note`) in the screens' language
// (COMPONENTS §8.4; the web mock reads this table from the source, the same
// way it reads metrics.Defs).
//
// Definitions are the openapi description's, item for item. Where it names a
// column (`session_hop.rule`, `lane.delegated_from_task_id`, the FR-7.2
// `empty_turn` row) the SQL below is the reading of it, and the `note` says
// the same thing in the user's words.
package observations

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Row is one line of the §11 observation table (openapi ObservationRow).
// A distribution row fills Median · P95; a ratio row fills Value.
type Row struct {
	Key       string
	Label     string
	Note      string
	N         int
	Value     *float64
	Median    *float64
	P95       *float64
	Breakdown []Breakdown // routing_concentration only
}

// Breakdown is one routing rule's share of the hops that made a task.
type Breakdown struct {
	Kind  string // "1".."8" or "platform"
	Share float64
	N     int
}

// Def is the fixed half of a row: everything but the numbers.
type Def struct {
	Key, Label, Note string
}

// Defs is PRD §11's 관찰 table in row order. Label and Note are what the
// dashboard shows (wording lock sinkVars: Korean, §8.4 words, no internal
// nouns). The label is the PRD row's own name.
var Defs = []Def{
	{Key: "chain_scale",
		Label: "트리거 사슬 규모",
		Note:  "사람이 쓴 메시지 하나가 다음 사람 메시지 전까지 에이전트 사이에 일으킨 할 일 수, 그 중앙값과 상위 5% 값. 표본 수는 에이전트를 깨운 사람 메시지 수."},
	{Key: "chain_depth",
		Label: "트리거 사슬 깊이",
		Note:  "세션 안에서 사람의 메시지에서 시작해 에이전트 사이의 멘션이 이어진 가장 깊은 단계, 그 중앙값과 상위 5% 값. 표본 수는 세션 수."},
	{Key: "join_breadth",
		Label: "합류 폭",
		Note:  "한 할 일이 위임으로 만든 작업 줄기의 수(합류 그룹의 크기), 그 중앙값과 상위 5% 값. 표본 수는 위임 그룹 수."},
	{Key: "routing_concentration",
		Label: "라우팅 집중",
		Note:  "할 일이 어느 라우팅 규칙으로 만들어졌는지의 비율. 값은 멘션 없이 담당 에이전트에게 간 비율(규칙 6·7). 표본 수는 할 일을 만든 트리거 수."},
	{Key: "empty_turn_rate",
		Label: "빈 턴 비율",
		Note:  "메시지 게시·플랫폼 조작·파일 편집이 하나도 없이 끝난 실행 ÷ 완료된 실행 전체. 표본 수는 완료된 실행 수."},
}

// DefaultWindow is openapi getWorkspaceObservations `window` default.
const DefaultWindow = "P30D"

// ruleKinds is the breakdown's fixed shape: FR-3.3's eight rule numbers and
// the platform's own triggers (router.RulePlatform), always all nine, so the
// dashboard draws the same columns whether or not a rule fired.
var ruleKinds = []string{"1", "2", "3", "4", "5", "6", "7", "8", "platform"}

// EmptyTurnObjectRef is the object_ref of the FR-7.2 row tasks.Finish writes
// for a turn that did nothing; empty_turn_rate counts exactly that row.
const EmptyTurnObjectRef = tasks.EmptyTurnObjectRef

// Compute returns the five rows for wsID over [now-window, now], in §11 order.
func Compute(ctx context.Context, q db.DBTX, wsID uuid.UUID, window time.Duration, now time.Time) ([]Row, error) {
	since := now.Add(-window)
	out := make([]Row, 0, len(Defs))
	for _, d := range Defs {
		r := Row{Key: d.Key, Label: d.Label, Note: d.Note}
		var err error
		switch d.Key {
		case "chain_scale":
			err = distribution(ctx, q, sqlChainScale, wsID, since, &r)
		case "chain_depth":
			err = chainDepth(ctx, q, wsID, since, &r)
		case "join_breadth":
			err = distribution(ctx, q, sqlJoinBreadth, wsID, since, &r)
		case "routing_concentration":
			err = routingConcentration(ctx, q, wsID, since, &r)
		case "empty_turn_rate":
			err = ratio(ctx, q, sqlEmptyTurnRate, wsID, since, &r)
		}
		if err != nil {
			return nil, fmt.Errorf("observations: %s: %w", d.Key, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// distribution runs one (median, p95, n) statement. n = 0 forces both to
// null whatever the aggregate returned.
func distribution(ctx context.Context, q db.DBTX, sql string, wsID uuid.UUID, since time.Time, r *Row) error {
	var median, p95 *float64
	var n int
	if err := q.QueryRow(ctx, sql, wsID, since).Scan(&median, &p95, &n); err != nil {
		return err
	}
	if n == 0 {
		median, p95 = nil, nil
	}
	r.Median, r.P95, r.N = median, p95, n
	return nil
}

// ratio runs one (value, n) statement, metrics.valueN's shape.
func ratio(ctx context.Context, q db.DBTX, sql string, wsID uuid.UUID, since time.Time, r *Row) error {
	var v *float64
	var n int
	if err := q.QueryRow(ctx, sql, wsID, since).Scan(&v, &n); err != nil {
		return err
	}
	if n == 0 {
		v = nil
	}
	r.Value, r.N = v, n
	return nil
}

// 1. 사람 hop(from_agent_id IS NULL) 하나가 다음 사람 hop 전까지 만든 에이전트 hop
// 수(allowed = task 가 생겼다). "파생" 이므로 사람의 멘션이 직접 만든 task 는 세지
// 않는다 — 사람 hop 은 HITL 답·재개(RecordHumanHop)로도 생기고 그때는 task 를
// 만들지 않아서, 포함하면 그 경우가 1 로 부풀고 제외하면 직접 멘션이 0 이 된다.
// 둘 중 "사람 한 마디가 얼마나 많은 턴을 부르는가" 를 재는 쪽은 뒤따른 수다.
// 창은 사람 hop 의 시각. n = 사람 hop 수.
const sqlChainScale = `
WITH hops AS (
	SELECT h.id, h.session_id, h.from_agent_id, h.created_at
	FROM session_hop h JOIN room s ON s.id = h.session_id
	WHERE s.workspace_id = $1 AND h.allowed),
human AS (
	SELECT id, session_id, created_at, lead(id) OVER (PARTITION BY session_id ORDER BY id) AS next_id
	FROM hops WHERE from_agent_id IS NULL),
scale AS (
	SELECT (SELECT count(*) FROM hops a
	        WHERE a.session_id = hm.session_id AND a.from_agent_id IS NOT NULL
	          AND a.id > hm.id AND (hm.next_id IS NULL OR a.id < hm.next_id)) AS derived
	FROM human hm WHERE hm.created_at >= $2)
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY derived),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY derived), count(*)
FROM scale`

// 3. 합류 그룹 = 같은 delegated_from_task_id 에서 나온 자식 lane 집합(0006). 그
// 크기의 분포. 창은 lane 생성 시각. n = 그룹 수.
const sqlJoinBreadth = `
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY g.n),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY g.n), count(*)
FROM (SELECT count(*) AS n
      FROM lane l JOIN room s ON s.id = l.session_id
      WHERE s.workspace_id = $1 AND l.delegated_from_task_id IS NOT NULL AND l.created_at >= $2
      GROUP BY l.delegated_from_task_id) g`

// 5. completed attempt 중 FR-7.2 빈 턴 행(status/turn_end/"empty_turn", tasks.Finish
// 가 쓴다)이 있는 비율. 창은 attempt 종료 시각. n = completed attempt 수.
const sqlEmptyTurnRate = `
SELECT avg(CASE WHEN EXISTS (
	SELECT 1 FROM task_event e
	WHERE e.task_id = a.task_id AND e.attempt = a.attempt
	  AND e.class = 'status' AND e.verb = 'turn_end' AND e.object_ref = to_jsonb('` + EmptyTurnObjectRef + `'::text))
	THEN 1 ELSE 0 END), count(*)
FROM task_attempt a JOIN task t ON t.id = a.task_id JOIN room s ON s.id = t.session_id
WHERE s.workspace_id = $1 AND a.outcome = 'completed' AND a.finished_at >= $2`

// 2. 세션이 도달한 최대 chain_depth. 깊이의 규칙은 router.chainDepth 하나뿐이라
// (S-78: 원인 hop 을 따라가는 인과 깊이, 형제 위임은 같은 깊이) 여기서 SQL 로
// 다시 쓰지 않고 그 세션의 hop 전부를 router.MaxChainDepth 에 넣는다. 창은
// "그 창 안에 hop 이 하나라도 있는 세션"; 깊이는 세션 전체 이력으로 센다(창
// 밖의 사람 hop 이 뿌리일 수 있다). n = 세션 수.
func chainDepth(ctx context.Context, q db.DBTX, wsID uuid.UUID, since time.Time, r *Row) error {
	rows, err := q.Query(ctx, `
		SELECT h.session_id, h.id, h.from_agent_id, h.to_agent_id, h.created_at, COALESCE(h.cause_hop_id, 0)
		FROM session_hop h JOIN room s ON s.id = h.session_id
		WHERE s.workspace_id = $1
		  AND h.session_id IN (SELECT h2.session_id FROM session_hop h2 JOIN room s2 ON s2.id = h2.session_id
		                       WHERE s2.workspace_id = $1 AND h2.created_at >= $2)
		ORDER BY h.session_id, h.id`, wsID, since)
	if err != nil {
		return err
	}
	defer rows.Close()
	bySession := map[uuid.UUID][]router.Hop{}
	var order []uuid.UUID
	for rows.Next() {
		var sid uuid.UUID
		var h router.Hop
		var from *uuid.UUID
		if err := rows.Scan(&sid, &h.ID, &from, &h.ToAgent, &h.At, &h.CauseID); err != nil {
			return err
		}
		if from != nil {
			h.FromAgent = *from
		}
		if _, seen := bySession[sid]; !seen {
			order = append(order, sid)
		}
		bySession[sid] = append(bySession[sid], h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	depths := make([]float64, 0, len(order))
	for _, sid := range order {
		depths = append(depths, float64(router.MaxChainDepth(bySession[sid])))
	}
	r.N = len(depths)
	if r.N > 0 {
		m, p := percentile(depths, 0.5), percentile(depths, 0.95)
		r.Median, r.P95 = &m, &p
	}
	return nil
}

// percentile is Postgres percentile_cont: linear interpolation between the
// two nearest ranks, so the Go-computed row reads like the SQL ones.
func percentile(xs []float64, p float64) float64 {
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// 4. task 를 만든 hop(allowed)의 rule 분포. breakdown 은 아홉 종류를 항상 싣고
// (규칙 1~8 + platform = router.RulePlatform), value 는 규칙 6·7 — 멘션 없이
// assignee 에게 간 폴백 — 의 비율. 창은 hop 시각. n = hop 수.
func routingConcentration(ctx context.Context, q db.DBTX, wsID uuid.UUID, since time.Time, r *Row) error {
	rows, err := q.Query(ctx, `
		SELECT h.rule, count(*)
		FROM session_hop h JOIN room s ON s.id = h.session_id
		WHERE s.workspace_id = $1 AND h.allowed AND h.created_at >= $2
		GROUP BY h.rule`, wsID, since)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var rule, n int
		if err := rows.Scan(&rule, &n); err != nil {
			return err
		}
		kind := "platform"
		if rule >= 1 && rule <= 8 {
			kind = strconv.Itoa(rule)
		}
		counts[kind] += n
		total += n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.N = total
	for _, kind := range ruleKinds {
		b := Breakdown{Kind: kind, N: counts[kind]}
		if total > 0 {
			b.Share = float64(counts[kind]) / float64(total)
		}
		r.Breakdown = append(r.Breakdown, b)
	}
	if total > 0 {
		v := float64(counts["6"]+counts["7"]) / float64(total)
		r.Value = &v
	}
	return nil
}
