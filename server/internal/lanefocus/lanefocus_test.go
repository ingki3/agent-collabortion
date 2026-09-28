package lanefocus

// T-FOCUS (PRD FR-3.1.5 item 3 · openapi v0.3.8 LaneFocus): the derived
// sentence is a wording table — its four shapes are locked here byte for
// byte, and the trimming rules (120 runes, the trigger's first 40) each have a
// row that fails when the rule is switched off.
//
// **조사는 읽는 대로 붙인다**(리뷰 B1, Lead 판정). 이 표는 실제로 쓰는 이름으로
// 채운다 — 사람(민지 · Simplist · 수아)과 에이전트(Designer · Researcher ·
// Writer · Lead · 기획팀), 받침 있는 인용과 없는 인용. 전에는 지금 출력을 그대로
// 기대값에 박아 틀린 문장(「@리더 가 … 「정리」를」)을 굳히고 있었다.
//
// 회귀 주입: Truncate 의 cut 을 지우면 (120) FAIL; Quote 의 cut 을 지우면 (40)
// FAIL; Sentence 의 Delegated 가지를 지우면 (delegate) FAIL; Quote 의
// leadingMentionsRe 치환을 지우면 (lead mention) FAIL; apperr.JosaSpoken 의
// 라틴 갈래(l·m·n)를 지우면 (josa 이름) FAIL; particle 을 고정 「를」로 되돌리면
// (josa 목적격) FAIL; FocusRequestSentence 의 「의」 앞에 공백을 넣으면 (lock)
// FAIL; who == "" 갈래를 알림 문장으로 되돌리면 (NN3) FAIL.

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
		// 요청 — 「〈이름〉의」, 공백 없이(lock).
		{"request · person 한글", Trigger{AuthorType: "user", AuthorName: "민지", Content: "커브를 고쳐 주세요"},
			"민지의 「커브를 고쳐 주세요」 요청을 처리하고 있습니다"},
		{"request · person 영문", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "코너 미끄러짐을 고쳐 주세요"},
			"Simplist의 「코너 미끄러짐을 고쳐 주세요」 요청을 처리하고 있습니다"},
		{"request · 이름에 괄호", Trigger{AuthorType: "user", AuthorName: "민지(Director)", Content: "커브를 고쳐 주세요"},
			"민지(Director)의 「커브를 고쳐 주세요」 요청을 처리하고 있습니다"},
		{"request · agent (lock)", Trigger{AuthorType: "agent", AuthorName: "Lead", Content: "BGM v2 를 16분음표 격자로"},
			"@Lead의 「BGM v2 를 16분음표 격자로」 요청을 처리하고 있습니다"},
		{"lead mention", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "[@R](mention://agent/1e0c) [@W](mention://agent/2f) 커브를 봐 주세요"},
			"Simplist의 「커브를 봐 주세요」 요청을 처리하고 있습니다"},
		{"inner mention kept as @name", Trigger{AuthorType: "user", AuthorName: "Simplist", Content: "커브는 [@W](mention://agent/2f) 와 같이"},
			"Simplist의 「커브는 @W 와 같이」 요청을 처리하고 있습니다"},

		// 위임 — 주격은 이름의 발음(josa 이름), 목적격은 인용의 발음(josa 목적격).
		{"delegate · 영문 무받침 + 받침 인용", Trigger{AuthorType: "agent", AuthorName: "Designer", Content: "[@R](mention://agent/1) 타이어 접지 한계 공식을 점검", Delegated: true},
			"@Designer가 맡긴 「타이어 접지 한계 공식을 점검」을 하고 있습니다"},
		{"delegate · 영문 무받침 + 무받침 인용", Trigger{AuthorType: "agent", AuthorName: "Writer", Content: "[@R](mention://agent/1) 자료 정리", Delegated: true},
			"@Writer가 맡긴 「자료 정리」를 하고 있습니다"},
		{"delegate · 영문 n 받침", Trigger{AuthorType: "agent", AuthorName: "Simon", Content: "자료 정리", Delegated: true},
			"@Simon이 맡긴 「자료 정리」를 하고 있습니다"},
		{"delegate · 한글 무받침", Trigger{AuthorType: "agent", AuthorName: "수아", Content: "타이어 점검", Delegated: true},
			"@수아가 맡긴 「타이어 점검」을 하고 있습니다"},
		{"delegate · 한글 받침", Trigger{AuthorType: "agent", AuthorName: "기획팀", Content: "정리", Delegated: true},
			"@기획팀이 맡긴 「정리」를 하고 있습니다"},
		{"delegate · 숫자로 끝나는 인용", Trigger{AuthorType: "agent", AuthorName: "Researcher", Content: "테스트 28항목을 28", Delegated: true},
			"@Researcher가 맡긴 「테스트 28항목을 28」을 하고 있습니다"},

		// 나머지 갈래.
		{"system notice", Trigger{AuthorType: "system", Content: "위임한 작업 2건이 모두 끝났습니다"},
			"알림 「위임한 작업 2건이 모두 끝났습니다」에 따라 작업하고 있습니다"},
		{"NN3 · 이름 없는 사람 메시지는 알림이 아니다", Trigger{AuthorType: "user", AuthorName: "", Content: "커브를 고쳐 주세요"},
			"받은 「커브를 고쳐 주세요」 요청을 처리하고 있습니다"},
		{"newlines and emphasis collapse", Trigger{AuthorType: "user", AuthorName: "D", Content: "**급함**\n\n`빌드` 깨짐"},
			"D의 「급함 빌드 깨짐」 요청을 처리하고 있습니다"},
		{"empty trigger → no sentence", Trigger{AuthorType: "user", AuthorName: "D", Content: "[@R](mention://agent/1)"}, ""},
	}
	for _, r := range rows {
		if got := Sentence(r.in); got != r.want {
			t.Errorf("%s:\n got %q\nwant %q", r.name, got, r.want)
		}
	}
}

// 사람이 읽는 줄에 「이(가)」 같은 괄호 조사나 조사 앞 공백이 있으면 안 된다 —
// 표의 기대값을 한 줄씩 고치는 대신 규칙으로 잡는다(리뷰 B1 ①②).
func TestSentencesReadAsSpeech(t *testing.T) {
	for _, in := range []Trigger{
		{AuthorType: "user", AuthorName: "민지", Content: "커브"},
		{AuthorType: "user", AuthorName: "Simplist", Content: "커브"},
		{AuthorType: "agent", AuthorName: "Designer", Content: "타이어 점검", Delegated: true},
		{AuthorType: "agent", AuthorName: "기획팀", Content: "정리", Delegated: true},
		{AuthorType: "agent", AuthorName: "수아", Content: "정리", Delegated: true},
	} {
		got := Sentence(in)
		for _, bad := range []string{"이(가)", "을(를)", "(으)로", " 의 ", " 가 ", " 이 ", " 를 ", " 을 "} {
			if strings.Contains(got, bad) {
				t.Errorf("%q 에 %q — 사람이 읽는 줄에는 괄호 조사도, 조사 앞 공백도 없다", got, bad)
			}
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
