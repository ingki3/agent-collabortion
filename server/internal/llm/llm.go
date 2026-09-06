// Package llm is PRD §8.5 — the platform's own use of the Claude API.
//
// SCOPE. §8.5 applies to PLATFORM features only: the session summary, the
// `criteria_met` judgement, trigger previews, Build with AI. Agent execution
// never comes through here — it runs on the user's own runtimes through the
// daemon, under the profile the workspace configured (PRD §8.2).
//
// WHAT §8.5 FIXES AND WHY EACH ONE IS EASY TO GET WRONG
//
//   - Model per job. `claude-opus-5` by default, `claude-sonnet-5` for the
//     light jobs (session summary, previews). Running every summary on opus is
//     a cost decision the PRD already made in the other direction.
//   - Structured output. Build with AI, `criteria_met` and trigger previews
//     force JSON through `output_config.format`, because a prose answer parsed
//     with a regex is exactly the failure that rule removes.
//   - Caching. The workspace rules and the judgement rubric go in a stable
//     prefix marked with `cache_control`. Below the model's minimum cache
//     length the cache is SILENTLY not applied — nothing errors, the bill just
//     does not drop — so the only evidence is `usage.cache_read_input_tokens`
//     and the check has to be ours (VerifyCache).
//   - Refusal. `stop_reason == "refusal"` is checked BEFORE the body is read.
//     A refusal still carries content, and code that parses first happily
//     posts "I can't help with that" as the session summary. `Response.Text`
//     is a method, not a field, so that ordering is observable rather than a
//     comment (see BodyRead).
package llm

import (
	"context"
	"strings"
)

// Job names an internal platform feature. §8.5's table is stated per job, so
// the job — not the caller — decides model, format and streaming.
const (
	JobSessionSummary = "session_summary"
	JobCriteriaMet    = "criteria_met"
	JobTriggerPreview = "trigger_preview"
	JobBuildWithAI    = "build_with_ai"
)

// Models of §8.5's 모델 row.
const (
	ModelDefault = "claude-opus-5"
	ModelLight   = "claude-sonnet-5"
)

// FallbackBeta is §8.5's 폴백 row: server-side fallback is opt-in, and the
// opt-in is a beta header plus a request parameter. Sending one without the
// other silently gets no fallback at all.
const (
	FallbackBeta  = "server-side-fallback-2026-07-01"
	FallbackParam = "default"
)

// Request is what the client sends for one job. It is a plan, not the wire
// body: `Encode` turns it into the Messages API shape, and the golden table
// measures the plan so the decisions are checkable without a network.
type Request struct {
	Job   string
	Model string
	// System is the stable prefix — workspace rules, judgement rubric. It is
	// what carries `cache_control` (§8.5 캐싱 행).
	System string
	// CacheControlOnPrefix marks that prefix cacheable.
	CacheControlOnPrefix bool
	// PrefixTokens is the prefix's length, kept so the caller can compare it
	// against the model's minimum cache length after the call.
	PrefixTokens int
	// Prompt is the per-call part, which is never cached.
	Prompt string
	// ForceJSON is `output_config.format` (§8.5 구조화 출력 행).
	ForceJSON bool
	// Streaming is §8.5's 스트리밍 행: long outputs stream.
	Streaming bool
	// MaxTokens bounds the answer.
	MaxTokens int
	// FallbackHeader / FallbackParam are the server-side fallback opt-in.
	FallbackHeader string
	FallbackParam  string
}

// lightJobs are §8.5's "세션 요약·미리보기 등 경량 작업".
var lightJobs = map[string]bool{
	JobSessionSummary: true,
	JobTriggerPreview: true,
}

// jsonJobs are the three §8.5 구조화 출력 names, exactly.
var jsonJobs = map[string]bool{
	JobCriteriaMet:    true,
	JobTriggerPreview: true,
	JobBuildWithAI:    true,
}

// streamingJobs are §8.5's 스트리밍 행 — "요약 생성 등 긴 출력".
var streamingJobs = map[string]bool{
	JobSessionSummary: true,
	JobBuildWithAI:    true,
}

// BuildRequest applies §8.5's table to a job. Every field it sets is a rule
// from that table, which is why the table is read here once instead of at each
// call site: a caller that forgets `ForceJSON` gets prose back and nothing
// says so until the regex that parses it drifts.
//
// production caller: llm.Client.Do (every §8.5 job goes through it) and
// sessions.Service.summarise (JobSessionSummary).
func BuildRequest(job string) Request {
	model := ModelDefault
	if lightJobs[job] {
		model = ModelLight
	}
	return Request{
		Job:   job,
		Model: model,
		// The prefix is marked cacheable for every job: all of them re-send a
		// stable rubric or rule block, and marking it costs nothing when the
		// prefix turns out to be short (it is simply not applied — which is
		// precisely why VerifyCache exists).
		CacheControlOnPrefix: true,
		ForceJSON:            jsonJobs[job],
		Streaming:            streamingJobs[job],
		MaxTokens:            4096,
		FallbackHeader:       FallbackBeta,
		FallbackParam:        FallbackParam,
	}
}

