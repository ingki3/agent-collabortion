package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.ListSessionsParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	o := sessions.ListOptions{Cursor: params.Cursor, Query: params.Q}
	if params.Status != nil {
		for _, st := range *params.Status {
			o.Status = append(o.Status, string(st))
		}
	}
	if params.DirectorUserId != nil {
		v := *params.DirectorUserId
		o.DirectorUserID = &v
	}
	if params.AgentId != nil {
		v := *params.AgentId
		o.AgentID = &v
	}
	if params.RuntimeId != nil {
		v := *params.RuntimeId
		o.RuntimeID = &v
	}
	if params.Limit != nil {
		o.Limit = *params.Limit
	}
	items, next, err := s.Sessions.List(r.Context(), workspaceId, o)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.CreateSessionParams) {
	u, _, p := s.member(r, workspaceId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.SessionCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	s.idempotent(r.Context(), w, "user:"+u.Id.String(), optKey(params.IdempotencyKey), requestHash(r, body), func() (int, any, *Problem) {
		sess, err := s.Sessions.Create(r.Context(), workspaceId, u.Id, in)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, sess, nil
	})
}

func (s *Server) GetSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	u, p := s.sessionAccess(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	v := sessions.Viewer{}
	if u != nil {
		v.UserID = &u.Id
	}
	sess, err := s.Sessions.Get(r.Context(), sessionId, v)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) ListMessages(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.ListMessagesParams) {
	if _, p := s.sessionAccess(r, sessionId); p != nil {
		writeProblem(w, p)
		return
	}
	o := messages.ListOptions{}
	if params.Thread != nil {
		v := *params.Thread
		o.Thread = &v
	}
	if params.IncludeReplies != nil {
		o.IncludeReplies = *params.IncludeReplies
	}
	if params.Kind != nil {
		for _, k := range *params.Kind {
			o.Kinds = append(o.Kinds, string(k))
		}
	}
	if params.Before != nil {
		id, err := uuid.Parse(*params.Before)
		if err != nil {
			writeProblem(w, apperr.Validation(apperr.Field("before", "invalid_cursor", "before must be a message id")))
			return
		}
		o.Before = &id
	}
	if params.After != nil {
		id, err := uuid.Parse(*params.After)
		if err != nil {
			writeProblem(w, apperr.Validation(apperr.Field("after", "invalid_cursor", "after must be a message id")))
			return
		}
		o.After = &id
	}
	if params.Limit != nil {
		o.Limit = *params.Limit
	}
	items, hasBefore, hasAfter, total, err := messages.List(r.Context(), s.DB, sessionId, o)
	if err != nil {
		writeErr(w, err)
		return
	}
	page := gen.MessagePage{Items: make([]gen.Message, 0, len(items)), HasMoreBefore: hasBefore, HasMoreAfter: hasAfter}
	for _, m := range items {
		page.Items = append(page.Items, messages.ToAPI(m))
	}
	page.BeforeCursor = nullable.NewNullNullable[string]()
	page.AfterCursor = nullable.NewNullNullable[string]()
	if len(items) > 0 {
		page.BeforeCursor = nullable.NewNullableWithValue(items[0].ID.String())
		page.AfterCursor = nullable.NewNullableWithValue(items[len(items)-1].ID.String())
	}
	page.Total = nullable.NewNullNullable[int]()
	if total != nil {
		page.Total = nullable.NewNullableWithValue(*total)
	}
	writeJSON(w, http.StatusOK, page)
}

// PostMessage is postMessage. Idempotency-Key is a UUID for every principal
// (openapi.yaml; the generated binder rejects anything else): users send a
// random UUID, the CLI derives UUIDv5(task:<task_id>:<seq>) with seq
// continuing across attempts from CliContext.last_seq (colab-cli.md §1 v0.2).
// Keys are scoped per principal (user or task) so they never collide.
func (s *Server) PostMessage(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.PostMessageParams) {
	key := params.IdempotencyKey.String()
	u, p := s.sessionAccess(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.MessageCreate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if strings.TrimSpace(in.Content) == "" {
		writeProblem(w, apperr.Validation(apperr.Field("content", "required", "content is required")))
		return
	}
	pr := principalOf(r)
	var author router.Author
	var scope string
	if pr.Task != nil {
		tid := pr.Task.TaskID
		aid := pr.Task.AgentID
		author = router.Author{Type: "agent", AgentID: &aid, TaskID: &tid, Attempt: pr.Task.Attempt}
		scope = taskScope(tid)
	} else {
		author = router.Author{Type: "user", UserID: &u.Id}
		scope = "user:" + u.Id.String()
	}
	var clientSeq *int
	if params.XColabClientSeq != nil && pr.Task != nil {
		v := int(*params.XColabClientSeq)
		clientSeq = &v
	}
	s.idempotentSeq(r.Context(), w, scope, key, requestHash(r, body), clientSeq, func() (int, any, *Problem) {
		res, err := s.Router.Post(r.Context(), sessionId, author, in)
		switch {
		case err == router.ErrParentNotFound:
			return 0, nil, apperr.Validation(apperr.Field("parent_id", "not_found", "parent message not in this session"))
		case err != nil:
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, res, nil
	})
}

// taskScope is the idempotency scope of a task token (any attempt).
func taskScope(taskID uuid.UUID) string { return "task:" + taskID.String() }

// cliIdempotencyNamespace is the CLI's fixed UUIDv5 namespace
// (cli/internal/client/uuid.go IdempotencyNamespace = UUIDv5(NameSpace_DNS, "colab")).
var cliIdempotencyNamespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte("colab"))

// cliIdempotencyKey is what `colab message post` sends for seq:
// UUIDv5(namespace, "task:<task_id>:<seq>") (colab-cli.md §1 v0.2).
func cliIdempotencyKey(taskID uuid.UUID, seq int) string {
	return uuid.NewSHA1(cliIdempotencyNamespace, []byte(fmt.Sprintf("task:%s:%d", taskID, seq))).String()
}

// lastSeqProbeLimit bounds the UUIDv5 fallback walk per request.
const lastSeqProbeLimit = 10000

// lastClientSeq is CliContext.last_seq: the **maximum** client seq this task
// has used so far, across attempts — not a count, so a hole left by a failed
// post never makes the CLI reuse a key (openapi v0.4 CliContext.last_seq, R1).
//
// Source 1: idempotency_key.client_seq (X-Colab-Client-Seq, CLI v0.3).
// Source 2 (keys stored without the header — web or an older CLI): walk
// UUIDv5(task:<task_id>:<n>) for n = max+1, max+2, … and take the last seq
// whose key exists (openapi ClientSeq description). Keys expire after 24h,
// so the count of this task's messages (the only P1 CLI write) is the floor.
func (s *Server) lastClientSeq(r *http.Request, taskID uuid.UUID) (int, error) {
	ctx := r.Context()
	scope := taskScope(taskID)
	var maxSeq, msgs int
	if err := s.DB.QueryRow(ctx, `
		SELECT (SELECT COALESCE(max(client_seq), 0) FROM idempotency_key WHERE scope = $1),
		       (SELECT count(*) FROM message WHERE source_task_id = $2)`, scope, taskID).Scan(&maxSeq, &msgs); err != nil {
		return 0, err
	}
	for n := maxSeq + 1; n <= maxSeq+lastSeqProbeLimit; n++ {
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM idempotency_key WHERE scope = $1 AND key = $2)`, scope, cliIdempotencyKey(taskID, n)).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			break
		}
		maxSeq = n
	}
	if msgs > maxSeq {
		maxSeq = msgs
	}
	return maxSeq, nil
}

func (s *Server) taskAccess(r *http.Request, taskId uuid.UUID) (*tasks.Row, *Problem) {
	t, err := tasks.Get(r.Context(), s.DB, taskId)
	if err != nil {
		return nil, apperr.NotFound("task")
	}
	pr := principalOf(r)
	if pr.Task != nil {
		if pr.Task.SessionID != t.SessionID {
			return nil, apperr.Forbidden("outside_task_scope", "task token cannot access another session")
		}
		return t, nil
	}
	if _, p := s.sessionAccess(r, t.SessionID); p != nil {
		return nil, p
	}
	return t, nil
}

func (s *Server) GetTask(w http.ResponseWriter, r *http.Request, taskId gen.TaskId) {
	t, p := s.taskAccess(r, taskId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	attempts, err := tasks.ListAttempts(r.Context(), s.DB, t.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	usage, err := tasks.GetUsage(r.Context(), s.DB, t.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if attempts == nil {
		attempts = []tasks.Attempt{}
	}
	writeJSON(w, http.StatusOK, tasks.ToAPI(t, attempts, usage))
}

func (s *Server) ListTaskEvents(w http.ResponseWriter, r *http.Request, taskId gen.TaskId, params gen.ListTaskEventsParams) {
	if _, p := s.taskAccess(r, taskId); p != nil {
		writeProblem(w, p)
		return
	}
	after := -1
	if params.AfterSeq != nil {
		after = *params.AfterSeq
	}
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	inc := params.IncludeSuperseded != nil && *params.IncludeSuperseded
	items, more, err := s.Events.List(r.Context(), taskId, after, inc, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": more, "structured": true})
}

func (s *Server) GetCliContext(w http.ResponseWriter, r *http.Request) {
	pr := principalOf(r)
	if pr.Task == nil {
		writeProblem(w, apperr.Unauthorized("unauthorized", "TaskToken required"))
		return
	}
	sc := pr.Task
	t, err := tasks.Get(r.Context(), s.DB, sc.TaskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	sess, err := sessions.Load(r.Context(), s.DB, sc.SessionID, sessions.Viewer{})
	if err != nil {
		writeErr(w, err)
		return
	}
	lastSeq, err := s.lastClientSeq(r, sc.TaskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := gen.CliContext{
		TaskId: sc.TaskID, LaneId: sc.LaneID, SessionId: sc.SessionID, AgentId: sc.AgentID, WorkspaceId: sess.WorkspaceId,
		Attempt: sc.Attempt, LastSeq: lastSeq, ExpiresAt: sc.ExpiresAt,
		DelegatedFromTaskId:        tasks.NullUUID(t.DelegatedFromTaskID),
		SuppressedDelegatorAgentId: nullable.NewNullNullable[openapi_types.UUID](),
		OpenHitlRequestId:          nullable.NewNullNullable[openapi_types.UUID](),
	}
	parts := make([]struct {
		AgentId     openapi_types.UUID `json:"agent_id"`
		MentionLink string             `json:"mention_link"`
		Name        string             `json:"name"`
		Role        *gen.AgentRole     `json:"role,omitempty"`
	}, 0)
	if sess.Participants != nil {
		for _, p := range *sess.Participants {
			role := p.Agent.Role
			link := ""
			if p.MentionLink != nil {
				link = *p.MentionLink
			}
			if p.AgentId == sc.AgentID {
				out.AgentName = &p.Agent.Name
			}
			parts = append(parts, struct {
				AgentId     openapi_types.UUID `json:"agent_id"`
				MentionLink string             `json:"mention_link"`
				Name        string             `json:"name"`
				Role        *gen.AgentRole     `json:"role,omitempty"`
			}{AgentId: p.AgentId, MentionLink: link, Name: p.Agent.Name, Role: &role})
		}
	}
	out.Participants = &parts
	writeJSON(w, http.StatusOK, out)
}

// StreamEvents is the one SSE stream (openapi.md D1).
func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.StreamEventsParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, apperr.New(http.StatusInternalServerError, "no_flush", "streaming unsupported"))
		return
	}
	var sessionIDs []uuid.UUID
	if params.SessionId != nil {
		for _, id := range *params.SessionId {
			sessionIDs = append(sessionIDs, id)
		}
	}
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" && params.LastEventID != nil {
		lastID = *params.LastEventID
	}
	if q := r.URL.Query().Get("last_event_id"); lastID == "" && q != "" {
		lastID = q
	}

	sub := s.Hub.Subscribe(workspaceId, sessionIDs)
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(id, typ string, data []byte) {
		if id != "" {
			fmt.Fprintf(w, "id: %s\n", id)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, data)
		flusher.Flush()
	}

	if lastID != "" {
		var cursor int64
		if _, err := fmt.Sscan(lastID, &cursor); err == nil {
			backfill, resync, err := s.Hub.Backfill(r.Context(), workspaceId, cursor, sessionIDs)
			if err != nil {
				s.Log.Warn("sse backfill", "err", err)
			}
			if resync {
				write("", "resync", []byte(`{"id":"","type":"resync","at":"`+s.Clock.Now().UTC().Format(time.RFC3339)+`","payload":{"reason":"cursor_outside_retention"}}`))
			}
			for _, e := range backfill {
				data, _ := e.MarshalJSON()
				write(fmt.Sprint(e.ID), e.Type, data)
			}
		}
	}
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e := <-sub.C:
			data, err := e.MarshalJSON()
			if err != nil {
				continue
			}
			id := ""
			if !e.Ephemeral {
				id = fmt.Sprint(e.ID)
			}
			write(id, e.Type, data)
		}
	}
}
