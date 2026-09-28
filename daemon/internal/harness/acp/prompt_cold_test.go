// daemon-protocol v0.10.3 §4.1 · harness v0.9.14 §6·§10 (맥락 1단계 ②③):
// which turn prompt reaches which session. A resumed session gets `prompt`
// (the server's delta); every NEW session — no resume, resume_rejected, the
// D-13 cold retry — gets `prompt_cold` when the bundle has one.
package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
)

const (
	deltaPrompt = "DELTA: messages after the anchor only"
	coldPrompt  = "COLD: the whole turn prompt"
)

// sentPrompts is the text of every session/prompt the fake received, and the
// number of session/load calls.
func sentPrompts(t *testing.T, f *fixture) (prompts []string, loads int) {
	t.Helper()
	for _, r := range f.records() {
		switch r.Method {
		case "session/load":
			loads++
		case "session/prompt":
			var p struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			if err := json.Unmarshal(r.Params, &p); err != nil || len(p.Prompt) == 0 {
				t.Fatalf("session/prompt params %s: %v", r.Params, err)
			}
			prompts = append(prompts, p.Prompt[0].Text)
		}
	}
	return prompts, loads
}

func deltaBundle(kind contracts.RuntimeKind, sid string) contracts.TaskBundle {
	b := bundle(kind)
	b.Resume = resumeRef(kind, sid, sid)
	b.Prompt, b.PromptCold = deltaPrompt, coldPrompt
	return b
}

// 회귀 주입: runner.promptFor 가 늘 b.Prompt 를 돌려주면 이 테스트의 둘째·셋째
// 경우가 FAIL(새 세션에 델타가 간다).
func TestPromptColdGoesToEveryNewSession(t *testing.T) {
	t.Run("resumed session gets the delta", func(t *testing.T) {
		f := newFixture(t, acpfake.Script{KnownSessions: []string{"live"}}, deltaBundle(contracts.RuntimeClaudeCode, "live"), nil)
		if res := f.run(); res.ResumeOutcome != "resumed" {
			t.Fatalf("result %+v", res)
		}
		got, loads := sentPrompts(t, f)
		if loads != 1 || len(got) != 1 || got[0] != deltaPrompt {
			t.Fatalf("loads=%d prompts=%q — a resumed session gets `prompt`", loads, got)
		}
	})
	t.Run("resume_rejected sends prompt_cold to the new session (E8-02)", func(t *testing.T) {
		f := newFixture(t, acpfake.Script{}, deltaBundle(contracts.RuntimeClaudeCode, "gone"), nil)
		if res := f.run(); res.ResumeOutcome != "cold_start" {
			t.Fatalf("result %+v", res)
		}
		got, _ := sentPrompts(t, f)
		if len(got) != 1 || got[0] != coldPrompt {
			t.Fatalf("prompts=%q — a new session after resume_rejected must get prompt_cold", got)
		}
	})
	t.Run("D-13 cold retry sends prompt_cold", func(t *testing.T) {
		f := newFixture(t, acpfake.Script{
			Kind: "hermes", KnownSessions: []string{"old"},
			Turns: []acpfake.Turn{
				{StopReason: "refusal"},
				{Steps: []acpfake.Step{{Chunk: "done"}}},
			},
		}, deltaBundle(contracts.RuntimeHermes, "old"), nil)
		if res := f.run(); res.ResumeOutcome != "cold_start" || res.Outcome != "completed" {
			t.Fatalf("result %+v", res)
		}
		got, _ := sentPrompts(t, f)
		if len(got) != 2 || got[0] != deltaPrompt || got[1] != coldPrompt {
			t.Fatalf("prompts=%q — the resumed try gets the delta, the D-13 retry the cold prompt", got)
		}
	})
	t.Run("no prompt_cold: the cold fallback sends prompt (old server)", func(t *testing.T) {
		b := deltaBundle(contracts.RuntimeClaudeCode, "gone")
		b.PromptCold = ""
		f := newFixture(t, acpfake.Script{}, b, nil)
		f.run()
		got, _ := sentPrompts(t, f)
		if len(got) != 1 || got[0] != deltaPrompt {
			t.Fatalf("prompts=%q", got)
		}
	})
}

// daemon-protocol v0.10.3 §4.1: `resume: null` is the server's decision — no
// session/load at all, the first session/prompt is `prompt`.
func TestResumeNullNeverLoads(t *testing.T) {
	b := bundle(contracts.RuntimeClaudeCode)
	b.Prompt = coldPrompt
	f := newFixture(t, acpfake.Script{KnownSessions: []string{"live"}}, b, nil)
	if res := f.run(); res.ResumeOutcome != "" {
		t.Fatalf("result %+v — no resume was attempted", res)
	}
	got, loads := sentPrompts(t, f)
	if loads != 0 || len(got) != 1 || got[0] != coldPrompt {
		t.Fatalf("loads=%d prompts=%q", loads, got)
	}
}
