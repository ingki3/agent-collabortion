package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// historyLimit is §8.4's history cap. It is tasks.DefaultHistoryLimit and not
// a second number: the golden table injects its own HistoryLimit, so a
// literal here was never compared with anything (S-38, review §5).
const historyLimit = tasks.DefaultHistoryLimit

// buildBundle assembles the TaskBundle (daemon-protocol §4.1): profile, brief
// [1]~[8] (PRD §8.4), the turn prompt with history/trigger/<resumed>, limits
// and posted_message_ids for attempt ≥ 2 (FR-7.1 M5).
func buildBundle(ctx context.Context, tx pgx.Tx, t *tasks.Row, token string) (*contracts.TaskBundle, error) {
	var (
		agentName, agentRole, roleDesc, instructions   string
		toolsJSON, optionsJSON, envJSON, isolationJSON []byte
		runtimeKind, model                             string
		args                                           []string
		title, goal, directorName                      string
		criteria                                       []string
		limitsJSON                                     []byte
		runtimeSessionRef                              []byte
		reentry                                        int
		budgetPerTask                                  *float64
		prevWorkdir                                    *string
	)
	err := tx.QueryRow(ctx, `
		SELECT a.name, a.role, a.role_description, a.instructions, a.tools, a.budget_per_task,
		       p.runtime_kind, p.model, p.options, p.env, p.args,
		       s.title, s.goal, s.acceptance_criteria, s.isolation, s.limits, u.display_name,
		       l.runtime_session_ref, l.reentry_count, w.path_or_ref
		FROM task t
		JOIN agent a ON a.id = t.agent_id
		JOIN agent_profile p ON p.id = t.profile_id
		JOIN session s ON s.id = t.session_id
		JOIN app_user u ON u.id = s.director_user_id
		JOIN lane l ON l.id = t.lane_id
		LEFT JOIN workdir w ON w.id = l.workdir_id
		WHERE t.id = $1`, t.ID).Scan(
		&agentName, &agentRole, &roleDesc, &instructions, &toolsJSON, &budgetPerTask,
		&runtimeKind, &model, &optionsJSON, &envJSON, &args,
		&title, &goal, &criteria, &isolationJSON, &limitsJSON, &directorName,
		&runtimeSessionRef, &reentry, &prevWorkdir)
	if isNoRows(err) {
		return nil, errNoBundle
	}
	if err != nil {
		return nil, fmt.Errorf("queue: bundle: %w", err)
	}

	var tools []string
	_ = json.Unmarshal(toolsJSON, &tools)
	var options map[string]any
	_ = json.Unmarshal(optionsJSON, &options)
	var env map[string]string
	_ = json.Unmarshal(envJSON, &env)
	var isolation struct {
		Kind     string `json:"kind"`
		RepoPath string `json:"repo_path"`
	}
	_ = json.Unmarshal(isolationJSON, &isolation)
	var limits struct {
		BudgetUSD *float64 `json:"budget_usd"`
	}
	_ = json.Unmarshal(limitsJSON, &limits)

	// Roster (brief [5])
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.name, a.role, a.role_description,
		       EXISTS (SELECT 1 FROM task x WHERE x.agent_id = a.id AND x.session_id = sp.session_id AND x.status IN ('dispatched','preparing','running'))
		FROM session_participant sp JOIN agent a ON a.id = sp.agent_id WHERE sp.session_id = $1 ORDER BY sp.joined_at`, t.SessionID)
	if err != nil {
		return nil, err
	}
	var roster strings.Builder
	for rows.Next() {
		var id uuid.UUID
		var name, role, desc string
		var working bool
		if err := rows.Scan(&id, &name, &role, &desc, &working); err != nil {
			rows.Close()
			return nil, err
		}
		status := "idle"
		if working {
			status = "working"
		}
		self := ""
		if id == t.AgentID {
			self = " (you)"
		}
		fmt.Fprintf(&roster, "- %s%s — %s: %s — mention: %s — status: %s\n", name, self, role, desc, router.MentionLink(name, id), status)
	}
	rows.Close()

	// Brief [6] Context and [7] Decision Log (§8.4, S-37). The decision table
	// and the artifact table were both written from P2 on and nothing read
	// them back into a prompt, so every turn re-derived what had already been
	// decided from the raw history.
	sessionContext, err := briefContext(ctx, tx, t.SessionID)
	if err != nil {
		return nil, err
	}
	decisionLog, err := briefDecisionLog(ctx, tx, t.SessionID)
	if err != nil {
		return nil, err
	}

	// Trigger messages, history, posted ids.
	//
	// coalesced_message_ids is the arrival-ordered list of everything this task
	// answers, the first message included (FR-3.4 "도착 순서대로 인용", E2-10) —
	// so it is the list, not an addition to trigger_message_id. Concatenating
	// the two quoted the first message twice. trigger_message_id remains the
	// fallback for rows written before the list carried it.
	triggerIDs := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	if t.TriggerMessageID != nil {
		triggerIDs, seen[*t.TriggerMessageID] = append(triggerIDs, *t.TriggerMessageID), true
	}
	for _, id := range t.CoalescedMessageIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		triggerIDs = append(triggerIDs, id)
	}
	var trigger strings.Builder
	for _, id := range triggerIDs {
		m, err := messages.Get(ctx, tx, id)
		if err != nil {
			continue
		}
		fmt.Fprintf(&trigger, "<message id=%q author=%q at=%q>\n%s\n</message>\n", m.ID, authorLabel(m), m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), m.Content)
	}
	history, _, _, _, err := messages.List(ctx, tx, t.SessionID, messages.ListOptions{IncludeReplies: true, Limit: historyLimit})
	if err != nil {
		return nil, err
	}
	var total int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1`, t.SessionID).Scan(&total)
	var hist strings.Builder
	for _, m := range history {
		// The id is here so `Messages you already posted` above can be matched
		// line by line against what the session actually holds (S-36).
		fmt.Fprintf(&hist, "[%s] %s %s: %s\n", m.CreatedAt.UTC().Format("15:04"), m.ID, authorLabel(m), m.Content)
	}

	// posted is the bundle's `posted_message_ids` (bare ids, §4.1); postedLines
	// is what the PROMPT shows. S-36: a list of uuids is nothing an agent can
	// compare its draft against, so each line is `id — first 80 chars` and the
	// history lines below carry the same ids.
	var posted, postedLines []string
	var postedIDs []uuid.UUID
	prow, err := tx.Query(ctx, `SELECT id, content FROM message WHERE source_task_id = $1 ORDER BY created_at`, t.ID)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var id uuid.UUID
		var content string
		if err := prow.Scan(&id, &content); err != nil {
			prow.Close()
			return nil, err
		}
		posted = append(posted, id.String())
		postedLines = append(postedLines, fmt.Sprintf("%s — %s", id, preview(content, 80)))
		postedIDs = append(postedIDs, id)
	}
	prow.Close()

	// Why this attempt exists, read from the previous one. The cause decides
	// whether the prompt continues interrupted work or replaces it, and whether
	// the ref that was in the lane is still worth trying (harness §6).
	prevOutcome, prevFailure, prevResumed := "unknown", "", (*bool)(nil)
	if t.Attempt >= 2 {
		var o, fk *string
		_ = tx.QueryRow(ctx, `SELECT outcome, failure_kind::text, resumed FROM task_attempt WHERE task_id = $1 AND attempt = $2`,
			t.ID, t.Attempt-1).Scan(&o, &fk, &prevResumed)
		if o != nil {
			prevOutcome = *o
		}
		if fk != nil {
			prevFailure = *fk
		}
	}
	// The previous attempt held a ref and reported a cold start: the runtime
	// no longer has that session (`resume_rejected`, harness §6). Handing the
	// same id to the next attempt costs a round trip to learn it again.
	prevColdStarted := len(runtimeSessionRef) > 0 && prevResumed != nil && !*prevResumed
	cause := causeOf(t, prevFailure)
	newInstruction := ""
	if cause == tasks.CauseRestart {
		newInstruction = trigger.String()
	}

	// The human's answer that put this task back in the queue. Only these two
	// causes have one: a retry or a heartbeat re-queue answers no question.
	var answered *hitlAnswer
	if cause == tasks.CauseHitlAnswer || cause == tasks.CauseBudgetApproved {
		answered, err = lastAnsweredHitl(ctx, tx, t.ID)
		if err != nil {
			return nil, err
		}
	}

	// Brief [1]~[8] (PRD §8.4).
	var brief strings.Builder
	fmt.Fprintf(&brief, "[1] Agent Identity\nYou are %s, %s in the Colab workspace. %s\n\nInstructions:\n%s\n\n", agentName, agentRole, roleDesc, instructions)
	brief.WriteString("[2] Workspace rules and colab CLI\n" +
		"- Mention syntax: [@Name](mention://agent/<id>). Only mention session participants listed in [5].\n" +
		"- Post every reply to the session with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool). Text you print to stdout is NOT delivered.\n" +
		"- Read more history with `colab session messages`, session details with `colab session get`.\n" +
		"- Mentioning an agent creates work for it; do not mention agents just to acknowledge.\n" +
		"- Your COLAB_TASK_TOKEN is valid for this attempt only; if a call returns token_revoked, stop immediately.\n\n")
	if agentRole == "lead" {
		// §8.4 marks [3] "(lead만)". A researcher handed the coordination
		// protocol starts handing out work to the roster it can see, which is
		// how a session grows two coordinators.
		brief.WriteString("[3] Coordination Protocol\n" +
			"- You are the lead. Split the goal into pieces and hand each one to the agent in [5] whose role fits it, by mentioning that agent.\n" +
			"- One mention is one unit of work: do not mention an agent to acknowledge, and do not mention two agents for the same piece.\n" +
			"- Wait for a reply before handing out work that depends on it; independent pieces go out together.\n" +
			"- When a decision needs a person, ask with `colab hitl ask` rather than guessing; the answer comes back in the next turn's `<resumed>`.\n" +
			"- Report to the Director yourself; the other agents report to you.\n\n")
	}
	fmt.Fprintf(&brief, "[4] Session\nTitle: %s\nGoal: %s\n", title, goal)
	if len(criteria) > 0 {
		brief.WriteString("Acceptance criteria:\n")
		for _, c := range criteria {
			fmt.Fprintf(&brief, "- %s\n", c)
		}
	}
	fmt.Fprintf(&brief, "Director: %s\nIsolation: %s\n\n", directorName, isolation.Kind)
	fmt.Fprintf(&brief, "[5] Roster\n%s\n", roster.String())
	if sessionContext != "" {
		fmt.Fprintf(&brief, "[6] Context\n%s\n", sessionContext)
	}
	if decisionLog != "" {
		fmt.Fprintf(&brief, "[7] Decision Log\n%s\n", decisionLog)
	}
	brief.WriteString("[8] Instruction precedence: user instruction > session goal > agent instructions > runtime defaults.\n")

	// The next attempt's shape — resume vs cold start, `<resumed>`, the history
	// header, the workdir-check line — is PlanAttempt's decision (FR-5.4,
	// §8.4). buildBundle only renders it.
	plan := tasks.PlanAttempt(tasks.AttemptInput{
		TaskID: t.ID, Attempt: t.Attempt - 1, MaxAttempts: t.MaxAttempts,
		TriggerMessageID:   uuidOrNil(t.TriggerMessageID),
		SessionRef:         storedRefSessionID(runtimeSessionRef),
		RefRuntimeKind:     storedRefKind(runtimeSessionRef),
		ProfileRuntimeKind: runtimeKind,
		ResumeRejected:     prevColdStarted,
		Cause:              cause,
		PostedMessageIDs:   postedIDs,
		PrevWorkdir:        deref(prevWorkdir),
		HistoryTotal:       total, HistoryLimit: historyLimit,
		NewInstruction: newInstruction,
	})

	// Turn prompt
	var prompt strings.Builder
	renderResumedSection(&prompt, plan, t.Attempt, prevOutcome, postedLines, answered)
	fmt.Fprintf(&prompt, "<history included=%d total=%d truncated=%t>\n%s</history>\n\n",
		plan.HistoryIncluded, plan.HistoryTotal, plan.HistoryTruncated, hist.String())
	// A re-instruction's trigger IS the new instruction, and `<resumed>` is
	// absent above — so the same rendering serves both (§8.4, E8-06).
	fmt.Fprintf(&prompt, "<trigger>\n%s</trigger>\n\n", trigger.String())
	prompt.WriteString("Respond to the trigger. Post your reply with `colab message post`; mention the person or agent you are answering when a reply is expected.\n")

	transport := contracts.BriefACPMetaSystemPrompt
	adapterPin := contracts.ClaudeAgentACPPin
	if contracts.RuntimeKind(runtimeKind) == contracts.RuntimeHermes {
		transport = contracts.BriefInstructionFile
		adapterPin = ""
	}
	// daemon-protocol §4.1: `resume` is the lane's stored ref, and only when
	// the next runtime can load it (E8-08, E8-13).
	var resume *contracts.RuntimeSessionRef
	if plan.ResumeRef != "" {
		resume = tasks.PlanBundleResume(runtimeSessionRef, runtimeKind)
	}
	// daemon-protocol §4.1·§4.4 v0.7.1: `limits.budget_usd` is the SESSION's
	// remaining budget, and the task ceiling travels in `task.budget_usd` /
	// `task.budget_override_usd`. The daemon then enforces D-16's
	// min(task 상한, 세션 잔여) — before v0.7.1 this field carried the task
	// ceiling again, so the session remainder was a number the daemon could
	// not see and the last lane of a session could spend past it.
	//
	// The daemon's own half of D-16 is backlog D-16; the server's in-turn
	// enforcement (httpapi.enforceBudgetFor, §4.2 usage) applies the same min
	// on every heartbeat regardless.
	budget := sessionRemainingBudget(ctx, tx, t.SessionID, limits.BudgetUSD)
	// S-44: an approved raise carries along the lane it was granted on, so the
	// daemon enforces the same ceiling the server does. Without this the
	// server would allow $3 (httpapi.loadBudgetState reads the same fallback)
	// while `task.budget_override_usd` told the daemon there was no raise at
	// all, and the two halves of the double enforcement would disagree.
	override := t.BudgetOverride
	if override == nil {
		if v, err := tasks.LaneBudgetOverride(ctx, tx, t.LaneID); err == nil {
			override = v
		}
	}
	workdirKind := "dir"
	wdPlan := workdirs.WorktreePlan{}
	if isolation.Kind == "worktree" {
		workdirKind = "worktree"
		// FR-6.4/C3: ONE worktree per agent, reused across that agent's lanes.
		// The existing path is looked up by agent, not by lane, so a second
		// lane of the same agent gets the same checkout back rather than a
		// second worktree of the same branch (E13-02, E16-B's "워크트리 2개").
		//
		// E13-08 is the same query read the other way: the bundle names only
		// what THIS agent owns. A reviewer handed the Frontend checkout can
		// edit the code it is reviewing, and under `worktree` two agents in one
		// tree is repository corruption, not a stale read.
		existing := ""
		if paths, err := workdirs.BundleWorkdirPaths(ctx, tx, t.SessionID, t.AgentID); err == nil && len(paths) > 0 {
			existing = paths[0]
		}
		wdPlan = workdirs.PlanWorktree(workdirs.WorktreeRequest{
			SessionSlug:      workdirs.Slug(title),
			AgentSlug:        workdirs.Slug(agentName),
			AgentID:          t.AgentID,
			ExistingForAgent: existing,
		})
	}
	b := &contracts.TaskBundle{
		Task: contracts.BundleTask{
			ID: t.ID.String(), Attempt: t.Attempt, LaneID: t.LaneID.String(), SessionID: t.SessionID.String(),
			AgentID: t.AgentID.String(), AgentName: agentName,
			BudgetUSD: budgetPerTask, BudgetOverrideUSD: override,
		},
		TaskToken: token,
		Profile: contracts.BundleProfile{
			RuntimeKind: contracts.RuntimeKind(runtimeKind), Model: model, Options: options, Env: env, Args: args, Tools: tools, AdapterPin: adapterPin,
		},
		Workdir: contracts.BundleWorkdir{
			Kind: workdirKind, RepoPath: isolation.RepoPath,
			Path: wdPlan.Path, Branch: wdPlan.Branch,
			// `reuse` is true for a retry, a lane re-entry, AND — under
			// `worktree` — whenever this agent already has a checkout in this
			// session, which is every lane after its first (C3).
			Reuse: t.Attempt > 1 || reentry > 0 || (workdirKind == "worktree" && !wdPlan.Created),
		},
		Brief:  contracts.BundleBrief{Transport: transport, Text: brief.String()},
		Prompt: prompt.String(),
		Resume: resume,
		Limits: contracts.BundleLimits{BudgetUSD: budget, StallSeconds: int(contracts.StallTimeout.Seconds())},
	}
	if t.TriggerMessageID != nil {
		b.Task.TriggerMessageID = t.TriggerMessageID.String()
	}
	if t.DelegatedFromTaskID != nil {
		b.Task.DelegatedFromTaskID = t.DelegatedFromTaskID.String()
	}
	if t.RestartedFromTaskID != nil {
		b.Task.RestartedFromTaskID = t.RestartedFromTaskID.String()
	}
	if t.Attempt >= 2 {
		b.PostedMessageIDs = posted
	}
	return b, nil
}

