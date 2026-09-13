package httpapi

import (
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/metrics"
)

// TestP5WorkspaceMetricsOverHTTP is getWorkspaceMetrics' wire shape (openapi
// MetricsReport): member-only, ten metrics in §11 order, null values on an
// empty workspace, the default window echoed, a bad window 422. The numbers
// themselves are internal/metrics' tests.
func TestP5WorkspaceMetricsOverHTTP(t *testing.T) {
	f := newP2Fixture(t)
	out := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/metrics", nil)
	if str(out, "window") != metrics.DefaultWindow || str(out, "workspace_id") != f.wsID || str(out, "computed_at") == "" {
		t.Errorf("report header = %v", out)
	}
	ms, _ := out["metrics"].([]any)
	if len(ms) != 10 {
		t.Fatalf("metrics = %d, want 10", len(ms))
	}
	for i, raw := range ms {
		m := raw.(map[string]any)
		if str(m, "key") != metrics.Defs[i].Key {
			t.Errorf("metrics[%d].key = %s, want %s", i, str(m, "key"), metrics.Defs[i].Key)
		}
		if v, present := m["value"]; !present || v != nil {
			t.Errorf("%s value = %v, want an explicit null (표본 없음)", str(m, "key"), v)
		}
		if m["n"].(float64) != 0 {
			t.Errorf("%s n = %v", str(m, "key"), m["n"])
		}
		for _, k := range []string{"label", "unit", "target_op", "note"} {
			if str(m, k) == "" {
				t.Errorf("%s lacks %s", str(m, "key"), k)
			}
		}
		if str(m, "key") == "task_success_rate_by_runtime" {
			if bd, _ := m["breakdown"].([]any); len(bd) != 2 {
				t.Errorf("breakdown = %v", m["breakdown"])
			}
		} else if _, has := m["breakdown"]; has {
			t.Errorf("%s carries a breakdown", str(m, "key"))
		}
	}
	// window echoes and parses; a malformed one is 422 on the parameter.
	out = f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/metrics?window=P7D", nil)
	if str(out, "window") != "P7D" {
		t.Errorf("window echo = %q", str(out, "window"))
	}
	if st, out, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/metrics?window=7days", nil); st != 422 {
		t.Errorf("bad window = %d %v, want 422", st, out)
	}
	// Not a member: 403.
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "X", "email": "x@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	if st, _, _ := other.do("GET", f.p+"/workspaces/"+f.wsID+"/metrics", nil); st != 403 {
		t.Errorf("non-member = %d, want 403", st)
	}
}
