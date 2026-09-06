// Golden table for the session summary and the platform LLM client (EVAL
// E6-11 · E5-08 · E6-03 as they are RE-MET in P4, plus the §8.5 client rules) —
// PRD FR-2.4 (세션 요약), FR-4.4 (컨텍스트 재사용 상한), PRD §8.5 (플랫폼 내부
// Claude API 사용 원칙: 모델·캐싱·`stop_reason == "refusal"`·`stop_details.category`),
// contracts/openapi.yaml `WorkspaceSettings.context_reuse` · `MessageKind`
// (`summary`).
//
// WHY THESE ROWS EXIST WHEN E6 IS ALREADY GREEN. P2 assembled the summary from
// database rows, so `summaryStopReason()` is a function that returns
// "end_turn" (complete.go:359 says so out loud) and E6-11's refusal branch has
// never been reached by production code. P4 puts a real model on that path.
// Everything that can go wrong — a refusal, a truncated answer, a transport
// error, two summaries — arrives with it, and the existing E6 rows do not
// see any of it.
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - EXACTLY ONE `session_summary`. Not "at least one". The retry that a
//     flaky model invites is the obvious way to end up with two, and the
//     second one is indistinguishable from the first in the timeline.
//   - `stop_reason` is checked BEFORE the body is read (§8.5 폴백 행:
//     "응답 본문을 읽기 전에 검사"). A refusal still carries content, and code
//     that parses first will happily post the refusal text as the summary.
//   - A refusal COMPLETES the session (E6-11). Staying in `completing` because
//     the summary failed holds a finished session hostage to a cosmetic step.
//   - The failure is recorded with `stop_details.category`, not a generic
//     "요약 실패" — the category is the only thing that tells an operator
//     whether to change the prompt or the model.
//   - Cache breakage is silent by design (§8.5 캐싱 행). Below the model's
//     minimum cache length `cache_control` does nothing and no error is
//     raised, so the only proof is `usage.cache_read_input_tokens`.
//   - The context-reuse cap is a cap on what we SEND, not on what we store
//     (FR-4.4, §8.4 [6]). Truncating the stored summary loses the record.
//
// HOW THIS FILE FAILS TODAY. `sessions.RunSummary` covers the shape of the
// verdict (completed/1 msg vs completed/feed error) and may pass through the
// adapter as a regression guard. There is no LLM client, no request builder,
// no cache verification and no context-reuse trimming, so those hooks are nil
// until T-S9 wires them.
//
// VOCABULARY. `summaryRequest`/`summaryResponse` are this table's words for
// the §8.5 client's inputs and outputs. §8.5 is prose about an external API,
// not a wire contract of ours; the adapter maps it (P2_TASKS §0-8).
package sessions

import (
	"strings"
	"testing"
)

// caseNameP4 keeps the source id in the test name. It takes PRD/§ references
// as well as EVAL ids, because half of these rows are pinned by §8.5 prose
// that EVAL has no row for; the characters that a shell would fight over are
// folded to `_`.
func caseNameP4(ref, name string) string {
	out := make([]byte, 0, len(ref))
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return strings.Trim(string(out), "_") + "_" + name
}

// ---------------------------------------------------------------------------
// 1. The summary message — FR-2.4, E5-08, E6-03
// ---------------------------------------------------------------------------

// summaryRun is one pass of the summary step of `completing`.
type summaryRun struct {
	// StopReason is what the platform LLM returned (§8.5).
	StopReason string
	// StopCategory is `stop_details.category`, present on a refusal.
	StopCategory string
	// Body is the model's text. A refusal still carries one — which is why
	// the stop_reason check has to come first.
	Body string
	// TransportError is a network/5xx failure with no stop_reason at all.
	TransportError bool
	// Attempt is which try this is; the second call on the same session is
	// how a retry loop posts two summaries.
	Attempt int
}

