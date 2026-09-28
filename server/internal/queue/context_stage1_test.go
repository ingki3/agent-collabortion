package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// 맥락 1단계 (harness v0.9.14 · daemon-protocol v0.10.3, Director 승인
// 2026-09-28): ① the brief is fixed, ② a resumed turn gets a delta and the
// whole prompt beside it, ③ the server decides resume by the session's size.

// ctxFixture is one task that ran attempt 1 on runtime session "sess-1" and
// failed retryably, so attempt 2 would resume it.
type ctxFixture struct {
	q    *Postgres
	s    testdb.Seed
	id   uuid.UUID
	now  func() time.Time
	adv  func(time.Duration)
	msgs int
}

func newCtxFixture(t *testing.T, promptCold bool) *ctxFixture {
	t.Helper()
	q, c, s := newQueue(t)
	f := &ctxFixture{q: q, s: s, now: c.Now, adv: c.Advance}
	if promptCold {
		f.exec(t, `UPDATE runtime SET daemon_features = '{prompt_cold}' WHERE id = $1`, s.RuntimeID)
	}
	f.id = testdb.AddTask(t, q.DB, s, s.SessionID, t0)
	for i := 0; i < 3; i++ {
		f.message(t, "before anchor")
	}
	return f
}

func (f *ctxFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.q.DB.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// message posts one room message, one second after the last.
func (f *ctxFixture) message(t *testing.T, body string) uuid.UUID {
	t.Helper()
	f.msgs++
	var id uuid.UUID
	if err := f.q.DB.QueryRow(context.Background(), `
		INSERT INTO message (session_id, author_type, author_id, content, created_at, work_id)
		VALUES ($1, 'agent', $2, $3, $4, (SELECT legacy_work_id FROM room WHERE id = $1)) RETURNING id`,
		f.s.SessionID, f.s.AgentID, body, t0.Add(time.Duration(f.msgs)*time.Second)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *ctxFixture) claim(t *testing.T) contracts.TaskBundle {
	t.Helper()
	bs, err := f.q.Claim(context.Background(), f.s.RuntimeID.String(), 1, f.now())
	if err != nil || len(bs) != 1 {
		t.Fatalf("claim = %v %v", bs, err)
	}
	run(t, f.q, f.id, bs[0].Task.Attempt)
	return bs[0]
}

// sample records one heartbeat usage for the attempt: cache_read + cache_write
// + input = total.
func (f *ctxFixture) sample(t *testing.T, attempt int, total int64) {
	t.Helper()
	f.adv(15 * time.Second)
	if err := f.q.Tasks.RecordTurnUsage(context.Background(), f.id, attempt, contracts.Usage{CacheReadTokens: total - 100, CacheWriteTokens: 90, InputTokens: 10, Estimated: true}, f.now()); err != nil {
		t.Fatal(err)
	}
}

func (f *ctxFixture) failWithRef(t *testing.T, attempt int, sid string) {
	t.Helper()
	ref := &contracts.RuntimeSessionRef{RuntimeKind: contracts.RuntimeClaudeCode, SessionID: sid, CWD: "/w", CreatedAt: t0}
	if _, err := f.q.Tasks.Finish(context.Background(), f.id, attempt, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther, StopReason: "crash", RuntimeSessionRef: ref}); err != nil {
		t.Fatal(err)
	}
	f.adv(time.Minute)
}

// ① E12-11 v0.9.14: a new message, a new decision, a new artifact version and
// a status change between two bundles of the same room·mission·agent·surface
// leave brief.text byte-identical; the brief has no [6]·[7]; <room_decisions>
// carries all 21 decisions; the blocks come in the contract's order.
//
// 회귀 주입: bundle.go 가 [6]/[7] 을 브리프에 다시 쓰면(또는 decisionLogLimit 으로
// 결정을 자르면) FAIL.
func TestBriefIsFixedAcrossTurns(t *testing.T) {
	f := newCtxFixture(t, true)
	for i := 0; i < 20; i++ {
		f.exec(t, `INSERT INTO decision (session_id, summary, source, created_at) VALUES ($1, $2, 'agent', $3)`, f.s.SessionID, "old decision", t0.Add(time.Duration(i)*time.Second))
	}
	f.exec(t, `INSERT INTO artifact (session_id, name, type, version, storage_ref, created_at) VALUES ($1, 'plan.md', 'document', 1, 'x', $2)`, f.s.SessionID, t0)
	b1 := f.claim(t)
	f.failWithRef(t, 1, "sess-1")

	// What an agent turn makes — none of it may touch the brief.
	f.message(t, "a new message")
	f.exec(t, `INSERT INTO decision (session_id, summary, source, created_at) VALUES ($1, 'THE NEW DECISION', 'agent', $2)`, f.s.SessionID, f.now())
	f.exec(t, `INSERT INTO artifact (session_id, name, type, version, storage_ref, created_at) VALUES ($1, 'plan.md', 'document', 2, 'y', $2)`, f.s.SessionID, f.now())
	b2 := f.claim(t)

	if b1.Brief.Text != b2.Brief.Text {
		t.Fatalf("brief changed between two turns of the same room/mission/agent/surface:\n--- 1\n%s\n--- 2\n%s", b1.Brief.Text, b2.Brief.Text)
	}
	for _, gone := range []string{"[6]", "[7]", "Decision Log", "plan.md", "old decision"} {
		if strings.Contains(b2.Brief.Text, gone) {
			t.Fatalf("brief carries %q (harness v0.9.14 moved it to the turn prompt):\n%s", gone, b2.Brief.Text)
		}
	}
	for _, p := range []string{b2.Prompt, b2.PromptCold} {
		if !strings.Contains(p, "<room_decisions count=21>\n") || !strings.Contains(p, "THE NEW DECISION") || strings.Contains(p, "older than the ones in") {
			t.Fatalf("<room_decisions> is not every decision:\n%s", p)
		}
		if !strings.Contains(p, "<room_artifacts count=1>\n") || !strings.Contains(p, "plan.md (document, v2, id ") {
			t.Fatalf("<room_artifacts> lacks the latest version:\n%s", p)
		}
	}
	// Block order (harness §10 v0.9.14).
	order := []string{"<resumed", "Your runtime session already holds", "<history since=", "<room_decisions", "<room_artifacts", "<roster_status>", "<folders", "<trigger>", "Respond to the trigger"}
	last := -1
	for _, tag := range order {
		i := strings.Index(b2.Prompt, tag)
		if i < 0 || i < last {
			t.Fatalf("%q out of order (at %d after %d):\n%s", tag, i, last, b2.Prompt)
		}
		last = i
	}
}

// FR-4.1's truncation line ends with the v0.9.14 sentence.
func TestTruncationNoteNamesRoomDecisions(t *testing.T) {
	got := truncationNote(3, roomHistory{}, false, SurfaceFor("claude_code"))
	if !strings.Contains(got, "Every decision is in <room_decisions>.") || strings.Contains(got, "[7]") {
		t.Fatalf("truncation note = %q", got)
	}
}

// ② harness §10 v0.9.14: a resumed bundle's `prompt` carries only the messages
// after the anchor (the room's latest message when attempt 1's bundle was
// built), under `<history since=…>` and the fixed head line; `prompt_cold` is
// the whole shape; every block outside ①·② is byte-identical between them.
//
// 회귀 주입: renderPrompt 의 anchor 필터를 지우면(델타에 기준점 앞 메시지가
// 실리면), 또는 PromptCold 를 비우면 FAIL.
func TestResumedTurnGetsDeltaAndPromptCold(t *testing.T) {
	f := newCtxFixture(t, true)
	b1 := f.claim(t)
	if b1.PromptCold != "" || strings.Contains(b1.Prompt, "since=") {
		t.Fatalf("a cold first turn got a delta")
	}
	var anchor uuid.UUID
	if err := f.q.DB.QueryRow(context.Background(), `SELECT context_anchor_message_id FROM task_attempt WHERE task_id = $1 AND attempt = 1`, f.id).Scan(&anchor); err != nil {
		t.Fatalf("attempt 1 anchor: %v", err)
	}
	f.failWithRef(t, 1, "sess-1")
	after1 := f.message(t, "AFTER-ONE")
	after2 := f.message(t, "AFTER-TWO")
	b2 := f.claim(t)

	if b2.Resume == nil || b2.Resume.SessionID != "sess-1" {
		t.Fatalf("resume = %+v, want sess-1", b2.Resume)
	}
	if b2.PromptCold == "" {
		t.Fatal("resumed bundle has no prompt_cold")
	}
	hist := between(b2.Prompt, "<history ", "</history>")
	if !strings.HasPrefix(hist, `since="`+anchor.String()+`" included=2 total=2 truncated=false>`) {
		t.Fatalf("delta history head = %q", hist[:min(len(hist), 120)])
	}
	if strings.Contains(hist, "before anchor") || strings.Contains(hist, "hello") || !strings.Contains(hist, after1.String()) || !strings.Contains(hist, after2.String()) {
		t.Fatalf("delta history is not exactly the messages after the anchor:\n%s", hist)
	}
	if !strings.Contains(b2.Prompt, "Your runtime session already holds this room's messages up to "+anchor.String()+" from earlier turns;") {
		t.Fatalf("delta head line missing:\n%s", b2.Prompt)
	}
	cold := between(b2.PromptCold, "<history ", "</history>")
	if strings.Contains(b2.PromptCold, "since=") || strings.Contains(b2.PromptCold, "Your runtime session already holds") ||
		!strings.Contains(cold, "before anchor") || !strings.Contains(cold, "AFTER-TWO") {
		t.Fatalf("prompt_cold is not the whole shape:\n%s", b2.PromptCold)
	}
	// Outside ①·② the two are the same bytes: strip the head line and the
	// history block from both.
	strip := func(p string) string {
		p = strings.Replace(p, "Your runtime session already holds this room's messages up to "+anchor.String()+" from earlier turns; <history> and <mission_messages> below carry only the messages after it.\n", "", 1)
		i, j := strings.Index(p, "<history"), strings.Index(p, "</history>")
		return p[:i] + p[j:]
	}
	if strip(b2.Prompt) != strip(b2.PromptCold) {
		t.Fatalf("blocks outside ①·② differ:\n--- prompt\n%s\n--- prompt_cold\n%s", strip(b2.Prompt), strip(b2.PromptCold))
	}
	// The metric measures the delta and says so.
	m := readMetric(t, f.q, f.id, 2)
	if m.PromptBytes != len(b2.Prompt) || m.Counts["delta"] != 1 || m.Counts["prompt_cold_bytes"] != len(b2.PromptCold) || m.Counts["history_total"] != 2 {
		t.Fatalf("metric = %d bytes, counts %v", m.PromptBytes, m.Counts)
	}
}

// ② the three no-delta cases (harness §10): a daemon that did not advertise
// prompt_cold, a session whose anchor was never recorded, and a cold turn.
//
// 회귀 주입: runtimeKnowsPromptCold 가 늘 true 면 첫째, laneContextAnchor 검사를
// 지우면 둘째가 FAIL.
func TestNoDeltaWithoutFeatureOrAnchor(t *testing.T) {
	t.Run("old daemon", func(t *testing.T) {
		f := newCtxFixture(t, false)
		f.claim(t)
		f.failWithRef(t, 1, "sess-1")
		b := f.claim(t)
		if b.Resume == nil || b.PromptCold != "" || strings.Contains(b.Prompt, "since=") {
			t.Fatalf("old daemon: resume=%v prompt_cold=%d delta=%v", b.Resume, len(b.PromptCold), strings.Contains(b.Prompt, "since="))
		}
	})
	t.Run("session without an anchor (before the rollout)", func(t *testing.T) {
		f := newCtxFixture(t, true)
		f.claim(t)
		f.failWithRef(t, 1, "sess-1")
		f.exec(t, `UPDATE lane SET context_anchor_message_id = NULL, context_anchor_at = NULL`)
		b := f.claim(t)
		if b.Resume == nil || b.PromptCold != "" || strings.Contains(b.Prompt, "since=") {
			t.Fatalf("no anchor: resume=%v prompt_cold=%d", b.Resume, len(b.PromptCold))
		}
	})
	t.Run("cold turn", func(t *testing.T) {
		f := newCtxFixture(t, true)
		b := f.claim(t)
		if b.Resume != nil || b.PromptCold != "" {
			t.Fatalf("cold: resume=%v prompt_cold=%d", b.Resume, len(b.PromptCold))
		}
	})
}

// ③ harness §6 v0.9.14: the session's start size is samples[0] of its last
// finished turn. 300,001 → planned cold start (resume null, the lane keeps
// its ref); 300,000 → resume; no sample → resume.
//
// 회귀 주입: overSessionCap 이 늘 false 면 첫째, `>` 를 `>=` 로 바꾸면 둘째,
// 표본 없음을 0 으로 읽어 적용하면(ok 무시) 셋째가 FAIL — 그리고 samples[0]
// 대신 마지막 표본을 읽으면 첫째·둘째가 FAIL(둘째 표본은 늘 상한 위).
func TestSessionCapDecidesResume(t *testing.T) {
	for _, tc := range []struct {
		name   string
		first  int64 // 0: no sample
		resume bool
	}{
		{"over the cap", contracts.ResumeSessionMaxTokens + 1, false},
		{"at the cap", contracts.ResumeSessionMaxTokens, true},
		{"no sample", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCtxFixture(t, true)
			f.claim(t)
			if tc.first > 0 {
				f.sample(t, 1, tc.first)
				f.sample(t, 1, 9_000_000) // the turn's running total — never the session size
			}
			f.failWithRef(t, 1, "sess-1")
			b := f.claim(t)
			if (b.Resume != nil) != tc.resume {
				t.Fatalf("resume = %+v, want resume=%v", b.Resume, tc.resume)
			}
			if !tc.resume {
				if b.PromptCold != "" || strings.Contains(b.Prompt, "since=") || !strings.Contains(b.Prompt, "before anchor") {
					t.Fatal("a planned cold start must carry the whole prompt and no prompt_cold")
				}
				var sid string
				if err := f.q.DB.QueryRow(context.Background(), `SELECT runtime_session_ref->>'session_id' FROM lane l JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, f.id).Scan(&sid); err != nil || sid != "sess-1" {
					t.Fatalf("lane ref = %q %v, want sess-1 kept", sid, err)
				}
				m := readMetric(t, f.q, f.id, 2)
				if m.PlannedResume || m.Counts["session_capped"] != 1 || m.Counts["session_start_tokens"] != int(tc.first) {
					t.Fatalf("metric planned_resume=%v counts=%v", m.PlannedResume, m.Counts)
				}
			}
		})
	}
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	if j := strings.Index(s, to); j >= 0 {
		return s[:j]
	}
	return s
}
