// Runtime offline grace and session rebinding — PRD FR-9.2, EVAL E14.
//
// THE PATH BOTH REVIEWS MISSED. A session is pinned to `runtime_id` (C4), so a
// machine that never comes back leaves it `queued` forever with nobody told.
// The grace sweep is what turns that silence into a decision: at the threshold
// the session becomes `paused(runtime_offline)` and the Director picks
// rebinding or ending.
package runtimes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// ---------------------------------------------------------------------------
// 1. The grace sweep — E14-01, E14-02, E14-09, E14-10
// ---------------------------------------------------------------------------

// PauseReasonOffline is the `pause_reason` FR-9.2 uses.
const PauseReasonOffline = "runtime_offline"

// DefaultGrace is `runtime_offline_grace`'s default (FR-9.2).
const DefaultGrace = 7 * 24 * time.Hour

// RebindChoices are the two things FR-9.2 offers, and there are exactly two.
// They are constants because the notification, the inbox action list and the
// delete-runtime refusal all name the same pair; three copies of the words is
// how one of them ends up offering something the API cannot do.
var RebindChoices = []string{"rebind", "cancel"}

// OfflineCase is one runtime the sweep looks at.
type OfflineCase struct {
	RuntimeID uuid.UUID
	// OfflineFor is how long it has been offline at sweep time. Zero means
	// online.
	OfflineFor time.Duration
	// Grace is the workspace's `runtime_offline_grace` (default P7D).
	Grace time.Duration
	// QueuedTasks is how many tasks are waiting on this runtime.
	QueuedTasks int
	// SessionState before the sweep.
	SessionState string
	// OfflineSince is when it went offline, for `grace_ends_at`.
	OfflineSince time.Time
}

// OfflineOutcome is what the sweep decided for one session.
type OfflineOutcome struct {
	SessionState string
	PauseReason  string
	// DirectorNotified is the inbox item (`runtime_offline`, FR-8).
	DirectorNotified bool
	// Choices is what the notification offers.
	Choices []string
	// Dispatched counts tasks handed out during the sweep. A paused session
	// dispatches nothing (E5-04).
	Dispatched int
	// GraceEndsAt is `Runtime.grace_ends_at`, so S11 can show "언제까지"
	// instead of a bare "오프라인".
	GraceEndsAt time.Time
}

// PlanOffline decides one session's fate.
//
// THE GRACE PERIOD IS A THRESHOLD, NOT A MOOD. At 6일 23시간 the session is
// still `active` and nobody is told; at 7일 it is paused and the Director is.
// Notifying early trains people to ignore the alert; notifying late leaves the
// session silently queued forever, which is the failure FR-9.2 closes.
//
// The sweep is periodic, so a second pass over an already-paused session must
// not notify again — an implementation that keys off "offline > grace" alone
// re-notifies every tick and the inbox fills with one repeated item
// [EVAL 제안 행 E14-10].
//
// production caller: runtimes.Service.SweepOffline.
func PlanOffline(c OfflineCase) OfflineOutcome {
	grace := c.Grace
	if grace <= 0 {
		grace = DefaultGrace
	}
	o := OfflineOutcome{SessionState: c.SessionState}
	if !c.OfflineSince.IsZero() {
		o.GraceEndsAt = c.OfflineSince.Add(grace)
	}
	switch {
	case c.OfflineFor <= 0:
		// The machine is here. Queued work proceeds, and nothing happened at
		// all (E14-09).
		if c.SessionState == "active" {
			o.Dispatched = c.QueuedTasks
		}
		return o
	case c.SessionState != "active":
		// Already paused (or finished). Left alone, and above all not
		// re-notified.
		if c.SessionState == "paused" {
			o.PauseReason = PauseReasonOffline
		}
		return o
	case c.OfflineFor < grace:
		// A laptop closed for a weekend is normal; the queued tasks simply
		// wait, and there is no machine to dispatch to (E14-01).
		return o
	}
	o.SessionState, o.PauseReason = "paused", PauseReasonOffline
	o.DirectorNotified = true
	o.Choices = append([]string(nil), RebindChoices...)
	if o.GraceEndsAt.IsZero() {
		// A runtime marked offline without an `offline_since` still owes S11 a
		// date; the sweep's own clock is the honest one.
		o.GraceEndsAt = time.Time{}
	}
	return o
}

