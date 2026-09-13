package loop

// daemon-protocol v0.8 §4.5 테스트 채팅 — 데몬 몫 (T-D12).
//
// (a) 토큰 없는 번들: COLAB_* 0개 · session/new 에 mcpServers 없음 · hermes 래퍼
//     파일 없음 · 프롬프트의 `colab ` 치환 없음. 세션 task 번들은 그대로다 —
//     회귀 주입(§0-9): loop.mcpServers 의 ColabSurface 분기를 빼면 (a) 의 테스트
//     채팅 쪽이, acp.Env 의 분기를 빼면 env 쪽이 깨진다.
// (b) workdir: <root>/.colab/testchat/<id> mkdir -p, 그 밖은 거부.
// (c) resume: 번들 resume 그대로 session/load.
// (d) finish.transport=acp. (e) §6 lane 종료 보고 없음.
// (f) gc 경로 가드 — testchat 아래만 rm, 나머지 refused + reason, 행 모양.
// (g) 시작 시 24h 방어.
//
// 레시피: acpfake record(실제 session/new·prompt 본문) + phaseHook(preparing
// 시점의 디스크 상태) — T-D9b 메모.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

const testChatID = "7e1a2b3c-0000-4000-8000-00000000c4a7"

// testChatBundle is the §4.5 shape as server/internal/testchat/bundle.go
// builds it: kind=test_chat, id=test_chat id, attempt=turn, token "", no
// lane/session/trigger, workdir {dir, <root>/.colab/testchat/<id>, reuse}.
func testChatBundle(root string, kind contracts.RuntimeKind, turn int) contracts.TaskBundle {
	b := contracts.TaskBundle{
		Task: contracts.BundleTask{ID: testChatID, Attempt: turn, Kind: "test_chat", TestChatID: testChatID,
			AgentID: "ag-1", AgentName: "Guide"},
		TaskToken: "",
		Profile:   contracts.BundleProfile{RuntimeKind: kind, Model: "sonnet"},
		Workdir:   contracts.BundleWorkdir{Kind: "dir", Path: workdir.TestChatPath(root, testChatID), Reuse: true},
		Brief:     contracts.BundleBrief{Transport: contracts.BriefACPMetaSystemPrompt, Text: "[1] Agent Identity\nYou are Guide.\n[8] precedence\n"},
		Prompt:    "이것은 시험 대화다 — 플랫폼 명령은 쓸 수 없다.\n\n안녕, 너는 누구니? (예: `colab message post` 는 쓰지 마라)",
		Limits:    contracts.BundleLimits{StallSeconds: 180},
	}
	if kind == contracts.RuntimeHermes {
		b.Brief.Transport = contracts.BriefInstructionFile
	}
	return b
}

// recordingSpawn is newDaemon's SpawnConfig with the DAEMON's own env on the
// fake (attemptEnv) and the fake's request log kept: what the test reads is
// the real session/new·prompt the loop sent, under the env the loop built.
func recordingSpawn(t *testing.T, d *Daemon, script acpfake.Script) (record string, envs *map[string][]string) {
	t.Helper()
	record = filepath.Join(t.TempDir(), "record.jsonl")
	seen := map[string][]string{}
	envs = &seen
	d.SpawnConfig = func(b contracts.TaskBundle, wd string) acp.Config {
		cmd, args, fakeEnv := acpfake.Command(script, record)
		env := d.attemptEnv(b)
		seen[key(b.Task.ID, b.Task.Attempt)] = env
		return acp.Config{Command: cmd, Args: args, Env: append(env, fakeEnv...), KillAfter: time.Second}
	}
	return record, envs
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

// sessionOpen returns the mcpServers count and method of the session/new or
// session/load the fake recorded.
func sessionOpen(t *testing.T, record string) (method string, mcp int) {
	t.Helper()
	recs, err := acpfake.ReadRecords(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		switch r.Method {
		case acp.MethodSessionNew:
			var p acp.NewSessionParams
			_ = json.Unmarshal(r.Params, &p)
			return r.Method, len(p.MCPServers)
		case acp.MethodSessionLoad:
			var p acp.LoadSessionParams
			_ = json.Unmarshal(r.Params, &p)
			return r.Method, len(p.MCPServers)
		}
	}
	t.Fatal("no session/new or session/load recorded")
	return "", 0
}

func runOne(t *testing.T, d *Daemon, srv *memServer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
}

// (a)(b)(d)(e) on claude_code: no COLAB_*, no mcpServers, directory under
// .colab/testchat, transport acp, no lane report.
func TestTestChatClaudeCodeHasNoColabSurface(t *testing.T) {
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	srv.queue = []contracts.TaskBundle{testChatBundle(root, contracts.RuntimeClaudeCode, 1)}
	record, envs := recordingSpawn(t, d, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "나는 Guide"}}}}})
	want := workdir.TestChatPath(root, testChatID)
	var seenDir bool
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" {
			st, err := os.Stat(want)
			seenDir = err == nil && st.IsDir()
		}
	}
	runOne(t, d, srv)

	// (a) env: zero COLAB_* — not even COLAB_SERVER_URL.
	env := (*envs)[key(testChatID, 1)]
	if n := countPrefix(env, acp.ReservedEnvPrefix); n != 0 {
		t.Errorf("test chat env has %d COLAB_* vars, want 0: %v", n, env)
	}
	// (a) session/new without mcpServers.
	if method, mcp := sessionOpen(t, record); method != acp.MethodSessionNew || mcp != 0 {
		t.Errorf("%s mcpServers=%d, want session/new with 0", method, mcp)
	}
	// (b) the directory is <root>/.colab/testchat/<id> and existed at spawn.
	if !seenDir {
		t.Errorf("%s did not exist at phase=preparing", want)
	}
	if len(srv.phases) == 0 || srv.phases[0].WorkdirPath != want {
		t.Errorf("phase workdir_path %+v, want %s", srv.phases, want)
	}
	// (d) transport.
	f := srv.finishes[0]
	if f.Outcome != "completed" || f.Transport != contracts.TransportACP {
		t.Errorf("finish %+v, want completed + transport acp", f)
	}
	if f.RuntimeSessionRef == nil || f.RuntimeSessionRef.SessionID == "" {
		t.Errorf("finish has no runtime_session_ref — the next turn's resume needs it: %+v", f)
	}
	// (e) no §6 lane-end row for the test chat directory.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, rep := range srv.workdirReports {
		for _, w := range rep.Workdirs {
			if w.Path == want {
				t.Errorf("test chat directory reported as a workdir row: %+v", w)
			}
		}
	}
}

