// Wiring for the P4 summary golden table (FR-2.4 · §8.4 [6] · §8.5).
//
// Every hook in summary_golden_test.go is server-owned, so this file carries no
// `//go:build` tag and the table runs in the default `go test ./...`.
//
// PRODUCTION CALL SITES:
//
//	runSummaryP4     → sessions.PlanSummary        sessions.Service.summarise, from
//	                                               ApplyCompletionEvent's `completed`
//	                                               branch (complete.go)
//	buildLLMRequest  → llm.BuildRequest            llm.HTTPClient.Do, for every §8.5 job;
//	                                               summarise builds JobSessionSummary
//	verifyCache      → llm.VerifyCache             llm.HTTPClient.Do, after every
//	                                               response with a marked prefix
//	planContextReuse → sessions.PlanContextReuse   queue.briefContext (§8.4 [6])
//	buildSummaryBody → sessions.BuildSummaryBody   sessions.Service.summarise — the
//	                                               model's fact block, and the whole
//	                                               body when no LLM is configured
package sessions

import (
	"github.com/ingki3/agent-collabortion/server/internal/llm"
)

func init() {
	runSummaryP4 = adaptRunSummaryP4
	buildLLMRequest = adaptBuildLLMRequest
	verifyCache = adaptVerifyCache
	planContextReuse = adaptPlanContextReuse
	buildSummaryBody = adaptBuildSummaryBody
}

// adaptRunSummaryP4 replays one pass of the summary step.
//
// The `Response` is built through llm.NewResponse and handed to PlanSummary
// UNREAD, which is what makes `BodyWasRead` a measurement rather than a claim:
// the flag is set by `Response.Text()`, so it comes back true only if the
// implementation actually parsed before deciding.
func adaptRunSummaryP4(r summaryRun) summaryResult {
	var res *llm.Response
	var callErr error
	if r.TransportError {
		callErr = errTransport
	} else {
		res = llm.NewResponse(r.StopReason, r.StopCategory, r.Body, llm.Usage{})
	}
	// Attempt ≥ 2 is the table's way of saying the session already has its
	// summary: the first attempt posted one.
	alreadyPosted := r.Attempt > 1
	plan := PlanSummary(res, callErr, alreadyPosted)

	out := summaryResult{
		SessionState: plan.SessionState,
		FeedError:    plan.FeedError, ErrorCategory: plan.ErrorCategory,
	}
	if res != nil {
		out.BodyWasRead = res.BodyRead()
	}
	// SummaryMsgs is counted over the whole session, not over this call. The
	// production count comes from `SELECT count(*) … kind = 'summary'` after the
	// guarded insert; here the same arithmetic is stated on the two facts the
	// table gives: what was already there, and what this pass added.
	if alreadyPosted {
		out.SummaryMsgs = 1
	}
	if plan.Post {
		out.SummaryMsgs++
	}
	if out.SummaryMsgs > 0 {
		// The row the insert writes (complete.go): kind `summary`, author
		// `system`.
		out.MessageKind, out.AuthorType = "summary", "system"
	}
	return out
}

type transportError struct{}

func (transportError) Error() string { return "llm: transport failure" }

var errTransport = transportError{}

func adaptBuildLLMRequest(job string) llmRequest {
	r := llm.BuildRequest(job)
	return llmRequest{
		Model:                r.Model,
		CacheControlOnPrefix: r.CacheControlOnPrefix,
		PrefixTokens:         r.PrefixTokens,
		ForceJSON:            r.ForceJSON,
		Streaming:            r.Streaming,
		FallbackHeader:       r.FallbackHeader,
		FallbackParam:        r.FallbackParam,
	}
}

func adaptVerifyCache(prefixTokens, minCacheTokens, cacheReadTokens int) cacheVerification {
	v := llm.VerifyCache(prefixTokens, minCacheTokens, cacheReadTokens)
	return cacheVerification{
		CacheReadInputTokens: v.CacheReadInputTokens,
		HitVerified:          v.HitVerified,
		WarnedTooShort:       v.WarnedTooShort,
	}
}

func adaptPlanContextReuse(c contextReuseCase) contextReuseResult {
	p := PlanContextReuse(ContextReuseInput{
		StoredSummaryTokens: c.StoredSummaryTokens,
		MaxSummaryTokens:    c.MaxSummaryTokens,
		IncludeArtifacts:    c.IncludeArtifacts,
		ArtifactCount:       c.ArtifactCount,
	})
	return contextReuseResult{
		InjectedTokens:      p.InjectedTokens,
		StoredTokensAfter:   p.StoredTokensAfter,
		TruncationDisclosed: p.TruncationDisclosed,
		ArtifactLinks:       p.ArtifactLinks,
		ArtifactBodies:      p.ArtifactBodies,
	}
}

// adaptBuildSummaryBody maps the table's counts onto the facts the composer
// takes. An entry with no text is a real row — an artifact submitted without a
// title — and the composer renders it as such, so the counts survive the map
// without the adapter inventing content.
func adaptBuildSummaryBody(decisions, artifacts, tasks int, costUSD float64) summaryContent {
	c := BuildSummaryBody(SummaryFacts{
		Title:     "골든 세션",
		Goal:      "표가 세는 것은 절 네 개의 존재다",
		Decisions: make([]string, decisions),
		Artifacts: make([]string, artifacts),
		Tasks:     tasks,
		CostUSD:   costUSD,
	})
	return summaryContent{Body: c.Body, Sections: c.Sections}
}
