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
