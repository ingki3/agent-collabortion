package queue

// NN2·NN3 (PR #382 리뷰): 계측의 두 성질에 테스트가 없어 그것만 바꿔도 스위트가
// 통과했다 — add 의 합산과 session_depth 의 「같은 세션」 조건.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// NN2 — add 는 같은 키가 다시 오면 더한다. 덮어쓰기여도 기존 스위트는 통과했지만,
// 합산은 실제로 일한다: prompt.history/detail 은 히스토리 메시지마다 한 번씩
// add 되므로 덮어쓰기면 마지막 한 조각만 남는다(리뷰 프로브 7 → 2).
//
// DB 가 필요 없는 단위 테스트다 — 표 하나에 모아 두면 다음 사람이 이 성질을
// 못 보고 지나치지 않는다.
func TestContextMetricAddSumsARepeatedKey(t *testing.T) {
	m := newContextMetric()
	m.add("prompt.history", "aaaa")   // 4 바이트
	m.add("prompt.history", "bbbbbb") // 6 바이트 — 덮어쓰기면 6 으로 남는다
	if got := m.Sections["prompt.history"]; got.Bytes != 10 {
		t.Errorf("두 번 add 한 키의 bytes = %d, want 10 (합산이어야 한다)", got.Bytes)
	}
	// 토큰 추정도 같이 더해져야 한다 — 바이트만 더하고 토큰을 덮어쓰면 구역별
	// 토큰이 조용히 작아진다.
	one := sizeOf("aaaa").TokensEst + sizeOf("bbbbbb").TokensEst
	if got := m.Sections["prompt.history"].TokensEst; got != one {
		t.Errorf("tokens_est = %d, want %d (조각들의 합)", got, one)
	}
	// 빈 조각은 키를 만들지 않는다(안 쓴 구역과 「0바이트 썼다」를 구분한다).
	m.add("prompt.folders", "")
	if _, ok := m.Sections["prompt.folders"]; ok {
		t.Error("빈 문자열이 구역을 만들었다 — 안 쓴 구역은 키가 없어야 한다")
	}
	// 다른 키는 서로 섞이지 않는다.
	m.add("prompt.trigger", "cc")
	if m.Sections["prompt.history"].Bytes != 10 || m.Sections["prompt.trigger"].Bytes != 2 {
		t.Errorf("키가 섞였다: %v", m.Sections)
	}
}

// wrote 는 add 위에 올라가 있으므로, 버퍼에 두 번 쓴 구역도 합산돼야 한다.
func TestContextMetricWroteSumsTwoWritesToOneKey(t *testing.T) {
	m := newContextMetric()
	var b strings.Builder
	n := b.Len()
	b.WriteString("<history>")
	m.wrote("prompt.history", &b, n)
	n = b.Len()
	b.WriteString("</history>")
	m.wrote("prompt.history", &b, n)
	if got := m.Sections["prompt.history"].Bytes; got != len("<history></history>") {
		t.Errorf("두 번 쓴 구역 = %d bytes, want %d", got, len("<history></history>"))
	}
}

// NN3 — session_depth 는 「같은 런타임 세션」에서 앞선 턴만 센다. 조건을 빼도
// 기존 스위트는 통과했다(사본 재생에서도 해가 없었다) — 칸의 뜻이 「이 세션에서
// 앞선 턴 수」라 조건이 본질이므로, 다른 세션의 턴이 섞이면 틀린다.
//
// 1단계 ③(harness v0.9.14 §6)이 세션 상한 판정에 이 계열을 읽으므로 더욱 그렇다.
func TestContextMetricSessionDepthCountsOnlyTheSameSession(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()

	// 같은 방에서 세 턴: A·B 는 세션 "sess-A", C 는 다른 세션 "sess-B".
	// 시간순으로 A → C → B 로 끝내, depth 가 시간이 아니라 세션으로 갈리는지 본다.
	type turn struct {
		id      uuid.UUID
		session string
	}
	var turns []turn
	for i, sess := range []string{"sess-A", "sess-B", "sess-A"} {
		c.Advance(time.Minute)
		id := testdb.AddTask(t, q.DB, s, s.SessionID, c.Now())
		bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
		if err != nil || len(bundles) != 1 {
			t.Fatalf("turn %d claim = %v %v", i, bundles, err)
		}
		run(t, q, id, 1)
		c.Advance(time.Minute)
		ref := &contracts.RuntimeSessionRef{
			RuntimeKind: contracts.RuntimeClaudeCode, SessionID: sess, CWD: "/w", CreatedAt: t0,
		}
		if _, err := q.Tasks.Finish(ctx, id, 1, contracts.Finish{
			Outcome: "completed", StopReason: "end_turn", RuntimeSessionRef: ref,
		}); err != nil {
			t.Fatalf("turn %d finish: %v", i, err)
		}
		turns = append(turns, turn{id, sess})
	}

	// A 가 첫 턴이니 0, C 는 다른 세션의 첫 턴이니 0, B 는 A 뒤라 1.
	want := []int{0, 0, 1}
	for i, tn := range turns {
		m := readMetric(t, q, tn.id, 1)
		if m.Depth == nil {
			t.Fatalf("턴 %d (%s): session_depth 가 NULL — 세션을 보고했으면 값이 있어야 한다", i, tn.session)
		}
		if *m.Depth != want[i] {
			t.Errorf("턴 %d (%s): session_depth = %d, want %d — 같은 세션의 앞선 턴만 센다",
				i, tn.session, *m.Depth, want[i])
		}
		if m.FinishSession == nil || *m.FinishSession != tn.session {
			t.Errorf("턴 %d: finish_session_id = %v, want %q", i, m.FinishSession, tn.session)
		}
	}
}
