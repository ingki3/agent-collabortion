package cost

import "testing"

// One model reaches the price table under several spellings, and they are one
// price. Getting this wrong is silent: an unmatched name prices at nothing and
// the session shows $0 — the exact symptom S-20 exists to remove.
func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5":             "claude-opus-5",
		"CLAUDE-OPUS-5":             "claude-opus-5",
		"  claude-opus-5  ":         "claude-opus-5",
		"anthropic:claude-sonnet-5": "claude-sonnet-5", // Hermes (PRD FR-1.6)
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		"claude-opus-4-5@20251101":  "claude-opus-4-5", // Vertex snapshot
		"openai:gpt-9":              "gpt-9",
		"":                          "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// T-COSTMODEL: the three spellings Claude Code reports. Each is asserted on
// the EXACT key, not through Price — Price's longest-prefix fallback happens
// to rescue some of them today, and a test that passed through it would not
// notice the rule going away.
func TestNormalizeContextTag(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5[1m]":             "claude-opus-5",
		"CLAUDE-OPUS-5[1M]":             "claude-opus-5",
		"claude-sonnet-4-6 [1m]":        "claude-sonnet-4-6",
		"claude-haiku-4-5-20251001[1m]": "claude-haiku-4-5",
		"anthropic:claude-opus-5[1m]":   "claude-opus-5",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q — a context-window tag is priced at the base rate", in, got, want)
		}
	}
}

func TestNormalizeCommaListTakesFirst(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5[1m],claude-haiku-4-5-20251001": "claude-opus-5",
		"claude-haiku-4-5-20251001,claude-opus-5[1m]": "claude-haiku-4-5",
		" claude-sonnet-5 , claude-opus-5":            "claude-sonnet-5",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q — the first model is the main one", in, got, want)
		}
	}
}

func TestNormalizeDateVariants(t *testing.T) {
	for in, want := range map[string]string{
		"claude-haiku-4-5-2025-10-01": "claude-haiku-4-5",
		"claude-opus-4-8-latest":      "claude-opus-4-8",
		"claude-haiku-4-5-20251001":   "claude-haiku-4-5",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	// a 4-digit tail is still a model name, not a date
	if got := Normalize("claude-opus-4-8-2026"); got != "claude-opus-4-8-2026" {
		t.Errorf("Normalize stripped a non-date: %q", got)
	}
}

// The comma rule changes a PRICE, not just a key: the override for the main
// model is what must win for the joined string.
func TestCommaListPricesTheMainModel(t *testing.T) {
	tab := NewTable(map[string]Price{"claude-opus-5": {Input: 7, Output: 7}})
	p, ok := tab.Price("claude-opus-5[1m],claude-haiku-4-5-20251001")
	if !ok || p.Input != 7 {
		t.Fatalf("price = %+v ok=%v, want the claude-opus-5 override", p, ok)
	}
}

// A `-1234` suffix is a model name, not a date — only 8 digits are a snapshot.
func TestNormalizeKeepsShortSuffixes(t *testing.T) {
	if got := Normalize("claude-haiku-4-5"); got != "claude-haiku-4-5" {
		t.Fatalf("Normalize stripped a version suffix: %q", got)
	}
}

func TestPriceOverrideBeatsDefault(t *testing.T) {
	tab := NewTable(map[string]Price{"claude-opus-5": {Input: 1, Output: 2, CacheRead: 0.5}})
	p, ok := tab.Price("anthropic:claude-opus-5")
	if !ok || p.Input != 1 || p.Output != 2 {
		t.Fatalf("override = %+v ok=%v", p, ok)
	}
	// a model the workspace did not override still prices from the defaults
	if p, ok := tab.Price("claude-haiku-4-5"); !ok || p != Defaults["claude-haiku-4-5"] {
		t.Fatalf("default = %+v ok=%v", p, ok)
	}
}

// An unknown model has NO price, and saying so is the point: `estimated: true`
// with nothing behind it is honest, an invented rate is not.
func TestUnknownModelHasNoPrice(t *testing.T) {
	if _, ok := (Table{}).Price("some-local-llama"); ok {
		t.Fatal("unknown model got a price")
	}
	if _, ok := (Table{}).Estimate("some-local-llama", 1000, 1000, 0); ok {
		t.Fatal("unknown model got an estimate")
	}
	if _, ok := (Table{}).Price(""); ok {
		t.Fatal("empty model got a price")
	}
}

func TestEstimateIsPerMillionTokens(t *testing.T) {
	tab := NewTable(map[string]Price{"m": {Input: 3, Output: 15, CacheRead: 0.3}})
	got, ok := tab.Estimate("m", 1_000_000, 100_000, 2_000_000)
	if !ok {
		t.Fatal("no estimate")
	}
	want := 3.0 + 1.5 + 0.6
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Estimate = %v, want %v", got, want)
	}
}
