// Package metrics computes PRD §11's ten success indicators for one workspace
// (openapi getWorkspaceMetrics, S14 「대시보드」, G9).
//
// The rule that shapes everything here: a metric with no sample is
// `value: null, n: 0`, never 0 — the dashboard shows "아직 잴 수 없음" and a zero
// must not look like a measurement. Each metric is one SQL statement that
// returns (value, n); the table `Defs` carries the label, target and the
// definition sentence (`note`) in the screens' language (COMPONENTS §8.4).
//
// Definitions are the openapi description's, item for item; where the
// contract names a column (`hitl_request.answered_at`, `posted_message_ids`)
// the SQL below is the reading of it, and the `note` says the same thing in
// the user's words.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Metric is one row of the §11 table (openapi Metric).
type Metric struct {
	Key       string
	Label     string
	Unit      string // minutes | ratio | count
	Value     *float64
	Target    float64
	TargetOp  string // lt | gt
	N         int
	Note      string
	Breakdown []Breakdown // task_success_rate_by_runtime only
}

// Breakdown is one runtime kind's success rate.
type Breakdown struct {
	Kind   string
	Value  *float64
	Target float64
	N      int
}

// Def is the fixed half of a metric: everything but the numbers.
type Def struct {
	Key, Label, Unit, TargetOp, Note string
	Target                           float64
}

// Defs is PRD §11 in column order. Label and Note are what the dashboard shows
// (wording lock sinkVars: no internal nouns, Korean, §8.4 table).
var Defs = []Def{
	{Key: "f1_minutes", Unit: "minutes", Target: 15, TargetOp: "lt",
		Label: "컴퓨터 연결부터 첫 세션 완료까지 걸린 시간",
		Note:  "사용자별로 첫 컴퓨터가 연결된 시각부터 그 사용자가 Director 인 첫 완료 세션이 끝난 시각까지, 그 중앙값(분). 표본 수는 그런 사용자 수."},
	{Key: "auto_complete_rate", Unit: "ratio", Target: 0.6, TargetOp: "gt",
		Label: "세션이 저절로 끝난 비율",
		Note:  "완료된 세션 중 Director 가 직접 끝내지 않고 종료 조건으로 끝난 비율."},
	{Key: "hitl_response_minutes", Unit: "minutes", Target: 30, TargetOp: "lt",
		Label: "확인 요청에 사람이 답하기까지 걸린 시간",
		Note:  "확인 요청이 만들어진 때부터 사람이 답한 때까지, 그 중앙값(분). 기한이 지나 자동으로 진행된 요청은 빼고 센다."},
	{Key: "delegation_autonomous_rate", Unit: "ratio", Target: 0.7, TargetOp: "gt",
		Label: "에이전트 사이의 위임이 사람 개입 없이 처리된 비율",
		Note:  "다른 에이전트가 넘긴 할 일 중 확인 요청도 막힘도 없이 완료된 비율."},
	{Key: "parallel_wallclock_reduction", Unit: "ratio", Target: 0.4, TargetOp: "gt",
		Label: "여러 작업 줄기를 함께 돌려 줄어든 시간의 비율",
		Note:  "작업 줄기가 둘 이상인 완료 세션에서, 세션 시작부터 완료까지 걸린 시간이 각 할 일에 걸린 시간의 합보다 얼마나 짧았는지(1 - 전체 시간 ÷ 합)의 평균. 표본 수는 세션 수."},
	{Key: "task_success_rate_by_runtime", Unit: "ratio", Target: 0.85, TargetOp: "gt",
		Label: "컴퓨터 종류별 할 일 성공률",
		Note:  "컴퓨터 종류별로 완료된 할 일 ÷ (완료 + 실패). 종류별 값과 목표는 따로 나눠 준다."},
	{Key: "duplicate_after_resume_rate", Unit: "ratio", Target: 0.01, TargetOp: "lt",
		Label: "다시 이어 간 뒤 같은 메시지를 두 번 올린 비율",
		Note:  "두 번째 이상 실행된 할 일 중 같은 내용의 메시지가 두 번 올라간 것이 관측된 비율."},
	{Key: "resume_success_rate", Unit: "ratio", Target: 0.9, TargetOp: "gt",
		Label: "다시 이어 갈 때 이전 대화를 그대로 이어받은 비율",
		Note:  "이전 대화를 이어받으려 한 실행 중 실제로 이어받은(처음부터 다시 시작하지 않은) 비율."},
	{Key: "blocked_response_minutes", Unit: "minutes", Target: 5, TargetOp: "lt",
		Label: "막힌 질문에 답이 닿기까지 걸린 시간",
		Note:  "작업 줄기가 막혀 질문을 올린 때부터 그 질문에 첫 답글이 달린 때까지, 그 중앙값(분)."},
	{Key: "weekly_active_sessions", Unit: "count", Target: 5, TargetOp: "gt",
		Label: "이번 주에 움직인 세션 수",
		Note:  "최근 7일 안에 할 일이 하나라도 돌아간 세션 수. 표본 수는 이 워크스페이스에서 할 일을 돌린 적 있는 세션 수."},
}

