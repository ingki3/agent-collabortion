package sessions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/llm"
)

// ---------------------------------------------------------------------------
// FR-2.4 — the session summary, now with a model on the path
// ---------------------------------------------------------------------------

// SummaryPlan is what the `completing` step does with the platform LLM's
// answer. It is a value rather than a set of writes so the four things that
// can go wrong — refusal, transport error, truncation, a second pass — are
// decided in one place and can be read at a glance.
type SummaryPlan struct {
	// Post is whether a `session_summary` message is written. It is false for
	// every failure AND for a repeat pass over a session that already has one.
	Post bool
	Body string
	// SessionState is always `completed`. E6-11: the work is finished either
	// way, and holding a session in `completing` because a cosmetic step
	// failed strands it forever.
	SessionState string
	// FeedError / ErrorCategory are the activity-feed entry a failure leaves.
	// The category is `stop_details.category` verbatim — a generic "요약 실패"
	// does not tell an operator whether to change the prompt or the model.
	FeedError     bool
	ErrorCategory string
	// GeneratedBy is who wrote the body: `platform_llm` or `fallback` (the
	// row-composed summary used when no API key is configured). It goes on the
	// feed so a reader does not mistake a mechanical assembly for a written
	// summary (Lead T-S9 ask 3 (i)).
	GeneratedBy string
}

// Who wrote a summary.
const (
	GeneratedByLLM      = "platform_llm"
	GeneratedByFallback = "fallback"
)

// PlanSummary decides the outcome of one summary attempt.
//
// THE ORDERING IS THE POINT. `res.Refused()` reads no content; `res.Text()`
// does and marks the response as read. A refusal still carries a body ("I
// can't help with that."), so an implementation that parses first posts the
// refusal AS the summary — §8.5's 폴백 행 says the check comes first, and
// llm.Response makes the body reachable only through a method so this file is
// the only place the order can be got wrong.
//
// `alreadyPosted` is FR-2.4's "요약 1개". The summariser runs behind the
// completing transition and a retry after a timeout is the normal way it is
// called twice; two summaries in a timeline are indistinguishable and the
// reader cannot tell which is current.
//
// production caller: sessions.Service.summarise, from ApplyCompletionEvent's
// `completed` branch.
func PlanSummary(res *llm.Response, callErr error, alreadyPosted bool) SummaryPlan {
	p := SummaryPlan{SessionState: "completed"}
	switch {
	case callErr != nil:
		// A 5xx or a dropped connection has no stop_reason at all. E6-11's
		// rule is about the OUTCOME, and an implementation that only branches
		// on the literal string "refusal" strands every other failure
		// [EVAL 제안 행 E6-12].
		p.FeedError, p.ErrorCategory = true, "transport_error"
	case res == nil:
		p.FeedError, p.ErrorCategory = true, "no_response"
	case res.Refused():
		p.FeedError, p.ErrorCategory = true, res.Category()
	case !res.Succeeded():
		// max_tokens, or anything else the API grows later.
		p.FeedError, p.ErrorCategory = true, res.Category()
	case alreadyPosted:
		// Nothing failed; there is simply nothing left to write.
	default:
		body := strings.TrimSpace(res.Text())
		if body == "" {
			// An empty body would make the `session_summary` message a blank
			// card, which reads as a platform bug rather than a summary that
			// could not be written.
			p.FeedError, p.ErrorCategory = true, "empty_summary"
			break
		}
		p.Post, p.Body = true, body
	}
	return p
}

// ---------------------------------------------------------------------------
// FR-2.4 — what the summary contains
// ---------------------------------------------------------------------------

// SummarySection names one of FR-2.4's four parts. They are constants because
// the prompt asks the model for exactly these and the fallback composer emits
// exactly these; a section that exists in one and not the other is a summary
// whose shape depends on whether an API key was configured.
const (
	SectionDecisions = "decisions"
	SectionArtifacts = "artifacts"
	SectionCost      = "cost"
	SectionTimeline  = "timeline"
)

// SummaryFacts is the session, as rows. The platform LLM is given these and
// asked to write prose; with no client configured the same facts are rendered
// directly (see BuildSummaryBody), which is why they are gathered before the
// call rather than inside it.
type SummaryFacts struct {
	Title string
	Goal  string
	// Decisions·Artifacts·Timeline are one line each, in the order they
	// happened. An entry with no text still occupies its slot — an artifact
	// submitted without a title is a real row, and dropping it would make the
	// counts in the summary disagree with the session.
	Decisions []string
	Artifacts []string
	Timeline  []string
	Lanes     int
	Tasks     int
	CostUSD   float64
	// Estimated marks a cost rolled up from token estimates rather than
	// runtime-reported usage (FR-7.3, S-48).
	Estimated bool
	StartedAt *time.Time
	EndedAt   *time.Time
}

