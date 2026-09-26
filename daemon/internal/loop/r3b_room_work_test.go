package loop

// T-R3b — the daemon consumes the bundle's `task.room_id`·`task.work_id`
// (daemon-protocol v0.9.0 §4.1): COLAB_ROOM_ID·COLAB_WORK_ID reach the agent
// on both tool surfaces (claude_code: the colab MCP server's env; hermes: the
// wrapper's exports), COLAB_SESSION_ID stays alongside until R4, a turn
// outside any mission has no COLAB_WORK_ID, and the claim log line names both.
//
// 회귀 주입(빌드를 깨지 않게):
//   - acp.TaskEnv.colabVars 의 `if t.RoomID != ""` 를 `&& false` 로 → (a)(b) ROOM
//   - loop.taskEnv 에서 `WorkID: b.Task.WorkID` 를 `WorkID: ""` 로 → (a)(b) WORK
//   - claim 로그의 `orDash(b.Task.WorkID)` 를 `orDash("")` 로 → (a) 로그

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

const (
	r3bRoom = "22222222-2222-4222-8222-222222222222"
	r3bWork = "33333333-3333-4333-8333-333333333333"
)

func mcpEnv(s acp.MCPServer) map[string]string {
	m := map[string]string{}
	for _, e := range s.Env {
		m[e.Name] = e.Value
	}
	return m
}

// (a) claude_code, inside a mission: MCP env + process env + claim log.
func TestRoomWorkReachTheMCPServer(t *testing.T) {
	b := bundle("t-rw")
	b.Task.SessionID, b.Task.RoomID, b.Task.WorkID = r3bRoom, r3bRoom, r3bWork
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, _ := newDaemon(t, srv, script)
	cap := &logCapture{}
	d.Log = func(f string, a ...any) { cap.sink(f, a...); t.Logf(f, a...) }
	record, envs := recordingSpawn(t, d, script)
	runOne(t, d, srv)

	colab, _ := sessionNewOf(t, record)
	env := mcpEnv(colab)
	for k, want := range map[string]string{"COLAB_ROOM_ID": r3bRoom, "COLAB_WORK_ID": r3bWork, "COLAB_SESSION_ID": r3bRoom} {
		if env[k] != want {
			t.Errorf("colab MCP env %s=%q, want %q", k, env[k], want)
		}
		if v := acp.EnvValue((*envs)[key("t-rw", 1)], k); v != want {
			t.Errorf("runtime process env %s=%q, want %q", k, v, want)
		}
	}
	if countContains(cap.all(), "t-rw.1 claim kind=task") != 1 || countContains(cap.all(), " room="+r3bRoom+" work="+r3bWork) != 1 {
		t.Errorf("claim line does not name room/work:\n%s", strings.Join(cap.all(), "\n"))
	}
}

// (b) hermes, outside any mission: the wrapper exports the room and no
// COLAB_WORK_ID at all; the claim line says `work=-`.
func TestRoomWithoutMissionInTheWrapper(t *testing.T) {
	b := hermesBundle("t-rh")
	b.Task.SessionID, b.Task.RoomID, b.Task.WorkID = r3bRoom, r3bRoom, ""
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, root := newDaemon(t, srv, hermesScript())
	cap := &logCapture{}
	d.Log = func(f string, a ...any) { cap.sink(f, a...); t.Logf(f, a...) }
	recordingSpawn(t, d, hermesScript())
	var wrapper string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" {
			x, _ := os.ReadFile(toolwrap.Path(root, "t-rh", 1))
			wrapper = string(x)
		}
	}
	runOne(t, d, srv)
	if !strings.Contains(wrapper, "export COLAB_ROOM_ID='"+r3bRoom+"'\n") || !strings.Contains(wrapper, "export COLAB_SESSION_ID='"+r3bRoom+"'\n") {
		t.Errorf("wrapper does not export the room (and the old session id):\n%s", wrapper)
	}
	if strings.Contains(wrapper, "COLAB_WORK_ID") {
		t.Errorf("mission-less turn exports COLAB_WORK_ID:\n%s", wrapper)
	}
	if countContains(cap.all(), " room="+r3bRoom+" work=-") != 1 {
		t.Errorf("claim line for a mission-less turn:\n%s", strings.Join(cap.all(), "\n"))
	}
}