// SweepOffline is FR-9.2's periodic pass.
//
// production caller: cmd/server.scheduler (the one-minute purge tick).
func (s *Service) SweepOffline(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `
		SELECT sess.id, sess.workspace_id, sess.director_user_id, sess.status::text,
		       r.id, r.offline_since,
		       COALESCE(ws.runtime_offline_grace, interval '7 days'),
		       (SELECT count(*) FROM task t WHERE t.session_id = sess.id AND t.status IN ('queued', 'deferred'))
		FROM session sess
		JOIN runtime r ON r.id = sess.runtime_id
		LEFT JOIN workspace_settings ws ON ws.workspace_id = sess.workspace_id
		WHERE sess.status = 'active' AND r.status = 'offline' AND r.offline_since IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("runtimes: offline sweep: %w", err)
	}
	type row struct {
		sessionID, wsID, runtimeID uuid.UUID
		director                   uuid.UUID
		c                          OfflineCase
	}
	var candidates []row
	for rows.Next() {
		var rr row
		var offlineSince time.Time
		var grace time.Duration
		if err := rows.Scan(&rr.sessionID, &rr.wsID, &rr.director, &rr.c.SessionState,
			&rr.runtimeID, &offlineSince, &grace, &rr.c.QueuedTasks); err != nil {
			rows.Close()
			return 0, err
		}
		rr.c.RuntimeID = rr.runtimeID
		rr.c.OfflineSince = offlineSince
		rr.c.Grace = grace
		rr.c.OfflineFor = now.Sub(offlineSince)
		candidates = append(candidates, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, rr := range candidates {
		o := PlanOffline(rr.c)
		if o.SessionState != "paused" {
			continue
		}
		if err := s.pauseForOffline(ctx, rr.sessionID, rr.wsID, rr.director, rr.runtimeID, o, now); err != nil {
			s.warn("runtimes: pause for offline runtime", "session", rr.sessionID, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

func (s *Service) pauseForOffline(ctx context.Context, sessionID, wsID, director, runtimeID uuid.UUID, o OfflineOutcome, now time.Time) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	detail := tasks.PausedDetail(PauseReasonOffline, now)
	// The guarded UPDATE is what makes the sweep idempotent under concurrency:
	// only a session still `active` moves, so two overlapping passes cannot
	// both pause it and both notify.
	tag, err := tx.Exec(ctx, `
		UPDATE session SET status = 'paused', paused_reason = 'runtime_offline', paused_detail = $2,
		       updated_at = $3
		WHERE id = $1 AND status = 'active'`, sessionID, detail, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if o.DirectorNotified {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
			SELECT m.id, 'runtime_offline'::inbox_item_type, $1::inbox_severity, $2, $3, $4
			FROM member m WHERE m.workspace_id = $5 AND m.user_id = $6`,
			inbox.Severity(inbox.TypeRuntimeOffline), sessionID, runtimeID, now, wsID, director); err != nil {
			return fmt.Errorf("runtimes: offline inbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, nil, wsID, &sid, "session.updated", map[string]any{
			"session_id":    sessionID,
			"status":        "paused",
			"paused_reason": PauseReasonOffline,
			"choices":       o.Choices,
		})
	}
	return nil
}

// ---------------------------------------------------------------------------
// 2. Candidate selection — E14-03, E14-04, E14-05
// ---------------------------------------------------------------------------

// CandidateCase asks "may this runtime take the session?".
type CandidateCase struct {
	Isolation string // worktree | none | container
	// SessionRemote is the original repository's remote URL.
	SessionRemote string
	Online        bool
	Repos         []contracts.Repo
}

// CandidateVerdict is one row of the picker (openapi RuntimeCandidate).
type CandidateVerdict struct {
	Eligible bool
	// Reason is shown next to a disabled row: a silently missing machine looks
	// like a bug to the person staring at it.
	Reason          string
	MatchedRepoPath string
}

