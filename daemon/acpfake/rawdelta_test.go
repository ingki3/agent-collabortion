package acpfake

import "testing"

// D-27 (PR #204 리뷰 NN4) — consecutive raw deltas are never the same bytes,
// and they still read as the pieces of one JSON string (an opening quote,
// then words): the shape the real adapter's `partial_json` has.
func TestRawDeltasDiffer(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 400; i++ {
		d := rawDeltaJSON(i)
		if d == "" {
			t.Fatalf("delta %d empty", i)
		}
		if j, dup := seen[d]; dup {
			t.Fatalf("delta %d repeats delta %d: %q", i, j, d)
		}
		seen[d] = i
	}
	if rawDeltaJSON(0)[0] != '"' {
		t.Errorf("first delta %q does not open the JSON string", rawDeltaJSON(0))
	}
}
