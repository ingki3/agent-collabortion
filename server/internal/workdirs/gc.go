package workdirs

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// ---------------------------------------------------------------------------
// 1. Repository validation — FR-2.1, E13-01
// ---------------------------------------------------------------------------

// RepoCheck is what the daemon found for a `repo_path` (openapi RepoCheck,
// `checkRepo`).
type RepoCheck struct {
	RepoPath      string
	Exists        bool
	IsGit         bool
	Clean         bool
	DefaultBranch string
	CurrentBranch string
	RemoteURL     string
	// TracksBriefFile is §8.4's pollution-prevention hint: whether the
	// repository already tracks `CLAUDE.md`/`AGENTS.md`, which decides
	// skip-worktree vs `.git/info/exclude` on the daemon side (M3).
	TracksBriefFile *bool
}

// RepoVerdict is what the S6 wizard does with it.
type RepoVerdict struct {
	OK bool
	// FormBlocked is E13-01's "폼에서 차단".
	FormBlocked bool
	// Problems is the reason list the form prints next to the disabled button.
	// It is never empty when blocked: a disabled Next with no explanation is
	// the bug this exists to prevent.
	Problems []string
	// HTTPStatus is what `checkRepo` answers. `ok: false` is an ANSWER, not an
	// error — the contract says 200 either way, and a 4xx would make the web
	// client show a failure toast instead of the reason inside the form.
	HTTPStatus int
}

// remoteMissing is the one problem that warns without blocking.
const remoteMissing = "remote 가 없습니다 — 이 머신이 사라지면 다른 컴퓨터로 재바인딩할 수 없습니다(FR-9.2)"

// CheckRepo turns a daemon repo report into the wizard's verdict.
//
// production caller: httpapi.Server.CheckRepo (POST
// /runtimes/{id}/repo-checks) and sessions.Create's worktree branch.
func CheckRepo(c RepoCheck) RepoVerdict {
	v := RepoVerdict{HTTPStatus: 200}
	switch {
	case !c.Exists:
		v.Problems = append(v.Problems, "그 경로가 이 컴퓨터에 없습니다")
	case !c.IsGit:
		v.Problems = append(v.Problems, "git 저장소가 아닙니다 — `worktree` 격리는 저장소가 필요합니다")
	default:
		if !c.Clean {
			// A dirty tree is not permanent, but `git worktree add` from it is
			// how a session starts on top of somebody's half-done work — and it
			// trips E13-13's GC rules two weeks later.
			v.Problems = append(v.Problems, "작업 트리가 깨끗하지 않습니다 — 커밋하거나 정리한 뒤 다시 확인하세요")
		}
		if c.DefaultBranch == "" {
			v.Problems = append(v.Problems, "기본 브랜치를 찾지 못했습니다")
		}
		if c.RemoteURL == "" {
			// Not fatal: a repository with no remote still works on one
			// machine. It is called out because FR-9.2 decides "같은 저장소" by
			// remote URL, so this session could never be rebound.
			v.Problems = append(v.Problems, remoteMissing)
		}
	}
	blocking := 0
	for _, p := range v.Problems {
		if p != remoteMissing {
			blocking++
		}
	}
	v.OK = blocking == 0
	v.FormBlocked = !v.OK
	return v
}

// ---------------------------------------------------------------------------
// 2. Worktree preparation — FR-6.4 표, E13-02
// ---------------------------------------------------------------------------

// WorktreesDir is the segment the daemon lays its checkouts out under:
// `<workdir_root>/worktrees/<session>/<agent>`
// (daemon/internal/workdir/worktree.go WorktreePath). The server assembles the
// SAME string because v0.7.3 §4.1 makes the path the server's to own — two
// halves each computing "their own correct" layout is exactly how G7's 차단 ①
// stayed invisible to both golden tables.
const WorktreesDir = "worktrees"

// WorktreeRequest is one lane starting under `worktree` isolation.
type WorktreeRequest struct {
	// Root is the daemon's `workdir_root` (probe §3, runtime.workdir_root).
	// Empty means the server does not know it — the plan then names NO path at
	// all (see PlanWorktree). It must never fall back to a relative one.
	Root        string
	SessionSlug string
	AgentSlug   string
	AgentID     uuid.UUID
	// ExistingForAgent is the worktree this agent already has in this session,
	// empty when it has none.
	ExistingForAgent string
	// BaseBranch is the repository's default branch (RepoCheck.default_branch).
	BaseBranch string
}

