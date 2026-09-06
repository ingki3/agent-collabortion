package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The inbox (openapi listInbox · getInboxSummary · markInboxRead ·
// markAllInboxRead, FR-8, SCREEN §4.6).
//
// "나에게 열려 있는 HITL 요청 + 기한 임박 항목" is a union over the caller's
// memberships, so every query here is scoped by member_id — an inbox that
// leaks one row of another workspace is a data breach, not a bug.

// inboxRow is one stored item plus what its card needs.
type inboxRow struct {
	ID          uuid.UUID
	MemberID    uuid.UUID
	WorkspaceID uuid.UUID
	Type        string
	Severity    string
	SessionID   *uuid.UUID
	SessionName *string
	RefID       *uuid.UUID
	ReadAt      *time.Time
	CreatedAt   time.Time

	// HITL card data (NULL for other types).
	HitlType        *string
	HitlQuestion    *string
	HitlContext     *string
	HitlDefault     *string
	HitlDueAt       *time.Time
	HitlOverdue     *bool
	HitlStatus      *string
	HitlSpec        *string
	HitlCreatedAt   *time.Time
	HitlAgentName   *string
	SessionDirector *uuid.UUID
	SessionDeputy   *uuid.UUID
	SessionPaused   *string
}

const selectInbox = `
	SELECT i.id, i.member_id, m.workspace_id, i.type::text, i.severity::text, i.session_id, s.title,
	       i.ref_id, i.read_at, i.created_at,
	       h.type::text, h.question, h.context, h.proposed_default, h.due_at, h.overdue, h.status::text,
	       h.approver_spec, h.created_at, a.name, s.director_user_id, s.deputy_director_user_id, s.paused_reason::text
	FROM inbox_item i
	JOIN member m ON m.id = i.member_id
	LEFT JOIN session s ON s.id = i.session_id
	LEFT JOIN hitl_request h ON h.id = i.ref_id AND i.type = 'hitl_request'
	LEFT JOIN task t ON t.id = h.task_id
	LEFT JOIN agent a ON a.id = t.agent_id`

func scanInbox(rows pgx.Rows) ([]inboxRow, error) {
	var out []inboxRow
	for rows.Next() {
		var r inboxRow
		if err := rows.Scan(&r.ID, &r.MemberID, &r.WorkspaceID, &r.Type, &r.Severity, &r.SessionID, &r.SessionName,
			&r.RefID, &r.ReadAt, &r.CreatedAt,
			&r.HitlType, &r.HitlQuestion, &r.HitlContext, &r.HitlDefault, &r.HitlDueAt, &r.HitlOverdue, &r.HitlStatus,
			&r.HitlSpec, &r.HitlCreatedAt, &r.HitlAgentName, &r.SessionDirector, &r.SessionDeputy, &r.SessionPaused); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Server) ListInbox(w http.ResponseWriter, r *http.Request, params gen.ListInboxParams) {
	u, p := s.user(r)
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
	args := []any{u.Id}
	where := "m.user_id = $1"
	if params.WorkspaceId != nil {
		args = append(args, *params.WorkspaceId)
		where += fmt.Sprintf(" AND m.workspace_id = $%d", len(args))
	}
	if params.SessionId != nil {
		args = append(args, *params.SessionId)
		where += fmt.Sprintf(" AND i.session_id = $%d", len(args))
	}
	if params.Type != nil && len(*params.Type) > 0 {
		kinds := make([]string, 0, len(*params.Type))
		for _, k := range *params.Type {
			kinds = append(kinds, string(k))
		}
		args = append(args, kinds)
		where += fmt.Sprintf(" AND i.type::text = ANY($%d)", len(args))
	}
	if params.Filter != nil {
		switch *params.Filter {
		case "unread":
			where += " AND i.read_at IS NULL"
		case "action_required":
			// Reading an action_required item does not resolve it — the
			// response does (openapi markInboxRead) — so the filter is on
			// severity, not on read_at.
			where += " AND i.severity = 'action_required'"
		}
	}
	rows, err := s.DB.Query(r.Context(), selectInbox+` WHERE `+where+` ORDER BY i.created_at DESC LIMIT 500`, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	list, err := scanInbox(rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := s.Clock.Now()
	items := make([]gen.InboxItem, 0, len(list))
	for i := range list {
		items = append(items, s.inboxAPI(&list[i], u.Id, now))
	}
	// SCREEN §4.6's order: overdue → action_required → attention → info, and
	// inside a group the soonest deadline first. Ordering in SQL would need the
	// same CASE in four places; the page is bounded, so it is done once here.
	sort.SliceStable(items, func(a, b int) bool {
		ra := inbox.SortRank(string(items[a].Severity), boolp(items[a].Overdue))
		rb := inbox.SortRank(string(items[b].Severity), boolp(items[b].Overdue))
		if ra != rb {
			return ra < rb
		}
		da, oka := dueOf(items[a])
		dbb, okb := dueOf(items[b])
		switch {
		case oka && okb:
			return da.Before(dbb)
		case oka:
			return true
		case okb:
			return false
		}
		return items[a].CreatedAt.After(items[b].CreatedAt)
	})
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "has_more": hasMore})
}

func (s *Server) inboxAPI(r *inboxRow, viewer uuid.UUID, now time.Time) gen.InboxItem {
	out := gen.InboxItem{
		Id: r.ID, WorkspaceId: r.WorkspaceID,
		Type: gen.InboxItemType(r.Type), Severity: gen.InboxSeverity(r.Severity),
		SessionId: tasks.NullUUID(r.SessionID), RefId: tasks.NullUUID(r.RefID),
		ReadAt: tasks.NullTime(r.ReadAt), CreatedAt: r.CreatedAt,
		DueAt: nullableTime(nil),
	}
	if r.SessionID != nil && r.SessionName != nil {
		out.Session = &gen.SessionRef{Id: *r.SessionID, Title: *r.SessionName}
	}
	canRespond := false
	hitlType := ""
	body := ""
	title := ""
	switch r.Type {
	case inbox.TypeHitlRequest:
		if r.HitlType != nil {
			hitlType = *r.HitlType
		}
		if r.HitlQuestion != nil {
			title = *r.HitlQuestion
		}
		if r.HitlContext != nil {
			body = *r.HitlContext
		}
		if r.HitlDueAt != nil {
			out.DueAt = nullableTime(r.HitlDueAt)
			od := (r.HitlOverdue != nil && *r.HitlOverdue) || now.After(*r.HitlDueAt)
			out.Overdue = &od
		}
		if r.HitlStatus != nil && *r.HitlStatus == hitl.StatusOpen && r.HitlSpec != nil && r.HitlCreatedAt != nil {
			az := hitl.Authorize(hitl.AuthzInput{
				Spec: *r.HitlSpec, Director: derefUUID(r.SessionDirector), Deputy: derefUUID(r.SessionDeputy),
				Responder: viewer, IsMember: true,
				Elapsed: now.Sub(*r.HitlCreatedAt), DueIn: r.HitlDueAt.Sub(*r.HitlCreatedAt),
			})
			canRespond = az.Allowed
			// O5: the deputy's copy is marked so the card can say
			// "위임됨 · 지금부터 응답 가능" instead of looking like a duplicate.
			delegated := r.SessionDeputy != nil && viewer == *r.SessionDeputy
			out.Delegated = &delegated
		}
	case inbox.TypeSessionPaused:
		title = "세션이 멈췄습니다"
		if r.SessionPaused != nil {
			body = *r.SessionPaused
		}
		canRespond = viewer == derefUUID(r.SessionDirector)
	case inbox.TypeRunFailed:
		title = "작업이 실패했습니다"
		canRespond = viewer == derefUUID(r.SessionDirector)
	}
	acts := inbox.Actions(r.Type, hitlType, canRespond)
	out.Actions = make([]gen.InboxItemActions, 0, len(acts))
	for _, a := range acts {
		out.Actions = append(out.Actions, gen.InboxItemActions(a))
	}
	fillInboxCard(&out, title, body, r)
	return out
}

// fillInboxCard sets the generated anonymous card struct. It is its own
// function because the generated type is an inline struct literal and writing
// it at each branch above would bury the branch.
func fillInboxCard(out *gen.InboxItem, title, body string, r *inboxRow) {
	out.Card = &struct {
		AgentName       nullable.Nullable[string]             `json:"agent_name,omitempty"`
		Body            *string                               `json:"body,omitempty"`
		FailureKind     *gen.FailureKind                      `json:"failure_kind,omitempty"`
		GraceEndsAt     nullable.Nullable[time.Time]          `json:"grace_ends_at,omitempty"`
		HitlType        *gen.HitlType                         `json:"hitl_type,omitempty"`
		LaneId          nullable.Nullable[openapi_types.UUID] `json:"lane_id,omitempty"`
		MessageId       nullable.Nullable[openapi_types.UUID] `json:"message_id,omitempty"`
		PausedReason    *gen.PauseReason                      `json:"paused_reason,omitempty"`
		ProposedDefault nullable.Nullable[string]             `json:"proposed_default,omitempty"`
		RuntimeName     nullable.Nullable[string]             `json:"runtime_name,omitempty"`
		Summary         nullable.Nullable[string]             `json:"summary,omitempty"`
		Title           *string                               `json:"title,omitempty"`
	}{}
	if title != "" {
		out.Card.Title = &title
	}
	if body != "" {
		out.Card.Body = &body
	}
	out.Card.AgentName = tasks.NullString(r.HitlAgentName)
	out.Card.ProposedDefault = tasks.NullString(r.HitlDefault)
	if r.HitlType != nil {
		k := gen.HitlType(*r.HitlType)
		out.Card.HitlType = &k
	}
	if r.SessionPaused != nil {
		pr := gen.PauseReason(*r.SessionPaused)
		out.Card.PausedReason = &pr
	}
}

func nullableTime(t *time.Time) nullable.Nullable[time.Time] {
	if t == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(t.UTC())
}

func (s *Server) GetInboxSummary(w http.ResponseWriter, r *http.Request, params gen.GetInboxSummaryParams) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	args := []any{u.Id, s.Clock.Now()}
	where := "m.user_id = $1"
	if params.WorkspaceId != nil {
		args = append(args, *params.WorkspaceId)
		where += fmt.Sprintf(" AND m.workspace_id = $%d", len(args))
	}
	var actionRequired, overdue, unread int
	// `info` is excluded from the badge on purpose: counting it makes the
	// number permanent and unreadable (SCREEN §4.6).
	err := s.DB.QueryRow(r.Context(), `
		SELECT count(*) FILTER (WHERE i.severity = 'action_required'),
		       count(*) FILTER (WHERE h.id IS NOT NULL AND h.status = 'open' AND h.due_at < $2),
		       count(*) FILTER (WHERE i.read_at IS NULL)
		FROM inbox_item i
		JOIN member m ON m.id = i.member_id
		LEFT JOIN hitl_request h ON h.id = i.ref_id AND i.type = 'hitl_request'
		WHERE `+where, args...).Scan(&actionRequired, &overdue, &unread)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.InboxSummary{ActionRequired: actionRequired, Overdue: overdue, Unread: &unread})
}

