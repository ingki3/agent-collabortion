package apperr

import (
	"errors"
	"net/http"
	"regexp"
	"testing"
)

var hangul = regexp.MustCompile(`[가-힣]`)

func TestJosa(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"세션", "세션을"},        // 받침 ㄴ
		{"작업 폴더", "작업 폴더를"},  // 받침 없음
		{"Lead", "Lead을(를)"}, // 한글이 아니면 둘 다
		{"", "을(를)"},
	} {
		if got := Josa(c.in, "을", "를"); got != c.want {
			t.Errorf("Josa(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Josa("Researcher", "이", "가"); got != "Researcher이(가)" {
		t.Errorf("Josa(agent name) = %q", got)
	}
}

// TestJosaRo is FR-2.1.2 「…(으)로 바꿨습니다」: ㄹ 받침은 「로」(v0.19.4).
func TestJosaRo(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"결제팀", "결제팀으로"},     // 받침 ㅁ
		{"결제·정산", "결제·정산으로"}, // 받침 ㄴ
		{"리서치", "리서치로"},      // 받침 없음
		{"서울", "서울로"},        // ㄹ 받침
		{"STO", "STO(으)로"},   // 한글이 아니면 둘 다
		{"", "(으)로"},
	} {
		if got := JosaRo(c.in); got != c.want {
			t.Errorf("JosaRo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestJosaSpoken is 리뷰 B1: a sentence the screen reads aloud picks ONE
// particle, by the word's final SOUND. The rows are the names this workspace
// actually has (Designer · Researcher · Writer · Simplist · 수아) plus the
// shapes that decide the rule.
func TestJosaSpoken(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// 한글 — Josa 와 같은 받침 규칙.
		{"기획팀", "기획팀이"},
		{"수아", "수아가"},
		{"민지", "민지가"},
		// 라틴 — l·m·n 으로 끝나면 받침(빌·샘·사이먼), 나머지는 모음 꼬리.
		{"Designer", "Designer가"},     // 디자이너
		{"Researcher", "Researcher가"}, // 리서처
		{"Writer", "Writer가"},         // 라이터
		{"Simplist", "Simplist가"},     // 심플리스트
		{"Lead", "Lead가"},             // 리드
		{"Bill", "Bill이"},             // 빌
		{"Sam", "Sam이"},               // 샘
		{"Simon", "Simon이"},           // 사이먼
		// 꼬리를 괄호·따옴표가 가려도 발음되는 글자로 정한다.
		{"민지(Director)", "민지(Director)가"},
		{"「타이어 점검」", "「타이어 점검」이"},
		{"「자료 정리」", "「자료 정리」가"},
		// 숫자는 한자음: 1 일 · 3 삼 · 6 육 · 7 칠 · 8 팔 받침, 2 이 · 4 사 · 5 오 · 9 구 없음.
		{"28", "28이"},
		{"29", "29가"},
		{"v2", "v2가"},
		// 읽을 꼬리가 없으면 Josa 의 괄호 형태로 물러난다.
		{"…", "…이(가)"},
		{"", "이(가)"},
	} {
		if got := JosaSpoken(c.in, "이", "가"); got != c.want {
			t.Errorf("JosaSpoken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 목적격도 같은 판정이다.
	if got := JosaSpoken("「타이어 점검」", "을", "를"); got != "「타이어 점검」을" {
		t.Errorf("JosaSpoken 목적격 = %q", got)
	}
	// Josa 는 건드리지 않았다 — 시스템 줄은 여전히 「Lead이(가)」로 정직하게 적고,
	// 웹 목의 대조 테스트(lib/mock/server-wording)가 그 규칙을 잠근다.
	if got := Josa("Lead", "이", "가"); got != "Lead이(가)" {
		t.Errorf("Josa 가 바뀌었다: %q", got)
	}
}

func TestNotFoundSpeaksTheScreensLanguage(t *testing.T) {
	p := NotFound("session")
	if p.Status != http.StatusNotFound || p.Code != "not_found" {
		t.Fatalf("status/code = %d/%s", p.Status, p.Code)
	}
	if p.Detail != "방을 찾을 수 없습니다" {
		t.Errorf("detail = %q", p.Detail)
	}
	if p.Title != "찾을 수 없음" {
		t.Errorf("title = %q", p.Title)
	}
	for k, v := range NotFoundNouns {
		if !hangul.MatchString(v) {
			t.Errorf("NotFoundNouns[%q] = %q is not Korean", k, v)
		}
	}
}

func TestTitlesAreKorean(t *testing.T) {
	for _, s := range []int{400, 401, 403, 404, 409, 410, 413, 422, 429, 500, 501, 418, 599} {
		if !hangul.MatchString(Title(s)) {
			t.Errorf("Title(%d) = %q", s, Title(s))
		}
	}
}

func TestInternalKeepsTheCauseOutOfTheSentence(t *testing.T) {
	p := Internal(errors.New("pq: connection refused"))
	if !hangul.MatchString(p.Detail) || p.Extra["cause"] != "pq: connection refused" {
		t.Errorf("detail=%q extra=%v", p.Detail, p.Extra)
	}
	if As(errors.New("x")).Status != 500 {
		t.Error("As(plain error) should be 500")
	}
}

func TestStatusLabel(t *testing.T) {
	if StatusLabel("active") != "진행 중" || StatusLabel("waiting_human") != "사람 확인 대기" {
		t.Error("known statuses map to the badge words")
	}
	if StatusLabel("weird") != "weird" {
		t.Error("unknown status passes through")
	}
}