// SummaryContent is a composed summary and the sections it actually carries.
type SummaryContent struct {
	Body     string
	Sections []string
}

// BuildSummaryBody renders FR-2.4's four sections.
//
// Every section is emitted even when it is empty. A session with no decisions
// still gets a 결정 기록 heading saying so, because a MISSING section and an
// EMPTY one read the same to a person and only one of them is true —
// EVAL_USER's 여정 row promises the reader all four, and a summary that
// silently drops three of them because the session was short is the shape that
// makes people stop reading summaries.
//
// production caller: sessions.Service.summarise — as the prompt's fact block
// for the model, and as the whole body when no platform LLM is configured.
func BuildSummaryBody(f SummaryFacts) SummaryContent {
	var b strings.Builder
	title := f.Title
	if strings.TrimSpace(title) == "" {
		title = "(제목 없음)"
	}
	fmt.Fprintf(&b, "## 세션 요약 — %s\n\n", title)
	if strings.TrimSpace(f.Goal) != "" {
		fmt.Fprintf(&b, "목표: %s\n\n", f.Goal)
	}

	writeList := func(heading string, lines []string, empty string) {
		fmt.Fprintf(&b, "### %s\n", heading)
		if len(lines) == 0 {
			b.WriteString(empty + "\n\n")
			return
		}
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				l = "(제목 없음)"
			}
			fmt.Fprintf(&b, "- %s\n", l)
		}
		b.WriteString("\n")
	}

	writeList("결정 기록", f.Decisions, "기록된 결정이 없습니다.")
	writeList("아티팩트", f.Artifacts, "제출된 아티팩트가 없습니다.")

	b.WriteString("### 비용\n")
	cost := fmt.Sprintf("$%.2f", f.CostUSD)
	if f.Estimated {
		// FR-7.3: an estimate presented as a measurement is a number somebody
		// will put in a budget report.
		cost += " (추정)"
	}
	fmt.Fprintf(&b, "%s · lane %d개 · task %d개\n\n", cost, f.Lanes, f.Tasks)

	b.WriteString("### 타임라인\n")
	if f.StartedAt != nil {
		fmt.Fprintf(&b, "- 시작 %s\n", f.StartedAt.UTC().Format("2006-01-02 15:04"))
	}
	for _, l := range f.Timeline {
		if strings.TrimSpace(l) == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", l)
	}
	if f.EndedAt != nil {
		fmt.Fprintf(&b, "- 종료 %s\n", f.EndedAt.UTC().Format("2006-01-02 15:04"))
	}
	if f.StartedAt == nil && f.EndedAt == nil && len(f.Timeline) == 0 {
		b.WriteString("기록된 사건이 없습니다.\n")
	}

	return SummaryContent{
		Body:     b.String(),
		Sections: []string{SectionDecisions, SectionArtifacts, SectionCost, SectionTimeline},
	}
}

// summaryPrompt is what the platform LLM is asked for. The facts are handed
// over rendered — the model rewrites them into prose, it does not query the
// database — so a refusal or an outage costs the wording and nothing else.
func summaryPrompt(c SummaryContent) string {
	return "다음은 방금 끝난 협업 세션의 사실 목록이다. 이것을 바탕으로 " +
		"결정 기록·아티팩트·비용·타임라인 네 절을 그대로 유지한 한국어 요약을 써라. " +
		"사실에 없는 내용을 추가하지 마라. 마크다운으로, 표제는 그대로 둔다.\n\n" +
		c.Body
}

// summarySystemPrompt is the stable prefix §8.5 asks us to mark cacheable: it
// is identical for every session, so it is exactly the block `cache_control`
// exists for.
const summarySystemPrompt = `당신은 Colab 플랫폼의 세션 요약기다.

규칙:
- 입력은 한 세션의 사실 목록(결정 기록·아티팩트·비용·타임라인)이다.
- 네 절의 표제를 그대로 유지하고, 각 절의 내용을 읽기 좋은 한국어 산문으로 다듬는다.
- 사실에 없는 내용을 만들지 않는다. 수치는 입력의 수치를 그대로 쓴다.
- 세션에 참여하지 않은 사람이 읽고 무슨 일이 있었는지 알 수 있게 쓴다.
- 출력은 마크다운 본문만. 인사말·메타 설명·코드펜스를 넣지 않는다.`

