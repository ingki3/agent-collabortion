// Command acpprobe runs the P0-a ACP spikes (PLAN.md §4 #1·2·3·4a) against
// real runtimes and writes JSONL logs + a summary JSON per run.
//
//	acpprobe -runtime claude -scenario spike1 -turns 30 -resumes 10 -cancels 10 -workdir /tmp/w -logdir plan/spikes/logs
//	acpprobe -runtime hermes -scenario hermes-loss -n 3
//
// Runtimes: claude → `npx -y @zed-industries/claude-code-acp`, hermes → `hermes acp`.
// Override with -cmd "prog arg arg".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ingki3/agent-collabortion/daemon/internal/acpprobe"
)

type opts struct {
	runtime  string
	cmd      string
	scenario string
	workdir  string
	logdir   string
	turns    int
	resumes  int
	cancels  int
	n        int
	model    string
	session  string
	timeout  time.Duration
}

var (
	rateLimitRe = regexp.MustCompile(`(?i)rate[ _-]?limit|usage limit|limit reached|hit your limit|out of (extra )?usage|\b429\b|overloaded|resets? (at|in) |· resets `)
	resetTimeRe = regexp.MustCompile(`(?i)reset[s]? (?:at |in )?([0-9]{1,2}(?::[0-9]{2})?\s*(?:am|pm)?(?:\s*\([^)]*\))?|[0-9]+ ?(?:min|hour|h|m)[a-z]*)`)
)

// ErrRateLimited aborts the run: PLAN says stop immediately, do not wait.
var ErrRateLimited = errors.New("rate_limited")

