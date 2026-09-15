// Package cost prices token usage that the runtime did not price itself.
//
// harness.md §11 (v0.7.1) splits the job in two: the daemon says whether a
// cost is measured, and the SERVER supplies the number when it is not. It has
// to be the server because the price list is workspace-owned (PRD §8.2.6
// "워크스페이스 가격표로 계산") — a daemon knows nothing about the account's
// rates, and FR-7.3 wants the estimate badged rather than blank.
//
// Before this existed the whole chain dropped the number on the floor: the
// daemon sent `cost_usd: 0`, the server stored the 0, and every session read a
// confident $0.00 (G4 3판 W16).
package cost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/jackc/pgx/v5"
)

// Price is USD per 1,000,000 tokens — the unit every published price list uses
// and the unit `budget_policy.pricing_overrides` is declared in (openapi).
type Price struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	CacheRead float64 `json:"cache_read"`
}

// Defaults is the fallback price list: Anthropic list prices (per 1M tokens,
// as published 2026-06) for the models this product runs. Cache reads are 10%
// of input except where the published rate differs (Fable 5.1: $0.25/MTok).
//
// These are DEFAULTS, not truth. A workspace on a different contract sets
// `budget_policy.pricing_overrides` and that wins per model; this table only
// keeps an unconfigured workspace from showing $0 for every session. Cache
// WRITES are not priced here — `task_usage` does not store them (the contract's
// TaskUsage has input, output, cache_read), so pricing them would be inventing
// a token count, and the number is an estimate either way.
var Defaults = map[string]Price{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1},
	"claude-opus-5":     {Input: 5, Output: 25, CacheRead: 0.5},
	"claude-opus-4-8":   {Input: 5, Output: 25, CacheRead: 0.5},
	"claude-opus-4-7":   {Input: 5, Output: 25, CacheRead: 0.5},
	"claude-opus-4-6":   {Input: 5, Output: 25, CacheRead: 0.5},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.2},
	"claude-sonnet-4-6": {Input: 3, Output: 15, CacheRead: 0.3},
	"claude-haiku-4-5":  {Input: 1, Output: 5, CacheRead: 0.1},
}

// Table is one workspace's price list: the defaults with its overrides on top.
type Table struct {
	overrides map[string]Price
}

// Load reads `workspace_settings.budget_policy.pricing_overrides`.
//
// A workspace with no settings row (or no overrides) is not an error — it just
// prices from Defaults. Malformed JSON is ignored for the same reason: a bad
// override must not turn every finish into a 500.
func Load(ctx context.Context, q db.DBTX, workspaceID uuid.UUID) (Table, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT budget_policy FROM workspace_settings WHERE workspace_id = $1`, workspaceID).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// S-22: a real database error used to look exactly like "no settings
		// row" — both fell through to the empty table, and an administrator who
		// had configured prices saw $0 with nothing anywhere saying why.
		// Pricing from defaults is still the answer (a finish must not 500 over
		// it), but the reason is now in the log.
		slog.Warn("cost: read pricing overrides", "err", err, "workspace", workspaceID)
		return Table{}, nil
	}
	if err != nil || len(raw) == 0 {
		return Table{}, nil //nolint:nilerr // missing settings price from defaults
	}
	var policy struct {
		PricingOverrides map[string]Price `json:"pricing_overrides"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		// Same reasoning, one level in: malformed overrides are ignored so the
		// finish survives, and said out loud so they can be fixed.
		slog.Warn("cost: pricing_overrides is not valid JSON — pricing from defaults",
			"err", err, "workspace", workspaceID)
		return Table{}, nil
	}
	t := Table{}
	if len(policy.PricingOverrides) > 0 {
		t.overrides = make(map[string]Price, len(policy.PricingOverrides))
		for k, v := range policy.PricingOverrides {
			t.overrides[Normalize(k)] = v
		}
	}
	return t, nil
}

// NewTable builds a table from overrides directly (tests, callers that already
// hold the policy).
func NewTable(overrides map[string]Price) Table {
	t := Table{}
	if len(overrides) > 0 {
		t.overrides = make(map[string]Price, len(overrides))
		for k, v := range overrides {
			t.overrides[Normalize(k)] = v
		}
	}
	return t
}

// Normalize maps the many spellings of one model onto one key.
//
// The same model reaches us under at least three names: the profile's
// (`claude-opus-5`), Hermes' provider-prefixed one (`anthropic:claude-sonnet-5`
// — PRD FR-1.6), and a dated snapshot (`claude-haiku-4-5-20251001`, or Vertex'
// `@`-separated form). They are one price.
func Normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, ":"); i >= 0 { // anthropic:claude-sonnet-5
		m = m[i+1:]
	}
	if i := strings.Index(m, "@"); i >= 0 { // claude-opus-4-5@20251101
		m = m[:i]
	}
	// a trailing -YYYYMMDD snapshot is the same model at the same price
	if i := strings.LastIndex(m, "-"); i > 0 && len(m)-i == 9 && isDigits(m[i+1:]) {
		m = m[:i]
	}
	return m
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// Price returns the model's rate: the workspace override first, then the
// default table. The second result is false when neither knows the model —
// the caller must NOT invent a number for it (an unknown model's cost stays
// unknown, which `estimated` already says).
func (t Table) Price(model string) (Price, bool) {
	m := Normalize(model)
	if m == "" {
		return Price{}, false
	}
	if p, ok := t.overrides[m]; ok {
		return p, true
	}
	if p, ok := Defaults[m]; ok {
		return p, true
	}
	// a dated or suffixed variant of a known model ("claude-opus-5-preview")
	if p, ok := longestPrefix(t.overrides, m); ok {
		return p, true
	}
	return longestPrefix(Defaults, m)
}

func longestPrefix(table map[string]Price, m string) (Price, bool) {
	best, out, ok := "", Price{}, false
	for k, v := range table {
		if strings.HasPrefix(m, k) && len(k) > len(best) {
			best, out, ok = k, v, true
		}
	}
	return out, ok
}

// Estimate prices one usage row. ok is false when the model has no rate — the
// caller leaves the stored cost alone rather than writing a made-up 0.
func (t Table) Estimate(model string, inputTokens, outputTokens, cacheRead int64) (float64, bool) {
	p, ok := t.Price(model)
	if !ok {
		return 0, false
	}
	const perMillion = 1_000_000.0
	usd := (float64(inputTokens)*p.Input + float64(outputTokens)*p.Output + float64(cacheRead)*p.CacheRead) / perMillion
	if usd < 0 {
		return 0, false
	}
	return usd, true
}
