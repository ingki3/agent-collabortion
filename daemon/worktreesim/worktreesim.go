// Package worktreesim is a test-usable entry point into the daemon's
// crash-recovery behaviour under `worktree` isolation (FR-9.1, E11-05·06,
// P4_TASKS §3 "kill -9 뒤 이중 쓰기 0").
//
// WHY IT IS PUBLIC. The P4a simulator lives in the SERVER module
// (`server/test/sim/worktree_double_write_test.go`) and Go will not let that
// module import `daemon/internal/…`. The file itself names the way out: "the
// daemon side exposes a test-usable entry point (a public package, or a small
// `cmd` the wiring drives as a subprocess) and the wiring PR binds these hooks
// to THAT. Re-implementing pgid bookkeeping on the server to satisfy the hooks
// would make this simulator measure the simulator." Lead confirmed that
// reading on 2026-09-07. This is that package; `daemon/acpfake` (PR #121) is
// the precedent for a public test package in this module.
//
// WHAT IS REAL AND WHAT IS NOT.
//
//	real — the git repository and its worktrees; the writer processes, in
//	       their own process groups, appending to files in the checkout; the
//	       on-disk pgid records of daemon/internal/orphan; the sweep that
//	       kills them (SIGTERM → SIGKILL) and the OS-level liveness check;
//	       the HTTP round trip an orphan makes with its task token.
//	stood in — the ACP conversation (a writer process is not a runtime), and
//	       the SERVER's decision to re-queue and revoke (that is T-S9's
//	       `requeuedAttempt` hook; Requeue below only carries it out so the
//	       daemon-side rows can run before T-S9 lands).
//
// The property under test is an ORDER — kill orphans, then claim — and an
// order is only measurable while it is happening. Restart therefore samples
// live-writer counts DURING its own run and remembers the peak; Writers
// returns that peak, not just the count at the moment it is asked. Without
// that, swapping the two steps still leaves one writer alive by the time
// anyone looks, and the simulator would pass on a daemon that claims first.
package worktreesim

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// Lane is one lane running under `worktree` isolation.
type Lane struct {
	SessionID string
	AgentID   string
	TaskID    string
	Attempt   int
	// Path is the checkout the lane works in.
	Path string
	// Branch is `colab/<session>/<agent>`.
	Branch string
}

// SpawnRecord is the on-disk note FR-9.1 requires before a spawn.
type SpawnRecord struct {
	PGID      int
	TaskID    string
	Attempt   int
	Path      string
	WrittenAt int64
	// WrittenBeforeSpawn is the ordering FR-9.1 names. A record written after
	// the fork is useless for exactly the crash it exists to survive. It is
	// measured, not asserted: the writer process waits on a gate file and
	// stamps the moment it starts working, and this is true when the record
	// is older than that stamp.
	WrittenBeforeSpawn bool
}

// Harness owns one machine's worth of state: a repository, a workdir root,
// the orphan store, and the writer processes.
type Harness struct {
	Repo string
	Root string

	// KillGrace is the SIGTERM → SIGKILL wait of the sweep. Short in tests.
	KillGrace time.Duration

	mu      sync.Mutex
	store   orphan.Store
	writers map[string][]*writer // path → every writer ever started there
	revoked map[string]bool      // task token → revoked
	tokens  map[string]string    // task id.attempt → token
	peak    map[string]int       // path → peak live writers during the last restart
	srv     *httptest.Server
	lanes   map[string]Lane // task id → the lane as first dispatched
}

type writer struct {
	pgid    int
	cmd     *exec.Cmd
	dir     string // command inbox
	target  string
	gate    string
	stamp   string
	token   string
	taskID  string
	attempt int
}