func main() {
	var o opts
	flag.StringVar(&o.runtime, "runtime", "claude", "claude | hermes")
	flag.StringVar(&o.cmd, "cmd", "", "override agent command line (space separated)")
	flag.StringVar(&o.scenario, "scenario", "smoke", "smoke | spike1 | spike2 | spike3 | spike4a | hermes-loss | hermes-smoke")
	flag.StringVar(&o.workdir, "workdir", "", "session cwd (default: temp dir)")
	flag.StringVar(&o.logdir, "logdir", "plan/spikes/logs", "where JSONL + summary go")
	flag.IntVar(&o.turns, "turns", 30, "spike1: number of prompt turns")
	flag.IntVar(&o.resumes, "resumes", 10, "spike1: number of process-restart + session/load")
	flag.IntVar(&o.cancels, "cancels", 10, "spike1: number of cancelled turns")
	flag.IntVar(&o.n, "n", 10, "spike4a / hermes-loss: repetitions")
	flag.StringVar(&o.session, "session", "", "spike1: continue an existing session id via session/load instead of session/new")
	flag.StringVar(&o.model, "model", "haiku", "model id substring to select via session/set_model (empty = runtime default)")
	flag.DurationVar(&o.timeout, "timeout", 4*time.Minute, "per-request timeout")
	flag.Parse()

	if o.workdir == "" {
		d, err := os.MkdirTemp("", "acpprobe-"+o.scenario+"-")
		if err != nil {
			fatal(err)
		}
		o.workdir = d
	}
	abs, _ := filepath.Abs(o.workdir)
	o.workdir = abs
	if err := os.MkdirAll(o.logdir, 0o755); err != nil {
		fatal(err)
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	base := filepath.Join(o.logdir, fmt.Sprintf("%s_%s_%s", o.scenario, o.runtime, ts))
	rec, err := acpprobe.NewRecorder(base + ".jsonl")
	if err != nil {
		fatal(err)
	}
	defer rec.Close()
	r := &runner{o: o, rec: rec, base: base, summary: map[string]any{
		"scenario": o.scenario, "runtime": o.runtime, "workdir": o.workdir, "started_at": ts,
		"model_requested": o.model, "turns_requested": o.turns, "resumes_requested": o.resumes, "cancels_requested": o.cancels, "n": o.n,
	}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runErr error
	switch o.scenario {
	case "smoke", "hermes-smoke":
		runErr = r.smoke(ctx)
	case "spike1":
		runErr = r.spike1(ctx)
	case "spike2":
		runErr = r.spike2(ctx)
	case "spike3":
		runErr = r.spike3(ctx)
	case "spike4a":
		runErr = r.spike4a(ctx)
	case "hermes-loss":
		runErr = r.hermesLoss(ctx)
	default:
		fatal(fmt.Errorf("unknown scenario %q", o.scenario))
	}
	r.summary["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	if runErr != nil {
		r.summary["error"] = runErr.Error()
		if errors.Is(runErr, ErrRateLimited) {
			r.summary["rate_limited"] = true
		}
	}
	r.closeAll()
	b, _ := json.MarshalIndent(r.summary, "", "  ")
	_ = os.WriteFile(base+".summary.json", b, 0o644)
	fmt.Println(string(b))
	fmt.Fprintln(os.Stderr, "log:", base+".jsonl")
	if runErr != nil {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "acpprobe:", err)
	os.Exit(2)
}

// runner holds per-run state shared by scenarios.
type runner struct {
	o       opts
	rec     *acpprobe.Recorder
	base    string
	summary map[string]any
	live    []*acpprobe.Client
	// aggregated across all processes of the run
	agg     acpprobe.Stats
	crashes int
}

func (r *runner) command() (string, []string) {
	if r.o.cmd != "" {
		f := strings.Fields(r.o.cmd)
		return f[0], f[1:]
	}
	switch r.o.runtime {
	case "claude":
		return "npx", []string{"-y", "@zed-industries/claude-code-acp"}
	case "hermes":
		return "hermes", []string{"acp"}
	}
	fatal(fmt.Errorf("unknown runtime %q", r.o.runtime))
	return "", nil
}

// spawn starts a fresh agent process and runs initialize.
func (r *runner) spawn(ctx context.Context, label string) (*acpprobe.Client, *acpprobe.InitializeResult, error) {
	cmd, args := r.command()
	c, err := acpprobe.Spawn(ctx, acpprobe.Config{
		Command:    cmd,
		Args:       args,
		Dir:        r.o.workdir,
		Recorder:   r.rec,
		StderrPath: r.base + ".stderr.txt",
		Label:      label,
		Env:        []string{"HERMES_YOLO_MODE=0"},
	})
	if err != nil {
		return nil, nil, err
	}
	r.live = append(r.live, c)
	ictx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	init, err := c.Initialize(ictx)
	if err != nil {
		r.absorb(c)
		_ = c.Close()
		return nil, init, err
	}
	if r.summary["agent_info"] == nil {
		r.summary["agent_info"] = init.AgentInfo
		r.summary["agent_capabilities"] = json.RawMessage(init.AgentCapabilities)
		r.summary["protocol_version"] = init.ProtocolVersion
	}
	return c, init, nil
}

// absorb folds a client's stats into the run aggregate and detects crashes.
func (r *runner) absorb(c *acpprobe.Client) {
	s := c.Stats
	r.agg.PermissionRequests += s.PermissionRequests
	r.agg.AllowOnceMissing += s.AllowOnceMissing
	r.agg.PermissionCancelled += s.PermissionCancelled
	r.agg.Updates += s.Updates
	r.agg.ToolCalls += s.ToolCalls
	r.agg.ToolCallUpdates += s.ToolCallUpdates
	r.agg.AgentMessageChunks += s.AgentMessageChunks
	r.agg.UnexpectedExit += s.UnexpectedExit
	r.agg.OtherClientRequests += s.OtherClientRequests
	if r.agg.OptionKindsSeen == nil {
		r.agg.OptionKindsSeen = map[string]int64{}
	}
	for k, v := range s.OptionKindsSeen {
		r.agg.OptionKindsSeen[k] += v
	}
}

// retire closes a process we are done with (planned exit, not a crash).
func (r *runner) retire(c *acpprobe.Client) {
	if exited, err := c.Exited(); exited {
		// died before we closed it
		r.crashes++
		r.rec.Note("crash_detected_at_retire", map[string]any{"error": fmt.Sprint(err)})
	}
	r.absorb(c)
	_ = c.Close()
	for i, l := range r.live {
		if l == c {
			r.live = append(r.live[:i], r.live[i+1:]...)
			break
		}
	}
}

func (r *runner) closeAll() {
	for _, c := range r.live {
		r.absorb(c)
		_ = c.Close()
	}
	r.live = nil
	r.summary["stats"] = r.agg
	r.summary["crashes"] = r.crashes
}

// newSession creates a session and applies the model selection.
func (r *runner) newSession(ctx context.Context, c *acpprobe.Client, meta map[string]any) (*acpprobe.SessionResult, error) {
	sctx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	s, err := c.NewSession(sctx, r.o.workdir, meta)
	if err != nil {
		return nil, err
	}
	if r.summary["models_available"] == nil && s.Models != nil {
		r.summary["models_available"] = s.Models.AvailableModels
		r.summary["model_default"] = s.Models.CurrentModelID
	}
	if r.o.model != "" && s.Models != nil {
		if id := pickModel(s.Models, r.o.model); id != "" && id != s.Models.CurrentModelID {
			if err := c.SetModel(sctx, s.SessionID, id); err != nil {
				r.rec.Note("set_model_failed", map[string]any{"model": id, "error": err.Error()})
			} else {
				r.summary["model_selected"] = id
			}
		} else if id == "" {
			r.rec.Note("model_not_found", map[string]any{"wanted": r.o.model})
		}
	}
	return s, nil
}

func pickModel(ms *acpprobe.ModelState, want string) string {
	want = strings.ToLower(want)
	for _, m := range ms.AvailableModels {
		if strings.Contains(strings.ToLower(m.ModelID), want) || strings.Contains(strings.ToLower(m.Name), want) {
			return m.ModelID
		}
	}
	return ""
}

// turn runs one prompt with the per-request timeout and rate-limit check.
func (r *runner) turn(ctx context.Context, c *acpprobe.Client, sid, prompt string) (*acpprobe.TurnResult, error) {
	tctx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	tr, err := c.Prompt(tctx, sid, prompt)
	if err != nil {
		if errors.Is(err, acpprobe.ErrProcessExited) {
			r.crashes++
		}
		if rateLimitRe.MatchString(err.Error()) {
			return tr, r.rateLimited(err.Error())
		}
		return tr, err
	}
	if rateLimitRe.MatchString(tr.Text) && (tr.StopReason == "refusal" || tr.ToolCalls == 0 && len(tr.Text) < 600) {
		return tr, r.rateLimited(tr.Text)
	}
	return tr, nil
}

func (r *runner) rateLimited(text string) error {
	reset := ""
	if m := resetTimeRe.FindStringSubmatch(text); len(m) > 1 {
		reset = m[1]
	}
	r.summary["rate_limited"] = true
	r.summary["rate_limit_text"] = text
	r.summary["rate_limit_reset"] = reset
	r.rec.Note("rate_limited", map[string]any{"text": text, "reset": reset})
	return fmt.Errorf("%w: reset %q: %s", ErrRateLimited, reset, firstLine(text))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (r *runner) logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05")+" "+format+"\n", a...)
}

// smoke: one process, one session, one turn.
func (r *runner) smoke(ctx context.Context) error {
	c, _, err := r.spawn(ctx, "smoke")
	if err != nil {
		return err
	}
	s, err := r.newSession(ctx, c, nil)
	if err != nil {
		return err
	}
	r.summary["session_id"] = s.SessionID
	r.summary["session_meta"] = json.RawMessage(s.Meta)
	tr, err := r.turn(ctx, c, s.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
	if err != nil {
		return err
	}
	r.summary["turn"] = tr
	r.retire(c)
	return nil
}