// summaryResult is the state after the step.
type summaryResult struct {
	SessionState string
	// SummaryMsgs is how many `session_summary` messages now exist in the
	// session — counted over the whole session, not over this call.
	SummaryMsgs int
	// MessageKind must be the contract's `summary` (openapi MessageKind), and
	// AuthorType `system`: the timeline renders it as a card, and a plain
	// `text` message from an agent author reads as if somebody said it.
	MessageKind string
	AuthorType  string

	// FeedError is the activity-feed entry a failure leaves.
	FeedError     bool
	ErrorCategory string
	// BodyWasRead is true when the implementation touched the response body
	// before deciding. §8.5 forbids it on the refusal path.
	BodyWasRead bool
}

var runSummaryP4 func(r summaryRun) summaryResult

func mustSummary(t *testing.T, r summaryRun) summaryResult {
	t.Helper()
	if runSummaryP4 == nil {
		t.Fatalf("unimplemented: platform-LLM session summary (FR-2.4, §8.5, E6-11). T-S9 must " +
			"wire `runSummaryP4` — see the P4a hand-off report 'required API'")
	}
	return runSummaryP4(r)
}

func TestSessionSummaryGolden(t *testing.T) {
	t.Run(caseNameP4("FR-2.4", "a_successful_summary_posts_exactly_one_system_summary_message"), func(t *testing.T) {
		r := mustSummary(t, summaryRun{StopReason: "end_turn", Body: "## 세션 요약\n결정 3건…", Attempt: 1})

		if r.SessionState != "completed" {
			t.Errorf("session = %q, want completed", r.SessionState)
		}
		if r.SummaryMsgs != 1 {
			t.Errorf("session_summary messages = %d, want exactly 1 (FR-2.4) — not \"at least "+
				"one\": two summaries in a timeline are indistinguishable and the reader cannot "+
				"tell which is current", r.SummaryMsgs)
		}
		if r.MessageKind != "summary" {
			t.Errorf("kind = %q, want summary (openapi MessageKind)", r.MessageKind)
		}
		if r.AuthorType != "system" {
			t.Errorf("author_type = %q, want system (FR-3.1) — the platform wrote it, not an agent",
				r.AuthorType)
		}
	})

	t.Run(caseNameP4("E6-11", "a_refusal_completes_the_session_without_a_summary_and_records_the_category"), func(t *testing.T) {
		r := mustSummary(t, summaryRun{
			StopReason: "refusal", StopCategory: "policy_violation",
			Body:    "I can't help with that.",
			Attempt: 1,
		})

		if r.SessionState != "completed" {
			t.Fatalf("session = %q, want completed — the work is finished; holding it in "+
				"`completing` because a cosmetic step failed strands it forever (E6-11)",
				r.SessionState)
		}
		if r.SummaryMsgs != 0 {
			t.Errorf("summary messages = %d, want 0 — the refusal text is NOT a summary, and "+
				"posting it puts \"I can't help with that\" at the end of the session (E6-11)",
				r.SummaryMsgs)
		}
		if !r.FeedError {
			t.Error("활동 피드에 오류 (E6-11)")
		}
		if r.ErrorCategory != "policy_violation" {
			t.Errorf("category = %q, want stop_details.category verbatim — a generic \"요약 실패\" "+
				"does not tell an operator whether to change the prompt or the model (§8.5 폴백 행)",
				r.ErrorCategory)
		}
		if r.BodyWasRead {
			t.Error("§8.5: `stop_reason == \"refusal\"` 처리는 응답 본문을 읽기 전에 검사한다. " +
				"Parsing first is how the refusal text becomes the summary")
		}
	})

	t.Run(caseNameP4("E6-11", "a_transport_failure_completes_the_session_the_same_way"), func(t *testing.T) {
		r := mustSummary(t, summaryRun{TransportError: true, Attempt: 1})
		if r.SessionState != "completed" {
			t.Errorf("session = %q, want completed — a 5xx from the summariser is not a reason to "+
				"leave a finished session in `completing`; E6-11's rule is about the OUTCOME, "+
				"and an implementation that only branches on the literal string \"refusal\" "+
				"strands every other failure [EVAL 제안 행 E6-12]", r.SessionState)
		}
		if !r.FeedError || r.ErrorCategory == "" {
			t.Errorf("feed error = %t category = %q, want the failure recorded with a category "+
				"[EVAL 제안 행 E6-12]", r.FeedError, r.ErrorCategory)
		}
		if r.SummaryMsgs != 0 {
			t.Errorf("summary messages = %d, want 0", r.SummaryMsgs)
		}
	})

	t.Run(caseNameP4("FR-2.4", "a_second_pass_over_a_completed_session_does_not_post_a_second_summary"), func(t *testing.T) {
		r := mustSummary(t, summaryRun{StopReason: "end_turn", Body: "요약", Attempt: 2})
		if r.SummaryMsgs != 1 {
			t.Errorf("session_summary messages = %d, want 1 — the summariser runs behind a "+
				"scheduler and a retry after a timeout is the normal way this is called twice "+
				"(FR-2.4 '요약 1개')", r.SummaryMsgs)
		}
	})
}

