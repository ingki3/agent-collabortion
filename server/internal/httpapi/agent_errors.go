package httpapi

// Agent-facing error sentences (T-AGENTFIX B5).
//
// The binder's and the idempotency gate's `detail` is written for a person at
// a screen: 「화면을 새로고침한 뒤 다시 시도해 주세요」. An agent calling with its
// TaskToken has no screen to refresh — 실측(게임 제작 방): Writer 가
// colab_artifact_get 에 아티팩트 이름을 넣고 이 문장만 받아, 무엇을 고쳐야
// 하는지 몰랐다. So for a TaskToken caller, and ONLY for one, these problems
// carry a sentence the agent can act on: which argument, what it must be, what
// it sent. The person's sentences are unchanged byte for byte — they are what
// the web shows, and a browser session never carries a TaskToken.
//
// Code and status do not change: the CLI maps them to exit codes
// (colab-cli.md §2), and an agent that branches on `code` sees the same one.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// isTaskCaller: the request was authenticated with a TaskToken (an agent's
// turn), not a person's session or a daemon.
func isTaskCaller(ctx context.Context) bool {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	// `p.User == nil` is a deliberate redundant guard: principal.go resolves a
	// Bearer with an exclusive switch, so Task and User never stand together
	// today. If that ever changes, a person's session keeps the screen sentence.
	return p != nil && p.Task != nil && p.User == nil
}

// bindParam names the parameter the generated binder rejected, and whether it
// was missing rather than malformed.
func bindParam(err error) (name string, missing bool) {
	var inv *gen.InvalidParamFormatError
	var req *gen.RequiredParamError
	var hdr *gen.RequiredHeaderError
	var unm *gen.UnmarshalingParamError
	var many *gen.TooManyValuesForParamError
	switch {
	case errors.As(err, &inv):
		return inv.ParamName, false
	case errors.As(err, &req):
		return req.ParamName, true
	case errors.As(err, &hdr):
		return hdr.ParamName, true
	case errors.As(err, &unm):
		return unm.ParamName, false
	case errors.As(err, &many):
		return many.ParamName, false
	}
	return "", false
}

// sentValue is what the caller put in that parameter — path, then query,
// then header — cut so a pasted document does not come back in the error.
func sentValue(r *http.Request, name string) string {
	v := r.PathValue(name)
	if v == "" {
		v = r.URL.Query().Get(name)
	}
	if v == "" {
		v = r.Header.Get(name)
	}
	if rs := []rune(v); len(rs) > 80 {
		v = string(rs[:80]) + "…"
	}
	return v
}

// isIDParam: every `…Id` / `…_id` parameter in openapi is format uuid.
func isIDParam(name string) bool {
	return strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "_id")
}

// agentBindSentence is the sentence an agent reads for a rejected parameter.
func agentBindSentence(name string, missing bool, sent string) string {
	switch {
	case name == "":
		return "요청 인자 형식이 올바르지 않습니다 — colab 명령의 인자를 확인하세요"
	case name == "Idempotency-Key" || name == "X-Colab-Client-Seq":
		// The CLI and the MCP server set these themselves (colab-cli §1).
		return fmt.Sprintf("`%s` 헤더가 없거나 올바르지 않습니다 — colab CLI·MCP 툴이 붙이는 값이니 HTTP 를 직접 부르지 말고 colab 명령으로 호출하세요", name)
	case missing:
		return fmt.Sprintf("`%s` 인자가 필요합니다", name)
	case name == "artifactId":
		return fmt.Sprintf("artifact id(uuid)가 필요합니다 — 받은 값: %q. 아티팩트 이름이 아니라 id 입니다: 브리프의 아티팩트 줄 끝 `id …` 또는 room read 결과의 artifacts[].id 를 넣으세요", sent)
	case isIDParam(name):
		return fmt.Sprintf("`%s` 는 id(uuid)여야 합니다 — 받은 값: %q. 이름이 아니라 colab 명령이 돌려준 id 를 넣으세요", name, sent)
	case name == "limit":
		return fmt.Sprintf("`limit` 은 1~200 정수여야 합니다 — 받은 값: %q", sent)
	case name == "cursor" || name == "before" || name == "after":
		return fmt.Sprintf("`%s` 는 앞 응답이 돌려준 커서 값 그대로여야 합니다 — 받은 값: %q", name, sent)
	}
	return fmt.Sprintf("`%s` 값의 형식이 올바르지 않습니다 — 받은 값: %q", name, sent)
}

// agentBindProblem is validationFromBind's problem rewritten for a TaskToken
// caller: detail and errors[] name the parameter; the binder's own text rides
// along as `cause`.
func agentBindProblem(r *http.Request, human *Problem, err error) *Problem {
	name, missing := bindParam(err)
	msg := agentBindSentence(name, missing, sentValue(r, name))
	field := name
	if field == "" {
		field = "params"
	}
	return &Problem{Status: human.Status, Code: human.Code, Title: human.Title, Detail: msg,
		Errors: []apperr.FieldError{{Field: field, Code: human.Code, Message: msg}},
		Extra:  map[string]any{"cause": err.Error()}}
}

// agentIdempotencyReused is idempotency_key_reused for a TaskToken caller.
const agentIdempotencyReused = "같은 요청 키로 다른 내용을 보냈습니다 — 새 요청이면 새 키가 필요합니다. colab CLI·MCP 툴은 키를 스스로 만드니 HTTP 를 직접 부르지 말고 colab 명령으로 다시 호출하세요"
