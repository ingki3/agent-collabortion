package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// Workspace settings (SCREEN §2.3). P2 turns these on because FR-3.5's loop
// limits are "워크스페이스 설정에서 조정" and the router now reads them.
//
// S-12: 501 was never authorisation. The moment the operation exists, a
// non-admin member of the workspace — and anyone outside it — must be refused,
// or one member can raise another team's loop limits and budgets.

func (s *Server) GetWorkspaceSettings(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId) {
	if _, p := s.admin(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := loadSettings(r.Context(), s.DB, workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) UpdateWorkspaceSettings(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId) {
	if _, p := s.admin(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.WorkspaceSettingsUpdate
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if p := validateSettings(in); p != nil {
		writeProblem(w, p)
		return
	}
	now := s.Clock.Now()
	sets, args := []string{}, []any{workspaceId}
	add := func(expr string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", expr, len(args)))
	}
	if in.LoopLimits != nil {
		add("loop_limits", mergeJSON(in.LoopLimits))
	}
	if in.BudgetPolicy != nil {
		add("budget_policy", mergeJSON(in.BudgetPolicy))
	}
	if in.ContextReuse != nil {
		add("context_reuse", mergeJSON(in.ContextReuse))
	}
	if in.RuntimePolicy != nil {
		add("runtime_policy", mergeJSON(in.RuntimePolicy))
	}
	if in.DefaultIsolation != nil {
		add("default_isolation", string(*in.DefaultIsolation))
	}
	if in.WorkdirRetentionDays != nil {
		add("workdir_retention_days", *in.WorkdirRetentionDays)
	}
	if in.WorkdirDiskQuotaGb.IsSpecified() {
		if in.WorkdirDiskQuotaGb.IsNull() {
			add("workdir_disk_quota_gb", nil)
		} else {
			add("workdir_disk_quota_gb", in.WorkdirDiskQuotaGb.MustGet())
		}
	}
	if in.RuntimeOfflineGrace != nil {
		add("runtime_offline_grace", *in.RuntimeOfflineGrace)
	}
	if in.TaskEventMasking != nil {
		add("task_event_masking", *in.TaskEventMasking)
	}
	add("updated_at", now)

	q := "UPDATE workspace_settings SET " + strings.Join(sets, ", ") + " WHERE workspace_id = $1"
	tag, err := s.DB.Exec(r.Context(), q, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		// 0001 creates the row with the workspace, but a workspace made before
		// that guard existed would 404 here rather than silently do nothing.
		writeProblem(w, apperr.NotFound("workspace_settings"))
		return
	}
	out, err := loadSettings(r.Context(), s.DB, workspaceId)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// validateSettings rejects values the router would then have to defend against
// at every read. A loop limit of 0 disables the limit silently, which is the
// one thing FR-3.5 must not allow by accident.
func validateSettings(in gen.WorkspaceSettingsUpdate) *Problem {
	var errs []apperr.FieldError
	if l := in.LoopLimits; l != nil {
		check := func(name string, v *int, max int) {
			if v != nil && (*v < 1 || *v > max) {
				errs = append(errs, apperr.Field("loop_limits."+name, "out_of_range",
					fmt.Sprintf("%s must be between 1 and %d", name, max)))
			}
		}
		check("max_chain_depth", l.MaxChainDepth, 100)
		check("max_hops_per_hour", l.MaxHopsPerHour, 10000)
		check("max_pair_roundtrips", l.MaxPairRoundtrips, 100)
	}
	if v := in.WorkdirRetentionDays; v != nil && *v < 0 {
		errs = append(errs, apperr.Field("workdir_retention_days", "out_of_range", "must be >= 0"))
	}
	if in.WorkdirDiskQuotaGb.IsSpecified() && !in.WorkdirDiskQuotaGb.IsNull() && in.WorkdirDiskQuotaGb.MustGet() <= 0 {
		errs = append(errs, apperr.Field("workdir_disk_quota_gb", "out_of_range", "must be > 0"))
	}
	if v := in.DefaultIsolation; v != nil {
		switch *v {
		case "worktree", "container", "none":
		default:
			errs = append(errs, apperr.Field("default_isolation", "invalid", "unknown isolation kind"))
		}
	}
	if len(errs) > 0 {
		return apperr.Validation(errs...)
	}
	return nil
}

func loadSettings(ctx context.Context, q db.DBTX, wsID uuid.UUID) (*gen.WorkspaceSettings, error) {
	var out gen.WorkspaceSettings
	var loop, budget, reuse, runtime []byte
	var isolation string
	var quota *int
	var graceSeconds float64
	err := q.QueryRow(ctx, `
		SELECT loop_limits, budget_policy, context_reuse, runtime_policy, default_isolation::text,
		       workdir_retention_days, workdir_disk_quota_gb,
		       EXTRACT(epoch FROM runtime_offline_grace)::float8, task_event_masking, updated_at
		FROM workspace_settings WHERE workspace_id = $1`, wsID).
		Scan(&loop, &budget, &reuse, &runtime, &isolation,
			&out.WorkdirRetentionDays, &quota, &graceSeconds, &out.TaskEventMasking, &out.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, apperr.NotFound("workspace_settings")
	}
	if err != nil {
		return nil, err
	}
	out.DefaultIsolation = gen.IsolationKind(isolation)
	out.RuntimeOfflineGrace = isoDuration(time.Duration(graceSeconds) * time.Second)
	if quota != nil {
		out.WorkdirDiskQuotaGb = nullable.NewNullableWithValue(*quota)
	} else {
		out.WorkdirDiskQuotaGb = nullable.NewNullNullable[int]()
	}
	_ = json.Unmarshal(loop, &out.LoopLimits)
	_ = json.Unmarshal(budget, &out.BudgetPolicy)
	_ = json.Unmarshal(reuse, &out.ContextReuse)
	_ = json.Unmarshal(runtime, &out.RuntimePolicy)
	return &out, nil
}

// isoDuration renders a Postgres interval as the ISO 8601 form the contract
// asks for. Only whole days and hours occur in practice (default P7D).
func isoDuration(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	days := int(d / (24 * time.Hour))
	rest := d - time.Duration(days)*24*time.Hour
	out := "P"
	if days > 0 {
		out += fmt.Sprintf("%dD", days)
	}
	if rest > 0 {
		out += fmt.Sprintf("T%dS", int(rest/time.Second))
	}
	if out == "P" {
		return "PT0S"
	}
	return out
}

func mergeJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