// The control: the SAME loop with a session task keeps every part of the
// colab surface. Remove the ColabSurface branch and this stays green while
// the test above goes red — or the other way round if the branch is inverted.
func TestSessionTaskKeepsColabSurface(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t-ctl")}}
	d, _ := newDaemon(t, srv, acpfake.Script{})
	d.Cfg.ServerURL = "http://colab.test"
	record, envs := recordingSpawn(t, d, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}})
	runOne(t, d, srv)
	env := (*envs)[key("t-ctl", 1)]
	if acp.EnvValue(env, "COLAB_TASK_TOKEN") != "ctk_x" || acp.EnvValue(env, "COLAB_SERVER_URL") != "http://colab.test" || countPrefix(env, acp.ReservedEnvPrefix) < 6 {
		t.Errorf("session task env lost its COLAB_* set: %v", env)
	}
	if method, mcp := sessionOpen(t, record); method != acp.MethodSessionNew || mcp != 1 {
		t.Errorf("%s mcpServers=%d, want session/new with the colab MCP server", method, mcp)
	}
	if srv.finishes[0].Transport != contracts.TransportACP {
		t.Errorf("session task finish.transport=%q, want acp (allowed on every attempt)", srv.finishes[0].Transport)
	}
}

// (a) on hermes: the cli_wrapper surface is off too — no wrapper file at
// spawn time and the prompt's `colab ` left alone. (c) turn 2 resumes.
func TestTestChatHermesNoWrapperAndResume(t *testing.T) {
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	b1 := testChatBundle(root, contracts.RuntimeHermes, 1)
	srv.queue = []contracts.TaskBundle{b1}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, KnownSessions: []string{"sess-1"},
		Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	record, _ := recordingSpawn(t, d, script)
	wrapper := toolwrap.Path(root, testChatID, 1)
	wrapperSeen := false
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" {
			_, err := os.Stat(wrapper)
			wrapperSeen = err == nil
		}
	}
	runOne(t, d, srv)
	if wrapperSeen {
		t.Errorf("hermes test chat got a CLI wrapper at %s", wrapper)
	}
	if p := promptOf(t, record); !strings.Contains(p, "`colab message post`") {
		t.Errorf("prompt was rewritten although there is no wrapper:\n%s", p)
	}
	if method, _ := sessionOpen(t, record); method != acp.MethodSessionNew {
		t.Errorf("turn 1 opened with %s, want session/new", method)
	}

	// (c) turn 2: the server puts turn 1's ref in `resume`; the daemon loads.
	srv2 := &memServer{}
	d2, _ := newDaemon(t, srv2, acpfake.Script{})
	d2.Cfg.WorkdirRoot = root
	b2 := testChatBundle(root, contracts.RuntimeHermes, 2)
	b2.Resume = srv.finishes[0].RuntimeSessionRef
	b2.Prompt = "한 문장으로 다시"
	srv2.queue = []contracts.TaskBundle{b2}
	record2, _ := recordingSpawn(t, d2, script)
	runOne(t, d2, srv2)
	if method, mcp := sessionOpen(t, record2); method != acp.MethodSessionLoad || mcp != 0 {
		t.Errorf("turn 2 opened with %s mcpServers=%d, want session/load with 0", method, mcp)
	}
	if f := srv2.finishes[0]; f.ResumeOutcome != "resumed" || f.Transport != contracts.TransportACP {
		t.Errorf("turn 2 finish %+v, want resumed + acp", f)
	}
}

