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
		{"작업 폴더", "작업 폴더를"}, // 받침 없음
		{"Lead", "Lead을(를)"},   // 한글이 아니면 둘 다
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

func TestNotFoundSpeaksTheScreensLanguage(t *testing.T) {
	p := NotFound("session")
	if p.Status != http.StatusNotFound || p.Code != "not_found" {
		t.Fatalf("status/code = %d/%s", p.Status, p.Code)
	}
	if p.Detail != "세션을 찾을 수 없습니다" {
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