// runtimeTargets is §11 row 6: "Claude Code > 95%, 기타 > 85%".
var runtimeTargets = map[contracts.RuntimeKind]float64{
	contracts.RuntimeClaudeCode: 0.95,
	contracts.RuntimeHermes:     0.85,
}

// DefaultWindow is openapi getWorkspaceMetrics `window` default.
const DefaultWindow = "P30D"

var isoDuration = regexp.MustCompile(`^P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// ErrBadWindow — the `window` parameter is not an ISO 8601 duration.
var ErrBadWindow = errors.New("metrics: window is not an ISO 8601 duration")

// ParseWindow reads an ISO 8601 duration (P30D · P2W · PT12H · P1M …). Months
// are 30 days and years 365: an aggregation window, not a calendar.
func ParseWindow(s string) (time.Duration, error) {
	if s == "" {
		s = DefaultWindow
	}
	m := isoDuration.FindStringSubmatch(s)
	if m == nil || s == "P" || s == "PT" {
		return 0, ErrBadWindow
	}
	n := func(i int) time.Duration {
		if m[i] == "" {
			return 0
		}
		v, _ := strconv.Atoi(m[i])
		return time.Duration(v)
	}
	d := n(1)*365*24*time.Hour + n(2)*30*24*time.Hour + n(3)*7*24*time.Hour + n(4)*24*time.Hour +
		n(5)*time.Hour + n(6)*time.Minute + n(7)*time.Second
	if d <= 0 {
		return 0, ErrBadWindow
	}
	return d, nil
}

// Compute returns the ten metrics for wsID over [now-window, now], in §11
// order. Row 10 ignores the window (last 7 days, by contract).
func Compute(ctx context.Context, q db.DBTX, wsID uuid.UUID, window time.Duration, now time.Time) ([]Metric, error) {
	since := now.Add(-window)
	out := make([]Metric, 0, len(Defs))
	for _, d := range Defs {
		m := Metric{Key: d.Key, Label: d.Label, Unit: d.Unit, Target: d.Target, TargetOp: d.TargetOp, Note: d.Note}
		var err error
		switch d.Key {
		case "task_success_rate_by_runtime":
			err = successByRuntime(ctx, q, wsID, since, &m)
		case "weekly_active_sessions":
			err = weeklyActive(ctx, q, wsID, now, &m)
		default:
			var sql string
			switch d.Key {
			case "f1_minutes":
				sql = sqlF1
			case "auto_complete_rate":
				sql = sqlAutoComplete
			case "hitl_response_minutes":
				sql = sqlHitlResponse
			case "delegation_autonomous_rate":
				sql = sqlDelegationAutonomous
			case "parallel_wallclock_reduction":
				sql = sqlParallelReduction
			case "duplicate_after_resume_rate":
				sql = sqlDuplicateAfterResume
			case "resume_success_rate":
				sql = sqlResumeSuccess
			case "blocked_response_minutes":
				sql = sqlBlockedResponse
			}
			err = valueN(ctx, q, sql, wsID, since, &m)
		}
		if err != nil {
			return nil, fmt.Errorf("metrics: %s: %w", d.Key, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// valueN runs one (value, n) statement. n = 0 forces value to null whatever
// the aggregate returned (an avg over zero rows is NULL already; a count-based
// value would be 0, which is the thing this package refuses to show).
func valueN(ctx context.Context, q db.DBTX, sql string, wsID uuid.UUID, since time.Time, m *Metric) error {
	var v *float64
	var n int
	if err := q.QueryRow(ctx, sql, wsID, since).Scan(&v, &n); err != nil {
		return err
	}
	if n == 0 {
		v = nil
	}
	m.Value, m.N = v, n
	return nil
}

// 1. 사용자별 첫 컴퓨터 연결(runtime_pairing.ready_at = probe 도착) → 그 사용자가
// Director 인 첫 completed 세션의 finished_at. 중앙값(분). n = 사용자 수.
const sqlF1 = `
WITH first_rt AS (
	SELECT created_by AS user_id, min(ready_at) AS online_at
	FROM runtime_pairing WHERE workspace_id = $1 AND ready_at IS NOT NULL GROUP BY created_by),
first_done AS (
	SELECT director_user_id AS user_id, min(finished_at) AS done_at
	FROM session WHERE workspace_id = $1 AND status = 'completed' AND finished_at IS NOT NULL GROUP BY director_user_id)
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (d.done_at - r.online_at)) / 60), count(*)
FROM first_rt r JOIN first_done d USING (user_id)
WHERE d.done_at >= r.online_at AND d.done_at >= $2`

// 2. completed 세션 중 completion_met.manual(= completeSession, director_end)이 아닌 비율.
const sqlAutoComplete = `
SELECT avg(CASE WHEN COALESCE((completion_met->>'manual')::boolean, false) THEN 0 ELSE 1 END), count(*)
FROM session WHERE workspace_id = $1 AND status = 'completed' AND finished_at >= $2`

// 3. hitl_request answered_at - created_at 중앙값(분), auto_answered 제외.
const sqlHitlResponse = `
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (h.answered_at - h.created_at)) / 60), count(*)
FROM hitl_request h JOIN session s ON s.id = h.session_id
WHERE s.workspace_id = $1 AND h.status = 'answered' AND h.answered_at IS NOT NULL AND h.answered_at >= $2`

// 4. delegated_from_task_id 가 있는 끝난 task 중, HITL 도 lane blocked 도 없이 completed 된 비율.
const sqlDelegationAutonomous = `
SELECT avg(CASE WHEN t.status = 'completed'
                 AND NOT EXISTS (SELECT 1 FROM hitl_request h WHERE h.task_id = t.id)
                 AND l.status <> 'blocked' AND l.blocked_message_id IS NULL THEN 1 ELSE 0 END), count(*)
