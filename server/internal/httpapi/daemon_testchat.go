package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/testchat"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Test chat turns on the daemon protocol (daemon-protocol v0.8 §4.5).
//
// The four §4.2/§4.4 endpoints are shared with session tasks; taskForDaemon
// (daemon.go) hands a request here only after the task lookup failed and the
// id turned out to be a test_chat row. Nothing below touches the task tables:
// events are folded into the chat's agent turn instead of task_event, usage
// into the chat's totals instead of task_usage, and the preview goes out as
// SSE `test_chat.delta` instead of `message.delta`.
//
// The Problems written in this file are read by the daemon (its log), like
// daemon.go's — the wording lock (internal/wording) lists this file next to it.

// testChatForDaemon resolves `task_id` as a test chat for the calling runtime.
// It answers the request itself when the id is neither a task nor a chat (404,
// the same answer the task path gave before v0.8) or when the chat is fixed to
// another runtime (403).
func (s *Server) testChatForDaemon(w http.ResponseWriter, r *http.Request, d daemonCtx, id uuid.UUID) (*testchat.Row, bool) {
	tc, err := testchat.Get(r.Context(), s.DB, id)
	if err != nil {
		writeProblem(w, apperr.NotFound("task"))
		return nil, false
	}
	if tc.WorkspaceID != d.WorkspaceID || tc.RuntimeID == nil || *tc.RuntimeID != d.RuntimeID {
		writeProblem(w, apperr.Forbidden("runtime_mismatch", "this test chat is not fixed to the calling runtime"))
		return nil, false
	}
	return tc, true
}

// testChatErr maps the service's typed errors onto the same wire answers the
// task path gives (409 stale_attempt with the pending commands, 409
// invalid_transition), so a daemon needs no second error vocabulary.
func (s *Server) testChatErr(w http.ResponseWriter, r *http.Request, d daemonCtx, err error) {
	switch {
	case errors.Is(err, testchat.ErrStaleAttempt):
		s.staleAttempt(w, r, d)
	case errors.Is(err, testchat.ErrInvalidTransition):
		writeProblem(w, apperr.Conflict("invalid_transition", err.Error()))
	case errors.Is(err, testchat.ErrNotFound):
		writeProblem(w, apperr.NotFound("task"))
	default:
		writeErr(w, err)
	}
}

