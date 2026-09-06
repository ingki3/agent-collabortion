package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

const historyLimit = 30

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
		fmt.Fprintf(&hist, "[%s] %s: %s\n", m.CreatedAt.UTC().Format("15:04"), authorLabel(m), m.Content)
	}

	var posted []string
	var postedIDs []uuid.UUID
	prow, err := tx.Query(ctx, `SELECT id FROM message WHERE source_task_id = $1 ORDER BY created_at`, t.ID)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var id uuid.UUID
		if err := prow.Scan(&id); err != nil {
			prow.Close()
			return nil, err
		}
		posted = append(posted, id.String())
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

	// Brief [1]~[8] (PRD §8.4). [3] coordination protocol and [6]/[7] are P2.
	var brief strings.Builder
	fmt.Fprintf(&brief, "[1] Agent Identity\nYou are %s, %s in the Colab workspace. %s\n\nInstructions:\n%s\n\n", agentName, agentRole, roleDesc, instructions)
	brief.WriteString("[2] Workspace rules and colab CLI\n" +
		"- Mention syntax: [@Name](mention://agent/<id>). Only mention session participants listed in [5].\n" +
		"- Post every reply to the session with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool). Text you print to stdout is NOT delivered.\n" +
		"- Read more history with `colab session messages`, session details with `colab session get`.\n" +
		"- Mentioning an agent creates work for it; do not mention agents just to acknowledge.\n" +
		"- Your COLAB_TASK_TOKEN is valid for this attempt only; if a call returns token_revoked, stop immediately.\n\n")
	fmt.Fprintf(&brief, "[4] Session\nTitle: %s\nGoal: %s\n", title, goal)
	if len(criteria) > 0 {
		brief.WriteString("Acceptance criteria:\n")
		for _, c := range criteria {
			fmt.Fprintf(&brief, "- %s\n", c)
		}
	}
	fmt.Fprintf(&brief, "Director: %s\nIsolation: %s\n\n", directorName, isolation.Kind)
	fmt.Fprintf(&brief, "[5] Roster\n%s\n", roster.String())
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
	if plan.HasResumedSection {
		fmt.Fprintf(&prompt, "<resumed attempt=%d>\nYour previous attempt (%d) was interrupted: %s.\n", t.Attempt, t.Attempt-1, prevOutcome)
		if len(posted) > 0 {
			fmt.Fprintf(&prompt, "Messages you already posted: %s. Do not post them again.\n", strings.Join(posted, ", "))
		}
		if plan.ColdStart {
			// FR-5.4 step 2: the runtime no longer holds the session, so this
			// turn rebuilds from the brief, the history and the decision log
			// above — say so, or the agent reads `<resumed>` as "you still
			// remember" and skips the workdir it must inspect.
			prompt.WriteString("The runtime session could not be resumed, so this turn starts cold: everything you know is in this prompt.\n")
		}
		if plan.WorkdirCheckInstruction {
			prompt.WriteString("Before continuing, inspect the current state of the workdir (changed files, git status) and continue from there.\n")
		}
		prompt.WriteString("</resumed>\n\n")
	}
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
	budget := budgetPerTask
	if t.BudgetOverride != nil {
		budget = t.BudgetOverride
	}
	workdirKind := "dir"
	if isolation.Kind == "worktree" {
		workdirKind = "worktree"
	}
	b := &contracts.TaskBundle{
		Task: contracts.BundleTask{
			ID: t.ID.String(), Attempt: t.Attempt, LaneID: t.LaneID.String(), SessionID: t.SessionID.String(),
			AgentID: t.AgentID.String(), AgentName: agentName,
			BudgetUSD: budgetPerTask, BudgetOverrideUSD: t.BudgetOverride,
		},
		TaskToken: token,
		Profile: contracts.BundleProfile{
			RuntimeKind: contracts.RuntimeKind(runtimeKind), Model: model, Options: options, Env: env, Args: args, Tools: tools, AdapterPin: adapterPin,
		},
		Workdir: contracts.BundleWorkdir{Kind: workdirKind, RepoPath: isolation.RepoPath, Reuse: t.Attempt > 1 || reentry > 0},
		Brief:   contracts.BundleBrief{Transport: transport, Text: brief.String()},
		Prompt:  prompt.String(),
		Resume:  resume,
		Limits:  contracts.BundleLimits{BudgetUSD: budget, StallSeconds: int(contracts.StallTimeout.Seconds())},
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
