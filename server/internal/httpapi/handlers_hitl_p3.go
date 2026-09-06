package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The P3 half of HITL (openapi createHitlRequest · getHitlRequest ·
// listHitlRequests, and the agent/budget branches of respondHitlRequest).
// P2 answered exactly one platform-issued approval (handlers_hitl.go); this
// file is FR-5.1·5.2·5.4 for everything an agent asks.

// hitlSession is the session context a request is judged in.
type hitlSession struct {
	WorkspaceID uuid.UUID
	Director    uuid.UUID
	Deputy      *uuid.UUID
	Autonomy    string
	Status      string
}

func loadHitlSession(ctx context.Context, q db.DBTX, sessionID uuid.UUID) (*hitlSession, error) {
	var h hitlSession
	err := q.QueryRow(ctx, `
		SELECT workspace_id, director_user_id, deputy_director_user_id, autonomy::text, status::text
		FROM session WHERE id = $1`, sessionID).
		Scan(&h.WorkspaceID, &h.Director, &h.Deputy, &h.Autonomy, &h.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("session")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &h, nil
}

// ---------------------------------------------------------------------------
// createHitlRequest — `colab hitl ask` / `approve-request` / `request-info`
// ---------------------------------------------------------------------------

// hitlCreateFields is the HitlCreate oneOf flattened. The discriminator is
// `type` (openapi HitlCreate), so the union is read once here and every rule
// after it works on one shape.
type hitlCreateFields struct {
	Kind            string
	Question        string
	Context         string
	Options         []string
	ProposedDefault string
	ApproverSpec    string
	DueIn           time.Duration
	ArtifactID      *uuid.UUID
}

func readHitlCreate(raw []byte) (hitlCreateFields, *Problem) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return hitlCreateFields{}, apperr.Validation(apperr.Field("body", "malformed_json", err.Error()))
	}
	var in struct {
		Type            string     `json:"type"`
		Question        string     `json:"question"`
		Summary         string     `json:"summary"`
		What            string     `json:"what"`
		Why             string     `json:"why"`
		Context         string     `json:"context"`
		Options         []string   `json:"options"`
		ProposedDefault string     `json:"proposed_default"`
		ApproverSpec    string     `json:"approver_spec"`
		DueIn           string     `json:"due_in"`
		ArtifactID      *uuid.UUID `json:"artifact_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return hitlCreateFields{}, apperr.Validation(apperr.Field("body", "malformed_json", err.Error()))
	}
	f := hitlCreateFields{
		Kind: in.Type, Options: in.Options, ProposedDefault: in.ProposedDefault,
		ApproverSpec: in.ApproverSpec, ArtifactID: in.ArtifactID, DueIn: hitl.DefaultDueIn,
	}
	// FR-5.1's table gives each type its own flag name and stores all of them
	// in `question` — the CLI wording differs, the record does not.
	switch in.Type {
	case hitl.KindApproval:
		f.Question, f.Context = strings.TrimSpace(in.Summary), strings.TrimSpace(in.Context)
	case hitl.KindInfo:
		f.Question, f.Context = strings.TrimSpace(in.What), strings.TrimSpace(in.Why)
	default:
		f.Question, f.Context = strings.TrimSpace(in.Question), strings.TrimSpace(in.Context)
	}
	if in.DueIn != "" {
		d, err := parseISODuration(in.DueIn)
		if err != nil {
			return f, apperr.Validation(apperr.Field("due_in", "invalid", "due_in must be an ISO 8601 duration"))
		}
		f.DueIn = d
	}
	return f, nil
}

func (s *Server) CreateHitlRequest(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.CreateHitlRequestParams) {
	// `source: agent` only. A system-issued request is server-internal (the
	// contract says so) and letting a token mint one would let an agent forge
	// the platform's own budget approval.
	scope := principalOf(r).Task
	if scope == nil {
		writeProblem(w, apperr.Unauthorized("task_token_required",
			"createHitlRequest is called by the agent with its COLAB_TASK_TOKEN (openapi security TaskToken)"))
		return
	}
	if scope.SessionID != sessionId {
		writeProblem(w, apperr.Forbidden("outside_task_scope", "task token cannot access another session"))
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	f, p := readHitlCreate(body)
	if p != nil {
		writeProblem(w, p)
		return
	}
	key := ""
	if params.IdempotencyKey != nil {
		key = params.IdempotencyKey.String()
	}
	s.idempotent(r.Context(), w, "task:"+scope.TaskID.String(), key, requestHash(r, body),
		func() (int, any, *Problem) { return s.createHitl(r.Context(), scope.TaskID, sessionId, f) })
}

func (s *Server) createHitl(ctx context.Context, taskID, sessionID uuid.UUID, f hitlCreateFields) (int, any, *Problem) {
	now := s.Clock.Now()
	sess, err := loadHitlSession(ctx, s.DB, sessionID)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	t, err := tasks.Get(ctx, tx, taskID)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	// FR-7.1 step 4 needs the premise, not a unique-violation: the refusal has
	// to name the request that stands and leave a feed entry (E7-04).
	var openID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM hitl_request WHERE task_id = $1 AND status = 'open'`, taskID).Scan(&openID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, apperr.Internal(err)
	}

	plan := hitl.PlanRegister(hitl.RegisterInput{
		Kind: f.Kind, Question: f.Question, Options: f.Options,
		ProposedDefault: f.ProposedDefault, ApproverSpec: f.ApproverSpec,
		AlreadyOpen: openID != nil,
	})
	if !plan.Accepted {
		if plan.ErrorCode == hitl.CodeAlreadyOpen {
			if plan.FeedRecorded {
				if err := tasks.InsertServerEvent(ctx, tx, t.ID, t.Attempt, "status", "error", "hitl.rejected", "error",
					map[string]any{
						"note":     "이미 열린 HITL 요청이 있어 두 번째 요청을 거절했습니다",
						"question": f.Question, "open_hitl_request_id": openID.String(),
					}, now); err != nil {
					return 0, nil, apperr.Internal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					return 0, nil, apperr.Internal(err)
				}
			}
			return 0, nil, apperr.Conflict(hitl.CodeAlreadyOpen,
				"이 task에는 이미 열린 HITL 요청이 있습니다 (FR-7.1)")
		}
		return 0, nil, apperr.Validation(apperr.Field(plan.ErrorField, "invalid", plan.ErrorMessage))
	}

	// The card goes on the timeline at registration: the Director should see
	// the question while the agent is still finishing its turn (FR-5.2).
	agentName := ""
	_ = tx.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, t.AgentID).Scan(&agentName)
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, source_task_id, created_at)
		VALUES ($1, 'agent', $2, $3, 'hitl', $4, $5) RETURNING id`,
		sessionID, t.AgentID, hitlCardBody(f, agentName), t.ID, now).Scan(&msgID); err != nil {
		return 0, nil, apperr.Internal(err)
	}

	var id uuid.UUID
	options := f.Options
	if options == nil {
		options = []string{}
	}
	var def, cx *string
	if f.ProposedDefault != "" {
		def = &f.ProposedDefault
	}
	if f.Context != "" {
		cx = &f.Context
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, context, options, proposed_default,
		                          approver_spec, purpose, artifact_id, message_id, due_at, created_at)
		VALUES ($1, $2, 'agent', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		sessionID, t.ID, f.Kind, f.Question, cx, options, def, plan.ApproverSpec, plan.Purpose,
		f.ArtifactID, msgID, now.Add(f.DueIn), now).Scan(&id); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	if err := s.Tasks.SetPendingHitl(ctx, tx, t.ID, now); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	// FR-8: the people who may answer get an inbox item. PlanEscalation is
	// what says this path produces one request and one inbox item and wakes
	// nobody's delegator (E7-19).
	esc := hitl.PlanEscalation("hitl", uuid.Nil)
	if esc.InboxItems > 0 {
		if err := s.hitlInbox(ctx, tx, sess, sessionID, id, plan.ApproverSpec, now); err != nil {
			return 0, nil, apperr.Internal(err)
		}
	}
	if err := tasks.InsertServerEvent(ctx, tx, t.ID, t.Attempt, "status", "hitl", "hitl.requested", "ok",
		map[string]any{"hitl_request_id": id.String(), "type": f.Kind, "question": f.Question}, now); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, apperr.Internal(err)
	}
	s.publishHitl(ctx, sess.WorkspaceID, sessionID, id, "hitl.created")
	out, err := s.hitlAPI(ctx, s.DB, id, nil)
	if err != nil {
		return 0, nil, apperr.As(err)
	}
	return http.StatusCreated, map[string]any{
		"hitl_request": out, "turn_end_required": plan.TurnEndRequired, "message_id": msgID,
	}, nil
}

// hitlCardBody is the timeline card's text (SCREEN §4.6: enough to answer
// without opening the session).
func hitlCardBody(f hitlCreateFields, agentName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[HITL:%s] %s", f.Kind, f.Question)
	if f.Context != "" {
		fmt.Fprintf(&b, "\n%s", f.Context)
	}
	if len(f.Options) > 0 {
		fmt.Fprintf(&b, "\n선택지: %s", strings.Join(f.Options, " · "))
	}
	if f.ProposedDefault != "" {
		fmt.Fprintf(&b, "\n에이전트 제안: %s", f.ProposedDefault)
	}
	return b.String()
}

// hitlInbox files the request for whoever may answer it. The deputy is filed
// too and marked `delegated` (O5) — the inbox is where the hand-over becomes
// visible, and a deputy who only learns of the request at hour 12 has half the
// deadline to act on something they have never seen.
func (s *Server) hitlInbox(ctx context.Context, tx pgx.Tx, sess *hitlSession, sessionID, hitlID uuid.UUID, spec string, now time.Time) error {
	targets := []uuid.UUID{}
	switch spec {
	case hitl.SpecAnyMember:
		// The cursor is drained fully before any write below. pgx releases the
		// transaction's connection when Next() returns false, so the deferred
		// Close is safe here — the "conn busy" trap (G4 S7) is writing WHILE
		// iterating, which this does not do. Measured: reverting to a
		// write-inside-the-loop shape is what breaks it, not the defer.
		rows, err := tx.Query(ctx, `SELECT user_id FROM member WHERE workspace_id = $1`, sess.WorkspaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			targets = append(targets, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	case hitl.SpecDirector:
		targets = append(targets, sess.Director)
		if sess.Deputy != nil {
			targets = append(targets, *sess.Deputy)
		}
	default:
		if id, err := uuid.Parse(spec); err == nil {
			targets = append(targets, id)
		}
	}
	for _, u := range targets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
			SELECT m.id, $6::inbox_item_type, $7::inbox_severity, $1, $2, $3
			FROM member m WHERE m.workspace_id = $4 AND m.user_id = $5
			  AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.member_id = m.id AND i.ref_id = $2)`,
			sessionID, hitlID, now, sess.WorkspaceID, u, inbox.TypeHitlRequest, inbox.Severity(inbox.TypeHitlRequest)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) publishHitl(ctx context.Context, wsID, sessionID, hitlID uuid.UUID, event string) {
	if s.Hub == nil {
		return
	}
	out, err := s.hitlAPI(ctx, s.DB, hitlID, nil)
	if err != nil {
		return
	}
	sid := sessionID
	_ = s.Hub.Publish(ctx, s.DB, wsID, &sid, event, out)
}

