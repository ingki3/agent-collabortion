package httpapi

import (
	"fmt"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/observations"
)

// TestV11WorkspaceObservationsOverHTTP is getWorkspaceObservations' wire
// shape (openapi ObservationReport, K-18): member-only, five rows in §11
// order, breakdown only on routing_concentration, the window echoed and
// validated like metrics. The fixture's one session already holds a human hop
// (sessions.Create → RecordHumanHop, S-78), so the three hop rows read that
// single hop and the two others are empty; the numbers themselves are
// internal/observations' tests.
func TestV11WorkspaceObservationsOverHTTP(t *testing.T) {
	f := newP2Fixture(t)
	num := func(r map[string]any, k string) any {
		v, present := r[k]
		if !present {
			return "(absent)"
		}
		return v
	}
	out := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/observations", nil)
	if str(out, "window") != observations.DefaultWindow || str(out, "workspace_id") != f.wsID || str(out, "computed_at") == "" {
		t.Errorf("report header = %v", out)
	}
	rows, _ := out["rows"].([]any)
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	for i, raw := range rows {
		r := raw.(map[string]any)
		if str(r, "key") != observations.Defs[i].Key {
			t.Errorf("rows[%d].key = %s, want %s", i, str(r, "key"), observations.Defs[i].Key)
		}
		got := []any{num(r, "n"), num(r, "value"), num(r, "median"), num(r, "p95")}
		var want []any
		switch str(r, "key") {
		case "chain_scale": // the session's human hop, nothing derived from it
			want = []any{1.0, nil, 0.0, 0.0}
		case "chain_depth": // that hop is depth 1
			want = []any{1.0, nil, 1.0, 1.0}
		case "routing_concentration": // one platform hop, no fallback
			want = []any{1.0, 0.0, nil, nil}
		default: // join_breadth · empty_turn_rate — no sample: explicit nulls
			want = []any{0.0, nil, nil, nil}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s (n, value, median, p95) = %v, want %v", str(r, "key"), got, want)
		}
		if str(r, "label") == "" || str(r, "note") == "" {
			t.Errorf("%s lacks label/note", str(r, "key"))
		}
		if str(r, "key") == "routing_concentration" {
			bd, _ := r["breakdown"].([]any)
			if len(bd) != 9 {
				t.Errorf("breakdown = %v", r["breakdown"])
			} else if b := bd[8].(map[string]any); str(b, "kind") != "platform" || b["n"].(float64) != 1 || b["share"].(float64) != 1 {
				t.Errorf("breakdown[8] = %v, want platform · 1 · 1", b)
			} else if b := bd[0].(map[string]any); str(b, "kind") != "1" || b["n"].(float64) != 0 {
				t.Errorf("breakdown[0] = %v, want rule 1 · 0", b)
			}
		} else if _, has := r["breakdown"]; has {
			t.Errorf("%s carries a breakdown", str(r, "key"))
		}
	}
	out = f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/observations?window=P7D", nil)
	if str(out, "window") != "P7D" {
		t.Errorf("window echo = %q", str(out, "window"))
	}
	if st, out, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/observations?window=7days", nil); st != 422 {
		t.Errorf("bad window = %d %v, want 422", st, out)
	}
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "X", "email": "x-obs@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	if st, _, _ := other.do("GET", f.p+"/workspaces/"+f.wsID+"/observations", nil); st != 403 {
		t.Errorf("non-member = %d, want 403", st)
	}
}
