package httpapi

import (
	"testing"
)

// An agent_role / respond_to outside the contract enum reached the INSERT or
// UPDATE as text and came back as Postgres 22P02 — a 500 (#305 review side
// note: role "member", a workspace role, sent as an agent role). createAgent
// and updateAgent both declare 422, so the value is refused as a field error
// before it reaches SQL; listAgents' filter has no 422 and answers an empty
// page for a role no agent can have.
func TestR2AgentEnumOutsideContract(t *testing.T) {
	f := newP2Fixture(t)
	base := map[string]any{
		"name": "Enum", "role": "researcher", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "p", "runtime_kind": "hermes", "model": "m", "is_default": true}},
	}
	with := func(k string, v any) map[string]any {
		out := map[string]any{}
		for kk, vv := range base {
			out[kk] = vv
		}
		out[k] = v
		return out
	}
	for _, tc := range []struct{ field, value string }{{"role", "member"}, {"respond_to", "everyone"}} {
		st, body, _ := f.api.do("POST", f.p+"/workspaces/"+f.wsID+"/agents", with(tc.field, tc.value))
		if st != 422 {
			t.Fatalf("createAgent %s=%q = %d %v, want 422", tc.field, tc.value, st, body)
		}
		if !hasFieldError(body, tc.field, "invalid") {
			t.Fatalf("createAgent %s=%q errors = %v, want a %s field error", tc.field, tc.value, body["errors"], tc.field)
		}
	}
	a := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", base)
	for _, tc := range []struct{ field, value string }{{"role", "member"}, {"respond_to", "everyone"}} {
		st, body, _ := f.api.do("PATCH", f.p+"/agents/"+str(a, "id"), map[string]any{tc.field: tc.value})
		if st != 422 {
			t.Fatalf("updateAgent %s=%q = %d %v, want 422", tc.field, tc.value, st, body)
		}
		if !hasFieldError(body, tc.field, "invalid") {
			t.Fatalf("updateAgent %s=%q errors = %v, want a %s field error", tc.field, tc.value, body["errors"], tc.field)
		}
	}
	// The refused PATCH left the agent as it was.
	if got := f.api.must(200, "GET", f.p+"/agents/"+str(a, "id"), nil); str(got, "role") != "researcher" {
		t.Fatalf("role after refused PATCH = %q, want researcher", str(got, "role"))
	}
	st, body, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/agents?role=member", nil)
	if st != 200 {
		t.Fatalf("listAgents role=member = %d %v, want 200 (no 422 in the contract)", st, body)
	}
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("listAgents role=member items = %d, want none", len(items))
	}
}