// WorktreePlan is what the daemon is told to do.
type WorktreePlan struct {
	Branch string
	Path   string
	// Created is false when an existing worktree is reused.
	Created    bool
	BaseBranch string
}

// PlanWorktree names the branch and the ABSOLUTE checkout path for one lane
// (daemon-protocol v0.7.3 §4.1).
//
// THE PATH IS ABSOLUTE OR IT IS NOTHING. See the `Root` field and the
// `r.Root == ""` branch below.
//
// ONE WORKTREE PER AGENT, NOT PER LANE (FR-6.4/C3). The second lane of the
// same agent reuses the first one's checkout — which is also why those lanes
// run sequentially (FR-6.3), and why the crash simulator's double write is
// possible here at all. Creating a second worktree would make E16-B's verdict
// line count four instead of two and split one agent's work across two trees.
//
// production caller: queue.buildBundle (the `workdir` plan of a `worktree`
// lane). The `git worktree add` itself is the daemon's (T-D9).
func PlanWorktree(r WorktreeRequest) WorktreePlan {
	branch := "colab/" + r.SessionSlug + "/" + r.AgentSlug
	base := r.BaseBranch
	if base == "" {
		base = "HEAD"
	}
	if r.ExistingForAgent != "" {
		return WorktreePlan{Branch: branch, Path: r.ExistingForAgent, BaseBranch: base}
	}
	if r.Root == "" {
		// S-55: an unknown `workdir_root` yields an EMPTY path, never a
		// relative one. A relative path looks like an answer all the way down
		// the wire — the daemon absolutises it against its own CWD, checks a
		// worktree out INSIDE the user's repository and hands the runtime a
		// directory that does not exist, and every attempt dies as
		// `failed(config)` with a message about `npx` (T-I4 차단 ①). The caller
		// (queue.buildBundle) turns this into a refusal to build the bundle.
		return WorktreePlan{Branch: branch, Created: true, BaseBranch: base}
	}
	return WorktreePlan{
		Branch:     branch,
		Path:       path.Join(r.Root, WorktreesDir, r.SessionSlug, r.AgentSlug),
		Created:    true,
		BaseBranch: base,
	}
}

