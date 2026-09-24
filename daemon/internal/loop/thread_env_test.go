package loop

// T-THREAD — the daemon consumes the bundle's `task.thread_root_id`
// (daemon-protocol v0.9.2 §4.1): COLAB_THREAD_ID reaches the agent on both
// tool surfaces (claude_code: the colab MCP server's env and the runtime
// process; hermes: the wrapper's exports), and a top-level turn has no
// COLAB_THREAD_ID key at all — not an empty one (harness v0.9.3 §2.1).
//
// 회귀 주입(빌드를 깨지 않게):
//   - acp.TaskEnv.colabVars 의 `if t.ThreadID != ""` 를 `if true` 로 → (d) 키 부재 FAIL
//   - loop.taskEnv 에서 `ThreadID: b.Task.ThreadRootID` 를 `ThreadID: ""` 로 → (c)(e) FAIL

import (
	"os"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

const threadRoot = "44444444-4444-4444-8444-444444444444"

func claudeThreadEnvs(t *testing.T, id, thread string) (mcp map[string]string, proc []string) {
	t.Helper()
	b := bundle(id)
	b.Task.RoomID, b.Task.ThreadRootID = r3bRoom, thread
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, _ := newDaemon(t, srv, script)
	record, envs := recordingSpawn(t, d, script)
	runOne(t, d, srv)
	colab, _ := sessionNewOf(t, record)
	return mcpEnv(colab), (*envs)[key(id, 1)]
}

// (c) claude_code, a thread turn: the MCP server and the runtime process both
// carry COLAB_THREAD_ID = the thread root.
func TestThreadIDReachesTheMCPServer(t *testing.T) {
	mcp, proc := claudeThreadEnvs(t, "t-th", threadRoot)
	if mcp["COLAB_THREAD_ID"] != threadRoot {
		t.Errorf("colab MCP env COLAB_THREAD_ID=%q, want %q", mcp["COLAB_THREAD_ID"], threadRoot)
	}
	if v := acp.EnvValue(proc, "COLAB_THREAD_ID"); v != threadRoot {
		t.Errorf("runtime process env COLAB_THREAD_ID=%q, want %q", v, threadRoot)
	}
}

// (d) claude_code, a top-level turn: the key is absent, not empty.
func TestTopLevelTurnHasNoThreadKey(t *testing.T) {
	mcp, proc := claudeThreadEnvs(t, "t-tl", "")
	if _, ok := mcp["COLAB_THREAD_ID"]; ok {
		t.Errorf("top-level turn put COLAB_THREAD_ID in the MCP env: %q", mcp["COLAB_THREAD_ID"])
	}
	for _, kv := range proc {
		if strings.HasPrefix(kv, "COLAB_THREAD_ID=") {
			t.Errorf("top-level turn put %q in the runtime process env", kv)
		}
	}
}

// (e) hermes: the wrapper exports the thread on a thread turn and nothing on
// a top-level one.
func TestThreadIDInTheWrapper(t *testing.T) {
	for _, tc := range []struct{ id, thread string }{{"t-hw1", threadRoot}, {"t-hw0", ""}} {
		b := hermesBundle(tc.id)
		b.Task.RoomID, b.Task.ThreadRootID = r3bRoom, tc.thread
		srv := &memServer{queue: []contracts.TaskBundle{b}}
		d, root := newDaemon(t, srv, hermesScript())
		recordingSpawn(t, d, hermesScript())
		var wrapper string
		srv.phaseHook = func(req api.PhaseRequest) {
			if req.Phase == "preparing" {
				x, _ := os.ReadFile(toolwrap.Path(root, tc.id, 1))
				wrapper = string(x)
			}
		}
		runOne(t, d, srv)
		if wrapper == "" {
			t.Fatalf("%s: wrapper was never written", tc.id)
		}
		has := strings.Contains(wrapper, "export COLAB_THREAD_ID='"+threadRoot+"'\n")
		if tc.thread != "" && !has {
			t.Errorf("thread turn's wrapper does not export COLAB_THREAD_ID:\n%s", wrapper)
		}
		if tc.thread == "" && strings.Contains(wrapper, "COLAB_THREAD_ID") {
			t.Errorf("top-level turn's wrapper exports COLAB_THREAD_ID:\n%s", wrapper)
		}
	}
}