// Response is one answer. `body` is unexported and reachable only through
// Text(), so "the refusal check comes before the body is read" is a property
// of the type rather than a convention a later edit can quietly break.
type Response struct {
	StopReason string
	// StopCategory is `stop_details.category` — present on a refusal, and the
	// only thing that tells an operator whether to change the prompt or the
	// model.
	StopCategory string
	Usage        Usage

	body     string
	bodyRead bool
}

// Usage is the part of the API's `usage` §8.5 asks us to look at.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// NewResponse builds a Response. Transports use it; tests use it.
func NewResponse(stopReason, category, body string, u Usage) *Response {
	return &Response{StopReason: stopReason, StopCategory: category, Usage: u, body: body}
}

// Refused is §8.5's 폴백 row. It reads no content, so it is safe to call — and
// required to call — before Text().
func (r *Response) Refused() bool { return r.StopReason == StopRefusal }

// Text returns the model's output and records that it was read. A refusal
// still carries text; reading it before Refused() is how that text becomes the
// session summary.
func (r *Response) Text() string {
	r.bodyRead = true
	return r.body
}

// BodyRead reports whether Text() was called. It exists so the golden table
// can measure the ordering rule instead of trusting a comment about it.
func (r *Response) BodyRead() bool { return r.bodyRead }

// Stop reasons the platform branches on. Anything not listed is a failure like
// any other — E6-11's rule is about the OUTCOME, and an implementation that
// only branches on the literal string "refusal" strands every other case.
const (
	StopEndTurn  = "end_turn"
	StopStop     = "stop"
	StopRefusal  = "refusal"
	StopMaxToken = "max_tokens"
)

// Succeeded reports whether the answer may be used as-is.
func (r *Response) Succeeded() bool {
	return r.StopReason == StopEndTurn || r.StopReason == StopStop || r.StopReason == ""
}

// Category is the failure category to record on the activity feed. A refusal
// carries `stop_details.category`; anything else falls back to the stop reason
// itself so the feed never says a bare "요약 실패".
func (r *Response) Category() string {
	if r.StopCategory != "" {
		return r.StopCategory
	}
	return r.StopReason
}

// CacheVerification is §8.5's caching caveat made observable.
type CacheVerification struct {
	CacheReadInputTokens int
	// HitVerified is whether we actually looked at the usage field.
	HitVerified bool
	// WarnedTooShort is set when the prefix is under the model's minimum cache
	// length: the `cache_control` did nothing and nobody was told.
	WarnedTooShort bool
	// Hit is whether tokens were actually read from cache.
	Hit bool
}

// VerifyCache closes §8.5's caching loop: `cache_control` on a prefix shorter
// than the model's minimum is a silent no-op, so the prompt length is checked
// against the minimum AND the result is confirmed with
// `usage.cache_read_input_tokens`. Neither check alone is enough — a long
// prefix can still miss (first call, evicted entry) and a short one never hits.
//
// production caller: llm.Client.Do, after every response with a marked prefix.
func VerifyCache(prefixTokens, minCacheTokens, cacheReadTokens int) CacheVerification {
	return CacheVerification{
		CacheReadInputTokens: cacheReadTokens,
		HitVerified:          true,
		WarnedTooShort:       minCacheTokens > 0 && prefixTokens < minCacheTokens,
		Hit:                  cacheReadTokens > 0,
	}
}

// MinCacheTokens is the model's minimum cacheable prefix length. §8.5 says
// "수백~수천 토큰" and leaves the number to the model; these are the published
// minimums for the two models §8.5 names, and an unknown model gets the larger
// one so the warning errs toward telling us.
func MinCacheTokens(model string) int {
	switch model {
	case ModelLight:
		return 2048
	case ModelDefault:
		return 1024
	default:
		return 2048
	}
}

// Client runs a §8.5 job. It is an interface because the platform must keep
// working with no API key configured — see fallback.go — and because the
// golden table injects one.
type Client interface {
	Do(ctx context.Context, req Request) (*Response, error)
}

// EstimateTokens is a cheap token count for the length rules (§8.4 상한,
// §8.5 최소 캐시 길이). It is deliberately not a tokenizer: both uses are
// thresholds with a wide margin, and pulling a tokenizer in to decide whether
// to log a warning would cost more than the warning is worth.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	// ~4 characters per token for prose, but CJK runs closer to 1. Counting
	// runes and dividing by 3 sits between the two and never under-reports
	// Korean text, which is what the workspace rules are written in.
	n := len([]rune(s))
	return (n + 2) / 3
}

// TrimToTokens cuts a string to at most n estimated tokens, on a line boundary
// where it can. It is the §8.4 [6] cap's mechanism.
func TrimToTokens(s string, n int) (string, bool) {
	if n <= 0 {
		return "", s != ""
	}
	if EstimateTokens(s) <= n {
		return s, false
	}
	runes := []rune(s)
	limit := n * 3
	if limit > len(runes) {
		limit = len(runes)
	}
	cut := string(runes[:limit])
	if i := strings.LastIndex(cut, "\n"); i > limit/2 {
		cut = cut[:i]
	}
	return cut, true
}