// Slug makes a session title or an agent name usable inside a branch name and
// a path. git refuses a ref containing a space, `..`, `~`, `^`, `:`, `?`, `*`
// or `[`, so anything outside a conservative set becomes `-`.
func Slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// BundleWorkdirPaths is what a lane's TaskBundle may name as a workdir
// (daemon-protocol §4.1).
//
// E13-08: a reviewer reviews ARTIFACTS. Handing QA the Frontend checkout lets
// a reviewer edit the code it is reviewing, and under `worktree` two agents in
// one checkout is repository corruption rather than a stale read. So this asks
// by ownership, never "every workdir of the session".
//
// production caller: queue.buildBundle (the `workdir` field).
func BundleWorkdirPaths(ctx context.Context, q db.DBTX, sessionID, agentID uuid.UUID) ([]string, error) {
	// U2 (T-I4 §0.2): `gc_blocked_reason = 'runtime_gone'` is the stamp
	// runtimes.Rebind puts on the DEAD machine's rows. Those rows stay (the
	// checkouts still exist, on a computer nobody can reach), so
	// `status <> 'deleted'` still matched them and the bundle for the NEW
	// machine kept naming a path from the old one — the e2e run had to fold
	// them away by hand (`retire_workdirs`) for the rebind to go anywhere.
	// Filtering on the reason rather than on `retained` keeps a GC-refused row
	// — which is on THIS machine and legitimately reusable — in the answer.
	rows, err := q.Query(ctx, `
		SELECT DISTINCT w.path_or_ref
		FROM workdir w
		LEFT JOIN lane l ON l.id = w.lane_id
		WHERE w.session_id = $1
		  AND w.status <> 'deleted'
		  AND w.gc_blocked_reason IS DISTINCT FROM 'runtime_gone'
		  AND (w.agent_id = $2 OR l.agent_id = $2)
		ORDER BY w.path_or_ref`, sessionID, agentID)
	if err != nil {
		return nil, fmt.Errorf("workdirs: bundle paths: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 3. GC judgement — FR-6.4 정리(GC) 정책, E13-09~18
// ---------------------------------------------------------------------------

// GCCase is one workdir the GC pass looks at. Every duration is elapsed time
// against the injected clock; nothing here reads wall time.
type GCCase struct {
	WorkdirID uuid.UUID
	Path      string
	Isolation string // worktree | container | none
	// SessionStatus at the time of the pass.
	SessionStatus string
	// RetentionDays is `workdir_retention_days` (default 14).
	RetentionDays int
	// SinceSessionEnd is how long ago the session reached its final state.
	SinceSessionEnd time.Duration

	// The daemon's last §6 git report for this directory.
	Merged       bool
	CommitsAhead int
	TreeDirty    bool
}

// GCVerdict is the server's decision. The DAEMON never decides
// (daemon-protocol §6: "GC 판정은 서버가 한다 … 데몬은 스스로 지우지 않는다").
type GCVerdict struct {
	Delete bool
	// DeleteBranch is never true. FR-6.4: 삭제는 워크트리만 하고 브랜치는
	// 남긴다 — branches are cheap and a wrong deletion is not recoverable.
	DeleteBranch bool
	// NotifyDirector is FR-6.4's "삭제하지 않고 Director에게 알린다".
	NotifyDirector bool
	// Reason is one machine token. The two blocked cases must be told apart:
	// E13-12 asks the Director to merge, E13-13 asks them to commit or discard.
	Reason string
	// CommandIssued tracks Delete exactly — a decision nobody sends is not a
	// deletion.
	CommandIssued bool
}

// GC reason codes — this package's vocabulary for what the contracts describe
// in prose. They are what the notification and `deleteWorkdir`'s 409 detail
// are built from.
const (
	GCReasonUnmerged    = "unmerged_commits"
	GCReasonUncommitted = "uncommitted_changes"
)

// DefaultRetentionDays is FR-6.4's "기본 14일 보존".
const DefaultRetentionDays = 14

// JudgeGC decides one workdir.
//
// TWO INDEPENDENT GREEN LIGHTS, NOT ONE. "보존 기한이 지났다" only opens the
// question; the answer is yes only when (병합됨 AND 클린) or (커밋 0 AND
// 클린). FR-6.4 M4 spells out why the tree condition cannot be dropped:
// 시나리오 B submits a diff WITHOUT committing, so "커밋 없음" is the NORMAL
// state there, and a rule that reads commits alone deletes the whole feature.
//
// Retention counts from the session's END. Keying it off `created_at` — or off
// nothing — passes every "past retention" row and then deletes the checkout an
// agent is editing right now [EVAL 제안 행 E13-18].
//
// production caller: workdirs.Service.SweepGC (the scheduler pass).
func JudgeGC(c GCCase) GCVerdict {
	finished := c.SessionStatus == "completed" || c.SessionStatus == "cancelled"
	if !finished {
		// A live session keeps its directories however old they are.
		return GCVerdict{}
	}
	if c.Isolation != "worktree" {
		// `container`·`none` go the moment the session ends: the artifacts are
		// already on the server (FR-6.4, E13-14·15). The git columns are
		// meaningless here, and running these through the 14-day worktree rule
		// keeps disposable directories for two weeks.
		return GCVerdict{Delete: true, CommandIssued: true}
	}
	days := c.RetentionDays
	if days < 0 {
		days = DefaultRetentionDays
	}
	if c.SinceSessionEnd < time.Duration(days)*24*time.Hour {
		// The window has not passed. Nothing is wrong, so nothing is said — a
		// notification here would train the Director to ignore them.
		return GCVerdict{}
	}
	unmerged := !c.Merged && c.CommitsAhead > 0
	switch {
	case c.TreeDirty:
		// THE 시나리오 B case: a diff artifact was submitted and nothing was
		// committed. It takes precedence over the unmerged branch when both are
		// true — uncommitted work exists nowhere else at all, while a commit at
		// least survives on the branch this GC is forbidden from deleting. The
		// notification carries `commits_ahead` so the Director still learns the
		// other half (the reason enum has exactly two values, Lead T-S9 ask 2).
		return GCVerdict{NotifyDirector: true, Reason: GCReasonUncommitted}
	case unmerged:
		// These commits live ONLY in this worktree's branch on this machine —
		// push is a non-goal (§2.2), so deleting here destroys the work.
		return GCVerdict{NotifyDirector: true, Reason: GCReasonUnmerged}
	}
	return GCVerdict{Delete: true, CommandIssued: true}
}

// GCReasonText turns a reason code into the sentence the Director reads. The
// two are separate strings because the NEXT ACTION differs: E13-12 wants a
// merge, E13-13 wants a commit or a discard, and one sentence cannot ask both.
func GCReasonText(reason string) string {
	switch reason {
	case GCReasonUnmerged:
		return "미병합 커밋이 있어 삭제하지 않았습니다 — 브랜치를 병합하거나 필요 없으면 정리해 주세요"
	case GCReasonUncommitted:
		return "미커밋 변경이 있어 삭제하지 않았습니다 — 커밋하거나 버려 주세요"
	case "":
		return ""
	}
	return reason
}

// ---------------------------------------------------------------------------
// 4. Disk quota — FR-6.4 마지막 bullet, E13-16
// ---------------------------------------------------------------------------

// QuotaVerdict answers "may a new session be created right now?".
type QuotaVerdict struct {
	Blocked bool
	Code    string
	// DirectorAsked is FR-6.4's "Director에게 정리를 요청한다": a blocked
	// wizard with no cleanup path is a dead end.
	DirectorAsked bool
	HTTPStatus    int
	Detail        string
}

// QuotaCode is the contract's Problem.code.
const QuotaCode = "workdir_quota_exceeded"

const gib = int64(1) << 30

// CheckDiskQuota is E13-16.
//
// `>=`, not `>`: a quota that only trips when exceeded lets the disk fill to
// exactly full before anyone is told. `quotaGB <= 0` is "not set" — the column
// is `[integer, 'null']` and a null must not mean zero, which would block every
// session in a workspace that never configured one [EVAL 제안 행 E13-19].
//
// production caller: httpapi.Server.CreateSession, before the row is written.
func CheckDiskQuota(usedBytes int64, quotaGB int) QuotaVerdict {
	if quotaGB <= 0 {
		return QuotaVerdict{}
	}
	if usedBytes < int64(quotaGB)*gib {
		return QuotaVerdict{}
	}
	return QuotaVerdict{
		Blocked: true, Code: QuotaCode, DirectorAsked: true, HTTPStatus: 409,
		Detail: fmt.Sprintf("작업 공간 용량 상한(%dGB)에 도달했습니다 — Runtimes 화면에서 오래된 workdir 을 정리한 뒤 다시 시도하세요", quotaGB),
	}
}

// ---------------------------------------------------------------------------
// 5. The gc command — daemon-protocol §4.3 v0.7
// ---------------------------------------------------------------------------

// BuildGCCommand builds the §4.3 v0.7 payload.
//
// THE SERVER CARRIES THE PATHS. The daemon has never held a uuid↔path map, so
// an ids-only command falls back to EVERY lane workdir of the session — which
// is not what the retention rules decided, and under `worktree` would delete
// exactly the directories JudgeGC refused to touch. `workdir_ids` stays for a
// daemon still on v0.6.
//
// production caller: workdirs.Service.SweepGC and sessions.gcWorkdirs.
func BuildGCCommand(sessionID uuid.UUID, ids []uuid.UUID, paths []string) contracts.Command {
	targets := make([]contracts.GCWorkdir, 0, len(ids))
	strIDs := make([]string, 0, len(ids))
	for i, id := range ids {
		p := ""
		if i < len(paths) {
			p = paths[i]
		}
		strIDs = append(strIDs, id.String())
		targets = append(targets, contracts.GCWorkdir{ID: id.String(), Path: p})
	}
	return contracts.Command{
		Type:       contracts.CmdGC,
		SessionID:  sessionID.String(),
		WorkdirIDs: strIDs,
		Workdirs:   targets,
	}
}
