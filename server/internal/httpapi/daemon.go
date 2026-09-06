package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// workdirKindOf reads the session's isolation kind so a recorded workdir gets
// the right workdir_kind (C3: worktree binds to the agent, the others to the
// lane). An unreadable session falls back to `dir`, which is what `none`
// isolation produces.
func workdirKindOf(r *http.Request, s *Server, sessionID uuid.UUID) string {
	var raw []byte
	if err := s.DB.QueryRow(r.Context(), `SELECT isolation FROM session WHERE id = $1`, sessionID).Scan(&raw); err != nil {
		return "dir"
	}
	var iso struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &iso); err != nil {
		return "dir"
	}
	if iso.Kind == "worktree" || iso.Kind == "container" {
		return iso.Kind
	}
	return "dir"
}

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

// daemonWorkdirs accepts the §6 workdir report: the daemon lists what it has
// on disk (probe time and lane end) and the server turns each entry into a
// workdir row, binding the lane that runs in it (FR-6.1/6.4). GC judgement is
// the server's too (§6) but stays P4 — here the report is what keeps
// `lane.workdir_id` from being null forever and what feeds S13.
//
// A malformed body is 400 (N6). A single unusable entry is skipped and logged
// rather than failing the whole report: the daemon has no way to retry one
// line, and losing the other lanes' rows is worse than losing one.
func (s *Server) daemonWorkdirs(w http.ResponseWriter, r *http.Request, d daemonCtx) {
	var in struct {
		Workdirs []struct {
			ID         string     `json:"id"`
			Kind       string     `json:"kind"`
			Path       string     `json:"path"`
			SessionID  string     `json:"session_id"`
			AgentID    string     `json:"agent_id"`
			LaneID     string     `json:"lane_id"`
			Bytes      int64      `json:"bytes"`
			LastUsedAt *time.Time `json:"last_used_at"`
			Git        *struct {
				Branch       string `json:"branch"`
				Merged       bool   `json:"merged"`
				Dirty        bool   `json:"dirty"`
				CommitsAhead int    `json:"commits_ahead"`
			} `json:"git"`
			// GC is §6 v0.7's receipt for a gc command: what the daemon did
			// with this directory. Without it the server was guessing from
			// absence (review R2).
			GC *struct {
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"gc"`
		} `json:"workdirs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
		writeProblem(w, apperr.New(http.StatusBadRequest, "malformed_json", "workdir report must be JSON {workdirs: [...]}: "+err.Error()))
		return
	}
	now := s.Clock.Now()
	var gcReports []workdirs.GCReport
	for _, wd := range in.Workdirs {
		rep, ok := s.workdirReport(r, d, wd.Kind, wd.Path, wd.SessionID, wd.AgentID, wd.LaneID)
		if !ok {
			continue
		}
		rep.Bytes = wd.Bytes
		if wd.LastUsedAt != nil && !wd.LastUsedAt.IsZero() {
			rep.LastUsedAt = wd.LastUsedAt
		}
		if wd.Git != nil {
			branch, dirty := wd.Git.Branch, wd.Git.Dirty || !wd.Git.Merged && wd.Git.CommitsAhead > 0
			if branch != "" {
				rep.Branch = &branch
			}
			rep.Dirty = &dirty
			// P4: the three git facts are also kept APART. `dirty` keeps its
			// contract meaning (the OR, what S13 draws), but FR-6.4's GC rules
			// need `merged`, `commits_ahead` and the working-tree state on their
			// own — the OR cannot tell E13-12 from E13-13, and those two ask the
			// Director for different things.
			merged, ahead, tree := wd.Git.Merged, wd.Git.CommitsAhead, wd.Git.Dirty
			rep.Merged, rep.CommitsAhead, rep.TreeDirty = &merged, &ahead, &tree
		}
		id, err := workdirs.Record(r.Context(), s.DB, rep, now)
		if err != nil {
			s.Log.Warn("record workdir", "err", err, "path", wd.Path, "session", wd.SessionID)
			continue
		}
		if wd.GC != nil && wd.GC.Status != "" {
			// The row is upserted FIRST so the receipt lands on the id the
			// server knows, whether or not the daemon echoed one back.
			gcReports = append(gcReports, workdirs.GCReport{
				WorkdirID: id, Status: wd.GC.Status, Reason: wd.GC.Reason,
			})
		}
	}
	// §6 v0.7: the `gc` field is the receipt. `deleted` closes the row,
	// `refused` retains it and goes on the feed; both let the command be
	// consumed. Absence is no longer evidence of anything.
	refusals, err := workdirs.ApplyGCReports(r.Context(), s.DB, d.RuntimeID, gcReports, now)
	if err != nil {
		s.Log.Warn("apply gc reports", "err", err)
	}
	for _, ref := range refusals {
		s.recordGCRefusal(r.Context(), ref, now)
	}
	if err := tokens.ConsumeGCCommands(r.Context(), s.DB, d.RuntimeID, s.Clock.Now()); err != nil {
		s.Log.Warn("consume gc commands", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// recordGCRefusal puts §6's "GC 거부: <reason>" on the activity feed. The gc
// command carries a session, not a task, so the note lands on the last task of
// the lane that ran in that directory — the place a person looking at why the
// machine is still full will actually be.
func (s *Server) recordGCRefusal(ctx context.Context, ref workdirs.Refusal, now time.Time) {
	var taskID uuid.UUID
	var attempt int
	err := s.DB.QueryRow(ctx, `
		SELECT t.id, t.attempt
		FROM workdir w
		JOIN task t ON t.session_id = w.session_id
		         AND (w.lane_id IS NULL OR t.lane_id = w.lane_id)
		         AND (w.lane_id IS NOT NULL OR w.agent_id IS NULL OR t.agent_id = w.agent_id)
		WHERE w.id = $1
		ORDER BY t.created_at DESC
		LIMIT 1`, ref.WorkdirID).Scan(&taskID, &attempt)
	if err != nil {
		s.Log.Warn("gc refused with no task to record it on", "workdir", ref.WorkdirID, "reason", ref.Reason)
		return
	}
	reason := ref.Reason
	if reason == "" {
		reason = "이유 없음"
	}
	// S-52: `status` requires `command` — this row records the platform's own
	// `gc` command (§4.3) coming back refused — and closes the payload, so the
	// sentence goes under `args`.
	if err := s.writeServerEvent(ctx, taskID, attempt, "status", "error", "gc.refused", "info",
		map[string]any{"command": "gc", "result_ref": "workdir:" + ref.WorkdirID.String(),
			"args": map[string]any{"note": "GC 거부: " + reason}},
		now); err != nil {
		s.Log.Warn("record gc refusal", "err", err, "workdir", ref.WorkdirID)
	}
}

// workdirReport validates one reported entry against the calling runtime. A
// daemon token may only write rows for sessions of its own workspace — the
// report carries ids the daemon read off its own disk, so it is input, not
// authority.
func (s *Server) workdirReport(r *http.Request, d daemonCtx, kind, path, session, agent, lane string) (workdirs.Report, bool) {
	rep := workdirs.Report{Kind: kind, Path: path}
	sid, err := uuid.Parse(session)
	if err != nil || path == "" {
		return rep, false
	}
	var ws uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT workspace_id FROM session WHERE id = $1`, sid).Scan(&ws); err != nil || ws != d.WorkspaceID {
		return rep, false
	}
	rep.SessionID = sid
	if lane != "" {
		if id, err := uuid.Parse(lane); err == nil {
			var owner uuid.UUID
			if err := s.DB.QueryRow(r.Context(), `SELECT session_id FROM lane WHERE id = $1`, id).Scan(&owner); err == nil && owner == sid {
				rep.LaneID = &id
			}
		}
	}
	if agent != "" {
		if id, err := uuid.Parse(agent); err == nil {
			rep.AgentID = &id
		}
	}
	return rep, rep.AgentID != nil || rep.LaneID != nil
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
		// §4.2 carries workdir_path: this is the first moment the server can
		// know which directory the lane runs in, and the §6 inventory report
		// only arrives with the next probe. Binding here is what makes
		// lane.workdir_id true while the lane is alive rather than a day later.
		if in.WorkdirPath != "" {
			rep := workdirs.Report{Kind: workdirKindOf(r, s, t.SessionID), Path: in.WorkdirPath, SessionID: t.SessionID, LaneID: &t.LaneID}
			if rep.Kind == "worktree" {
				rep.AgentID = &t.AgentID
			}
			if _, err := workdirs.Record(r.Context(), s.DB, rep, s.Clock.Now()); err != nil {
				s.Log.Warn("record workdir from phase", "err", err, "task", t.ID)
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
	// FR-7.3 M9: the heartbeat's `usage` is the only in-turn signal there is.
	// Storing it and checking the limits here is what makes "턴 중 강제" exist —
	// pricing only at `finish` can stop a task no earlier than after it has
	// already spent past its budget.
	if in.Usage.InputTokens > 0 || in.Usage.OutputTokens > 0 || in.Usage.CostUSD > 0 {
		if err := s.Tasks.RecordTurnUsage(r.Context(), t.ID, in.Usage, s.Clock.Now()); err != nil {
			s.Log.Warn("record turn usage", "err", err, "task", t.ID)
		} else if _, err := s.enforceBudgetFor(r.Context(), t.ID); err != nil {
			s.Log.Warn("enforce budget", "err", err, "task", t.ID)
		}
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

// finishAndEnforce is §4.4's finish plus FR-7.3's "사후" half. tasks.Finish
// records the attempt, stores `usage` and rolls the session cost up; the
// budget is then checked against the number that roll-up just produced.
//
// S-44: FR-7.3 says the budget is checked DURING the turn precisely because
// checking it only between tasks lets one task overshoot by a lot — but the
// in-turn check is driven by the heartbeat's `usage`, and a daemon that
// reports usage only at `finish` (D-17) went through no check at all. The
// finish check is the floor under that: it cannot stop money already spent,
// but it stops the next task from spending the same way, which is the whole
// difference between a budget and a report.
//
// It runs after Finish has COMMITTED (and after its roll-up transaction), not
// inside it: Finish holds task row locks and enforceBudgetFor takes the
// session lock first, so nesting them makes the two lock orders opposite and a
// concurrent pair deadlocks — the same reason rollUpCost is its own tx.
func (s *Server) finishAndEnforce(ctx context.Context, taskID uuid.UUID, attempt int, in contracts.Finish) (tasks.Status, error) {
	final, err := s.Tasks.Finish(ctx, taskID, attempt, in)
	if err != nil {
		return final, err
	}
	if _, err := s.enforceBudgetFor(ctx, taskID); err != nil {
		// The attempt is committed either way. Losing the enforcement is worse
		// than an error nobody reads, so it is logged rather than turned into
		// a 500 that would make the daemon re-send a finish it already landed.
		//
		// S-47: the log line was the WHOLE record. A lost check leaves the
		// lane unlocked and the next task dispatches on a budget nobody
		// verified, and the session's timeline showed a turn that ended
		// normally. The note says otherwise, in the one place the Director
		// reads (tasks.NoteBudgetEnforceFailed also documents why the next
		// heartbeat or finish re-checks by itself).
		s.Log.Warn("enforce budget after finish", "err", err, "task", taskID)
		if nerr := s.Tasks.NoteBudgetEnforceFailed(ctx, taskID, attempt, err, s.Clock.Now()); nerr != nil {
			s.Log.Warn("note budget enforce failure", "err", nerr, "task", taskID)
		}
	}
	return final, nil
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
	final, err := s.finishAndEnforce(r.Context(), t.ID, attempt, in)
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
		// The lane frame is no longer emitted here: tasks.Finish publishes it
		// inside its own transaction, for every outcome (completed → done/queued,
		// paused_budget → paused, cancelled/failed → failed), together with
		// task.updated. Publishing again after the commit would only duplicate it.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": final})
	}
}