// ---------------------------------------------------------------------------
// FR-4.4 · §8.4 [6] — context reuse
// ---------------------------------------------------------------------------

// ContextReuseInput is a previous session's summary and artifacts being
// carried into a new session's brief.
type ContextReuseInput struct {
	// StoredSummaryTokens is the length of the summary AS STORED.
	StoredSummaryTokens int
	// MaxSummaryTokens is `context_reuse.max_summary_tokens` (default 2000).
	MaxSummaryTokens int
	// IncludeArtifacts is `context_reuse.include_artifacts`: links | none | full.
	IncludeArtifacts string
	ArtifactCount    int
}

// ContextReusePlan is what section [6] gets.
type ContextReusePlan struct {
	InjectedTokens int
	// StoredTokensAfter is the stored summary's length after the plan. It must
	// equal the input: FR-4.4 caps what we SEND.
	StoredTokensAfter int
	// TruncationDisclosed applies §8.4's history rule to [6]: the reader is
	// told it was cut.
	TruncationDisclosed bool
	ArtifactLinks       int
	ArtifactBodies      int
}

// Artifact reuse modes (openapi ContextReusePolicy.include_artifacts).
const (
	ReuseArtifactLinks = "links"
	ReuseArtifactNone  = "none"
	ReuseArtifactFull  = "full"
)

// DefaultMaxSummaryTokens is openapi ContextReusePolicy's default.
const DefaultMaxSummaryTokens = 2000

// PlanContextReuse applies FR-4.4's cap to one previous session.
//
// THE CAP GOVERNS WHAT WE SEND, NOT WHAT WE STORE (§8.4 [6]). Trimming the
// stored summary would destroy the record the session produced — the summary
// is a timeline message a person reads, and the cap exists because the brief
// is re-sent on every turn and has to stay cache-friendly (§8.4 캐시 친화).
//
// production caller: queue.briefContext (§8.4 [6]) — via ReuseSection.
func PlanContextReuse(in ContextReuseInput) ContextReusePlan {
	max := in.MaxSummaryTokens
	if max <= 0 {
		max = DefaultMaxSummaryTokens
	}
	p := ContextReusePlan{
		InjectedTokens:    in.StoredSummaryTokens,
		StoredTokensAfter: in.StoredSummaryTokens,
	}
	if in.StoredSummaryTokens > max {
		p.InjectedTokens = max
		// An agent that does not know it is reading a fragment answers as if
		// it has the whole thing; the history section reports
		// `included`/`total`/`truncated` for the same reason (§8.4).
		p.TruncationDisclosed = true
	}
	switch in.IncludeArtifacts {
	case ReuseArtifactNone:
		// A workspace that turned artifact reuse off expects it off.
	case ReuseArtifactFull:
		p.ArtifactBodies = in.ArtifactCount
	default: // links — openapi ContextReusePolicy's default
		p.ArtifactLinks = in.ArtifactCount
	}
	return p
}