// New builds a harness with a throwaway repository. The experiment target is
// NEVER the caller's own repository (P4_TASKS §0-18): dir should be a
// t.TempDir().
func New(dir string) (*Harness, error) {
	repo := filepath.Join(dir, "repo")
	root := filepath.Join(dir, "work")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return nil, err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "daemon@test"},
		{"config", "user.name", "daemon test"},
	} {
		if _, err := gitrepo.Run(repo, args...); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		return nil, err
	}
	if _, err := gitrepo.Run(repo, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := gitrepo.Run(repo, "commit", "-qm", "init"); err != nil {
		return nil, err
	}
	h := &Harness{
		Repo: repo, Root: root, KillGrace: 500 * time.Millisecond,
		store:   orphan.Store{Root: root, KillAfter: 500 * time.Millisecond},
		writers: map[string][]*writer{},
		revoked: map[string]bool{},
		tokens:  map[string]string{},
		peak:    map[string]int{},
		lanes:   map[string]Lane{},
	}
	// FR-9.1's last line of defence: a revoked task token is refused whether
	// or not daemon recovery got there first (E11-04). This is a real HTTP
	// round trip so the orphan's POST is answered by something other than our
	// own bookkeeping.
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		h.mu.Lock()
		bad := tok == "" || h.revoked[tok]
		h.mu.Unlock()
		if bad {
			http.Error(w, "token_revoked", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	return h, nil
}

// Close kills every writer and shuts the stand-in server down.
func (h *Harness) Close() {
	h.mu.Lock()
	all := h.writers
	h.writers = map[string][]*writer{}
	h.mu.Unlock()
	for _, ws := range all {
		for _, w := range ws {
			if orphan.Alive(w.pgid) {
				_ = syscall.Kill(-w.pgid, syscall.SIGKILL)
			}
			_ = w.cmd.Wait()
		}
	}
	if h.srv != nil {
		h.srv.Close()
	}
}

// StartAttempt prepares (or reuses) the agent's worktree, records the pgid,
// and spawns the writer — in that order, which is the order under test.
func (h *Harness) StartAttempt(sessionID, agentID, taskID string, attempt int) (Lane, SpawnRecord, error) {
	b := contracts.TaskBundle{
		Task:    contracts.BundleTask{ID: taskID, Attempt: attempt, SessionID: sessionID, AgentName: agentID},
		Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: h.Repo},
	}
	path, err := workdir.Prepare(h.Root, b)
	if err != nil {
		return Lane{}, SpawnRecord{}, err
	}
	lane := Lane{
		SessionID: sessionID, AgentID: agentID, TaskID: taskID, Attempt: attempt,
		Path: path, Branch: workdir.WorktreeBranch(sessionID, agentID),
	}
	w, err := h.spawnWriter(taskID, attempt, path)
	if err != nil {
		return Lane{}, SpawnRecord{}, err
	}
	// daemon-protocol §5: <workdir_root>/.colab/attempts/<task>.<attempt>.json
	// — written while the writer is still parked at the gate, which is the
	// daemon's OnSpawn callback in loop.go.
	rec := orphan.Record{TaskID: taskID, Attempt: attempt, PGID: w.pgid, StartedAt: time.Now().UTC(), Workdir: path}
	if err := h.store.Record(rec); err != nil {
		return Lane{}, SpawnRecord{}, err
	}
	recAt := statMTime(h.recordPath(taskID, attempt))
	// Open the gate: only now does the writer do any work.
	if err := os.WriteFile(w.gate, []byte("go\n"), 0o644); err != nil {
		return Lane{}, SpawnRecord{}, err
	}
	if err := waitFile(w.stamp, 5*time.Second); err != nil {
		return Lane{}, SpawnRecord{}, fmt.Errorf("writer never started: %w", err)
	}
	h.mu.Lock()
	h.lanes[taskID] = lane
	h.mu.Unlock()
	return lane, SpawnRecord{
		PGID: w.pgid, TaskID: taskID, Attempt: attempt, Path: path,
		WrittenAt:          recAt,
		WrittenBeforeSpawn: recAt > 0 && recAt <= statMTime(w.stamp),
	}, nil
}

func (h *Harness) recordPath(taskID string, attempt int) string {
	return filepath.Join(h.Root, ".colab", "attempts", fmt.Sprintf("%s.%d.json", taskID, attempt))
}

// writerScript is the surviving process. It is a real program in its own
// process group editing a real file in the checkout: killing the daemon does
// not stop it, and only a signal to its group does.
//
// IT READS BEFORE IT WRITES. That is not an optimisation, it is the property
// E8-04 (4) names. A retried attempt cannot remember what attempt 1 did, and
// no idempotency key can de-duplicate a file write; the only mechanism the
// PRD gives is the resume prompt's "workdir의 현재 상태를 먼저 확인하라"
// (FR-7.1, §8.4 `<resumed>`, PRD.md:1162). So the writer here behaves like an
// agent that obeys that line: it greps the checkout for the edit it is about
// to make and, if the edit is already there, leaves it alone. The edit id is
// deterministic per attempt — attempt 2 redoing `<edit-1>` produces the same
// marker attempt 1 wrote — which is what makes the inspection able to answer.
//
// Edits it did not make are none of its business: it never rewrites the file,
// only appends to it, so the orphan's `<orphan>` line survives untouched
// (E11-06 — a writer that "tidied" the checkout would delete the work
// recovery exists to preserve).
//
// It reports which of the two it did, so the caller's byte count is the truth
// about the file rather than an assumption about what a write does.
//
// It also carries its own deadline: a test binary that dies without calling
// Close would otherwise leave a shell loop behind in its own process group,
// where nothing reaps it. Ten minutes is far longer than any row here and far
// shorter than "until the machine reboots".
const writerScript = `
n=0
while [ ! -f "$GATE" ]; do
  sleep 0.01
  n=$((n+1))
  [ $n -gt 60000 ] && exit 0
done
: > "$STAMP"
n=0
while true; do
  n=$((n+1))
  [ $n -gt 60000 ] && exit 0
  for f in "$INBOX"/*.cmd; do
    [ -e "$f" ] || continue
    m=$(cat "$f")
    d="${f%.cmd}.done"
    if [ -f "$TARGET" ] && grep -qF -- "$m" "$TARGET"; then
      printf 'skipped\n' > "$d.part"
    else
      printf '%s\n' "$m" >> "$TARGET"
      printf 'wrote\n' > "$d.part"
    fi
    rm -f "$f"
    mv "$d.part" "$d"
  done
  sleep 0.01
done
`

func (h *Harness) spawnWriter(taskID string, attempt int, path string) (*writer, error) {
	base := filepath.Join(h.Root, ".colab", "sim", fmt.Sprintf("%s.%d", taskID, attempt))
	inbox := filepath.Join(base, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return nil, err
	}
	// The file the "agent" edits lives INSIDE the checkout — that is the
	// whole point: a second writer here corrupts the user's repository.
	target := filepath.Join(path, "AGENT_WORK.md")
	w := &writer{
		dir: inbox, target: target,
		gate:   filepath.Join(base, "gate"),
		stamp:  filepath.Join(base, "started"),
		token:  fmt.Sprintf("ctk_%s_%d", taskID, attempt),
		taskID: taskID, attempt: attempt,
	}
	cmd := exec.Command("/bin/sh", "-c", writerScript)
	cmd.Dir = path
	cmd.Env = append(os.Environ(),
		"GATE="+w.gate, "STAMP="+w.stamp, "INBOX="+inbox, "TARGET="+target)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}
	w.pgid, w.cmd = pgid, cmd
	h.mu.Lock()
	h.writers[path] = append(h.writers[path], w)
	h.tokens[fmt.Sprintf("%s.%d", taskID, attempt)] = w.token
	h.mu.Unlock()
	return w, nil
}

