package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

type Problem = apperr.Problem

// problemLog receives every 5xx writeProblem sends. The person gets one
// sentence and the Go error rides along as `cause` in the body (apperr.Internal)
// — but the body reaches only the client that asked, and a bug report is
// usually written by someone else. NewServer points this at Deps.Log.
var problemLog = slog.Default()

func writeProblem(w http.ResponseWriter, p *Problem) {
	if p.Status >= 500 {
		attrs := []any{"status", p.Status, "code", p.Code}
		if cause, ok := p.Extra["cause"]; ok {
			attrs = append(attrs, "cause", cause)
		}
		problemLog.Error("http: 5xx problem", attrs...)
	}
	body := map[string]any{
		"type":   "https://colab.dev/problems/" + p.Code,
		"title":  p.Title,
		"status": p.Status,
		"code":   p.Code,
	}
	if p.Detail != "" {
		body["detail"] = p.Detail
	}
	if len(p.Errors) > 0 {
		body["errors"] = p.Errors
	}
	for k, v := range p.Extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) { writeProblem(w, apperr.As(err)) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func notImplemented(w http.ResponseWriter, _ *http.Request, op string) {
	writeProblem(w, apperr.New(http.StatusNotImplemented, "not_implemented", "아직 지원하지 않는 기능입니다 ("+op+")"))
}

// unreadable is the 422 for a body the server could not parse. The person
// reads one sentence in the screens' language (errors[].message is what the
// signup and session forms print — COMPONENTS §8.4); the decoder's own text,
// which names the byte and the Go type, rides along as `cause` for whoever
// debugs the client.
func unreadable(field, code, msg string, err error) *Problem {
	p := apperr.Validation(apperr.Field(field, code, msg))
	if err != nil {
		p.Extra = map[string]any{"cause": err.Error()}
	}
	return p
}

const bodyUnreadable = "요청 내용을 읽을 수 없습니다 — 화면을 새로고침한 뒤 다시 시도해 주세요"

// decodeJSON reads a JSON body; a malformed body is a 422 (spec: validation).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) *Problem {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		// A bad email is the one decode failure a person causes by typing
		// (openapi_types.Email validates inside UnmarshalJSON), so it gets the
		// form's own sentence under its own field.
		if errors.Is(err, openapi_types.ErrValidationEmail) {
			return unreadable("email", "format", "이메일 주소 형식이 아닙니다", err)
		}
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return unreadable("body", "too_large", "요청이 너무 큽니다 — 4 MB 까지 보낼 수 있습니다", err)
		}
		return unreadable("body", "malformed_json", bodyUnreadable, err)
	}
	return nil
}

// limitMin/limitMax are the contract's bounds for every `limit` query
// parameter (openapi.yaml: minimum 1, maximum 200).
const (
	limitMin = 1
	limitMax = 200
)

// validateLimit is S-11. The server used to accept -1, 0 and 999999 and
// silently substitute 50: a client that asked for 500 rows got 50 and had no
// way to tell. The contract says 1..200, so anything else is a 422 — the
// service-layer clamps stay as the default for an OMITTED limit, not as a
// silent correction of a stated one.
func validateLimit(limit *int) *Problem {
	if limit == nil {
		return nil
	}
	if *limit < limitMin || *limit > limitMax {
		return apperr.Validation(apperr.Field("limit", "out_of_range",
			"한 번에 1~200개까지만 가져올 수 있습니다"))
	}
	return nil
}