func (s *Server) MarkInboxRead(w http.ResponseWriter, r *http.Request, inboxItemId openapi_types.UUID) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	// Idempotent (openapi): a second call keeps the first read_at.
	tag, err := s.DB.Exec(r.Context(), `
		UPDATE inbox_item i SET read_at = COALESCE(i.read_at, $3)
		FROM member m WHERE m.id = i.member_id AND m.user_id = $2 AND i.id = $1`, inboxItemId, u.Id, now)
	if err != nil {
		writeErr(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeProblem(w, apperr.NotFound("inbox_item"))
		return
	}
	rows, err := s.DB.Query(r.Context(), selectInbox+` WHERE i.id = $1 AND m.user_id = $2`, inboxItemId, u.Id)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	list, err := scanInbox(rows)
	if err != nil || len(list) == 0 {
		writeProblem(w, apperr.NotFound("inbox_item"))
		return
	}
	writeJSON(w, http.StatusOK, s.inboxAPI(&list[0], u.Id, now))
}

func (s *Server) MarkAllInboxRead(w http.ResponseWriter, r *http.Request, params gen.MarkAllInboxReadParams) {
	u, p := s.user(r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	args := []any{u.Id, s.Clock.Now()}
	where := "m.user_id = $1 AND i.read_at IS NULL AND i.severity <> 'action_required'"
	if params.WorkspaceId != nil {
		args = append(args, *params.WorkspaceId)
		where += fmt.Sprintf(" AND m.workspace_id = $%d", len(args))
	}
	// action_required is untouched (openapi markAllInboxRead): "전부 읽음" must
	// not clear the things that are still waiting for this person.
	tag, err := s.DB.Exec(r.Context(), `
		UPDATE inbox_item i SET read_at = $2 FROM member m WHERE m.id = i.member_id AND `+where, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": int(tag.RowsAffected())})
}

func boolp(b *bool) bool { return b != nil && *b }

func dueOf(i gen.InboxItem) (time.Time, bool) {
	if !i.DueAt.IsSpecified() || i.DueAt.IsNull() {
		return time.Time{}, false
	}
	v, err := i.DueAt.Get()
	return v, err == nil
}

var _ = errors.Is
var _ = context.Background
