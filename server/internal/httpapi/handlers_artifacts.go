package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/artifacts"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// multipartSlack is how much bigger than the file the whole body may be:
// part headers, boundaries and the three text fields. The 50 MB ceiling in the
// contract is about the artifact, not about MIME framing, so the body cap has
// to sit above it or a legal 50 MB file would be rejected for its own headers.
const multipartSlack = 1 << 20

// fieldMax bounds name/type/description. They are database text columns, but a
// multipart part with no limit is a memory hole regardless of where it lands.
const fieldMax = 64 << 10

// SubmitArtifact is FR-4.3 `colab artifact submit`. The bytes are stored
// whoever submitted them; whether that satisfies the `artifact_submitted`
// completion condition is a separate question the tree answers (E6-02), and
// the answer rides back in completion_progress.
func (s *Server) SubmitArtifact(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.SubmitArtifactParams) {
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	// The declared length is checked before a byte is read: a client that says
	// it is sending 4 GB gets 413 instead of four gigabytes of server time.
	if r.ContentLength > artifacts.MaxBytes+multipartSlack {
		writeProblem(w, tooLarge(r.ContentLength))
		return
	}
	body, p := readUpload(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	in, p := parseArtifactUpload(r, body)
	if p != nil {
		writeProblem(w, p)
		return
	}

	pr := principalOf(r)
	scope := ""
	if pr.Task != nil {
		id, agent := pr.Task.TaskID, pr.Task.AgentID
		in.TaskID, in.AgentID, scope = &id, &agent, taskScope(id)
	} else {
		id := pr.User.Id
		in.UserID, scope = &id, "user:"+id.String()
	}

	call := func() (int, any, *Problem) {
		row, err := s.Artifacts.Submit(r.Context(), sessionId, *in)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		// The completion condition is judged on the REAL path: this is the
		// call site the E6 golden table's `applyEvent` hook stands for.
		var actor uuid.UUID
		if in.AgentID != nil {
			actor = *in.AgentID
		}
		if _, err := s.Sessions.ApplyCompletionEvent(r.Context(), sessionId,
			sessions.Event{Kind: "artifact_submit", Actor: actor, Ref: &row.ID}); err != nil {
			return 0, nil, apperr.As(err)
		}
		prog, err := s.Sessions.Progress(r.Context(), sessionId)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		fresh, err := s.Artifacts.Get(r.Context(), row.ID)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		api := artifactAPI(fresh)
		// `artifact.created` had no publisher at all, so a submitted artifact
		// showed up in S7's 산출물 tab only after a reload (G4 2판 W13). It is
		// sent from here rather than from artifacts.Submit because the row the
		// web must see is the one the REST body carries — same mapping, same
		// object — and because the idempotent replay path never reaches this
		// closure, so a retried submit cannot emit a second frame.
		if s.Hub != nil {
			if wsID, err := s.Sessions.WorkspaceOf(r.Context(), sessionId); err == nil {
				sid := uuid.UUID(sessionId)
				_ = s.Hub.Publish(r.Context(), nil, wsID, &sid, "artifact.created", api)
			}
		}
		return http.StatusCreated, map[string]any{"artifact": api, "completion_progress": prog}, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	s.idempotent(r.Context(), w, scope, params.IdempotencyKey.String(), uploadHash(r, in), call)
}

// uploadHash is requestHash for a multipart body. It CANNOT hash the raw
// bytes: multipart.Writer picks a random boundary, so the CLI re-encoding the
// same upload produces different bytes every time and a legitimate retry would
// come back 422 idempotency_key_reused instead of replaying. What the contract
// means by "the same request" is the four parts, so those are what is hashed.
func uploadHash(r *http.Request, in *artifacts.SubmitInput) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s %s\n", r.Method, r.URL.Path)
	for _, field := range []string{in.Name, in.Type, in.Description, in.ContentType} {
		fmt.Fprintf(h, "%d:%s\n", len(field), field)
	}
	h.Write(in.Content)
	return hex.EncodeToString(h.Sum(nil))
}

// readUpload buffers the multipart body. It is the one request the 4 MB
// readBody cap cannot serve, and the cap is what turns an oversized upload
// into 413 rather than a slow 500.
func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, *Problem) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, artifacts.MaxBytes+multipartSlack))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, tooLarge(-1)
		}
		return nil, apperr.Validation(apperr.Field("body", "unreadable", err.Error()))
	}
	return b, nil
}