func authorLabel(m *messages.Row) string {
	switch m.AuthorType {
	case "system":
		return "system"
	default:
		if m.AuthorName != nil {
			return *m.AuthorName
		}
		return m.AuthorType
	}
}

// causeOf names why the next attempt exists (tasks.Cause*). A re-instruction is
// a NEW task with attempt 1 and restarted_from_task_id (FR-3.4 B); everything
// else is a continuation classified by what ended the previous attempt.
func causeOf(t *tasks.Row, prevFailure string) string {
	if t.RestartedFromTaskID != nil && t.Attempt == 1 {
		return tasks.CauseRestart
	}
	switch prevFailure {
	case "auth", "quota", "config":
		return tasks.CauseRetryAuth
	case "runtime_offline", "timeout", "stall":
		return tasks.CauseRequeueSweep
	case "":
		// No failure: the previous attempt ended cleanly and something else —
		// a HITL answer, a budget approval — put the task back in the queue.
		return tasks.CauseHitlAnswer
	default:
		return tasks.CauseRetryNetwork
	}
}

// storedRefSessionID / storedRefKind read the lane's jsonb without deciding
// anything; PlanBundleResume is what judges whether the ref is usable.
func storedRefSessionID(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var ref contracts.RuntimeSessionRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return ""
	}
	return ref.SessionID
}