// ---------------------------------------------------------------------------
// getHitlRequest · listHitlRequests
// ---------------------------------------------------------------------------

func (s *Server) GetHitlRequest(w http.ResponseWriter, r *http.Request, hitlRequestId gen.HitlRequestId) {
	row, err := loadHitlRow(r.Context(), s.DB, hitlRequestId)
	if err != nil {
		writeErr(w, err)
		return
	}
	u, p := s.sessionAccess(r, row.SessionID)
	if p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.hitlAPI(r.Context(), s.DB, hitlRequestId, viewerID(u))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ListHitlRequests(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId, params gen.ListHitlRequestsParams) {
	u, p := s.sessionAccess(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateLimit(params.Limit); p != nil {
		writeProblem(w, p)
		return
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	args := []any{sessionId}
	where := "h.session_id = $1"
	if params.Status != nil {
		args = append(args, string(*params.Status))
		where += fmt.Sprintf(" AND h.status = $%d", len(args))
	}
	args = append(args, limit+1)
	rows, err := s.DB.Query(r.Context(), `
		SELECT h.id FROM hitl_request h WHERE `+where+`
		ORDER BY h.created_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			writeErr(w, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}
	items := []gen.HitlRequest{}
	for i, id := range ids {
		if i >= limit {
			break
		}
		out, err := s.hitlAPI(r.Context(), s.DB, id, viewerID(u))
		if err != nil {
			writeErr(w, err)
			return
		}
		items = append(items, *out)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": len(ids) > limit})
}

func viewerID(u *gen.User) *uuid.UUID {
	if u == nil {
		return nil
	}
	id := u.Id
	return &id
}

// parseISODuration reads the subset of ISO 8601 durations `due_in` and
// `time_extension` use (PnDTnHnMnS). Months and years are rejected rather than
// guessed — "P1M" as 30 days is a deadline nobody agreed to.
func parseISODuration(s string) (time.Duration, error) {
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("not an ISO 8601 duration")
	}
	rest := s[1:]
	var total time.Duration
	inTime := false
	num := ""
	for _, c := range rest {
		switch {
		case c == 'T':
			inTime, num = true, ""
		case c >= '0' && c <= '9' || c == '.':
			num += string(c)
		default:
			if num == "" {
				return 0, fmt.Errorf("bad duration")
			}
			var v float64
			if _, err := fmt.Sscanf(num, "%g", &v); err != nil {
				return 0, err
			}
			switch {
			case c == 'D':
				total += time.Duration(v * float64(24*time.Hour))
			case c == 'W':
				total += time.Duration(v * float64(7*24*time.Hour))
			case c == 'H' && inTime:
				total += time.Duration(v * float64(time.Hour))
			case c == 'M' && inTime:
				total += time.Duration(v * float64(time.Minute))
			case c == 'S' && inTime:
				total += time.Duration(v * float64(time.Second))
			default:
				return 0, fmt.Errorf("unsupported duration unit %q", string(c))
			}
			num = ""
		}
	}
	if num != "" {
		return 0, fmt.Errorf("bad duration")
	}
	return total, nil
}

func derefUUID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}
