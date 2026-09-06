package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

type Problem = apperr.Problem

func writeProblem(w http.ResponseWriter, p *Problem) {
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
	writeProblem(w, apperr.New(http.StatusNotImplemented, "not_implemented", op+" is not part of P1"))
}

// decodeJSON reads a JSON body; a malformed body is a 422 (spec: validation).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) *Problem {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return apperr.Validation(apperr.Field("body", "malformed_json", err.Error()))
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
			"limit must be between 1 and 200"))
	}
	return nil
}
