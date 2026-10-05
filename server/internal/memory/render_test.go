package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var now = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func item(kind, content string, at time.Time) *Item {
	return &Item{ID: uuid.New(), Kind: kind, Content: content, Status: "active", CreatedAt: at}
}

func more(k string) string { return "`colab memory get --kind " + k + "`" }

// The lesson gate (PRD FR-4.6 3): support_count 1 is not promoted, 2 is; a
// promoted lesson leaves the prompt after 30 days (the row stays); every
// other kind is always promoted; nothing that is not active is rendered.
//
// 회귀 주입: Promoted 의 PromoteAt 비교를 >=1 로 바꾸면 (support 1), Rendered 의
// 반감기 검사를 지우면 (31 days), status 검사를 지우면 (superseded) FAIL.
func TestLessonGate(t *testing.T) {
	one := item("lesson", "retry with backoff", now)
	one.SupportCount = 1
	two := item("lesson", "retry with backoff", now)
	two.SupportCount = 2
	if one.Promoted() || Rendered(one, now) {
		t.Fatal("(support 1) a lesson seen once is promoted")
	}
	if !two.Promoted() || !Rendered(two, now) {
		t.Fatal("(support 2) a lesson seen twice is not promoted")
	}
	fact := item("fact", "x", now)
	if !fact.Promoted() {
		t.Fatal("a fact is not promoted (openapi: 그 밖은 항상 true)")
	}
	old := item("lesson", "old", now.Add(-LessonHalfLife-time.Second))
	old.SupportCount = 5
	young := item("lesson", "young", now.Add(-LessonHalfLife+time.Hour))
	young.SupportCount = 2
	if Rendered(old, now) {
		t.Fatal("(31 days) a lesson older than 30 days is still rendered")
	}
	if !Rendered(young, now) {
		t.Fatal("(29 days) a lesson inside 30 days is not rendered")
	}
	// openapi v0.3.13: the half-life counts from last_reinforced_at.
	reinforced := now.Add(-time.Hour)
	old.LastReinforcedAt = &reinforced
	if !Rendered(old, now) {
		t.Fatal("(reinforced) an old lesson reinforced an hour ago is not rendered")
	}
	// The fact is not subject to the half-life.
	if !Rendered(item("fact", "x", now.Add(-365*24*time.Hour)), now) {
		t.Fatal("(old fact) the half-life applies to a fact")
	}
	sup := item("fact", "x", now)
	sup.Status = "superseded"
	if Rendered(sup, now) {
		t.Fatal("(superseded) a superseded item is rendered")
	}
}

// Block shape: kinds in RenderOrder, oldest first inside a kind, the line
// format of harness v0.9.18, no block when nothing is rendered.
func TestRenderShape(t *testing.T) {
	work := uuid.New()
	if got := Render(work, []*Item{item("fact", "x", now)}[:0], now, more); got != "" {
		t.Fatalf("empty ledger rendered %q", got)
	}
	hidden := item("lesson", "seen once", now)
	hidden.SupportCount = 1
	if got := Render(work, []*Item{hidden}, now, more); got != "" {
		t.Fatalf("a ledger of only unpromoted lessons rendered %q", got)
	}
	g := "given"
	f1 := item("fact", "first fact", now.Add(-2*time.Hour))
	f1.Certainty = &g
	f2 := item("fact", "second\nfact  spread", now.Add(-time.Hour))
	p := item("plan", "the plan", now)
	o := "dead_end"
	l := item("lesson", "do not scrape", now)
	l.Outcome, l.SupportCount = &o, 3
	out := Render(work, []*Item{f1, f2, p, l, hidden}, now, more)
	want := fmt.Sprintf("<mission_ledger work=%q total=4>\n", work) +
		fmt.Sprintf("- [%s] plan the plan\n", p.ID) +
		fmt.Sprintf("- [%s] fact (given) first fact\n", f1.ID) +
		fmt.Sprintf("- [%s] fact second fact spread\n", f2.ID) +
		fmt.Sprintf("- [%s] lesson (dead_end, support 3) do not scrape\n", l.ID) +
		"</mission_ledger>\n\n"
	if out != want {
		t.Fatalf("render:\n%s\nwant:\n%s", out, want)
	}
}

// The per-kind bounds: past RenderKindMaxItems lines the NEWEST are kept and
// the kind ends with the overflow line; RenderKindMaxChars cuts long items
// before the count does. Other kinds are not affected by one kind's overflow.
//
// 회귀 주입: RenderKindMaxItems 를 1000 으로 두면 (items), RenderKindMaxChars 검사를
// 지우면 (chars), 오래된 것부터 남기면 (newest kept) FAIL.
func TestRenderBounds(t *testing.T) {
	work := uuid.New()
	var its []*Item
	for i := 0; i < 20; i++ {
		its = append(its, item("fact", fmt.Sprintf("fact-%02d", i), now.Add(time.Duration(i)*time.Minute)))
	}
	its = append(its, item("assignment", "R owns the parser", now))
	out := Render(work, its, now, more)
	if n := strings.Count(out, "] fact "); n != RenderKindMaxItems {
		t.Fatalf("(items) %d fact lines, want %d:\n%s", n, RenderKindMaxItems, out)
	}
	if !strings.Contains(out, "… and 5 more — `colab memory get --kind fact`\n") {
		t.Fatalf("(items) overflow line missing:\n%s", out)
	}
	if strings.Contains(out, "fact-00") || !strings.Contains(out, "fact-19") || !strings.Contains(out, "fact-05") {
		t.Fatalf("(newest kept) wrong facts kept:\n%s", out)
	}
	if strings.Index(out, "fact-05") > strings.Index(out, "fact-19") {
		t.Fatalf("(order) kept facts are not oldest first:\n%s", out)
	}
	if !strings.Contains(out, "R owns the parser") || !strings.Contains(out, "total=21") {
		t.Fatalf("another kind was cut by fact's overflow:\n%s", out)
	}

	var long []*Item
	for i := 0; i < 10; i++ {
		long = append(long, item("progress", strings.Repeat("가", 290)+fmt.Sprintf("%02d", i), now.Add(time.Duration(i)*time.Minute)))
	}
	out = Render(work, long, now, more)
	lines := strings.Count(out, "] progress ")
	if lines >= 10 || lines == 0 {
		t.Fatalf("(chars) %d long progress lines kept — the char bound did not cut", lines)
	}
	if !strings.Contains(out, fmt.Sprintf("… and %d more — `colab memory get --kind progress`", 10-lines)) {
		t.Fatalf("(chars) overflow line missing:\n%s", out)
	}
	body := between(out, ">\n", "… and")
	if n := len([]rune(body)); n > RenderKindMaxChars {
		t.Fatalf("(chars) kind body is %d characters, over %d", n, RenderKindMaxChars)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}

// KindAllowed is the 403 memory_kind_forbidden table: plan · progress are
// the lead's (and a person's); the other four are everyone's.
func TestKindAllowed(t *testing.T) {
	for _, role := range []string{"researcher", "writer", "engineer", "reviewer", "custom"} {
		for _, k := range []string{"plan", "progress"} {
			if KindAllowed(role, k) {
				t.Errorf("%s may write %s", role, k)
			}
		}
		for _, k := range []string{"fact", "assignment", "open_question", "lesson"} {
			if !KindAllowed(role, k) {
				t.Errorf("%s may not write %s", role, k)
			}
		}
	}
	for _, k := range []string{"plan", "progress", "fact"} {
		if !KindAllowed("lead", k) || !KindAllowed("", k) {
			t.Errorf("lead/person may not write %s", k)
		}
	}
}