// JudgeCandidate is FR-9.2 F's rule, in one place.
//
// "같은 저장소" IS DECIDED BY REMOTE URL, NEVER BY PATH. `repo_path` equality
// is the tempting comparison and it is wrong in both directions: the same
// repository cloned to a different path on another machine is THE normal case
// (E14-04), and the same path string on another machine is a different
// repository (E14-05) — rebinding there would apply this session's diff
// artifacts to somebody else's code.
//
// production caller: runtimes.Service.Candidates (listRuntimeCandidates) and
// runtimes.Service.Rebind — the rule is enforced at the REBIND too, because a
// direct API call must not bypass E14-05 just because the picker would have
// drawn the row disabled.
func JudgeCandidate(c CandidateCase) CandidateVerdict {
	if !c.Online {
		// Rebinding onto a second dead machine repeats the outage the Director
		// is trying to escape.
		return CandidateVerdict{Reason: "오프라인 — 이 컴퓨터의 데몬이 연결돼 있지 않습니다"}
	}
	if c.Isolation != "worktree" {
		// `none`·`container` have no repository to match, so any online
		// machine can take the session.
		return CandidateVerdict{Eligible: true}
	}
	want := NormalizeRemote(c.SessionRemote)
	if want == "" {
		return CandidateVerdict{Reason: "이 세션의 저장소 remote URL 을 알 수 없어 같은 저장소인지 판정할 수 없습니다"}
	}
	for _, repo := range c.Repos {
		if NormalizeRemote(repo.RemoteURL) == want {
			return CandidateVerdict{Eligible: true, MatchedRepoPath: repo.Path}
		}
	}
	if len(c.Repos) == 0 {
		return CandidateVerdict{Reason: "이 컴퓨터에서 감지된 저장소가 없습니다"}
	}
	return CandidateVerdict{Reason: "같은 remote URL 의 저장소가 없습니다 — " + c.SessionRemote}
}

// ---------------------------------------------------------------------------
// 3. The rebind — E14-03, E14-06
// ---------------------------------------------------------------------------

// RebindArtifact is one submitted artifact, in submission order.
type RebindArtifact struct {
	ID    uuid.UUID
	Order int
	Kind  string
	URL   string
}

// RebindPlan is the decision half of `rebindSession`: everything that can be
// decided before a row is written.
type RebindPlan struct {
	HTTPStatus int
	Problem    *apperr.Problem
	// ColdStart drops the dead machine's `runtime_session_ref`.
	ColdStart bool
	// PromptArtifactOrder is the artifact ids in the order the prompt lists
	// them: submission order.
	PromptArtifactOrder []uuid.UUID
	// PromptSaysApplyArtifacts is E14-06's sentence.
	PromptSaysApplyArtifacts bool
	Prompt                   string
	// PrepareCommandIssued is daemon-protocol §4.3 `rebind_prepare`.
	PrepareCommandIssued bool
	// PrepareCommandApplies is always false — the daemon DOWNLOADS, the PROMPT
	// applies (§4.3).
	PrepareCommandApplies bool
}

// RebindInput is what PlanRebind decides from.
type RebindInput struct {
	Isolation       string
	TargetEligible  bool
	AcknowledgeLoss bool
	SessionState    string
	PauseReason     string
	Artifacts       []RebindArtifact
}

// PlanRebind is FR-9.2's rebinding rule.
//
// REBINDING A `worktree` SESSION IS NOT "CARRY ON". The commits are on the dead
// machine's branch and push is a non-goal (§2.2), so the first prompt after the
// rebind MUST tell the agent to re-apply the session's diff artifacts IN
// SUBMISSION ORDER. Without that line the rebind is "start over" wearing a
// recovery label — and diffs applied out of order conflict.
//
// The prompt is a COLD START. The runtime session lives on the machine that is
// gone; carrying `runtime_session_ref` over would make the new daemon resume a
// session id it never issued, and `session/load` fails.
//
// production caller: runtimes.Service.Rebind (POST /sessions/{id}/rebind).
func PlanRebind(in RebindInput) RebindPlan {
	p := RebindPlan{HTTPStatus: 200, ColdStart: true}
	if in.SessionState != "paused" || in.PauseReason != PauseReasonOffline {
		// Rebinding a live session moves work away from a machine that is
		// still running it.
		p.HTTPStatus = 409
		p.Problem = apperr.Conflict("session_not_paused_offline",
			"재바인딩은 `paused(runtime_offline)` 세션에만 할 수 있습니다")
		return p
	}
	if in.Isolation == "worktree" && !in.AcknowledgeLoss {
		p.HTTPStatus = 422
		p.Problem = apperr.Validation(apperr.Field("acknowledge_loss", "required",
			"worktree 격리에서는 죽은 머신의 커밋이 유실됩니다 — 경고를 확인해 주세요"))
		return p
	}
	if !in.TargetEligible {
		// The candidate rule is enforced HERE, not only in the picker: a direct
		// API call must not bypass E14-05.
		p.HTTPStatus = 422
		p.Problem = apperr.Validation(apperr.Field("runtime_id", "not_a_candidate",
			"이 런타임은 이 세션의 재바인딩 후보가 아닙니다(같은 remote URL 의 저장소가 없거나 오프라인)"))
		return p
	}
	if in.Isolation != "worktree" {
		return p
	}
	diffs := make([]RebindArtifact, 0, len(in.Artifacts))
	for _, a := range in.Artifacts {
		if a.Kind == "diff" {
			diffs = append(diffs, a)
		}
	}
	for _, a := range diffs {
		p.PromptArtifactOrder = append(p.PromptArtifactOrder, a.ID)
	}
	p.PrepareCommandIssued = true
	p.PromptSaysApplyArtifacts = len(diffs) > 0
	p.Prompt = RebindPrompt(diffs)
	return p
}

