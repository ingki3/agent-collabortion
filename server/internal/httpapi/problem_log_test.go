package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// TestWriteProblemLogs5xxCause — the Go error behind a 500 is in the body as
// `cause` (PR #192 review (3)); NN6 wants it in the server log too, so whoever
// reads the log sees the same text the client got. 4xx stay quiet: a wrong
// password is not an incident.
func TestWriteProblemLogs5xxCause(t *testing.T) {
	var buf bytes.Buffer
	prev := problemLog
	problemLog = slog.New(slog.NewTextHandler(&buf, nil))
	t.Cleanup(func() { problemLog = prev })

	w := httptest.NewRecorder()
	writeProblem(w, apperr.Internal(errors.New(`pq: duplicate key value violates unique constraint "session_pkey"`)))
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	got := buf.String()
	for _, want := range []string{"level=ERROR", "status=500", "code=internal", "session_pkey"} {
		if !strings.Contains(got, want) {
			t.Errorf("5xx log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(w.Body.String(), "pq:") == false {
		t.Errorf("cause left the body — the log is in addition to it, not instead:\n%s", w.Body.String())
	}

	buf.Reset()
	writeProblem(httptest.NewRecorder(), apperr.Forbidden("forbidden", "권한이 없습니다"))
	writeProblem(httptest.NewRecorder(), apperr.NotFound("session"))
	if buf.Len() != 0 {
		t.Errorf("4xx wrote to the log:\n%s", buf.String())
	}
}