func tooLarge(got int64) *Problem {
	detail := fmt.Sprintf("artifact bodies are limited to %d bytes (50 MB)", artifacts.MaxBytes)
	if got > 0 {
		detail = fmt.Sprintf("declared %d bytes; %s", got, detail)
	}
	return apperr.New(http.StatusRequestEntityTooLarge, "payload_too_large", detail)
}

// parseArtifactUpload reads the contract's four parts and nothing else
// (openapi submitArtifact: name*, type*, file*, description).
func parseArtifactUpload(r *http.Request, body []byte) (*artifacts.SubmitInput, *Problem) {
	ct := r.Header.Get("Content-Type")
	mt, mp, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/form-data" || mp["boundary"] == "" {
		return nil, apperr.Validation(apperr.Field("body", "unsupported_media_type",
			"submitArtifact takes multipart/form-data {name, type, file, description}"))
	}
	in := &artifacts.SubmitInput{}
	seenFile := false
	mr := multipart.NewReader(bytes.NewReader(body), mp["boundary"])
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, apperr.Validation(apperr.Field("body", "malformed_multipart", err.Error()))
		}
		switch part.FormName() {
		case "file":
			// Read one byte past the ceiling: the difference between "exactly
			// 50 MB" (allowed) and "more" (413) is that byte.
			data, err := io.ReadAll(io.LimitReader(part, artifacts.MaxBytes+1))
			if err != nil {
				return nil, apperr.Validation(apperr.Field("file", "unreadable", err.Error()))
			}
			if int64(len(data)) > artifacts.MaxBytes {
				return nil, tooLarge(int64(len(data)))
			}
			in.Content, seenFile = data, true
			in.ContentType = part.Header.Get("Content-Type")
		case "name", "type", "description":
			v, err := io.ReadAll(io.LimitReader(part, fieldMax))
			if err != nil {
				return nil, apperr.Validation(apperr.Field(part.FormName(), "unreadable", err.Error()))
			}
			switch part.FormName() {
			case "name":
				in.Name = strings.TrimSpace(string(v))
			case "type":
				in.Type = strings.TrimSpace(string(v))
			case "description":
				in.Description = string(v)
			}
		}
		_ = part.Close()
	}
	var errs []apperr.FieldError
	if in.Name == "" {
		errs = append(errs, apperr.Field("name", "required", "name is required"))
	}
	if in.Type == "" {
		errs = append(errs, apperr.Field("type", "required", "type is required (file · diff · branch · doc · …)"))
	}
	if !seenFile {
		errs = append(errs, apperr.Field("file", "required", "the file part is required"))
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	if in.ContentType == "" {
		in.ContentType = "application/octet-stream"
	}
	return in, nil
}