// ReuseSection renders §8.4 [6]'s "이전 세션 요약" for one previous session,
// under the workspace's cap.
func ReuseSection(title, summary string, plan ContextReusePlan) string {
	body, cut := llm.TrimToTokens(summary, plan.InjectedTokens)
	var b strings.Builder
	fmt.Fprintf(&b, "이전 세션 요약 — %s", title)
	if cut || plan.TruncationDisclosed {
		fmt.Fprintf(&b, " (상한 %d 토큰으로 잘림 — 전문은 `colab session messages` 로 읽어라)",
			plan.InjectedTokens)
	}
	b.WriteString("\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The production summary step
// ---------------------------------------------------------------------------

// summarise runs FR-2.4 for a session that has just reached `completing`.
//
// It returns the plan; the caller writes the message inside the same
// transaction that flips the session to `completed`, so "exactly one summary"
// is enforced by the insert's WHERE NOT EXISTS rather than by this function
// having remembered.
func (s *Service) summarise(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, alreadyPosted bool) SummaryPlan {
	// The fact gathering runs in a SAVEPOINT. A failed statement aborts the
	// whole pgx transaction (25P02), and this one is nested inside the
	// completing → completed transition: one bad read in a cosmetic step would
	// otherwise 500 the completion itself and leave the session stuck in
	// `completing` — the exact outcome E6-11 exists to prevent, arriving
	// through the back door.
	facts := SummaryFacts{}
	if sp, err := tx.Begin(ctx); err == nil {
		facts = s.summaryFacts(ctx, sp, sessionID)
		if err := sp.Commit(ctx); err != nil {
			_ = sp.Rollback(ctx)
			s.logWarn("sessions: summary facts rolled back", "session", sessionID, "err", err)
			facts = SummaryFacts{}
		}
	}
	composed := BuildSummaryBody(facts)

	if s.LLM == nil {
		// No platform LLM configured. The session still gets its summary —
		// composed from the same rows P2 used — because FR-2.4's promise to
		// the reader does not depend on whether the operator has an API key,
		// and a workspace without one must still be able to finish a session.
		if alreadyPosted {
			return SummaryPlan{SessionState: "completed", GeneratedBy: GeneratedByFallback}
		}
		return SummaryPlan{Post: true, Body: composed.Body, SessionState: "completed", GeneratedBy: GeneratedByFallback}
	}

	req := llm.BuildRequest(llm.JobSessionSummary)
	req.System = summarySystemPrompt
	req.PrefixTokens = llm.EstimateTokens(summarySystemPrompt)
	req.Prompt = summaryPrompt(composed)

	res, err := s.LLM.Do(ctx, req)
	plan := PlanSummary(res, err, alreadyPosted)
	plan.GeneratedBy = GeneratedByLLM
	if plan.FeedError && s.Log != nil {
		s.Log.Warn("sessions: session summary failed", "session", sessionID,
			"category", plan.ErrorCategory, "err", err)
	}
	return plan
}

// summaryFacts gathers FR-2.4's four sections from rows. A failed read is
// never silently an empty one for the decision log (FR-4.2) — an empty section
// reads as "nothing was decided" — so a query error is logged and the section
// says so.
func (s *Service) summaryFacts(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) SummaryFacts {
	var f SummaryFacts
	var started, ended *time.Time
	var estimated *bool
	if err := tx.QueryRow(ctx, `
		SELECT title, goal, cost_usd, started_at, finished_at,
		       (SELECT bool_or(estimated) FROM task_usage u JOIN task t ON t.id = u.task_id WHERE t.session_id = $1)
		FROM session WHERE id = $1`, sessionID).
		Scan(&f.Title, &f.Goal, &f.CostUSD, &started, &ended, &estimated); err != nil {
		s.logWarn("sessions: summary facts", "session", sessionID, "err", err)
	}
	f.StartedAt, f.EndedAt = started, ended
	f.Estimated = estimated != nil && *estimated

	if rows, err := tx.Query(ctx, `
		SELECT summary, source::text, auto FROM decision WHERE session_id = $1 ORDER BY created_at`, sessionID); err == nil {
		for rows.Next() {
			var summary, source string
			var auto bool
			if rows.Scan(&summary, &source, &auto) == nil {
				line := summary + " (" + source
				if auto {
					line += ", 기한 경과 자동 진행"
				}
				line += ")"
				f.Decisions = append(f.Decisions, line)
			}
		}
		rows.Close()
	} else {
		s.logWarn("sessions: summary decisions", "session", sessionID, "err", err)
		f.Decisions = append(f.Decisions, "(결정 기록을 읽지 못했습니다 — 타임라인을 확인하세요)")
	}

	if rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (name) name, type::text, version FROM artifact
		WHERE session_id = $1 ORDER BY name, version DESC`, sessionID); err == nil {
		for rows.Next() {
			var name, typ string
			var version int
			if rows.Scan(&name, &typ, &version) == nil {
				f.Artifacts = append(f.Artifacts, fmt.Sprintf("%s (%s, v%d)", name, typ, version))
			}
		}
		rows.Close()
	}

	_ = tx.QueryRow(ctx, `SELECT count(*) FROM lane WHERE session_id = $1`, sessionID).Scan(&f.Lanes)
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM task WHERE session_id = $1 AND status = 'completed'`, sessionID).Scan(&f.Tasks)

	// A lane has no title of its own (§7) — it is one agent's line of work, so
	// the agent's name is what a reader recognises in a timeline.
	if rows, err := tx.Query(ctx, `
		SELECT a.name, l.status::text, l.finished_at
		FROM lane l JOIN agent a ON a.id = l.agent_id
		WHERE l.session_id = $1 AND l.finished_at IS NOT NULL ORDER BY l.finished_at`, sessionID); err == nil {
		for rows.Next() {
			var name, status string
			var at *time.Time
			if rows.Scan(&name, &status, &at) == nil && at != nil {
				f.Timeline = append(f.Timeline,
					fmt.Sprintf("%s %s — %s", at.UTC().Format("2006-01-02 15:04"), name, status))
			}
		}
		rows.Close()
	}
	return f
}

func (s *Service) logWarn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
		return
	}
	slog.Warn(msg, args...)
}