// KillDaemon is SIGKILL on the daemon: it takes no cleanup path, so the
// writer process group survives and keeps its hold on the checkout. Returns
// the pgid still running (0 if none, which would mean the scenario is not
// being exercised at all).
func (h *Harness) KillDaemon() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	// The daemon's in-memory state is gone; the on-disk records are not.
	// That asymmetry is what recovery relies on, so it is modelled by simply
	// forgetting nothing on disk and touching no process.
	for _, ws := range h.writers {
		for _, w := range ws {
			if orphan.Alive(w.pgid) {
				return w.pgid
			}
		}
	}
	return 0
}

// Requeue is the SERVER's step, stood in for so the daemon-side rows can run
// before T-S9 wires `requeuedAttempt`: the heartbeat sweep re-queues the task
// (attempt + 1, same workdir under FR-6.4/C3) and revokes the previous
// attempt's token.
func (h *Harness) Requeue(taskID string) (Lane, bool) {
	h.mu.Lock()
	lane, ok := h.lanes[taskID]
	if !ok {
		h.mu.Unlock()
		return Lane{}, false
	}
	tok := h.tokens[fmt.Sprintf("%s.%d", taskID, lane.Attempt)]
	if tok != "" {
		h.revoked[tok] = true
	}
	next := lane
	next.Attempt = lane.Attempt + 1
	h.lanes[taskID] = next
	h.mu.Unlock()
	return next, tok != ""
}

