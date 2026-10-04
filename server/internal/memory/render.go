package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// The turn prompt's <mission_ledger> (harness v0.9.18): grouped by kind,
// active items only, oldest first within a kind. A kind that has more than
// fits keeps its NEWEST items (the latest progress and facts are the ones a
// turn acts on) and ends with the overflow line. The bounds are what keep the
// block from regrowing the turn prompt the way ② did: at most
// RenderKindMaxItems lines and RenderKindMaxChars characters per kind, so the
// whole block is under 6 × RenderKindMaxChars plus its frame.
const (
	RenderKindMaxItems = 15
	RenderKindMaxChars = 2400
)

// RenderOrder is the order the kinds are written in: the Lead's plan and
// progress first (what the mission is doing), then the facts and who does
// what, then what is still open, then the lessons.
var RenderOrder = []string{
	string(gen.MemoryKindPlan), string(gen.MemoryKindProgress), string(gen.MemoryKindFact),
	string(gen.MemoryKindAssignment), string(gen.MemoryKindOpenQuestion), string(gen.MemoryKindLesson),
}

// Rendered reports whether an active item reaches the turn prompt at now:
// every active item does, except a lesson that is not promoted
// (support_count < 2) or is older than LessonHalfLife (PRD FR-4.6 3).
func Rendered(it *Item, now time.Time) bool {
	if it.Status != string(gen.MemoryStatusActive) {
		return false
	}
	if it.Kind != string(gen.MemoryKindLesson) {
		return true
	}
	return it.Promoted() && now.Sub(it.CreatedAt) <= LessonHalfLife
}

// Line is one item as the block writes it:
// `- [<id>] <kind> (<certainty>) <content>` for a fact,
// `- [<id>] lesson (<outcome>, support N) <content>` for a lesson,
// `- [<id>] <kind> <content>` for the rest. Line breaks in the content are
// folded so one item is one line.
func Line(it *Item) string {
	var tag string
	switch it.Kind {
	case string(gen.MemoryKindFact):
		if it.Certainty != nil {
			tag = " (" + *it.Certainty + ")"
		}
	case string(gen.MemoryKindLesson):
		parts := []string{}
		if it.Outcome != nil {
			parts = append(parts, *it.Outcome)
		}
		parts = append(parts, fmt.Sprintf("support %d", it.SupportCount))
		tag = " (" + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("- [%s] %s%s %s\n", it.ID, it.Kind, tag, strings.Join(strings.Fields(it.Content), " "))
}

// Render writes <mission_ledger> from the mission's active items (List with
// status active, oldest first). overflow is this surface's pointer for the
// rest of one kind, given the kind. Empty when nothing is rendered — the
// block is absent, not empty.
func Render(workID uuid.UUID, items []*Item, now time.Time, overflow func(kind string) string) string {
	byKind := map[string][]*Item{}
	total := 0
	for _, it := range items {
		if !Rendered(it, now) {
			continue
		}
		byKind[it.Kind] = append(byKind[it.Kind], it)
		total++
	}
	if total == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<mission_ledger work=%q total=%d>\n", workID.String(), total)
	for _, k := range RenderOrder {
		its := byKind[k]
		if len(its) == 0 {
			continue
		}
		// Newest first until a bound is hit, then written oldest first.
		keep := 0
		chars := 0
		for i := len(its) - 1; i >= 0; i-- {
			n := utf8.RuneCountInString(Line(its[i]))
			if keep == RenderKindMaxItems || chars+n > RenderKindMaxChars {
				break
			}
			keep++
			chars += n
		}
		for _, it := range its[len(its)-keep:] {
			b.WriteString(Line(it))
		}
		if more := len(its) - keep; more > 0 {
			fmt.Fprintf(&b, "… and %d more — %s\n", more, overflow(k))
		}
	}
	b.WriteString("</mission_ledger>\n\n")
	return b.String()
}