// ListArtifacts is the S7 sidebar and the daemon brief's artifact list.
func (s *Server) ListArtifacts(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.ListArtifactsParams) {
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	o := artifacts.ListOptions{}
	if params.LatestOnly != nil {
		o.LatestOnly = *params.LatestOnly
	}
	if params.Type != nil {
		o.Type = *params.Type
	}
	rows, err := s.Artifacts.List(r.Context(), sessionId, o)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]gen.Artifact, 0, len(rows))
	for _, a := range rows {
		out = append(out, artifactAPI(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// artifactAccess resolves the artifact and the caller's right to see it. A
// member of another workspace gets 404 rather than 403: FR-6.1 keeps
// cross-lane reads to artifacts, and confirming that an id exists elsewhere is
// already more than a stranger should learn.
func (s *Server) artifactAccess(r *http.Request, id uuid.UUID) (*artifacts.Row, *Problem) {
	a, err := s.Artifacts.Get(r.Context(), id)
	if err != nil {
		return nil, apperr.As(err)
	}
	if _, p := s.sessionAccess(r, a.SessionID); p != nil {
		if p.Status == http.StatusNotFound {
			return nil, apperr.NotFound("artifact")
		}
		return nil, p
	}
	return a, nil
}

// downloadAccess is `downloadArtifact`'s gate, which openapi v0.7.3 widens by
// exactly one scheme: `DaemonToken` — "그 런타임에 고정된 세션의 아티팩트만"
// (S-57). Every other artifact operation keeps artifactAccess, so a daemon
// token can read a body and nothing else.
//
// SCOPE IS THE SESSION'S PINNING, NOT THE WORKSPACE. A daemon paired to a
// workspace can hold several sessions' machines; `session.runtime_id` is what
// says this artifact belongs to work THIS computer is running (§4.1 "이 런타임에
// 고정된 세션"). Anything else answers 404 rather than 403, for the same reason
// artifactAccess does: an id that exists elsewhere is more than a stranger
// should learn.
func (s *Server) downloadAccess(r *http.Request, id uuid.UUID) (*artifacts.Row, *Problem) {
	pr := principalOf(r)
	if pr.Daemon == nil {
		return s.artifactAccess(r, id)
	}
	a, err := s.Artifacts.Get(r.Context(), id)
	if err != nil {
		return nil, apperr.As(err)
	}
	var wsID uuid.UUID
	var runtimeID *uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT workspace_id, runtime_id FROM session WHERE id = $1`, a.SessionID).
		Scan(&wsID, &runtimeID); err != nil {
		return nil, apperr.NotFound("artifact")
	}
	if wsID != pr.Daemon.WorkspaceID || runtimeID == nil || *runtimeID != pr.Daemon.RuntimeID {
		return nil, apperr.NotFound("artifact")
	}
	return a, nil
}

func (s *Server) GetArtifact(w http.ResponseWriter, r *http.Request, artifactId gen.ArtifactId) {
	a, p := s.artifactAccess(r, artifactId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	writeJSON(w, http.StatusOK, artifactAPI(a))
}

// DownloadArtifact streams the body. Content-Length is declared because the
// CLI compares it against the bytes it actually wrote (colab-cli README): with
// a chunked response a truncated download and a complete one look the same,
// and the agent that reads the half file never learns it was half.
func (s *Server) DownloadArtifact(w http.ResponseWriter, r *http.Request, artifactId gen.ArtifactId) {
	a, p := s.downloadAccess(r, artifactId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	c, err := s.Artifacts.Open(r.Context(), a)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer func() { _ = c.Close() }()
	w.Header().Set("Content-Type", c.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(c.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": c.Name}))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	// 50 MB never enters the heap in one piece: io.Copy runs a 32 KB window
	// from the large object straight to the socket.
	if _, err := io.Copy(w, c); err != nil {
		s.Log.Warn("artifact download interrupted", "artifact", a.ID, "err", err)
	}
}

// ReviewArtifact is `colab review approve|reject` (FR-2.2 agent_approval).
// The designation gate runs FIRST and stores nothing when it fails: E6-06 is
// not "the review is ignored", it is "the review never happened".
func (s *Server) ReviewArtifact(w http.ResponseWriter, r *http.Request, artifactId gen.ArtifactId, params gen.ReviewArtifactParams) {
	pr := principalOf(r)
	if pr.Task == nil {
		writeProblem(w, apperr.Forbidden("agent_only",
			"review is an agent tool (openapi reviewArtifact is TaskToken-only); a person answers the user_approval HITL instead"))
		return
	}
	a, p := s.artifactAccess(r, artifactId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.ReviewArtifactJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	comments := ""
	if in.Comments != nil {
		comments = strings.TrimSpace(*in.Comments)
	}
	var kind string
	switch in.Verdict {
	case gen.ReviewArtifactJSONBodyVerdictApprove:
		kind = "review_approve"
	case gen.ReviewArtifactJSONBodyVerdictReject:
		kind = "review_reject"
		if comments == "" {
			writeProblem(w, apperr.Validation(apperr.Field("comments", "required",
				"reject needs the reason — it is what the submitting lane re-enters with")))
			return
		}
	default:
		writeProblem(w, apperr.Validation(apperr.Field("verdict", "enum", "verdict must be approve or reject")))
		return
	}

	call := func() (int, any, *Problem) {
		// The same real path the E6 golden table describes: the tree decides
		// whether this agent may review at all, and approving is what closes
		// the session when `agent_approval` stands alone (E6-05).
		out, err := s.Sessions.ApplyCompletionEvent(r.Context(), a.SessionID, sessions.Event{
			Kind: kind, Actor: pr.Task.AgentID, Note: comments, Ref: &a.ID,
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		if out.CLIError != "" {
			// The code string is a contract: colab-cli.md §2.3 maps exactly
			// `not_designated_reviewer` to CLI exit 3.
			return 0, nil, apperr.Forbidden("not_designated_reviewer", out.CLIError)
		}
		rev := artifacts.ReviewRow{
			Verdict: string(in.Verdict), ReviewerAgentID: pr.Task.AgentID,
		}
		if comments != "" {
			rev.Comments = &comments
		}
		taskID := pr.Task.TaskID
		rev.ReviewerTaskID = &taskID
		if out.DecisionID != uuid.Nil {
			id := out.DecisionID
			rev.DecisionID = &id
		}
		stored, err := s.Artifacts.RecordReview(r.Context(), a.ID, rev)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		prog, err := s.Sessions.Progress(r.Context(), a.SessionID)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		res := map[string]any{"review": reviewAPI(stored), "completion_progress": prog}
		if kind == "review_reject" {
			msg, err := s.postRejectReason(r, a, pr.Task.AgentID, taskID, comments)
			if err != nil {
				return 0, nil, apperr.As(err)
			}
			if msg != nil {
				res["message"] = *msg
			}
		}
		return http.StatusOK, res, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	s.idempotent(r.Context(), w, taskScope(pr.Task.TaskID), params.IdempotencyKey.String(), requestHash(r, body), call)
}

// postRejectReason puts the reason back where the work is (openapi
// reviewArtifact: "그 아티팩트를 제출한 task의 lane 스레드에 답글로") AND wakes
// that lane — E16-B step 5.
//
// S-59: posting was not enough, and the reply thread was not the mechanism.
// The reply is written by the REVIEWER agent with no mention in it, so routing
// rule 4 stops it before rule 5 ever looks at the thread: T-I4 measured zero
// Frontend tasks carrying the rejection message as their trigger, and the
// re-entry that step 5 is about only happened because the PM relayed the
// rejection in a message of its own. openapi v0.7.3 settles it — the server
// re-enters the lane explicitly, as a PLATFORM event (router.PlatformTrigger),
// with the same result resolution rule 1 gives: same lane, `reentry_count`+1.
func (s *Server) postRejectReason(r *http.Request, a *artifacts.Row, reviewer, reviewerTask uuid.UUID, comments string) (*gen.Message, error) {
	var parent nullable.Nullable[openapi_types.UUID]
	var platform *router.PlatformTrigger
	if a.SubmittedByTaskID != nil {
		var msgID *uuid.UUID
		var laneID, agentID uuid.UUID
		err := s.DB.QueryRow(r.Context(), `
			SELECT coalesce(
				(SELECT m.id FROM message m WHERE m.source_task_id = $1 ORDER BY m.created_at DESC LIMIT 1),
				(SELECT t.trigger_message_id FROM task t WHERE t.id = $1)),
			       (SELECT t.lane_id FROM task t WHERE t.id = $1),
			       (SELECT t.agent_id FROM task t WHERE t.id = $1)`, *a.SubmittedByTaskID).
			Scan(&msgID, &laneID, &agentID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if msgID != nil {
			parent = nullable.NewNullableWithValue(openapi_types.UUID(*msgID))
		}
		if laneID != uuid.Nil && agentID != uuid.Nil && agentID != reviewer {
			// Not the reviewer's own lane: a self-rejection would otherwise
			// wake the reviewer with its own verdict.
			platform = &router.PlatformTrigger{AgentID: agentID, LaneID: laneID}
		}
	}
	out, err := s.Router.PostWithTrigger(r.Context(), a.SessionID,
		router.Author{Type: "agent", AgentID: &reviewer, TaskID: &reviewerTask, Attempt: 1},
		gen.MessageCreate{
			Content:  fmt.Sprintf("리뷰 반려 — %s v%d\n\n%s", a.Name, a.Version, comments),
			ParentId: parent,
		}, platform)
	if err != nil {
		return nil, err
	}
	return &out.Message, nil
}

func artifactAPI(a *artifacts.Row) gen.Artifact {
	out := gen.Artifact{
		Id: a.ID, SessionId: a.SessionID, Name: a.Name, Version: a.Version, Type: a.Type,
		StorageRef: a.StorageRef, CreatedAt: a.CreatedAt,
	}
	size := a.SizeBytes
	out.SizeBytes = &size
	latest := a.Latest
	out.Latest = &latest
	if a.ContentType != nil {
		out.ContentType = nullable.NewNullableWithValue(*a.ContentType)
	} else {
		out.ContentType = nullable.NewNullNullable[string]()
	}
	if a.Description != nil {
		out.Description = nullable.NewNullableWithValue(*a.Description)
	} else {
		out.Description = nullable.NewNullNullable[string]()
	}
	if a.SubmittedByTaskID != nil {
		out.SubmittedByTaskId = nullable.NewNullableWithValue(openapi_types.UUID(*a.SubmittedByTaskID))
	} else {
		out.SubmittedByTaskId = nullable.NewNullNullable[openapi_types.UUID]()
	}
	if a.SubmittedByAgentID != nil || a.SubmittedByUserID != nil {
		out.SubmittedBy = &struct {
			AgentId   *openapi_types.UUID `json:"agent_id,omitempty"`
			AgentName *string             `json:"agent_name,omitempty"`
			UserId    *openapi_types.UUID `json:"user_id,omitempty"`
		}{}
		if a.SubmittedByAgentID != nil {
			id := openapi_types.UUID(*a.SubmittedByAgentID)
			out.SubmittedBy.AgentId, out.SubmittedBy.AgentName = &id, a.AgentName
		}
		if a.SubmittedByUserID != nil {
			id := openapi_types.UUID(*a.SubmittedByUserID)
			out.SubmittedBy.UserId = &id
		}
	}
	if a.Review != nil {
		rev := reviewAPI(a.Review)
		out.Review = &rev
	}
	return out
}

func reviewAPI(r *artifacts.ReviewRow) gen.ArtifactReview {
	out := gen.ArtifactReview{
		ArtifactId: r.ArtifactID, Verdict: gen.ArtifactReviewVerdict(r.Verdict),
		ReviewerAgentId: r.ReviewerAgentID, ReviewedAt: r.ReviewedAt,
	}
	if r.Comments != nil {
		out.Comments = nullable.NewNullableWithValue(*r.Comments)
	} else {
		out.Comments = nullable.NewNullNullable[string]()
	}
	if r.ReviewerTaskID != nil {
		id := openapi_types.UUID(*r.ReviewerTaskID)
		out.ReviewerTaskId = &id
	}
	if r.DecisionID != nil {
		id := openapi_types.UUID(*r.DecisionID)
		out.DecisionId = &id
	}
	return out
}
