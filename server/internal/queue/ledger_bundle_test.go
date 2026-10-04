package queue

// T-LEDGER (harness v0.9.18, PRD FR-4.6 — 맥락 2단계): ② is a header index,
// <mission_ledger> follows it, the ledger is whole on a resumed turn, and the
// brief's [2] gains one fixed line without breaking E12-11.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ledgerItem inserts one memory_item of the fixture's mission.
func (f *ctxFixture) ledgerItem(t *testing.T, kind, content string, at time.Time, extra string) uuid.UUID {
	t.Helper()
	cols, vals := "", ""
	if kind == "lesson" && !strings.Contains(extra, "support_count") {
		cols, vals = ", support_count", ", 2"
	}
	if extra != "" {
		cols += ", " + strings.SplitN(extra, "=", 2)[0]
		vals += ", " + strings.SplitN(extra, "=", 2)[1]
	}
	var id uuid.UUID
	if err := f.q.DB.QueryRow(context.Background(), `
		INSERT INTO memory_item (work_id, kind, content, created_at`+cols+`)
		VALUES ((SELECT legacy_work_id FROM room WHERE id = $1), $2, $3, $4`+vals+`) RETURNING id`,
		f.s.SessionID, kind, content, at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// messageWithDetail posts one mission message with a 작업 내용.
func (f *ctxFixture) messageWithDetail(t *testing.T, body, detail string) uuid.UUID {
	t.Helper()
	f.msgs++
	var id uuid.UUID
	if err := f.q.DB.QueryRow(context.Background(), `
		INSERT INTO message (session_id, author_type, author_id, content, detail, created_at, work_id)
		VALUES ($1, 'agent', $2, $3, $4, $5, (SELECT legacy_work_id FROM room WHERE id = $1)) RETURNING id`,
		f.s.SessionID, f.s.AgentID, body, detail, t0.Add(time.Duration(f.msgs)*time.Second)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

var indexLineRe = regexp.MustCompile(`^- \[[0-9a-f-]{36}\] [^:\n]+: [^\n]*$`)

// ② harness v0.9.18: one line per message — `- [<id>] <author>: <120 chars>`,
// line breaks folded, no 작업 내용, a pointer to the full read; the count
// matches the lines.
//
// 회귀 주입: renderRoomHistoryTail 이 옛 상세 렌더(historyDetail 포함)로 돌아가면,
// missionIndexSummary 를 400 으로 두면, 줄바꿈을 접지 않으면 FAIL.
func TestMissionMessagesIsHeaderIndex(t *testing.T) {
	f := newCtxFixture(t, false)
	long := f.message(t, "첫 줄\n둘째 줄 "+strings.Repeat("가", 200))
	withDetail := f.messageWithDetail(t, "요약만", strings.Repeat("DETAIL ", 200))
	detailOnly := f.messageWithDetail(t, "", "카드 본문은 detail 에만 있다")
	for i := 0; i < 60; i++ {
		f.message(t, fmt.Sprintf("chatter %02d", i))
	}
	b := f.claim(t)
	mm := between(b.Prompt, "<mission_messages ", "</mission_messages>")
	if mm == "" {
		t.Fatalf("no <mission_messages>:\n%s", b.Prompt)
	}
	lines := strings.Split(strings.TrimRight(mm, "\n"), "\n")
	head, body, tail := lines[0], lines[1:len(lines)-1], lines[len(lines)-1]
	m := regexp.MustCompile(`count=(\d+)`).FindStringSubmatch(head)
	if m == nil || m[1] != fmt.Sprint(len(body)) {
		t.Fatalf("count attribute %v vs %d lines:\n%s", m, len(body), mm)
	}
	for _, l := range body {
		if !indexLineRe.MatchString(l) {
			t.Fatalf("not an index line: %q", l)
		}
	}
	if tail != "Read one in full with `colab_room_messages` 툴의 `thread: \"<id>\"`." {
		t.Fatalf("pointer line = %q", tail)
	}
	if strings.Contains(mm, "<detail") || strings.Contains(mm, "DETAIL DETAIL") || strings.Contains(mm, "작업 내용") {
		t.Fatalf("② carries a 작업 내용:\n%s", mm)
	}
	want := fmt.Sprintf("- [%s] Lead: 첫 줄 둘째 줄 %s…", long, strings.Repeat("가", 120-len([]rune("첫 줄 둘째 줄 "))))
	if !strings.Contains(mm, want+"\n") {
		t.Fatalf("long message line is not the first 120 characters, folded:\n%s", mm)
	}
	if !strings.Contains(mm, fmt.Sprintf("- [%s] Lead: 요약만\n", withDetail)) {
		t.Fatalf("message with detail is not its conversation only:\n%s", mm)
	}
	if !strings.Contains(mm, fmt.Sprintf("- [%s] Lead: 카드 본문은 detail 에만 있다\n", detailOnly)) {
		t.Fatalf("detail-only message has no summary:\n%s", mm)
	}
}

// <mission_ledger> in the bundle: right after ② and before ③; kinds grouped
// plan → progress → fact → …; active only; lessons only promoted and inside
// 30 days of the claim's clock; the fact overflow; no block without items.
//
// 회귀 주입: bundle 이 ledger 를 쓰지 않으면, Rendered 의 status/lesson 검사를
// 지우면, ledger 자리를 ③ 뒤로 옮기면 FAIL.
func TestMissionLedgerInTurnPrompt(t *testing.T) {
	f := newCtxFixture(t, false)
	if b := f.claim(t); strings.Contains(b.Prompt, "<mission_ledger") {
		t.Fatalf("a mission without ledger items got the block:\n%s", b.Prompt)
	}
	f.failWithRef(t, 1, "sess-1")
	now := f.now()
	for i := 0; i < 60; i++ {
		f.message(t, fmt.Sprintf("chatter %02d", i))
	}
	f.exec(t, `INSERT INTO decision (session_id, summary, source, created_at) VALUES ($1, 'd', 'agent', $2)`, f.s.SessionID, t0)
	plan := f.ledgerItem(t, "plan", "ship the parser first", now, "")
	f.ledgerItem(t, "progress", "parser half done", now.Add(-time.Hour), "")
	for i := 0; i < 20; i++ {
		f.ledgerItem(t, "fact", fmt.Sprintf("fact-%02d", i), now.Add(-time.Duration(30-i)*time.Minute), "certainty='given'")
	}
	f.ledgerItem(t, "assignment", "R owns the parser", now, "")
	promoted := f.ledgerItem(t, "lesson", "PROMOTED LESSON", now.Add(-29*24*time.Hour), "outcome='dead_end'")
	f.ledgerItem(t, "lesson", "SEEN ONCE", now, "support_count=1")
	f.ledgerItem(t, "lesson", "EXPIRED LESSON", now.Add(-31*24*time.Hour), "")
	f.ledgerItem(t, "fact", "RETIRED FACT", now, "status, invalidated_at='retired', now()")
	b := f.claim(t)
	led := between(b.Prompt, "<mission_ledger ", "</mission_ledger>")
	if led == "" {
		t.Fatalf("no <mission_ledger>:\n%s", b.Prompt)
	}
	for _, gone := range []string{"SEEN ONCE", "EXPIRED LESSON", "RETIRED FACT", "fact-04"} {
		if strings.Contains(led, gone) {
			t.Errorf("ledger renders %q:\n%s", gone, led)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("- [%s] plan ship the parser first\n", plan),
		fmt.Sprintf("- [%s] lesson (dead_end, support 2) PROMOTED LESSON\n", promoted),
		"] fact (given) fact-19\n",
		"… and 5 more — the `colab_memory_get` tool (`kind: \"fact\"`)\n",
		"total=24>",
	} {
		if !strings.Contains(led, want) {
			t.Errorf("ledger lacks %q:\n%s", want, led)
		}
	}
	order := []string{"] plan ", "] progress ", "] fact ", "] assignment ", "] lesson "}
	last := -1
	for _, k := range order {
		i := strings.Index(led, k)
		if i < last {
			t.Fatalf("%q out of kind order:\n%s", k, led)
		}
		last = i
	}
	// Place: ② → <mission_ledger> → ③.
	at := func(tag string) int { return strings.Index(b.Prompt, tag) }
	if at("</mission_messages>") < 0 || !(at("</mission_messages>") < at("<mission_ledger") && at("<mission_ledger") < at("<room_decisions count")) {
		t.Fatalf("<mission_ledger> is not between ② and ③:\n%s", b.Prompt)
	}
}

// harness v0.9.18: the ledger is not history — a resumed turn's delta
// carries it whole, byte for byte as prompt_cold; an item written between
// the turns changes the prompt but never the brief (E12-11).
//
// 회귀 주입: 델타 렌더에서 ledger 를 기준점 뒤 항목만으로 거르면, 또는 ledger
// 줄을 브리프에 쓰면 FAIL.
func TestMissionLedgerIsWholeOnResumedTurn(t *testing.T) {
	f := newCtxFixture(t, true)
	before := f.ledgerItem(t, "fact", "BEFORE-ANCHOR FACT", f.now(), "")
	b1 := f.claim(t)
	f.failWithRef(t, 1, "sess-1")
	after := f.ledgerItem(t, "assignment", "AFTER-ANCHOR ASSIGNMENT", f.now(), "")
	f.message(t, "after the anchor")
	b2 := f.claim(t)
	if b2.PromptCold == "" || !strings.Contains(b2.Prompt, "since=") {
		t.Fatal("premise: attempt 2 is not a resumed delta turn")
	}
	delta := between(b2.Prompt, "<mission_ledger ", "</mission_ledger>")
	cold := between(b2.PromptCold, "<mission_ledger ", "</mission_ledger>")
	if delta == "" || delta != cold {
		t.Fatalf("delta ledger differs from prompt_cold's:\n--- delta\n%s\n--- cold\n%s", delta, cold)
	}
	if !strings.Contains(delta, before.String()) || !strings.Contains(delta, after.String()) {
		t.Fatalf("the delta's ledger is not whole:\n%s", delta)
	}
	if b1.Brief.Text != b2.Brief.Text {
		t.Fatalf("a ledger write changed the brief (E12-11):\n--- 1\n%s\n--- 2\n%s", b1.Brief.Text, b2.Brief.Text)
	}
	if strings.Contains(b2.Brief.Text, "BEFORE-ANCHOR") || strings.Contains(b2.Brief.Text, "<mission_ledger work") {
		t.Fatal("the brief carries ledger items")
	}
}

// Brief [2]'s ledger line (harness v0.9.18): the contract's shell sentence
// word for word (backticks aside), the mcp one with the contract's tool
// phrase and the same head and tail, once in each surface's [2].
//
// 회귀 주입: Section2 에서 s.LedgerRule 을 빼면 (brief2), LedgerRule 문장을 바꾸면
// (contract), mcpSurface 에 셸 문장을 두면 (surface) FAIL.
func TestLedgerRuleMatchesContractV0918(t *testing.T) {
	c := harnessContract(t)
	m := regexp.MustCompile("셸 `(Mission facts[^`]+)` / mcp `([^`]+)`").FindStringSubmatch(c)
	if m == nil {
		t.Fatal("harness.md has no v0.9.18 brief [2] ledger sentences")
	}
	shell, mcp := m[1], m[2]
	got := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(LedgerRule, "- "), "\n"), "`", "")
	if got != shell {
		t.Errorf("(contract) shell line differs:\n got %q\nwant %q", got, shell)
	}
	parts := strings.Split(mcp, "…")
	if len(parts) != 3 {
		t.Fatalf("contract mcp quote changed shape: %q", mcp)
	}
	tool := strings.TrimSpace(parts[1])
	mcpLine := strings.ReplaceAll(LedgerRuleMCP, "`", "")
	if !strings.Contains(mcpLine, tool) {
		t.Errorf("(contract) mcp line lacks %q: %q", tool, mcpLine)
	}
	head := shell[:strings.Index(shell, ": colab memory note")]
	tail := shell[strings.Index(shell, " — "):]
	if !strings.HasPrefix(mcpLine, "- "+head+" ") || !strings.HasSuffix(mcpLine, tail+"\n") {
		t.Errorf("(contract) mcp line head/tail differ from the shell one: %q", mcpLine)
	}
	if strings.Contains(LedgerRuleMCP, "`colab ") || strings.Contains(LedgerRule, "colab_") {
		t.Error("(surface) a line names the other surface's command")
	}
	for kind, s := range map[string]Surface{"mcp": SurfaceFor("claude_code"), "cli_wrapper": SurfaceFor("hermes")} {
		if n := strings.Count(s.Section2(), s.LedgerRule); s.LedgerRule == "" || n != 1 {
			t.Errorf("(brief2) %s [2] carries the ledger line %d times", kind, n)
		}
	}
	if SurfaceFor("claude_code").LedgerRule != LedgerRuleMCP || SurfaceFor("hermes").LedgerRule != LedgerRule {
		t.Error("(surface) SurfaceFor picked the wrong ledger line")
	}
}
