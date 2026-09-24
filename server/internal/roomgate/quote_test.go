package roomgate

import (
	"strings"
	"testing"
)

// TestQuote is openapi 0.2.10 card.quote: the trigger message on one line.
func TestQuote(t *testing.T) {
	long := strings.Repeat("가", QuoteMax+5)
	for _, c := range []struct{ in, want string }{
		{"한 줄", "한 줄"},
		{"\n\n  첫 줄  \n둘째 줄", "첫 줄"},
		{"   \n\t", ""},
		{long, strings.Repeat("가", QuoteMax) + "…"},
	} {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
