// P4 operations — FR-6.4 (workdir 관리·GC) and FR-9.2 (오프라인 유예·재바인딩·
// 런타임 삭제 차단). openapi `checkRepo` · `listRuntimeWorkdirs` ·
// `deleteWorkdir` · `deleteRuntime` · `rebindSession`.
package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// ---------------------------------------------------------------------------
// checkRepo — FR-2.1, E13-01
// ---------------------------------------------------------------------------

// CheckRepo answers the S6 wizard's repository question for one runtime.
//
// WHERE THE ANSWER COMES FROM. The daemon already reports every repository it
// found — `repos: [{path, remote_url, branch, clean}]` on the probe (§3) — and
// that report is what the rebinding rule reads too (FR-9.2 F). There is no
// `repo_check` command in daemon-protocol §4.3, so asking the daemon
// synchronously would mean growing the protocol; the probe is the same fact,
// already on the server. A `probe` command is queued alongside the answer so
// the next call sees a fresh scan — that is what makes "the daemon is asked"
// true over time rather than in one round trip.
//
// **Contract gap reported to Lead (T-S9)**: the operation's text says the
// server asks the daemon and names a `504` for a slow answer. There is no
// command for it, so the `504` branch is unreachable in this implementation and
// a repository created since the last probe reads as "not found" until the
// queued probe lands. Closing that properly needs a `repo_check` command in
// §4.3, which is a contract change this PR may not make.
func (s *Server) CheckRepo(w http.ResponseWriter, r *http.Request, runtimeId gen.RuntimeId) {
	rt, err := s.Runtimes.Get(r.Context(), runtimeId)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, _, p := s.member(r, rt.WorkspaceId); p != nil {
		if p.Status == http.StatusForbidden {
			p = apperr.NotFound("runtime")
		}
		writeProblem(w, p)
		return
	}
	var in gen.CheckRepoJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if in.RepoPath == "" {
		writeProblem(w, apperr.Validation(apperr.Field("repo_path", "required", "repo_path is required")))
		return
	}
	if rt.Status != gen.RuntimeStatus("online") {
		writeProblem(w, apperr.Conflict("runtime_offline",
			"이 컴퓨터의 데몬이 연결돼 있지 않습니다 — 저장소를 확인할 수 없습니다"))
		return
	}

	check := workdirs.RepoCheck{RepoPath: in.RepoPath}
	for _, repo := range rt.Repos {
		if repo.Path != in.RepoPath {
			continue
		}
		check.Exists, check.IsGit = true, true
		if repo.Clean.IsSpecified() && !repo.Clean.IsNull() {
			check.Clean = repo.Clean.MustGet()
		}
		if repo.RemoteUrl.IsSpecified() && !repo.RemoteUrl.IsNull() {
			check.RemoteURL = repo.RemoteUrl.MustGet()
		}
		if repo.Branch.IsSpecified() && !repo.Branch.IsNull() {
			check.CurrentBranch = repo.Branch.MustGet()
			// The probe reports the CURRENT branch, not the default one. They
			// are the same on a fresh clone and differ the moment somebody
			// checks out a feature branch; treating current as default would
			// cut every worktree from whatever the person happened to be on.
			check.DefaultBranch = repo.Branch.MustGet()
		}
		break
	}
	// A fresh scan for next time, whatever this answer was: the probe is a
	// snapshot and the person is standing in a wizard looking at a path they
	// just created.
	if err := tokens.QueueCommand(r.Context(), s.DB, runtimeId, contracts.Command{Type: contracts.CmdProbe}); err != nil {
		s.Log.Warn("check repo: queue probe", "runtime", runtimeId, "err", err)
	}

	v := workdirs.CheckRepo(check)
	out := gen.RepoCheck{
		Ok:        v.OK,
		RepoPath:  in.RepoPath,
		Exists:    check.Exists,
		IsGit:     &check.IsGit,
		Problems:  &v.Problems,
		CheckedAt: p4ptr(s.Clock.Now().UTC()),
	}
	if check.Exists {
		out.Clean = nullable.NewNullableWithValue(check.Clean)
	} else {
		out.Clean = nullable.NewNullNullable[bool]()
	}
	out.DefaultBranch = nullStr(check.DefaultBranch)
	out.CurrentBranch = nullStr(check.CurrentBranch)
	out.RemoteUrl = nullStr(check.RemoteURL)
	out.TracksBriefFile = nullable.NewNullNullable[bool]()
	// `ok: false` is an ANSWER, not an error — the contract says 200 either way
	// and the form shows the reason next to the disabled button.
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// listRuntimeWorkdirs · deleteWorkdir — FR-6.4, S13
// ---------------------------------------------------------------------------

func (s *Server) ListRuntimeWorkdirs(w http.ResponseWriter, r *http.Request, runtimeId gen.RuntimeId, params gen.ListRuntimeWorkdirsParams) {
	rt, err := s.Runtimes.Get(r.Context(), runtimeId)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, _, p := s.member(r, rt.WorkspaceId); p != nil {
		if p.Status == http.StatusForbidden {
			p = apperr.NotFound("runtime")
		}
		writeProblem(w, p)
		return
	}
	q := workdirs.ListQuery{RuntimeID: runtimeId}
	if params.Status != nil {
		st := string(*params.Status)
		q.Status = &st
	}
	if params.SessionId != nil {
		sid := uuid.UUID(*params.SessionId)
		q.SessionID = &sid
	}
	if params.Limit != nil {
		q.Limit = *params.Limit
	}
	items, total, err := workdirs.ListForRuntime(r.Context(), s.DB, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	var quota *int
	var g int
	if err := s.DB.QueryRow(r.Context(),
		`SELECT COALESCE(workdir_disk_quota_gb, 0) FROM workspace_settings WHERE workspace_id = $1`,
		rt.WorkspaceId).Scan(&g); err == nil && g > 0 {
		quota = &g
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":            items,
		"disk_bytes_total": total,
		"disk_quota_gb":    quota,
		"has_more":         false,
		"next_cursor":      nil,
	})
}

// DeleteWorkdir is S13's manual delete.
//
// The default is a REFUSAL when the directory holds work (`409 workdir_dirty`),
// and `force=true` is the confirmation. The two blocked reasons stay apart in
// `Problem.detail` because the person's next action differs — merge, or commit
// and discard.
//
// The deletion itself is the daemon's, so this is a `202` and the outcome
// arrives as SSE `workdir.updated` when the §6 report says `gc: deleted`. The
// row is NOT closed here: the server asked, it did not observe.
func (s *Server) DeleteWorkdir(w http.ResponseWriter, r *http.Request, workdirId openapi_types.UUID, params gen.DeleteWorkdirParams) {
	id := uuid.UUID(workdirId)
	var wsID, sessionID uuid.UUID
	var runtimeID *uuid.UUID
	var director uuid.UUID
	var path, kind string
	if err := s.DB.QueryRow(r.Context(), `
		SELECT s.workspace_id, s.id, s.runtime_id, s.director_user_id, w.path_or_ref, w.kind::text
		FROM workdir w JOIN session s ON s.id = w.session_id WHERE w.id = $1`, id).
		Scan(&wsID, &sessionID, &runtimeID, &director, &path, &kind); err != nil {
		writeProblem(w, apperr.NotFound("workdir"))
		return
	}
	u, _, p := s.member(r, wsID)
	if p != nil {
		if p.Status == http.StatusForbidden {
			p = apperr.NotFound("workdir")
		}
		writeProblem(w, p)
		return
	}
	// owner·admin, or this session's Director (openapi deleteWorkdir 권한).
	if _, adminProblem := s.admin(r, wsID); adminProblem != nil && u.Id != openapi_types.UUID(director) {
		writeProblem(w, apperr.Forbidden("forbidden",
			"이 workdir 은 워크스페이스 관리자나 그 세션의 Director 만 삭제할 수 있습니다"))
		return
	}

	reason, merged, ahead, treeDirty, err := workdirs.BlockReason(r.Context(), s.DB, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if reason == "" {
		// The sweep records a reason only past the retention window; a manual
		// delete can arrive at any time, so the same judgement is applied to
		// the raw git columns here.
		switch {
		case treeDirty:
			reason = workdirs.GCReasonUncommitted
		case !merged && ahead > 0:
			reason = workdirs.GCReasonUnmerged
		}
	}
	force := params.Force != nil && *params.Force
	if reason != "" && reason != "runtime_gone" && !force {
		writeProblem(w, apperr.Conflict("workdir_dirty", workdirs.GCReasonText(reason)))
		return
	}
	if runtimeID == nil {
		writeProblem(w, apperr.Conflict("no_runtime",
			"이 세션에는 실행 머신이 없어 삭제를 요청할 데몬이 없습니다"))
		return
	}
	cmd := workdirs.BuildGCCommand(sessionID, []uuid.UUID{id}, []string{path})
	if err := tokens.QueueCommand(r.Context(), s.DB, *runtimeID, cmd); err != nil {
		writeErr(w, err)
		return
	}
	wd, err := workdirs.Load(r.Context(), s.DB, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, wd)
}

// ---------------------------------------------------------------------------
// deleteRuntime — FR-9.2 마지막 줄, E14-08
// ---------------------------------------------------------------------------

func (s *Server) DeleteRuntime(w http.ResponseWriter, r *http.Request, runtimeId gen.RuntimeId) {
	rt, err := s.Runtimes.Get(r.Context(), runtimeId)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, p := s.admin(r, rt.WorkspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	if err := s.Runtimes.DeleteRuntime(r.Context(), rt.WorkspaceId, runtimeId); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// rebindSession — FR-9.2, E14-03·06
// ---------------------------------------------------------------------------

func (s *Server) RebindSession(w http.ResponseWriter, r *http.Request, sessionId gen.SessionId) {
	_, wsID, p := s.sessionDirector(r, sessionId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RebindSessionJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	ack := in.AcknowledgeLoss != nil && *in.AcknowledgeLoss
	if _, err := s.Runtimes.Rebind(r.Context(), wsID, sessionId, uuid.UUID(in.RuntimeId), ack); err != nil {
		writeErr(w, err)
		return
	}
	u, _, p2 := s.member(r, wsID)
	if p2 != nil {
		writeProblem(w, p2)
		return
	}
	sess, err := s.Sessions.Get(r.Context(), sessionId, sessions.Viewer{UserID: &u.Id})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func p4ptr[T any](v T) *T { return &v }

// nullStr maps "" to the contract's null rather than to an empty string: a
// repository with no remote and a repository whose remote we did not read are
// different facts, and only one of them blocks a rebind (FR-9.2).
func nullStr(s string) nullable.Nullable[string] {
	if s == "" {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(s)
}