func (s *Server) daemonTestChatPhase(w http.ResponseWriter, r *http.Request, d daemonCtx, tc *testchat.Row, attempt int) {
	var in struct {
		Phase       string `json:"phase"`
		PGID        int    `json:"pgid"`
		WorkdirPath string `json:"workdir_path"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.TestChats.Phase(r.Context(), tc.ID, attempt, in.Phase, s.Clock.Now()); err != nil {
		s.testChatErr(w, r, d, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) daemonTestChatEvents(w http.ResponseWriter, r *http.Request, d daemonCtx, tc *testchat.Row, attempt int) {
	var in struct {
		Events []contracts.TaskEvent `json:"events"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	max, err := s.TestChats.Ingest(r.Context(), tc.ID, attempt, in.Events, s.Clock.Now())
	if err != nil {
		s.testChatErr(w, r, d, err)
		return
	}
	cmds, _ := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	writeJSON(w, http.StatusOK, map[string]any{"accepted_seq_max": max, "commands": cmds})
}

func (s *Server) daemonTestChatHeartbeat(w http.ResponseWriter, r *http.Request, d daemonCtx, tc *testchat.Row, attempt int) {
	var in struct {
		Usage   contracts.Usage `json:"usage"`
		LastSeq int             `json:"last_seq"`
		Preview json.RawMessage `json:"preview"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.TestChats.Heartbeat(r.Context(), tc.ID, attempt, in.Usage, s.Clock.Now()); err != nil {
		s.testChatErr(w, r, d, err)
		return
	}
	// §4.5: heartbeat `preview.text` → SSE `test_chat.delta` (ephemeral, S10).
	// A malformed preview is dropped without a feed note — there is no feed —
	// and never fails the heartbeat (§4.2 v0.3).
	if preview, drift := parsePreview(in.Preview); !drift && preview != nil && preview.Text != "" {
		s.Hub.PublishEphemeral(tc.WorkspaceID, nil, "test_chat.delta", map[string]any{
			"test_chat_id": tc.ID, "text": preview.Text,
		})
	}
	cmds, _ := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	writeJSON(w, http.StatusOK, map[string]any{"commands": cmds})
}

func (s *Server) daemonTestChatFinish(w http.ResponseWriter, r *http.Request, d daemonCtx, tc *testchat.Row, attempt int) {
	var in contracts.Finish
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	switch in.Outcome {
	case "completed", "failed", "cancelled", "paused_budget":
	default:
		writeProblem(w, apperr.Validation(apperr.Field("outcome", "enum", "outcome must be completed|failed|cancelled|paused_budget")))
		return
	}
	now := s.Clock.Now()
	// §4.3: the cancel closeTestChat queued for this turn is consumed by its
	// finish arriving — whatever the outcome says.
	if err := tokens.ConsumeAttemptCommands(r.Context(), s.DB, tc.ID, attempt, now); err != nil {
		s.Log.Warn("consume test chat attempt commands", "err", err)
	}
	res, err := s.TestChats.Finish(r.Context(), tc.ID, attempt, in, now)
	if err != nil {
		s.testChatErr(w, r, d, err)
		return
	}
	if !res.Repeated {
		s.publishTestChatTurn(r.Context(), res.Chat, res.Turn)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": in.Outcome})
}

// publishTestChatTurn is SSE `test_chat.turn` (openapi StreamEvent:
// `{test_chat_id, turn, transport, input_tokens, output_tokens}`), persisted
// so a reconnecting S10 backfills the answer it missed.
func (s *Server) publishTestChatTurn(ctx context.Context, tc *testchat.Row, turn testchat.Turn) {
	if s.Hub == nil || tc == nil {
		return
	}
	payload := map[string]any{
		"test_chat_id": tc.ID, "turn": testChatTurnAPI(turn),
		"transport": tc.Transport, "input_tokens": tc.InputTokens, "output_tokens": tc.OutputTokens,
	}
	if err := s.Hub.Publish(ctx, nil, tc.WorkspaceID, nil, "test_chat.turn", payload); err != nil {
		s.Log.Warn("publish test_chat.turn", "err", err, "test_chat", tc.ID)
	}
}

// consumeTestChatGC is the §6 report row that carries `test_chat_id`
// (daemon-protocol v0.8 §4.5): it is not stored as a workdir; its `gc`
// receipt consumes the chat's gc command. A refusal has no feed to land on
// and is logged.
func (s *Server) consumeTestChatGC(ctx context.Context, d daemonCtx, chatID, path string, gc *struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}, now time.Time) {
	id, err := uuid.Parse(chatID)
	if err != nil {
		s.Log.Warn("workdir report: test_chat_id is not a uuid", "value", trimForDetail(chatID), "runtime", d.RuntimeID)
		return
	}
	if gc == nil || gc.Status == "" {
		return
	}
	if gc.Status == "refused" {
		s.Log.Warn("test chat directory gc refused", "test_chat", id, "path", path, "reason", gc.Reason, "runtime", d.RuntimeID)
	}
	if err := tokens.ConsumeTestChatGCCommands(ctx, s.DB, d.RuntimeID, id, now); err != nil {
		s.Log.Warn("consume test chat gc commands", "err", err, "test_chat", id)
	}
}

// ExpireTestChatTurns is the scheduler sweep for §4.5's 5-minute/3-minute
// bounds: each expired turn is closed with an error (no requeue) and announced
// as `test_chat.turn` so S10 stops waiting. Returns the number closed.
func (s *Server) ExpireTestChatTurns(ctx context.Context) (int, error) {
	expired, err := s.TestChats.ExpireStale(ctx, s.Clock.Now())
	if err != nil {
		return 0, err
	}
	for _, e := range expired {
		s.publishTestChatTurn(ctx, e.Chat, e.Turn)
	}
	return len(expired), nil
}
