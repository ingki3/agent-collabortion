package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Daemon ↔ server protocol (contracts/daemon-protocol.md). Not in openapi.yaml
// by design (§1); the document is the spec.
const daemonBase = "/v1/daemon"

func (s *Server) daemonRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+daemonBase+"/pair", s.daemonPair)
	mux.HandleFunc("POST "+daemonBase+"/runtimes/{runtimeId}/probe", s.withDaemon(s.daemonProbe))
	mux.HandleFunc("POST "+daemonBase+"/runtimes/{runtimeId}/claim", s.withDaemon(s.daemonClaim))
	mux.HandleFunc("POST "+daemonBase+"/runtimes/{runtimeId}/workdirs", s.withDaemon(s.daemonWorkdirs))
	mux.HandleFunc("POST "+daemonBase+"/tasks/{taskId}/attempts/{attempt}/phase", s.withDaemon(s.daemonPhase))
	mux.HandleFunc("POST "+daemonBase+"/tasks/{taskId}/attempts/{attempt}/events", s.withDaemon(s.daemonEvents))
	mux.HandleFunc("POST "+daemonBase+"/tasks/{taskId}/attempts/{attempt}/heartbeat", s.withDaemon(s.daemonHeartbeat))
	mux.HandleFunc("POST "+daemonBase+"/tasks/{taskId}/attempts/{attempt}/finish", s.withDaemon(s.daemonFinish))
}

type daemonCtx struct {
	RuntimeID   uuid.UUID
	WorkspaceID uuid.UUID
}

// withDaemon authenticates the `cdt_` bearer and records presence.
func (s *Server) withDaemon(h func(w http.ResponseWriter, r *http.Request, d daemonCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeProblem(w, apperr.Unauthorized("unauthorized", "daemon token required"))
			return
		}
		rt, ws, err := s.Runtimes.VerifyDaemonToken(r.Context(), strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")))
		if err != nil {
			writeProblem(w, apperr.Unauthorized("invalid_daemon_token", "unknown daemon token"))
			return
		}
		if p := r.PathValue("runtimeId"); p != "" && p != rt.String() {
			writeProblem(w, apperr.Forbidden("runtime_mismatch", "token belongs to another runtime"))
			return
		}
		s.Runtimes.Touch(r.Context(), rt)
		h(w, r, daemonCtx{RuntimeID: rt, WorkspaceID: ws})
	}
}

func (s *Server) daemonPair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PairingCode   string `json:"pairing_code"`
		Hostname      string `json:"hostname"`
		OS            string `json:"os"`
		DaemonVersion string `json:"daemon_version"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if in.PairingCode == "" {
		writeProblem(w, apperr.Validation(apperr.Field("pairing_code", "required", "pairing_code is required")))
		return
	}
	id, token, err := s.Runtimes.Pair(r.Context(), in.PairingCode, in.Hostname, in.OS, in.DaemonVersion)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"runtime_id": id, "daemon_token": token})
}

func (s *Server) daemonProbe(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	var in contracts.Probe
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.Runtimes.Probe(r.Context(), d.RuntimeID, in); err != nil {
		writeErr(w, err)
		return
	}
	// §4.3: a probe command is consumed by the next probe.
	if err := tokens.ConsumeProbeCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now()); err != nil {
		s.Log.Warn("consume probe commands", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) daemonClaim(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	var in struct {
		Capacity int `json:"capacity"`
		WaitMs   int `json:"wait_ms"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	wait := time.Duration(in.WaitMs) * time.Millisecond
	if wait < 0 {
		wait = 0
	}
	bundles, err := s.Queue.ClaimWait(r.Context(), d.RuntimeID.String(), in.Capacity, wait)
	if err != nil {
		writeErr(w, err)
		return
	}
	if bundles == nil {
		bundles = []contracts.TaskBundle{}
	}
	cmds, err := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": bundles, "commands": cmds})
}