FROM task t JOIN lane l ON l.id = t.lane_id JOIN session s ON s.id = t.session_id
WHERE s.workspace_id = $1 AND t.delegated_from_task_id IS NOT NULL
  AND t.status IN ('completed', 'failed', 'cancelled') AND COALESCE(t.finished_at, t.updated_at) >= $2`

// 5. lane ≥ 2 인 완료 세션: 1 - wall(세션 시작→완료) / sum(task started_at→finished_at). 평균. n = 세션 수.
const sqlParallelReduction = `
WITH s AS (
	SELECT s.id, extract(epoch FROM (s.finished_at - COALESCE(s.started_at, s.created_at))) AS wall
	FROM session s
	WHERE s.workspace_id = $1 AND s.status = 'completed' AND s.finished_at >= $2
	  AND (SELECT count(*) FROM lane l WHERE l.session_id = s.id) >= 2),
t AS (
	SELECT session_id, sum(extract(epoch FROM (finished_at - started_at))) AS total
	FROM task WHERE started_at IS NOT NULL AND finished_at IS NOT NULL GROUP BY session_id)
SELECT avg(1 - s.wall / t.total), count(*)
FROM s JOIN t ON t.session_id = s.id WHERE t.total > 0 AND s.wall >= 0`

// 7. attempt ≥ 2 인 task 중 같은 내용의 메시지가 둘 이상 게시된 것이 관측된 비율.
const sqlDuplicateAfterResume = `
WITH t AS (
	SELECT t.id FROM task t JOIN session s ON s.id = t.session_id
	WHERE s.workspace_id = $1 AND t.attempt >= 2 AND t.updated_at >= $2)