// RebindPrompt is E14-06's sentence, with the artifact list in submission
// order. The order is part of the instruction, not decoration: diffs applied
// out of order conflict, and the agent has no other way to know which came
// first.
func RebindPrompt(diffs []RebindArtifact) string {
	if len(diffs) == 0 {
		return "이 세션이 실행되던 컴퓨터가 사라져 새 컴퓨터로 옮겼습니다. " +
			"workdir 은 비어 있습니다 — 현재 상태를 먼저 확인한 뒤 이어가라."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "이 세션이 실행되던 컴퓨터가 사라져 새 컴퓨터로 옮겼습니다. "+
		"이전 워크트리의 커밋은 그 머신에만 있으므로 남아 있지 않습니다.\n\n"+
		"이 세션의 diff 아티팩트 %d개를 제출 순서대로 새 workdir 에 적용한 뒤 이어가라. "+
		"순서를 바꾸면 충돌한다.\n\n", len(diffs))
	for _, a := range diffs {
		fmt.Fprintf(&b, "%d. `colab artifact get %s`\n", a.Order, a.ID)
	}
	b.WriteString("\n적용한 뒤 workdir 의 현재 상태를 확인하고, 중단된 지점부터 이어가라.\n")
	return b.String()
}

// Rebind carries out `rebindSession`.
func (s *Service) Rebind(ctx context.Context, wsID, sessionID, targetRuntime uuid.UUID, acknowledgeLoss bool) (RebindPlan, error) {
	now := s.Clock.Now()
	kind, remote, err := s.sessionIsolation(ctx, wsID, sessionID)
	if err != nil {
		return RebindPlan{}, err
	}
	var state string
	var pauseReason *string
	if err := s.DB.QueryRow(ctx, `SELECT status::text, paused_reason::text FROM session WHERE id = $1`, sessionID).
		Scan(&state, &pauseReason); errors.Is(err, pgx.ErrNoRows) {
		return RebindPlan{}, apperr.NotFound("session")
	} else if err != nil {
		return RebindPlan{}, err
	}
	reason := ""
	if pauseReason != nil {
		reason = *pauseReason
	}

	target, err := s.Get(ctx, targetRuntime)
	if err != nil {
		return RebindPlan{}, err
	}
	if target.WorkspaceId != wsID {
		return RebindPlan{}, apperr.NotFound("runtime")
	}
	cc := CandidateCase{Isolation: kind, SessionRemote: remote, Online: target.Status == "online"}
	for _, r := range target.Repos {
		repo := contracts.Repo{Path: r.Path}
		if r.RemoteUrl.IsSpecified() && !r.RemoteUrl.IsNull() {
			repo.RemoteURL = r.RemoteUrl.MustGet()
		}
		cc.Repos = append(cc.Repos, repo)
	}
	verdict := JudgeCandidate(cc)

	artifacts, err := s.sessionArtifacts(ctx, sessionID)
	if err != nil {
		return RebindPlan{}, err
	}
	plan := PlanRebind(RebindInput{
		Isolation: kind, TargetEligible: verdict.Eligible, AcknowledgeLoss: acknowledgeLoss,
		SessionState: state, PauseReason: reason, Artifacts: artifacts,
	})
	if plan.Problem != nil {
		return plan, plan.Problem
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return plan, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx, `
		UPDATE session SET runtime_id = $2, status = 'active', paused_reason = NULL, paused_detail = NULL,
		       updated_at = $3
		WHERE id = $1 AND status = 'paused' AND paused_reason = 'runtime_offline'`,
		sessionID, targetRuntime, now)
	if err != nil {
		return plan, err
	}
	if tag.RowsAffected() == 0 {
		return plan, apperr.Conflict("session_not_paused_offline",
			"재바인딩은 `paused(runtime_offline)` 세션에만 할 수 있습니다")
	}
	// The lane's `runtime_session_ref` points at a session id that lives on the
	// machine that is gone. Clearing it is what makes the next attempt a cold
	// start; leaving it makes the new daemon call `session/load` with an id it
	// never issued (openapi rebindSession).
	if _, err := tx.Exec(ctx, `
		UPDATE lane SET runtime_session_ref = NULL, updated_at = $2 WHERE session_id = $1`,
		sessionID, now); err != nil {
		return plan, fmt.Errorf("runtimes: clear session refs: %w", err)
	}
	// A `worktree` session's workdirs are on the dead machine. Marking them
	// `retained` keeps them out of the new machine's S13 list and out of the GC
	// sweep, which would otherwise keep asking a runtime that will never answer
	// to collect them.
	if _, err := tx.Exec(ctx, `
		UPDATE workdir SET status = 'retained', gc_blocked_reason = 'runtime_gone', updated_at = $2
		WHERE session_id = $1 AND status = 'active'`, sessionID, now); err != nil {
		return plan, fmt.Errorf("runtimes: retain old workdirs: %w", err)
	}
	if plan.PrepareCommandIssued {
		refs := make([]contracts.ArtifactRef, 0, len(artifacts))
		for _, a := range artifacts {
			if a.Kind != "diff" {
				continue
			}
			refs = append(refs, contracts.ArtifactRef{ID: a.ID.String(), Order: a.Order, URL: a.URL})
		}
		// §4.3: the daemon prepares the workdir and DOWNLOADS the artifacts.
		// Applying them is the agent's job under the prompt's instruction — a
		// daemon that patches silently hides conflicts from the agent that has
		// to resolve them.
		if err := tokens.QueueCommand(ctx, tx, targetRuntime, contracts.Command{
			Type: contracts.CmdRebindPrepare, SessionID: sessionID.String(), Artifacts: refs,
		}); err != nil {
			return plan, err
		}
	}
	// Queued work runs on the NEW runtime: the rebind IS the resume (openapi
	// rebindSession "…`active`로 되돌린다").
	if _, err := tx.Exec(ctx, `
		UPDATE task SET runtime_id = NULL, updated_at = $2
		WHERE session_id = $1 AND status IN ('queued', 'deferred')`, sessionID, now); err != nil {
		return plan, err
	}
	if err := tx.Commit(ctx); err != nil {
		return plan, err
	}
	s.publishRuntime(ctx, targetRuntime)
	return plan, nil
}