// daemonWorkdirs accepts the §6 workdir report. FR-6.4 GC judgement is P4;
// P1 validates the shape (a malformed body is 400 — N6) and uses the report
// to consume gc commands whose workdirs are gone (§4.3).
func (s *Server) daemonWorkdirs(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	var in struct {
		Workdirs []struct {
			ID string `json:"id"`
		} `json:"workdirs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
		writeProblem(w, apperr.New(http.StatusBadRequest, "malformed_json", "workdir report must be JSON {workdirs: [...]}: "+err.Error()))
		return
	}
	present := make([]string, 0, len(in.Workdirs))
	for _, wd := range in.Workdirs {
		if wd.ID != "" {
			present = append(present, wd.ID)
		}
	}
	if err := tokens.ConsumeGCCommands(r.Context(), s.DB, d.RuntimeID, present, s.Clock.Now()); err != nil {
		s.Log.Warn("consume gc commands", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// taskForDaemon checks the task belongs to the calling runtime.
func (s *Server) taskForDaemon(w http.ResponseWriter, r *http.Request, d daemonCtx) (*tasks.Row, int, bool) {
	taskID, err := uuid.Parse(r.PathValue("taskId"))
	if err != nil {
		writeProblem(w, apperr.Validation(apperr.Field("task_id", "format", "task_id must be a uuid")))
		return nil, 0, false
	}
	attempt, err := strconv.Atoi(r.PathValue("attempt"))
	if err != nil || attempt < 1 {
		writeProblem(w, apperr.Validation(apperr.Field("attempt", "format", "attempt must be a positive integer")))
		return nil, 0, false
	}
	t, err := tasks.Get(r.Context(), s.DB, taskID)
	if err != nil {
		writeProblem(w, apperr.NotFound("task"))
		return nil, 0, false
	}
	// The runtime that holds (task, attempt) is recorded on the attempt's token.
	var holder *uuid.UUID
	_ = s.DB.QueryRow(r.Context(), `SELECT runtime_id FROM task_token WHERE task_id = $1 AND attempt = $2`, taskID, attempt).Scan(&holder)
	if holder == nil && t.RuntimeID != nil {
		holder = t.RuntimeID
	}
	if holder == nil || *holder != d.RuntimeID {
		writeProblem(w, apperr.Forbidden("runtime_mismatch", "this attempt was not claimed by the calling runtime"))
		return nil, 0, false
	}
	return t, attempt, true
}

func (s *Server) daemonPhase(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	t, attempt, ok := s.taskForDaemon(w, r, d)
	if !ok {
		return
	}
	var in struct {
		Phase       string `json:"phase"`
		PGID        int    `json:"pgid"`
		WorkdirPath string `json:"workdir_path"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	err := s.Tasks.Phase(r.Context(), t.ID, attempt, in.Phase)
	switch {
	case errors.Is(err, tasks.ErrStaleAttempt):
		s.staleAttempt(w, r, d)
	case errors.Is(err, tasks.ErrInvalidTransition):
		writeProblem(w, apperr.Conflict("invalid_transition", err.Error()))
	case err != nil:
		writeErr(w, err)
	default:
		if in.Phase == "preparing" {
			// §4.3: rebind_prepare is consumed by the new attempt's preparing report.
			if err := tokens.ConsumeRebindCommands(r.Context(), s.DB, d.RuntimeID, t.SessionID, s.Clock.Now()); err != nil {
				s.Log.Warn("consume rebind commands", "err", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// staleAttempt answers 409 with the pending commands (the revoke is there).
func (s *Server) staleAttempt(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	cmds, _ := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	p := apperr.Conflict("stale_attempt", "this attempt is no longer current; its token was revoked")
	p.Extra = map[string]any{"commands": cmds}
	writeProblem(w, p)
}

func (s *Server) daemonEvents(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	t, attempt, ok := s.taskForDaemon(w, r, d)
	if !ok {
		return
	}
	var in struct {
		Events []contracts.TaskEvent `json:"events"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	max, err := s.Events.Ingest(r.Context(), t.ID, attempt, in.Events)
	switch {
	case errors.Is(err, tasks.ErrStaleAttempt):
		s.staleAttempt(w, r, d)
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	cmds, _ := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	writeJSON(w, http.StatusOK, map[string]any{"accepted_seq_max": max, "commands": cmds})
}

func (s *Server) daemonHeartbeat(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	t, attempt, ok := s.taskForDaemon(w, r, d)
	if !ok {
		return
	}
	// §4.2 v0.3: heartbeat is a liveness signal, so `preview` is decoded apart
	// from it — a daemon on the old shape (`preview` as a bare string) must not
	// lose its attempt to a 422 three minutes later (G3 C-1). Whatever arrives,
	// `usage`·`last_seq` refresh heartbeat_at; only a preview that does not match
	// {text, message_id?} is dropped, with one warning in the feed.
	var in struct {
		Usage   contracts.Usage `json:"usage"`
		LastSeq int             `json:"last_seq"`
		Preview json.RawMessage `json:"preview"`
	}
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	err := s.Tasks.Heartbeat(r.Context(), t.ID, attempt, s.Clock.Now())
	if errors.Is(err, tasks.ErrStaleAttempt) {
		s.staleAttempt(w, r, d)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if preview, drift := parsePreview(in.Preview); drift {
		if err := s.Tasks.NotePreviewDrift(r.Context(), t.ID, attempt, s.Clock.Now()); err != nil {
			s.Log.Warn("note heartbeat preview drift", "err", err)
		}
	} else if preview != nil && preview.Text != "" {
		sid := t.SessionID
		s.Hub.PublishEphemeral(t.WorkspaceID, &sid, "message.delta", map[string]any{
			"session_id": t.SessionID, "task_id": t.ID, "agent_id": t.AgentID, "message_id": preview.MessageID, "text": preview.Text,
		})
	}
	cmds, _ := tokens.PendingCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now())
	writeJSON(w, http.StatusOK, map[string]any{"commands": cmds})
}

// preview is the heartbeat's non-persisted partial output (§4.2 v0.3).
type preview struct {
	Text      string `json:"text"`
	MessageID string `json:"message_id"`
}

// parsePreview reads the `preview` field leniently. It returns (nil, false)
// when the field is absent or null, the value when it matches the contract,
// and (nil, true) — drift — for any other shape (a bare string from a v0.2
// daemon, a number, an object whose `text` is not a string).
func parsePreview(raw json.RawMessage) (*preview, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, false
	}
	var p preview
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, true
	}
	return &p, false
}

func (s *Server) daemonFinish(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	t, attempt, ok := s.taskForDaemon(w, r, d)
	if !ok {
		return
	}
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
	// §4.3: cancel/revoke of this attempt are consumed by its finish arriving —
	// whatever the server decides about the outcome (stale attempts included).
	if err := tokens.ConsumeAttemptCommands(r.Context(), s.DB, t.ID, attempt, s.Clock.Now()); err != nil {
		s.Log.Warn("consume attempt commands", "err", err)
	}
	final, err := s.Tasks.Finish(r.Context(), t.ID, attempt, in)
	switch {
	case errors.Is(err, tasks.ErrStaleAttempt):
		s.staleAttempt(w, r, d)
	case errors.Is(err, tasks.ErrInvalidTransition):
		writeProblem(w, apperr.Conflict("invalid_transition", err.Error()))
	case errors.Is(err, tasks.ErrInvalidSessionRef):
		writeProblem(w, apperr.Validation(apperr.Field("runtime_session_ref", "required", "runtime_session_ref needs runtime_kind and session_id (harness §6)")))
	case err != nil:
		writeErr(w, err)
	default:
		// The lane's status follows the task's (running → failed/done/queued);
		// S7 listens on lane.updated (openapi cancelLane: 완료는 SSE lane.updated).
		if lane, err := lanes.Load(r.Context(), s.DB, t.LaneID, false); err == nil {
			s.publishLane(r, t.WorkspaceID, t.SessionID, lane)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": final})
	}
}