SELECT avg(CASE WHEN EXISTS (
	SELECT 1 FROM message m WHERE m.source_task_id = t.id GROUP BY m.content HAVING count(*) >= 2) THEN 1 ELSE 0 END), count(*)
FROM t`

// 8. resume_outcome 이 있는 attempt(resumed IS NOT NULL) 중 resumed 비율.
const sqlResumeSuccess = `
SELECT avg(CASE WHEN a.resumed THEN 1 ELSE 0 END), count(*)
FROM task_attempt a JOIN task t ON t.id = a.task_id JOIN session s ON s.id = t.session_id
WHERE s.workspace_id = $1 AND a.resumed IS NOT NULL AND COALESCE(a.finished_at, a.started_at, a.dispatched_at, t.updated_at) >= $2`

// 9. lane blocked 진입(질문 카드 message.kind = blocked_q 의 created_at) → 그 카드의 첫 답글. 중앙값(분).
const sqlBlockedResponse = `
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (r.first_reply - q.created_at)) / 60), count(*)
FROM message q JOIN session s ON s.id = q.session_id
JOIN LATERAL (SELECT min(created_at) AS first_reply FROM message r WHERE r.parent_id = q.id AND r.created_at > q.created_at) r
     ON r.first_reply IS NOT NULL
WHERE s.workspace_id = $1 AND q.kind = 'blocked_q' AND q.created_at >= $2`

// 6. runtime_kind 별 completed / (completed + failed). breakdown 은 v1 런타임
// 두 종류를 항상 싣고, 표본이 없는 종류는 value null · n 0.
func successByRuntime(ctx context.Context, q db.DBTX, wsID uuid.UUID, since time.Time, m *Metric) error {
	rows, err := q.Query(ctx, `
		SELECT p.runtime_kind::text,
		       count(*) FILTER (WHERE t.status = 'completed'), count(*) FILTER (WHERE t.status = 'failed')
		FROM task t JOIN agent_profile p ON p.id = t.profile_id JOIN session s ON s.id = t.session_id
		WHERE s.workspace_id = $1 AND t.status IN ('completed', 'failed') AND COALESCE(t.finished_at, t.updated_at) >= $2
		GROUP BY 1`, wsID, since)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := map[string][2]int{}
	for rows.Next() {
		var kind string
		var done, failed int
		if err := rows.Scan(&kind, &done, &failed); err != nil {
			return err
		}
		counts[kind] = [2]int{done, failed}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var totalDone, totalN int
	for _, kind := range []contracts.RuntimeKind{contracts.RuntimeClaudeCode, contracts.RuntimeHermes} {
		c := counts[string(kind)]
		n := c[0] + c[1]
		b := Breakdown{Kind: string(kind), Target: runtimeTargets[kind], N: n}
		if n > 0 {
			v := float64(c[0]) / float64(n)
			b.Value = &v
		}
		m.Breakdown = append(m.Breakdown, b)
		totalDone += c[0]
		totalN += n
	}
	m.N = totalN
	if totalN > 0 {
		v := float64(totalDone) / float64(totalN)
		m.Value = &v
	}
	return nil
}

// 10. 최근 7일 안에 task 가 하나라도 돈(started_at) 세션 수 — 창과 무관. n 은 이
// 워크스페이스에서 task 를 돌린 적 있는 세션 수: 아무것도 돌린 적 없는 워크스페이스는
// null 이고, 한때 돌았다가 조용한 워크스페이스는 실측 0 이다.
func weeklyActive(ctx context.Context, q db.DBTX, wsID uuid.UUID, now time.Time, m *Metric) error {
	var active, ever int
	if err := q.QueryRow(ctx, `
		SELECT count(DISTINCT t.session_id) FILTER (WHERE t.started_at >= $2), count(DISTINCT t.session_id)
		FROM task t JOIN session s ON s.id = t.session_id
		WHERE s.workspace_id = $1 AND t.started_at IS NOT NULL`, wsID, now.Add(-7*24*time.Hour)).Scan(&active, &ever); err != nil {
		return err
	}
	m.N = ever
	if ever > 0 {
		v := float64(active)
		m.Value = &v
	}
	return nil
}