func storedRefKind(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var ref contracts.RuntimeSessionRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return ""
	}
	return string(ref.RuntimeKind)
}

func uuidOrNil(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// preview is the first n characters of one line of text — enough for a person
// or an agent to recognise a message without carrying its whole body (S-36).
func preview(content string, n int) string {
	line := strings.TrimSpace(content)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	r := []rune(line)
	if len(r) <= n {
		return line
	}
	return string(r[:n]) + "…"
}

// briefContext is §8.4 [6]: what this session already has attached. The
// artifacts are named, not inlined — the agent fetches the one it needs with
// `colab artifact get`, and a brief that carries file bodies stops being
// cacheable.
func briefContext(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (name) name, type, version, COALESCE(description, '')
		FROM artifact WHERE session_id = $1
		ORDER BY name, version DESC`, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var name, typ, desc string
		var version int
		if err := rows.Scan(&name, &typ, &version, &desc); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "- %s (%s, v%d)", name, typ, version)
		if desc != "" {
			fmt.Fprintf(&b, " — %s", preview(desc, 120))
		}
		b.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	// FR-4.4 · §8.4 [6]: what earlier sessions produced, under the workspace's
	// cap. This is what "이전 세션 요약 (설정 상한 내)" means, and it is why the
	// wizard offers `type: session` context at all — without it, attaching a
	// previous session did nothing to the brief.
	reuse, err := reusedSessionSummaries(ctx, tx, sessionID)
	if err != nil {
		return "", err
	}
	if b.Len() == 0 && reuse == "" {
		return "", nil
	}
	var out strings.Builder
	if b.Len() > 0 {
		out.WriteString("Artifacts submitted in this session (read one with `colab artifact get <name>`):\n")
		out.WriteString(b.String())
	}
	if reuse != "" {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(reuse)
	}
	return out.String(), nil
}

// reusedSessionSummaries is FR-4.4's context reuse.
//
// THE CAP GOVERNS WHAT WE SEND, NOT WHAT WE STORE. The previous session's
// summary stays whole in its own timeline; only the copy injected here is
// trimmed, and the trim is DISCLOSED — an agent that does not know it is
// reading a fragment answers as if it has the whole thing (§8.4's history rule,
// applied to [6]).
//
// The policy is the session's own override when it has one, else the
// workspace's (openapi Session.context_reuse_override · WorkspaceSettings).
func reusedSessionSummaries(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(s.context_reuse_override, ws.context_reuse, '{}'::jsonb)
		FROM session s
		LEFT JOIN workspace_settings ws ON ws.workspace_id = s.workspace_id
		WHERE s.id = $1`, sessionID).Scan(&raw); err != nil {
		return "", fmt.Errorf("queue: context reuse policy: %w", err)
	}
	var policy struct {
		MaxSummaryTokens *int    `json:"max_summary_tokens"`
		IncludeArtifacts *string `json:"include_artifacts"`
	}
	_ = json.Unmarshal(raw, &policy)
	maxTokens := sessions.DefaultMaxSummaryTokens
	if policy.MaxSummaryTokens != nil {
		maxTokens = *policy.MaxSummaryTokens
	}
	include := sessions.ReuseArtifactLinks
	if policy.IncludeArtifacts != nil && *policy.IncludeArtifacts != "" {
		include = *policy.IncludeArtifacts
	}

	// `session_context` rows of type `session` are the previous sessions the
	// wizard attached (§7 session_context.type).
	rows, err := tx.Query(ctx, `
		SELECT prev.title,
		       COALESCE((SELECT m.content FROM message m
		                  WHERE m.session_id = prev.id AND m.kind = 'summary'
		                  ORDER BY m.created_at DESC LIMIT 1), ''),
		       (SELECT count(*) FROM artifact a WHERE a.session_id = prev.id)
		FROM session_context sc
		JOIN session prev ON prev.id::text = sc.ref
		WHERE sc.session_id = $1 AND sc.type = 'session'
		ORDER BY sc.created_at`, sessionID)
	if err != nil {
		return "", fmt.Errorf("queue: reused sessions: %w", err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var title, summary string
		var artifacts int
		if err := rows.Scan(&title, &summary, &artifacts); err != nil {
			return "", err
		}
		if strings.TrimSpace(summary) == "" {
			// A session that never produced a summary has nothing to reuse.
			// Saying "이전 세션: (없음)" would spend brief budget on a fact the
			// agent cannot act on.
			continue
		}
		plan := sessions.PlanContextReuse(sessions.ContextReuseInput{
			StoredSummaryTokens: llm.EstimateTokens(summary),
			MaxSummaryTokens:    maxTokens,
			IncludeArtifacts:    include,
			ArtifactCount:       artifacts,
		})
		out.WriteString(sessions.ReuseSection(title, summary, plan))
		if plan.ArtifactLinks > 0 {
			fmt.Fprintf(&out, "이전 세션의 아티팩트 %d개 — `colab artifact get <name>` 로 읽어라.\n", plan.ArtifactLinks)
		}
		out.WriteString("\n")
	}
	return out.String(), rows.Err()
}

// briefDecisionLog is §8.4 [7]: FR-4.2's log, in the order it was written.
// `auto` is kept visible — "nobody answered and we used the proposal" is not
// the same instruction as "the Director said this" (E7-12).
func briefDecisionLog(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT summary, COALESCE(rationale, ''), source::text, auto, created_at
		FROM decision WHERE session_id = $1 ORDER BY created_at DESC LIMIT $2`, sessionID, decisionLogLimit)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var summary, rationale, source string
		var auto bool
		var at time.Time
		if err := rows.Scan(&summary, &rationale, &source, &auto, &at); err != nil {
			return "", err
		}
		line := fmt.Sprintf("- [%s] %s (%s", at.UTC().Format("2006-01-02 15:04"), summary, source)
		if auto {
			line += ", automatic: nobody answered in time"
		}
		line += ")"
		if rationale != "" {
			line += " — " + preview(rationale, 160)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	// Oldest first: the log reads as a sequence, and the newest N are the ones
	// that survived the LIMIT.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// decisionLogLimit caps §8.4 [7]. The brief is the cacheable prefix (§8.4
// "캐시 친화적"), so it may not grow without bound.
const decisionLogLimit = 20

// hitlAnswer is the answered request the next attempt is resuming from.
type hitlAnswer struct {
	Sections []string
	Kind     string
	Question string
	Context  string
	Answer   string
	Approved *bool
	Reason   string
	Auto     bool
}

// lastAnsweredHitl reads the most recently answered request of this task.
// `<resumed>` needs the ANSWER, so an open request (there can be at most one)
// is not a candidate and neither is a cancelled one (K-4).
//
// The reason comes from the decision row rather than hitl_request: a rejection
// with no answer text stores the reason in `answer` (handlers_hitl.go), and
// printing that as "answer: 근거가 부족합니다" would read as approval.
func lastAnsweredHitl(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) (*hitlAnswer, error) {
	var a hitlAnswer
	var answer, hctx, rationale *string
	err := tx.QueryRow(ctx, `
		SELECT h.type::text, h.question, h.context, h.answer, h.approved,
		       (SELECT d.rationale FROM decision d WHERE d.ref_id = h.id ORDER BY d.created_at DESC LIMIT 1),
		       COALESCE((SELECT d.auto FROM decision d WHERE d.ref_id = h.id ORDER BY d.created_at DESC LIMIT 1), false)
		FROM hitl_request h
		WHERE h.task_id = $1 AND h.status IN ('answered', 'auto_answered')
		ORDER BY h.answered_at DESC NULLS LAST, h.created_at DESC
		LIMIT 1`, taskID).Scan(&a.Kind, &a.Question, &hctx, &answer, &a.Approved, &rationale, &a.Auto)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: bundle: hitl answer: %w", err)
	}
	a.Sections = hitl.PromptSections(a.Kind)
	a.Answer, a.Context, a.Reason = deref(answer), deref(hctx), deref(rationale)
	if a.Kind == hitl.KindApproval {
		// The rejection reason is stored in `answer` when the responder gave
		// no separate answer text; it is a reason either way, never an answer.
		if a.Reason == "" {
			a.Reason = a.Answer
		}
		a.Answer = ""
	}
	return &a, nil
}

// renderHitlAnswer writes §8.4's HITL section of `<resumed>`. The section name
// is hitl.PromptSections' — the same value RespondPlan carries into the golden
// table (E7-07 question/answer, E7-17 approved:false + reason).
// renderResumedSection writes PRD §8.4's `<resumed>` block: why the previous
// attempt stopped, what it already posted, the HITL answer it was waiting for,
// whether this turn is cold, and the workdir check.
//
// It is a separate function because the two instructions inside it are E8-04's
// ONLY defence and nothing else pins them. E8-04 (4) asks for "파일 편집 중복
// 적용 0" on a re-queued attempt, and the sim models an agent that does not
// look before it writes — so the agent's only way to know an edit is already
// applied is to be told, in this block, to look. `WorkdirCheckInstruction` is
// pinned by the golden table as a boolean; the SENTENCE it turns into is
// pinned by resumed_prompt_test.go.
func renderResumedSection(prompt *strings.Builder, plan tasks.AttemptPlan, attempt int,
	prevOutcome string, postedLines []string, answered *hitlAnswer) {
	if !plan.HasResumedSection {
		return
	}
	fmt.Fprintf(prompt, "<resumed attempt=%d>\nYour previous attempt (%d) was interrupted: %s.\n", attempt, attempt-1, prevOutcome)
	if len(postedLines) > 0 {
		fmt.Fprintf(prompt, "Messages you already posted (do not post them again):\n%s\n",
			"- "+strings.Join(postedLines, "\n- "))
	}
	// PRD §8.4: `<resumed>` carries the HITL answer and the approval
	// verdict. hitl.PromptSections is the same function RespondPlan fills
	// its table value from, so the row the golden pins is the text handed
	// to the agent (review R1, E7-07·E7-17).
	if answered != nil {
		renderHitlAnswer(prompt, answered)
	}
	if plan.ColdStart {
		// FR-5.4 step 2: the runtime no longer holds the session, so this
		// turn rebuilds from the brief, the history and the decision log
		// above — say so, or the agent reads `<resumed>` as "you still
		// remember" and skips the workdir it must inspect.
		prompt.WriteString("The runtime session could not be resumed, so this turn starts cold: everything you know is in this prompt.\n")
	}
	if plan.WorkdirCheckInstruction {
		// E8-04 (4). The second sentence is the one that does the work: the
		// previous attempt died mid-flight and its partial edits are still on
		// disk, so "inspect the workdir" without "do not redo what is there"
		// leaves the agent free to append the same edit twice.
		prompt.WriteString("Before continuing, inspect the current state of the workdir (changed files, `git status`) and continue from there.\n")
		prompt.WriteString("Some of your previous attempt's edits are already applied: do NOT make an edit again if it is already in the file.\n")
	}
	prompt.WriteString("</resumed>\n\n")
}

func renderHitlAnswer(prompt *strings.Builder, a *hitlAnswer) {
	fmt.Fprintf(prompt, "<hitl_answer sections=%q>\n", strings.Join(a.Sections, ","))
	fmt.Fprintf(prompt, "question: %s\n", a.Question)
	if a.Context != "" {
		fmt.Fprintf(prompt, "context: %s\n", a.Context)
	}
	if a.Approved != nil {
		fmt.Fprintf(prompt, "approved: %t\n", *a.Approved)
	}
	if a.Answer != "" {
		fmt.Fprintf(prompt, "answer: %s\n", a.Answer)
	}
	if a.Reason != "" {
		fmt.Fprintf(prompt, "reason: %s\n", a.Reason)
	}
	if a.Auto {
		// E7-12: the deadline passed and the agent's own proposal was used.
		// Reading that as a human decision is how a guess becomes a fact.
		prompt.WriteString("decided_by: nobody answered before the deadline; your proposed default was used\n")
	}
	prompt.WriteString("</hitl_answer>\n")
}

// sessionRemainingBudget is §4.4's "세션 잔여": the session's limit less what
// its tasks have already spent, floored at zero. nil when the session carries
// no budget — a session without a limit must not hand every task a limit of
// zero.
// sessionRemainingBudget returns nil — the field is OMITTED, not zeroed — when
// the session has no budget.
//
// D-18 (server half): the daemon's mid-turn usage stream costs 4× the messages
// and 2× the bytes, and it exists only so the daemon can enforce a ceiling. A
// bundle whose `task.budget_usd`, `task.budget_override_usd` AND
// `limits.budget_usd` are all absent is an attempt with NOTHING to enforce, and
// that absence is the signal the daemon turns `usage_midturn` off on. Sending a
// 0 here instead of nil would read as "a budget of zero" — every turn instantly
// over its limit — so the nil is load-bearing, not a shortcut.
// bundle_budget_test.go pins it.
func sessionRemainingBudget(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, limit *float64) *float64 {
	if limit == nil || *limit <= 0 {
		return nil
	}
	var spent float64
	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(sum(u.cost_usd), 0) FROM task_usage u
		JOIN task t ON t.id = u.task_id WHERE t.session_id = $1`, sessionID).Scan(&spent)
	rem := *limit - spent
	if rem < 0 {
		rem = 0
	}
	return &rem
}
