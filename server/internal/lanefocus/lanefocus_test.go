package lanefocus

// T-FOCUS (PRD FR-3.1.5 item 3 · openapi v0.3.8 LaneFocus): the derived
// sentence is a wording table — its three shapes are locked here byte for
// byte, and the trimming rules (120 runes, the trigger's first 40) each have a
// row that fails when the rule is switched off.
//
// 회귀 주입: Truncate 의 cut 을 지우면 (120) FAIL; Quote 의 cut 을 지우면 (40)
// FAIL; Sentence 의 Delegated 가지를 지우면 (delegate) FAIL; Quote 의
// leadingMentionsRe 치환을 지우면 (lead mention) FAIL; Subject 의 받침 판정을
// 지우면 (josa) FAIL; FocusRequestSentence 문구를 바꾸면 (lock) FAIL.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSentenceTable(t *testing.T) {
	rows := []struct {
		name string
		in   Trigger
		want string
	}{
		{"request · agent (lock)", Trigger{AuthorType: "agent", AuthorName: "Lead", Content: "BGM v2 를 16분음표 격자로"},
			"@Lead 의 「BGM v2 를 16분음표 격자로」 요청을 처리하고 있습니다"},
		{"request · person", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "코너 미끄러짐을 고쳐 주세요"},
			"Simplist 의 「코너 미끄러짐을 고쳐 주세요」 요청을 처리하고 있습니다"},
		{"lead mention", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "[@R](mention://agent/1e0c) [@W](mention://agent/2f) 커브를 봐 주세요"},
			"Simplist 의 「커브를 봐 주세요」 요청을 처리하고 있습니다"},
		{"inner mention kept as @name", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "커브는 [@W](mention://agent/2f) 와 같이"},
			"Simplist 의 「커브는 @W 와 같이」 요청을 처리하고 있습니다"},
		{"delegate (the server's link + brief)", Trigger{AuthorType: "agent", AuthorName: "Lead", Content: "[@R](mention://agent/1) 타이어 접지 한계 공식을 점검", Delegated: true},
			"@Lead 가 맡긴 「타이어 접지 한계 공식을 점검」를 하고 있습니다"},
		{"josa · 받침", Trigger{AuthorType: "agent", AuthorName: "기획팀", Content: "정리", Delegated: true},
			"@기획팀이 맡긴 「정리」를 하고 있습니다"},
		{"josa · no 받침", Trigger{AuthorType: "agent", AuthorName: "리더", Content: "정리", Delegated: true},
			"@리더 가 맡긴 「정리」를 하고 있습니다"},
		{"system notice", Trigger{AuthorType: "system", Content: "위임한 작업 2건이 모두 끝났습니다"},
			"알림 「위임한 작업 2건이 모두 끝났습니다」에 따라 작업하고 있습니다"},
		{"newlines and emphasis collapse", Trigger{AuthorType: "user", AuthorName: "D", Content: "**급함**\n\n`빌드` 깨짐"},
			"D 의 「급함 빌드 깨짐」 요청을 처리하고 있습니다"},
		{"empty trigger → no sentence", Trigger{AuthorType: "user", AuthorName: "D", Content: "[@R](mention://agent/1)"}, ""},
	}
	for _, r := range rows {
		if got := Sentence(r.in); got != r.want {
			t.Errorf("%s:\n got %q\nwant %q", r.name, got, r.want)
		}
	}
}

// (40) the trigger is quoted by its first 40 runes, not bytes.
func TestQuoteCutsAtFortyRunes(t *testing.T) {
	long := strings.Repeat("가", 55)
	q := Quote(long)
	if n := utf8.RuneCountInString(q); n != TriggerRunes {
		t.Fatalf("quote = %d runes %q, want %d", n, q, TriggerRunes)
	}
	if !strings.HasSuffix(q, "…") {
		t.Fatalf("a cut quote must say it was cut: %q", q)
	}
	if Quote("짧은 요청") != "짧은 요청" {
		t.Fatal("a short trigger is quoted whole")
	}
}

// (120) the stored sentence — declared or derived — never exceeds 120 runes.
func TestTruncateAt120Runes(t *testing.T) {
	s := strings.Repeat("문", 200)
	got := Truncate(s)
	if n := utf8.RuneCountInString(got); n != MaxRunes {
		t.Fatalf("truncated = %d runes, want %d", n, MaxRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("cut sentence has no ellipsis: %q", got)
	}
	exact := strings.Repeat("a", MaxRunes)
	if Truncate(exact) != exact {
		t.Fatal("exactly 120 runes must not be cut")
	}
	// A derived sentence built from a long author name still fits.
	d := Sentence(Trigger{AuthorType: "user", AuthorName: strings.Repeat("이", 100), Content: strings.Repeat("나", 100)})
	if utf8.RuneCountInString(d) > MaxRunes {
		t.Fatalf("derived sentence = %d runes", utf8.RuneCountInString(d))
	}
}
