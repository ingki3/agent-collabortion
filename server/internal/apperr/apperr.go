// Package apperr is the RFC 9457 problem type services return so the HTTP
// layer can write exact status/code pairs (openapi.md §1) without each
// package importing httpapi.
//
// Every sentence a constructor here puts in Detail / Title / Errors[].Message
// is read by a person on the web (Problem.detail is the tooltip and the toast,
// errors[].message sits under the form field) — it is written in the language
// of the screens and in their words (COMPONENTS §8.4, S-67). internal/wording
// locks that: an English sentence or an internal noun (lane · task · runtime …)
// in one of these arguments fails `go test ./internal/wording`.
package apperr

import (
	"errors"
	"net/http"
)

type Problem struct {
	Status int
	Code   string
	Title  string
	Detail string
	Errors []FieldError
	Extra  map[string]any
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

// Title is the short RFC 9457 summary of the status, in the screens' language.
// The web shows it only when detail is missing, so it stays a plain noun.
func Title(status int) string {
	if t, ok := titles[status]; ok {
		return t
	}
	if status >= 500 {
		return "서버 오류"
	}
	if status >= 400 {
		return "요청 오류"
	}
	return http.StatusText(status)
}

var titles = map[int]string{
	http.StatusBadRequest:            "잘못된 요청",
	http.StatusUnauthorized:          "로그인 필요",
	http.StatusForbidden:             "권한 없음",
	http.StatusNotFound:              "찾을 수 없음",
	http.StatusConflict:              "지금은 할 수 없음",
	http.StatusGone:                  "더 이상 쓸 수 없음",
	http.StatusRequestEntityTooLarge: "너무 큼",
	http.StatusUnprocessableEntity:   "입력값 확인 필요",
	http.StatusTooManyRequests:       "요청이 너무 잦음",
	http.StatusInternalServerError:   "서버 오류",
	http.StatusNotImplemented:        "아직 지원하지 않음",
}

func New(status int, code, detail string) *Problem {
	return &Problem{Status: status, Code: code, Title: Title(status), Detail: detail}
}

func Unauthorized(code, detail string) *Problem { return New(http.StatusUnauthorized, code, detail) }
func Forbidden(code, detail string) *Problem    { return New(http.StatusForbidden, code, detail) }
func Conflict(code, detail string) *Problem     { return New(http.StatusConflict, code, detail) }
func Gone(code, detail string) *Problem         { return New(http.StatusGone, code, detail) }

// NotFoundNouns maps the resource key a caller passes to NotFound onto the
// word the screens use for it. A key missing here is a test failure
// (internal/wording), not a silent English fallback.
var NotFoundNouns = map[string]string{
	"session":            "세션",
	"user":               "사용자",
	"workspace":          "워크스페이스",
	"workspace_settings": "워크스페이스 설정",
	"invite":             "초대",
	"agent":              "에이전트",
	"agent_template":     "에이전트 템플릿",
	"profile":            "프로파일",
	"participant":        "참여자",
	"artifact":           "아티팩트",
	"workdir":            "작업 폴더",
	"runtime":            "컴퓨터",
	"pairing":            "연결 코드",
	"lane":               "작업 줄기",
	"task":               "할 일",
	"inbox_item":         "받은 요청",
	"hitl_request":       "확인 요청",
}

// NotFound is the 404 for a resource the caller named. `what` is a key of
// NotFoundNouns; the sentence never reveals whether the row exists in another
// workspace (openapi NotFound).
func NotFound(what string) *Problem {
	noun, ok := NotFoundNouns[what]
	if !ok {
		noun = what
	}
	return New(http.StatusNotFound, "not_found", Josa(noun, "을", "를")+" 찾을 수 없습니다")
}

func Validation(errs ...FieldError) *Problem {
	p := New(http.StatusUnprocessableEntity, "validation_failed", "입력값을 확인해 주세요")
	p.Errors = errs
	return p
}
func Field(field, code, msg string) FieldError {
	return FieldError{Field: field, Code: code, Message: msg}
}

// Internal is a 500. The person sees one sentence they can act on; the Go
// error text — which is for whoever reads the log, not for them — rides
// along as `cause` (Problem allows additional properties) so an e2e run or a
// bug report can still quote it.
func Internal(err error) *Problem {
	p := New(http.StatusInternalServerError, "internal", "서버에서 문제가 생겼습니다 — 잠시 뒤 다시 시도해 주세요")
	if err != nil {
		p.Extra = map[string]any{"cause": err.Error()}
	}
	return p
}

// As converts any error to a Problem (unknown errors become 500).
func As(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return Internal(err)
}

// Josa appends the Korean particle that follows `word`: `with` after a
// syllable that ends in a consonant (받침), `without` otherwise. A word that
// does not end in Hangul (an agent name, a path) gets both, "이(가)" style, so
// the sentence still reads.
func Josa(word, with, without string) string {
	r := []rune(word)
	if len(r) == 0 {
		return word + with + "(" + without + ")"
	}
	last := r[len(r)-1]
	if last < 0xAC00 || last > 0xD7A3 {
		return word + with + "(" + without + ")"
	}
	if (last-0xAC00)%28 == 0 {
		return word + without
	}
	return word + with
}

// StatusLabel is a session or lane status enum in the words of the badges the
// screens draw (web/lib/session-label.ts · components/LaneCard.tsx), for the
// "(현재 상태: …)" tail of a 409. An enum value this table does not know is
// returned as is rather than hidden.
func StatusLabel(status string) string {
	if l, ok := statusLabels[status]; ok {
		return l
	}
	return status
}

var statusLabels = map[string]string{
	"draft":         "시작 전",
	"active":        "진행 중",
	"paused":        "일시정지",
	"completed":     "완료",
	"cancelled":     "종료됨",
	"queued":        "대기 중",
	"running":       "실행 중",
	"blocked":       "답을 기다림",
	"waiting_human": "사람 확인 대기",
	"done":          "완료",
	"failed":        "실패",
}
