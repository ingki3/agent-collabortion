// The PR #172 review's loop-level gap and its wire note (T-D10b).
//
//	NN3 — the §4.1 데몬 방어 gate measured THROUGH THE LOOP: a bundle whose
//	      workdir is not there must not reach the runtime at all. The unit
//	      that existed measured `workdirDetail(workdir.Verify(...))` — a pure
//	      function — so deleting the gate from `runAttempt` left the package
//	      green (reviewer injection 3).
//	NN2 — the path the daemon had to resolve or relocate leaves the machine:
//	      `d.Log` is stderr nobody watches, and a server that starts sending
//	      §4.1-violating paths again has to be visible from the feed.
package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// NN3 — nothing is spawned when the workdir is not there, and the attempt
// dies `failed / config` with the path in the text.
//
// The preparer is overridden because since D-21 no bundle can make
// `workdir.Prepare` return a path it did not create; the fault this gate
// exists for came from exactly such a preparer (T-I4 차단 ①: the checkout was
// made inside the user's repository and the runtime was handed a CWD-relative
// path that had never existed). What is measured is everything after it.
func TestMissingWorkdirNeverReachesTheRuntime(t *testing.T) {
	repo := initRepo(t)
	srv := &memServer{queue: []contracts.TaskBundle{relativePathBundle("t-nn3", repo)}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)

	missing := filepath.Join(root, "worktrees", "sess-slug", "backend")
	d.PrepareWorkdir = func(string, contracts.TaskBundle) (string, error) { return missing, nil }
	var mu sync.Mutex
	spawns := 0
	d.SpawnConfig = func(_ contracts.TaskBundle, wd string) acp.Config {
		mu.Lock()
		spawns++
		mu.Unlock()
		cmd, args, env := acpfake.Command(script, "")
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	// (1) the adapter was never built, so it was never started. This is the
	// assertion injection 3 has to break: with the gate gone the runner is
	// constructed and `acp.Config` is asked for.
	mu.Lock()
	got := spawns
	mu.Unlock()
	if got != 0 {
		t.Errorf("the runtime was spawned %d time(s) in a workdir that does not exist "+
			"(§4.1 v0.7.3 데몬 방어, D-21(c))", got)
	}
	// and nothing was created behind our back to make it true.
	if _, err := os.Stat(missing); err == nil {
		t.Errorf("%s exists — the fixture no longer measures a missing workdir", missing)
	}

	// (2) the attempt is `failed / config`, and the text names the path.
	srv.mu.Lock()
	fin := srv.finishes[0]
	srv.mu.Unlock()
	if fin.Outcome != "failed" || fin.FailureKind != contracts.FailConfig {
		t.Fatalf("finish = %s / %s (%s), want failed / config", fin.Outcome, fin.FailureKind, fin.StopReason)
	}
	if !strings.Contains(fin.StopReason, missing) {
		t.Errorf("stop_reason %q does not name the missing workdir %q — the message it "+
			"replaces (`spawn: fork/exec …/npx: no such file or directory`) blamed node", fin.StopReason, missing)
	}
	if strings.Contains(fin.StopReason, "npx") {
		t.Errorf("stop_reason %q blames the adapter binary", fin.StopReason)
	}

	// (3) the feed says the same thing: class=runtime · verb=error ·
	// failure_kind=config · detail (PRD §7 v0.16).
	ev := lastRuntimeEvent(t, srv, "error")
	if ev == nil {
		t.Fatal("no class=runtime verb=error event — the activity feed shows a lane that " +
			"stopped for no stated reason")
	}
	if ev.Outcome != "failed" || payloadString(ev, "failure_kind") != string(contracts.FailConfig) {
		t.Errorf("event = %s / %s, want failed / config", ev.Outcome, payloadString(ev, "failure_kind"))
	}
	if !strings.Contains(payloadString(ev, "detail"), missing) {
		t.Errorf("event detail %q does not name %q", payloadString(ev, "detail"), missing)
	}
	// (4) no runtime event may share a seq with another: (task_id, attempt,
	// seq) is the §4.2 idempotency key and the server keeps the first.
	assertSeqsUnique(t, srv)
}

// NN2 — the daemon says on the WIRE that it had to resolve or move the path.
//
// §4.1 v0.7.3 has the server send an absolute path under the workdir root, so
// every resolution the daemon performs is a disagreement between the two
// halves. Before this it existed only in the daemon's stderr: the G7 차단 ①
// regression (a relative `<session-slug>/<agent-slug>`) looked, from the
// platform, like a perfectly ordinary lane.
func TestResolvedWorkdirPathIsReportedToTheServer(t *testing.T) {
	// Both disagreements §4.1 lets the daemon fix on its own: a RELATIVE path
	// read against the root, and a target RELOCATED out of the user's
	// repository. Each has to appear on the wire.
	for _, tc := range []struct {
		name string
		// bundlePath is what the server sent; want is where it really went.
		bundlePath func(root, repo string) string
		want       func(root, repo string) string
	}{
		{
			"relative path read against the workdir root",
			func(_, _ string) string { return "sess-slug/backend" },
			func(root, _ string) string { return filepath.Join(root, "sess-slug", "backend") },
		},
		{
			"absolute path relocated out of the user's repository",
			func(_, repo string) string { return filepath.Join(repo, "sess-slug", "backend") },
			func(root, _ string) string { return filepath.Join(root, "worktrees", blockerSession, "backend") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initRepo(t)
			srv := &memServer{}
			script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
			d, root := newDaemon(t, srv, script)
			b := relativePathBundle("t-nn2", repo)
			b.Workdir.Path = tc.bundlePath(root, repo)
			srv.queue = []contracts.TaskBundle{b}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
			cancel()
			<-done

			ev := lastRuntimeEvent(t, srv, "report")
			if ev == nil {
				t.Fatalf("the daemon resolved %q itself and told nobody but its own stderr "+
					"(§4.1 v0.7.3, NN2)", b.Workdir.Path)
			}
			if ev.Outcome != "info" {
				t.Errorf("outcome = %q, want info — nothing failed, the daemon is reporting", ev.Outcome)
			}
			detail := payloadString(ev, "detail")
			// Both halves of "X → Y": what the server sent, and where it went.
			if !strings.Contains(detail, b.Workdir.Path) {
				t.Errorf("detail %q does not carry the bundle's own path %q", detail, b.Workdir.Path)
			}
			if want := tc.want(root, repo); !strings.Contains(detail, want) {
				t.Errorf("detail %q does not carry the path the runtime actually ran in (%s)", detail, want)
			}
			if !strings.Contains(detail, root) {
				t.Errorf("detail %q does not name this daemon's workdir root", detail)
			}

			// The payload stays inside the closed `runtime` set of
			// contracts/task_event.schema.json — an unknown key is stored and
			// read by nobody (S-41/S-52), the same silence NN2 is closing.
			allowed := map[string]bool{"runtime_kind": true, "adapter_version": true, "protocol_version": true,
				"session_id": true, "failure_kind": true, "detail": true, "not_before": true,
				"stop_reason": true, "resume_reason": true}
			for key := range ev.Payload {
				if !allowed[key] {
					t.Errorf("payload key %q is not in the schema's `runtime` set", key)
				}
			}
			assertSeqsUnique(t, srv)
		})
	}
}

// A bundle whose absolute path the daemon honours produces no §4.1 note: it
// is a disagreement report, not a per-attempt banner.
func TestAnHonouredWorkdirPathIsNotReported(t *testing.T) {
	repo := initRepo(t)
	b := relativePathBundle("t-nn2b", repo)
	srv := &memServer{}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	b.Workdir.Path = filepath.Join(root, "worktrees", blockerSession, "backend")
	srv.mu.Lock()
	srv.queue = []contracts.TaskBundle{b}
	srv.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	if ev := lastRuntimeEvent(t, srv, "report"); ev != nil {
		t.Errorf("the daemon reported a resolution it did not have to make: %q",
			payloadString(ev, "detail"))
	}
	assertSeqsUnique(t, srv)
}

// lastRuntimeEvent returns the newest class=runtime event with that verb.
func lastRuntimeEvent(t *testing.T, srv *memServer, verb string) *contracts.TaskEvent {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for i := len(srv.events) - 1; i >= 0; i-- {
		if srv.events[i].Class == "runtime" && srv.events[i].Verb == verb {
			ev := srv.events[i]
			return &ev
		}
	}
	return nil
}

func payloadString(ev *contracts.TaskEvent, key string) string {
	if ev == nil || ev.Payload == nil {
		return ""
	}
	s, _ := ev.Payload[key].(string)
	return s
}

// assertSeqsUnique is the §4.2 idempotency key, checked per (task, attempt):
// the loop now emits before the runner does, and two events sharing a seq
// means the server silently keeps one of them.
func assertSeqsUnique(t *testing.T, srv *memServer) {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	type k struct {
		task    string
		attempt int
		seq     int
	}
	seen := map[k]contracts.TaskEvent{}
	for _, e := range srv.events {
		key := k{e.TaskID, e.Attempt, e.Seq}
		if prev, dup := seen[key]; dup {
			t.Errorf("seq %d reused on %s.%d: %s/%s and %s/%s — §4.2 makes "+
				"(task_id, attempt, seq) idempotent, so the server keeps one",
				e.Seq, e.TaskID, e.Attempt, prev.Class, prev.Verb, e.Class, e.Verb)
		}
		seen[key] = e
	}
}