// ---------------------------------------------------------------------------
// 2. The §8.5 request — model, JSON, caching
// ---------------------------------------------------------------------------

// llmRequest is what the platform client sends for a given internal job.
type llmRequest struct {
	Model string
	// CacheControlOnPrefix is whether the stable prefix (워크스페이스 규칙·
	// 판정 루브릭) is marked cacheable.
	CacheControlOnPrefix bool
	// PrefixTokens is the length of that prefix.
	PrefixTokens int
	// ForceJSON is `output_config.format` (§8.5 구조화 출력 행).
	ForceJSON bool
	// Streaming is §8.5's 스트리밍 행.
	Streaming bool
	// FallbackHeader / FallbackParam are the server-side fallback opt-in.
	FallbackHeader string
	FallbackParam  string
}

// llmJob names an internal platform feature (§8.5 applies to these ONLY —
// never to agent execution, which runs on the user's own runtimes).
var buildLLMRequest func(job string) llmRequest

func TestPlatformLLMRequestGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if buildLLMRequest == nil {
			t.Fatalf("unimplemented: platform LLM client (§8.5). T-S9 must wire " +
				"`buildLLMRequest` — see the P4a hand-off report 'required API'")
		}
	}

	t.Run(caseNameP4("§8.5", "the_summary_uses_the_light_model_and_the_default_model_elsewhere"), func(t *testing.T) {
		must(t)
		sum := buildLLMRequest("session_summary")
		if sum.Model != "claude-sonnet-5" {
			t.Errorf("summary model = %q, want claude-sonnet-5 — §8.5 모델 행: 세션 요약·미리보기 "+
				"등 경량 작업은 sonnet. Running every summary on opus is a cost decision the "+
				"PRD already made", sum.Model)
		}
		crit := buildLLMRequest("criteria_met")
		if crit.Model != "claude-opus-5" {
			t.Errorf("criteria_met model = %q, want claude-opus-5 (§8.5 기본)", crit.Model)
		}
	})

	t.Run(caseNameP4("§8.5", "judgement_jobs_force_json_output"), func(t *testing.T) {
		must(t)
		for _, job := range []string{"criteria_met", "trigger_preview", "build_with_ai"} {
			if r := buildLLMRequest(job); !r.ForceJSON {
				t.Errorf("%s: force JSON = false — §8.5 구조화 출력 행 names these three, and a "+
					"prose answer parsed with a regex is the failure mode that rule exists to "+
					"remove", job)
			}
		}
	})

	t.Run(caseNameP4("§8.5", "the_stable_prefix_is_marked_cacheable"), func(t *testing.T) {
		must(t)
		r := buildLLMRequest("criteria_met")
		if !r.CacheControlOnPrefix {
			t.Error("§8.5 캐싱 행: 워크스페이스 규칙·판정 루브릭을 안정 prefix 로 두고 cache_control")
		}
	})

	t.Run(caseNameP4("§8.5", "a_refusal_fallback_is_opted_into_explicitly"), func(t *testing.T) {
		must(t)
		r := buildLLMRequest("session_summary")
		if r.FallbackHeader != "server-side-fallback-2026-07-01" {
			t.Errorf("beta header = %q, want server-side-fallback-2026-07-01 (§8.5 폴백 행)",
				r.FallbackHeader)
		}
		if r.FallbackParam != "default" {
			t.Errorf("fallbacks = %q, want \"default\" (§8.5 폴백 행)", r.FallbackParam)
		}
	})
}

