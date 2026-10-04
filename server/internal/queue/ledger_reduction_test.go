package queue

// T-LEDGER 측정 (CONTEXT_MEMORY.md 2단계 성공 기준 「턴 프롬프트 −40%」): the
// same synthetic long mission rendered under the stage-1 ② (every mission
// message whole, 작업 내용 demoted to 400 characters) and under harness
// v0.9.18 (② a header index + <mission_ledger>). The 0단계 baseline
// (04-baseline.md §4) found ② the one unbounded block — a long mission's
// messages are agent reports of a few hundred characters with multi-KB 작업
// 내용, which is what the fixture models (150 mission messages, a third with a
// 3,000-character detail, plus 12 ledger items).
//
// The legacy ② is rendered here from the same rows with the stage-1 line
// (`[MM-DD HH:MM] <id> <author>: <content>\n` + historyDetail) — the code
// this PR replaced — and swapped into the real bundle's prompt, so every
// other block is byte-identical between the two numbers.
//
//	go test ./internal/queue -run TestLedgerPromptReduction -v

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
)

func legacyMissionMessages(work string, ms []*messages.Row, surf Surface) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<mission_messages work=%q count=%d note=\"this mission's messages older than <history>\">\n", work, len(ms))
	for _, m := range ms {
		fmt.Fprintf(&b, "[%s] %s %s: %s\n%s", m.CreatedAt.UTC().Format("01-02 15:04"), m.ID, authorLabel(m), m.Content, historyDetail(m, false, surf))
	}
	b.WriteString("</mission_messages>\n\n")
	return b.String()
}

// 회귀 주입: missionIndexLine 이 content 전문을 쓰면(요약 120자 없이) 절감률이
// 40% 아래로 떨어져 FAIL.
func TestLedgerPromptReduction(t *testing.T) {
	f := newCtxFixture(t, false)
	report := strings.Repeat("조사 결과를 정리했습니다 — 출처 세 곳을 대조했고 수치가 맞지 않는 부분은 표시했습니다. ", 6)
	detail := strings.Repeat("| 항목 | 값 | 출처 |\n| 매출 | 1,234억 | 공시 |\n", 75)
	const n = 150
	for i := 0; i < n; i++ {
		if i%3 == 0 {
			f.messageWithDetail(t, fmt.Sprintf("%03d %s", i, report), detail)
		} else {
			f.message(t, fmt.Sprintf("%03d %s", i, report))
		}
	}
	now := f.now()
	f.ledgerItem(t, "plan", "1) 공시 수집 2) 표 정리 3) 초안 4) 검토 — Lead 가 순서대로 카드로 나눈다", now, "")
	f.ledgerItem(t, "progress", "수집 끝, 표 정리 중 — 남은 일: 초안·검토", now, "")
	for i := 0; i < 5; i++ {
		f.ledgerItem(t, "fact", fmt.Sprintf("2024년 %d분기 매출 1,2%d4억 (공시 원문 기준)", i%4+1, i), now, "certainty='given'")
	}
	f.ledgerItem(t, "assignment", "R: 공시 수집 · W: 초안 · Reviewer: 검토", now, "")
	f.ledgerItem(t, "open_question", "연결/별도 중 무엇으로 쓸지 Director 확인 필요", now, "")
	f.ledgerItem(t, "lesson", "DART 검색 API 는 100건 제한 — 기간을 나눠 부른다", now, "outcome='corrected'")
	f.ledgerItem(t, "lesson", "PDF 표 추출은 깨진다 — XBRL 을 쓴다", now, "outcome='dead_end'")
	f.ledgerItem(t, "fact", "환율은 2024-12-31 종가 1,472원 사용", now, "certainty='derived'")

	b := f.claim(t)
	surf := SurfaceFor("claude_code")
	newMM := "<mission_messages " + between(b.Prompt, "<mission_messages ", "</mission_messages>") + "</mission_messages>\n\n"
	ledger := "<mission_ledger " + between(b.Prompt, "<mission_ledger ", "</mission_ledger>") + "</mission_ledger>\n\n"
	if !strings.Contains(b.Prompt, newMM) || !strings.Contains(b.Prompt, ledger) {
		t.Fatal("premise: the bundle has no ② or no ledger")
	}

	ctx := context.Background()
	tx, err := f.q.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var work string
	if err := tx.QueryRow(ctx, `SELECT legacy_work_id::text FROM room WHERE id = $1`, f.s.SessionID).Scan(&work); err != nil {
		t.Fatal(err)
	}
	recent, _, _, _, err := messages.List(ctx, tx, f.s.SessionID, messages.ListOptions{IncludeReplies: true, Limit: historyLimit})
	if err != nil {
		t.Fatal(err)
	}
	wid := f.workID(t)
	h, err := loadRoomHistory(ctx, tx, f.s.SessionID, &wid, recent)
	if err != nil {
		t.Fatal(err)
	}
	oldMM := legacyMissionMessages(work, h.MissionOlder, surf)
	before := strings.Replace(strings.Replace(b.Prompt, ledger, "", 1), newMM, oldMM, 1)

	tok := llm.EstimateTokens
	cut := 1 - float64(len(b.Prompt))/float64(len(before))
	t.Logf("mission messages in ②: %d (of %d)", len(h.MissionOlder), n+4)
	t.Logf("② before: %6d bytes ≈ %6d tokens", len(oldMM), tok(oldMM))
	t.Logf("② after:  %6d bytes ≈ %6d tokens  + <mission_ledger> %d bytes ≈ %d tokens", len(newMM), tok(newMM), len(ledger), tok(ledger))
	t.Logf("turn prompt: %d → %d bytes (≈ %d → %d tokens) = −%.1f%%", len(before), len(b.Prompt), tok(before), tok(b.Prompt), cut*100)
	t.Logf("②+ledger vs old ②: −%.1f%%", (1-float64(len(newMM)+len(ledger))/float64(len(oldMM)))*100)
	if cut < 0.40 {
		t.Fatalf("turn prompt cut is %.1f%%, below the stage-2 target of 40%%", cut*100)
	}
}

func (f *ctxFixture) workID(t *testing.T) (id uuid.UUID) {
	t.Helper()
	if err := f.q.DB.QueryRow(context.Background(), `SELECT legacy_work_id FROM room WHERE id = $1`, f.s.SessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