// Write is the runtime side: the process that holds the checkout makes the
// edit named by marker, and then tries to post with its task token. It
// returns how many bytes landed IN THE FILE and the HTTP status the post got.
//
// "How many bytes landed" is measured, not assumed. The writer inspects the
// workdir first (see writerScript) and an edit that is already there is not
// made again, so a re-applied edit reports 0 bytes — which is the honest
// answer for a file that did not change, and the shape E8-04 (4) asks about:
// after a retry each edit is present exactly once, not once per attempt.
//
// The write is dispatched to the LIVE writer for that path — if the orphan is
// still up it is the orphan's write, and after recovery it is the new
// attempt's. A path with no live writer is a lane nobody is running, and the
// call reports 0 bytes rather than pretending.
func (h *Harness) Write(path, marker string) (int, int) {
	w := h.liveWriter(path)
	if w == nil {
		return 0, 0
	}
	n, ok := h.dispatch(w, marker)
	if !ok {
		return 0, 0
	}
	return n, h.post(w.token)
}

// WriteAll dispatches marker to EVERY live writer holding path AT ONCE, and
// returns the bytes each of them reported, newest writer first.
//
// It exists for one observation and is not part of any golden row: while the
// orphan is still up and the restarted attempt is already writing, do two
// agents both obeying "inspect the workdir first" still leave the edit exactly
// once? Write cannot ask that — it always picks a single writer. E11-05 says
// this window is never open in a correct daemon, so what this measures is what
// would happen if it were.
func (h *Harness) WriteAll(path, marker string) []int {
	h.mu.Lock()
	ws := append([]*writer(nil), h.writers[path]...)
	h.mu.Unlock()
	live := make([]*writer, 0, len(ws))
	for i := len(ws) - 1; i >= 0; i-- {
		if orphan.Alive(ws[i].pgid) {
			live = append(live, ws[i])
		}
	}
	out := make([]int, len(live))
	var wg sync.WaitGroup
	for i, w := range live {
		wg.Add(1)
		go func(i int, w *writer) {
			defer wg.Done()
			n, ok := h.dispatch(w, marker)
			if !ok {
				n = 0
			}
			out[i] = n
		}(i, w)
	}
	wg.Wait()
	return out
}

// dispatch hands one edit to one writer and waits for that writer's own
// account of what it did. The reply is the writer's, not ours: a harness that
// assumed "a dispatched edit is an appended edit" would report bytes for a
// skipped write and hide the very thing being counted.
func (h *Harness) dispatch(w *writer, marker string) (int, bool) {
	name := fmt.Sprintf("%d-%d", time.Now().UnixNano(), cmdSeq.Add(1))
	if err := os.WriteFile(filepath.Join(w.dir, name+".cmd"), []byte(marker), 0o644); err != nil {
		return 0, false
	}
	done := filepath.Join(w.dir, name+".done")
	if err := waitFile(done, 5*time.Second); err != nil {
		return 0, false
	}
	b, err := os.ReadFile(done)
	_ = os.Remove(done)
	if err != nil {
		return 0, false
	}
	if strings.TrimSpace(string(b)) != "wrote" {
		return 0, true
	}
	return len(marker) + 1, true
}

// cmdSeq keeps two writers dispatched in the same nanosecond from colliding on
// an inbox file name.
var cmdSeq atomic.Int64

func (h *Harness) post(token string) int {
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/messages", nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *Harness) liveWriter(path string) *writer {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.writers[path]
	for i := len(ws) - 1; i >= 0; i-- {
		if orphan.Alive(ws[i].pgid) {
			return ws[i]
		}
	}
	return nil
}