// cacheVerification is §8.5's caching caveat made observable: below the
// model's minimum cache length the cache is silently NOT applied, so the only
// evidence is the usage field.
type cacheVerification struct {
	// CacheReadInputTokens is `usage.cache_read_input_tokens`.
	CacheReadInputTokens int
	// HitVerified is whether the implementation actually checked it.
	HitVerified bool
	// WarnedTooShort is the log/feed line for a prefix under the minimum.
	WarnedTooShort bool
}

var verifyCache func(prefixTokens, minCacheTokens, cacheReadTokens int) cacheVerification

func TestPlatformLLMCacheGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if verifyCache == nil {
			t.Fatalf("unimplemented: cache-hit verification (§8.5 캐싱 행). T-S9 must wire " +
				"`verifyCache` — see the P4a hand-off report 'required API'")
		}
	}

	t.Run(caseNameP4("§8.5", "a_prefix_under_the_models_minimum_is_reported_not_assumed_cached"), func(t *testing.T) {
		must(t)
		v := verifyCache(300, 1024, 0)
		if !v.WarnedTooShort {
			t.Error("§8.5: 모델별 최소 캐시 길이에 못 미치면 캐시가 조용히 적용되지 않는다. " +
				"Nothing errors, the bill just does not drop — so the check has to be ours")
		}
		if !v.HitVerified {
			t.Error("the verification is the point: `usage.cache_read_input_tokens` is the only " +
				"signal that says whether the cache_control did anything (§8.5)")
		}
	})

	t.Run(caseNameP4("§8.5", "a_long_prefix_with_a_read_count_counts_as_a_hit"), func(t *testing.T) {
		must(t)
		v := verifyCache(4000, 1024, 3800)
		if v.WarnedTooShort {
			t.Error("a 4000-token prefix is above the minimum; warning here would make the " +
				"warning noise")
		}
		if v.CacheReadInputTokens != 3800 {
			t.Errorf("cache_read_input_tokens = %d, want 3800 reported back", v.CacheReadInputTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// 3. Context reuse — FR-4.4, §8.4 [6]
// ---------------------------------------------------------------------------

// contextReuseCase is a previous session's summary being carried into a new
// session's brief (§8.4 [6] "Context: 첨부 자료 요약, 이전 세션 요약 (설정 상한 내)").
type contextReuseCase struct {
	// StoredSummaryTokens is the length of the summary as stored.
	StoredSummaryTokens int
	// MaxSummaryTokens is `context_reuse.max_summary_tokens` (default 2000).
	MaxSummaryTokens int
	// IncludeArtifacts is `context_reuse.include_artifacts`: links | none | full.
	IncludeArtifacts string
	ArtifactCount    int
}

type contextReuseResult struct {
	// InjectedTokens is what actually goes into the brief's [6] section.
	InjectedTokens int
	// StoredTokensAfter must be unchanged — the cap governs what we SEND.
	StoredTokensAfter int
	// TruncationDisclosed is the §8.4 history rule applied to [6]: the reader
	// is told it was cut.
	TruncationDisclosed bool
	// ArtifactLinks / ArtifactBodies are what section [6] carries.
	ArtifactLinks  int
	ArtifactBodies int
}

var planContextReuse func(c contextReuseCase) contextReuseResult

func TestContextReuseGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if planContextReuse == nil {
			t.Fatalf("unimplemented: context reuse cap (FR-4.4, §8.4 [6], openapi " +
				"ContextReusePolicy). T-S9 must wire `planContextReuse` — see the P4a hand-off " +
				"report 'required API'")
		}
	}

	t.Run(caseNameP4("FR-4.4", "a_summary_over_the_cap_is_trimmed_for_the_prompt_only"), func(t *testing.T) {
		must(t)
		r := planContextReuse(contextReuseCase{
			StoredSummaryTokens: 5000, MaxSummaryTokens: 2000, IncludeArtifacts: "links",
		})
		if r.InjectedTokens > 2000 {
			t.Errorf("injected = %d tokens, want ≤ 2000 — the cap exists because a stable brief "+
				"is re-sent every turn (§8.4 캐시 친화)", r.InjectedTokens)
		}
		if r.StoredTokensAfter != 5000 {
			t.Errorf("stored = %d, want 5000 unchanged — FR-4.4 caps what we SEND; trimming the "+
				"stored summary destroys the record the session produced", r.StoredTokensAfter)
		}
		if !r.TruncationDisclosed {
			t.Error("the brief must say it was cut, the same way the history section reports " +
				"`included`/`total`/`truncated` (§8.4) — an agent that does not know it is " +
				"reading a fragment answers as if it has the whole thing")
		}
	})

	t.Run(caseNameP4("FR-4.4", "a_summary_under_the_cap_is_injected_whole_and_undisclosed"), func(t *testing.T) {
		must(t)
		r := planContextReuse(contextReuseCase{
			StoredSummaryTokens: 800, MaxSummaryTokens: 2000, IncludeArtifacts: "links",
		})
		if r.InjectedTokens != 800 {
			t.Errorf("injected = %d, want the whole 800", r.InjectedTokens)
		}
		if r.TruncationDisclosed {
			t.Error("nothing was cut; a permanent \"truncated\" banner teaches the agent to " +
				"ignore it")
		}
	})

	t.Run(caseNameP4("FR-4.4", "include_artifacts_decides_links_versus_bodies_versus_nothing"), func(t *testing.T) {
		must(t)
		links := planContextReuse(contextReuseCase{
			StoredSummaryTokens: 100, MaxSummaryTokens: 2000,
			IncludeArtifacts: "links", ArtifactCount: 3,
		})
		if links.ArtifactLinks != 3 || links.ArtifactBodies != 0 {
			t.Errorf("links mode = %d links / %d bodies, want 3/0 (openapi ContextReusePolicy "+
				"default `links`)", links.ArtifactLinks, links.ArtifactBodies)
		}
		none := planContextReuse(contextReuseCase{
			StoredSummaryTokens: 100, MaxSummaryTokens: 2000,
			IncludeArtifacts: "none", ArtifactCount: 3,
		})
		if none.ArtifactLinks != 0 || none.ArtifactBodies != 0 {
			t.Errorf("none mode = %d/%d, want 0/0 — a workspace that turned artifact reuse off "+
				"expects it off", none.ArtifactLinks, none.ArtifactBodies)
		}
		full := planContextReuse(contextReuseCase{
			StoredSummaryTokens: 100, MaxSummaryTokens: 2000,
			IncludeArtifacts: "full", ArtifactCount: 3,
		})
		if full.ArtifactBodies != 3 {
			t.Errorf("full mode = %d bodies, want 3", full.ArtifactBodies)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. What the summary must contain — FR-2.4
// ---------------------------------------------------------------------------

// summaryContent is the four sections FR-2.4 names.
type summaryContent struct {
	Body string
	// Sections is what the builder claims it emitted.
	Sections []string
}

var buildSummaryBody func(decisions, artifacts, tasks int, costUSD float64) summaryContent

func TestSummaryContentGolden(t *testing.T) {
	t.Run(caseNameP4("FR-2.4", "the_summary_covers_decisions_artifacts_cost_and_timeline"), func(t *testing.T) {
		if buildSummaryBody == nil {
			t.Fatalf("unimplemented: summary composition (FR-2.4). T-S9 must wire " +
				"`buildSummaryBody` — see the P4a hand-off report 'required API'")
		}
		c := buildSummaryBody(3, 2, 7, 4.25)
		for _, want := range []string{"decisions", "artifacts", "cost", "timeline"} {
			var found bool
			for _, s := range c.Sections {
				if s == want {
					found = true
				}
			}
			if !found {
				t.Errorf("section %q missing from %v — FR-2.4 names 결정 기록·아티팩트·비용·"+
					"타임라인, and EVAL_USER's 여정 row promises the reader all four", want, c.Sections)
			}
		}
		if strings.TrimSpace(c.Body) == "" {
			t.Error("the body is what gets posted; an empty one makes the `session_summary` " +
				"message a blank card")
		}
	})
}
