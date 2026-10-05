package mcp

import "testing"

// TestToolRunsCoverTools: callTool's table has exactly one entry per
// registered tool — a tool added to Tools without a run would answer
// "unknown tool", and a run without a tool is dead.
//
// 회귀 주입: toolRuns 에서 한 줄을 지우거나 Tools 에 없는 이름을 더하면 FAIL.
func TestToolRunsCoverTools(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range Tools {
		seen[tl.Name] = true
		if toolRuns[tl.Name] == nil {
			t.Errorf("tool %s has no entry in toolRuns", tl.Name)
		}
	}
	for name := range toolRuns {
		if !seen[name] {
			t.Errorf("toolRuns has %s, which is not in Tools", name)
		}
	}
	if len(toolRuns) != 25 {
		t.Errorf("toolRuns has %d entries, want 25 (v0.9.12 ledger)", len(toolRuns))
	}
}