// (b) a bundle path outside .colab/testchat is refused before anything is
// created — the same class of guard as T-D10's worktree target.
func TestTestChatRefusesPathOutsideTestChatDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, p := range []string{
		filepath.Join(outside, "x"),                     // outside the root
		filepath.Join(root, "sessions", "s", "l"),       // a lane folder
		workdir.TestChatDir(root),                       // the parent itself
		filepath.Join(workdir.TestChatDir(root), "a/b"), // one level too deep
	} {
		b := testChatBundle(root, contracts.RuntimeClaudeCode, 1)
		b.Workdir.Path = p
		if wd, err := workdir.PrepareTestChat(root, b); err == nil {
			t.Errorf("PrepareTestChat accepted %q → %s", p, wd)
		}
		if _, err := os.Stat(p); err == nil && p != workdir.TestChatDir(root) {
			t.Errorf("refused path %q was created anyway", p)
		}
	}
	// A symlink under .colab/testchat pointing out of it is not a test chat dir.
	target := filepath.Join(outside, "victim")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir.TestChatDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workdir.TestChatDir(root), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := workdir.RemoveTestChat(root, link); err == nil {
		t.Errorf("RemoveTestChat followed a symlink out of the test chat dir")
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("symlink target was removed: %v", err)
	}
}

// (f) gc: only a path under .colab/testchat is removed; the receipt is the
// §4.5 row (id · kind dir · path · test_chat_id · bytes 0 · gc) with no
// session_id; everything else is refused with a reason.
func TestTestChatGCGuardAndReceipt(t *testing.T) {
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	d.init()
	chat := workdir.TestChatPath(root, testChatID)
	if err := os.MkdirAll(chat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lane := filepath.Join(root, "sessions", "s", "l")
	if err := os.MkdirAll(lane, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	d.gc(context.Background(), contracts.Command{Type: contracts.CmdGC, TestChatID: testChatID, Workdirs: []contracts.GCWorkdir{
		{ID: testChatID, Path: chat},
		{ID: "row-lane", Path: lane},
		{ID: "row-out", Path: outside},
	}})
	if _, err := os.Stat(chat); !os.IsNotExist(err) {
		t.Errorf("test chat directory still there: %v", err)
	}
	for _, p := range []string{lane, outside} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("gc removed %s, which is not a test chat directory", p)
		}
	}
	if len(srv.workdirReports) != 1 {
		t.Fatalf("workdir reports = %d, want 1", len(srv.workdirReports))
	}
	rows := srv.workdirReports[0].Workdirs
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3 (one per named workdir, no live list)", rows)
	}
	got := rows[0]
	if got.ID != testChatID || got.Kind != "dir" || got.Path != chat || got.TestChatID != testChatID || got.SessionID != "" || got.Bytes != 0 || got.GC == nil || got.GC.Status != workdir.GCDeleted {
		t.Errorf("receipt row %+v, want {id, dir, path, test_chat_id, session_id \"\", bytes 0, gc deleted}", got)
	}
	for _, r := range rows[1:] {
		if r.GC == nil || r.GC.Status != workdir.GCRefused || r.GC.Reason == "" || r.TestChatID != testChatID {
			t.Errorf("non-testchat path row %+v, want refused + reason + test_chat_id", r)
		}
	}
	// The wire shape: the row must carry the keys the server reads
	// (httpapi.daemonWorkdirs — test_chat_id, gc.status) and omit session_id's
	// mandatory-ness by being empty.
	raw, _ := json.Marshal(got)
	for _, k := range []string{`"id":"` + testChatID, `"test_chat_id":"` + testChatID, `"gc":{"status":"deleted"`, `"bytes":0`, `"session_id":""`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("receipt JSON lacks %s: %s", k, raw)
		}
	}
}

// (g) start-up sweep: a test chat directory older than 24h goes, a fresh
// one and everything else stays; the daemon logs each removal.
func TestTestChatSweep24h(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := workdir.TestChatPath(root, "old-chat")
	fresh := workdir.TestChatPath(root, "fresh-chat")
	lane := filepath.Join(root, "sessions", "s", "l")
	for _, p := range []string{old, fresh, lane} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stale := now.Add(-25 * time.Hour)
	for _, p := range []string{old, filepath.Join(old, "f"), lane, filepath.Join(lane, "f")} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	removed := workdir.SweepTestChats(root, now, workdir.TestChatMaxAge)
	if len(removed) != 1 || removed[0] != old {
		t.Fatalf("removed %v, want only %s", removed, old)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh test chat removed: %v", err)
	}
	if _, err := os.Stat(lane); err != nil {
		t.Errorf("lane folder removed by the test chat sweep: %v", err)
	}
	// Through Run: the sweep happens before the first claim and is logged.
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	srv := &memServer{}
	d, _ := newDaemon(t, srv, acpfake.Script{})
	d.Cfg.WorkdirRoot = root
	d.Orphans.Root = root
	var logs []string
	d.Log = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return d.claims() >= 1 })
	cancel()
	<-done
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("Run did not sweep %s", old)
	}
	if countContains(logs, "testchat sweep: removed "+old) != 1 {
		t.Errorf("no sweep log line:\n%s", strings.Join(logs, "\n"))
	}
}