// Restart brings the daemon back. The steps are returned in the order they
// were performed, because "before" is the entire property: a bare boolean
// cannot distinguish "killed then claimed" from "claimed then killed".
//
// While it runs it samples the number of live writers per path and remembers
// the peak (see the package comment).
func (h *Harness) Restart() ([]string, int, error) {
	stop := h.samplePeak()
	defer stop()
	var steps []string

	// E11-05: the sweep runs BEFORE the first claim. daemon/cmd/daemon and
	// loop.Run do it in this order; this is the same call.
	swept, err := h.store.Sweep()
	if err != nil {
		return nil, 0, err
	}
	killed := 0
	for _, s := range swept {
		if s.Alive {
			killed++
		}
	}
	steps = append(steps, "kill_orphans")

	// claim: the re-queued attempt is dispatched to this daemon and its
	// runtime is spawned into the SAME checkout (FR-6.4/C3).
	h.mu.Lock()
	lanes := make([]Lane, 0, len(h.lanes))
	for _, l := range h.lanes {
		lanes = append(lanes, l)
	}
	h.mu.Unlock()
	for _, l := range lanes {
		if l.Attempt < 2 {
			continue
		}
		if h.liveWriter(l.Path) != nil {
			continue
		}
		w, err := h.spawnWriter(l.TaskID, l.Attempt, l.Path)
		if err != nil {
			return nil, 0, err
		}
		if err := os.WriteFile(w.gate, []byte("go\n"), 0o644); err != nil {
			return nil, 0, err
		}
		rec := orphan.Record{TaskID: l.TaskID, Attempt: l.Attempt, PGID: w.pgid, StartedAt: time.Now().UTC(), Workdir: l.Path}
		if err := h.store.Record(rec); err != nil {
			return nil, 0, err
		}
		if err := waitFile(w.stamp, 5*time.Second); err != nil {
			return nil, 0, err
		}
	}
	steps = append(steps, "claim")
	return steps, killed, nil
}

// RestartClaimFirst is the REGRESSION INJECTION for E11-05: the same two
// steps in the wrong order. It exists so the simulator can be shown to fail
// on a daemon that claims before it cleans, which is the only way to know it
// would catch one.
func (h *Harness) RestartClaimFirst() ([]string, int, error) {
	stop := h.samplePeak()
	defer stop()
	h.mu.Lock()
	lanes := make([]Lane, 0, len(h.lanes))
	for _, l := range h.lanes {
		lanes = append(lanes, l)
	}
	h.mu.Unlock()
	for _, l := range lanes {
		if l.Attempt < 2 {
			continue
		}
		w, err := h.spawnWriter(l.TaskID, l.Attempt, l.Path)
		if err != nil {
			return nil, 0, err
		}
		if err := os.WriteFile(w.gate, []byte("go\n"), 0o644); err != nil {
			return nil, 0, err
		}
		if err := waitFile(w.stamp, 5*time.Second); err != nil {
			return nil, 0, err
		}
	}
	// The window is open here — two live writers in one checkout.
	time.Sleep(50 * time.Millisecond)
	swept, err := h.store.Sweep()
	if err != nil {
		return nil, 0, err
	}
	killed := 0
	for _, s := range swept {
		if s.Alive {
			killed++
		}
	}
	return []string{"claim", "kill_orphans"}, killed, nil
}

// samplePeak polls OS-level liveness while a restart runs and records, per
// path, the largest number of live writers it ever saw.
func (h *Harness) samplePeak() func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	h.mu.Lock()
	h.peak = map[string]int{}
	h.mu.Unlock()
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				h.sample()
				return
			case <-tick.C:
				h.sample()
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

func (h *Harness) sample() {
	h.mu.Lock()
	paths := make([]string, 0, len(h.writers))
	for p := range h.writers {
		paths = append(paths, p)
	}
	h.mu.Unlock()
	for _, p := range paths {
		n := h.liveNow(p)
		h.mu.Lock()
		if n > h.peak[p] {
			h.peak[p] = n
		}
		h.mu.Unlock()
	}
}

func (h *Harness) liveNow(path string) int {
	h.mu.Lock()
	ws := append([]*writer(nil), h.writers[path]...)
	h.mu.Unlock()
	n := 0
	for _, w := range ws {
		if orphan.Alive(w.pgid) {
			n++
		}
	}
	return n
}

// Writers is how many live processes hold the given checkout — measured from
// the OS (process-group liveness), never from our own bookkeeping, and taken
// as the peak over the last restart so the ORDER is what is being measured
// and not the tidy state afterwards.
func (h *Harness) Writers(path string) int {
	now := h.liveNow(path)
	h.mu.Lock()
	peak := h.peak[path]
	h.mu.Unlock()
	if peak > now {
		return peak
	}
	return now
}

// MarkerCount is how many times marker appears in the checkout's work file.
// Two occurrences of one edit is a duplicated write, and no idempotency key
// can undo a file write.
func (h *Harness) MarkerCount(path, marker string) int {
	b, err := os.ReadFile(filepath.Join(path, "AGENT_WORK.md"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), marker)
}

func waitFile(p string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", p)
}

func statMTime(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixNano()
}