// sessionArtifacts lists the session's artifacts in SUBMISSION order. The
// order is the whole point of E14-06, so it is `created_at`, never `name` or
// `id`.
func (s *Service) sessionArtifacts(ctx context.Context, sessionID uuid.UUID) ([]RebindArtifact, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, type::text, name, version FROM artifact
		WHERE session_id = $1 ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("runtimes: session artifacts: %w", err)
	}
	defer rows.Close()
	var out []RebindArtifact
	i := 0
	for rows.Next() {
		var a RebindArtifact
		var name string
		var version int
		if err := rows.Scan(&a.ID, &a.Kind, &name, &version); err != nil {
			return nil, err
		}
		i++
		a.Order = i
		a.URL = fmt.Sprintf("/v1/artifacts/%s/content", a.ID)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 4. Ending instead of rebinding — E14-07
// ---------------------------------------------------------------------------

// EndResult is what "종료" did.
type EndResult struct {
	SessionState string
	// ArtifactsRecovered is FR-9.2's "아티팩트만 회수한다".
	ArtifactsRecovered int
	// CompletionConditionsMet is false: giving up satisfies no completion
	// condition (FR-2.2).
	CompletionConditionsMet bool
}

// PlanOfflineEnd is E14-07.
//
// ENDING IS `cancelled`, NOT `completed`. The goal was never met, and filing a
// machine outage in the success column makes every completion metric a lie —
// and it would trigger FR-2.4's summary of a job that was never finished.
//
// production caller: httpapi.Server.CancelSession, for a session paused with
// `runtime_offline`.
func PlanOfflineEnd(artifacts int) EndResult {
	return EndResult{SessionState: "cancelled", ArtifactsRecovered: artifacts}
}

// ---------------------------------------------------------------------------
// 5. Deleting a runtime that still holds work — E14-08
// ---------------------------------------------------------------------------

// DeleteCase is what blocks a runtime deletion.
type DeleteCase struct {
	// ActiveSessions is how many non-terminal sessions are pinned to it.
	ActiveSessions int
	// PausedOfflineSessions is the subset sitting in `paused(runtime_offline)`
	// — exactly the sessions waiting for the Director's choice.
	PausedOfflineSessions int
	CompletedSessions     int
}

// DeleteResult is the answer.
type DeleteResult struct {
	Deleted    bool
	HTTPStatus int
	Code       string
	// BlockingSessions is `Problem.sessions[]` — the list the UI turns into
	// links, because "먼저 재바인딩/종료" is only actionable if the person is
	// told WHICH session.
	BlockingSessions int
	// AsksRebindOrEnd names the two ways out.
	AsksRebindOrEnd bool
	Detail          string
}

// DeleteCode is the contract's Problem.code.
const DeleteCode = "runtime_has_active_sessions"

// PlanRuntimeDelete is E14-08.
//
// `paused(runtime_offline)` COUNTS AS ACTIVE. It is the state a session sits in
// WHILE it waits for the Director's choice; treating it as inactive deletes
// exactly the sessions the grace sweep just parked, and this 409 is the last
// guard before an unrecoverable click.
//
// production caller: httpapi.Server.DeleteRuntime.
func PlanRuntimeDelete(c DeleteCase) DeleteResult {
	blocking := c.ActiveSessions + c.PausedOfflineSessions
	if blocking == 0 {
		// A guard that never lets go makes retiring a laptop impossible.
		return DeleteResult{Deleted: true, HTTPStatus: 204}
	}
	return DeleteResult{
		HTTPStatus: 409, Code: DeleteCode, BlockingSessions: blocking, AsksRebindOrEnd: true,
		Detail: fmt.Sprintf("이 컴퓨터에 걸린 세션 %d개가 아직 살아 있습니다 — 먼저 다른 컴퓨터로 재바인딩하거나 세션을 종료해 주세요(FR-9.2)", blocking),
	}
}

// DeleteRuntime carries out E14-08's guard and the deletion.
func (s *Service) DeleteRuntime(ctx context.Context, wsID, runtimeID uuid.UUID) error {
	var ws uuid.UUID
	if err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM runtime WHERE id = $1`, runtimeID).Scan(&ws); errors.Is(err, pgx.ErrNoRows) || (err == nil && ws != wsID) {
		return apperr.NotFound("runtime")
	} else if err != nil {
		return err
	}
	blocking, err := s.blockingSessions(ctx, runtimeID)
	if err != nil {
		return err
	}
	c := DeleteCase{}
	for _, b := range blocking {
		if b.status == "paused" && b.pauseReason == PauseReasonOffline {
			c.PausedOfflineSessions++
			continue
		}
		c.ActiveSessions++
	}
	res := PlanRuntimeDelete(c)
	if !res.Deleted {
		p := apperr.Conflict(res.Code, res.Detail)
		refs := make([]map[string]any, 0, len(blocking))
		for _, b := range blocking {
			refs = append(refs, map[string]any{"id": b.id, "title": b.title, "status": b.status})
		}
		p.Extra = map[string]any{"sessions": refs}
		return p
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM runtime WHERE id = $1`, runtimeID); err != nil {
		return fmt.Errorf("runtimes: delete: %w", err)
	}
	return nil
}

type blockingSession struct {
	id          uuid.UUID
	title       string
	status      string
	pauseReason string
}

func (s *Service) blockingSessions(ctx context.Context, runtimeID uuid.UUID) ([]blockingSession, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, title, status::text, COALESCE(paused_reason::text, '')
		FROM session WHERE runtime_id = $1 AND status IN ('draft', 'active', 'paused', 'completing')
		ORDER BY created_at`, runtimeID)
	if err != nil {
		return nil, fmt.Errorf("runtimes: blocking sessions: %w", err)
	}
	defer rows.Close()
	var out []blockingSession
	for rows.Next() {
		var b blockingSession
		if err := rows.Scan(&b.id, &b.title, &b.status, &b.pauseReason); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Service) warn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
		return
	}
	slog.Warn(msg, args...)
}

var _ = db.DBTX(nil)
