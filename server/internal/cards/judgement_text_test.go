package cards

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #406 리뷰 NN1 · NN5 — the acceptCard comment check.
// 회귀 주입: invisibleRune 을 unicode.IsSpace 하나로 되돌리면 (invisible-only) FAIL;
// 한글 채움 문자 case 를 빼면 (hangul-filler) FAIL; JudgementCommentMax 비교를 바이트로
// 바꾸면(handlers_cards.go) httpapi 의 (multibyte-boundary) FAIL.
func TestJudgementText(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":          "",
		"spaces":         " \t\n\r\u3000\u00a0",
		"zwsp":           "\u200b\u200b",
		"bom":            "\ufeff",
		"word-joiner":    "\u2060",
		"zwj-zwnj":       "\u200d\u200c",
		"bidi-marks":     "\u200e\u200f\u202a\u202c",
		"soft-hyphen":    "\u00ad",
		"nel":            "\u0085",
		"hangul-filler":  "\u3164",
		"hangul-fillers": "\u115f\u1160\uffa0",
		"braille-blank":  "\u2800",
		"invisible-only": " \u200b\ufeff\u2060\u3164 \u200b",
		"controls":       "\x00\x07\x1b",
	} {
		if text, ok := JudgementText(raw); ok {
			t.Errorf("(%s) %q passed as %q — nothing visible", name, raw, text)
		}
	}
	for name, c := range map[string]struct{ raw, want string }{
		"plain":          {"확인했다", "확인했다"},
		"trim-invisible": {"\ufeff\u200b 로그 확인 \u2060\u3164", "로그 확인"},
		"inner-kept":     {"로그\u200b확인", "로그\u200b확인"},
		"one-visible":    {"\u3164.\u3164", "."},
		"emoji":          {"\u200b👍\u200b", "👍"},
	} {
		got, ok := JudgementText(c.raw)
		if !ok || got != c.want {
			t.Errorf("(%s) JudgementText(%q) = %q, %v; want %q", name, c.raw, got, ok, c.want)
		}
	}
	// 600 is characters: 600 한글 = 1800 bytes is within the bound.
	s := strings.Repeat("가", JudgementCommentMax)
	if utf8.RuneCountInString(s) != JudgementCommentMax || len(s) != 3*JudgementCommentMax {
		t.Fatalf("fixture: %d runes %d bytes", utf8.RuneCountInString(s), len(s))
	}
}
