// Package probe detects the runtimes on this machine and advertises their
// capabilities (harness §9, daemon-protocol §3): CLI presence and version,
// adapter pin, login state, models, and — when asked — one real "PONG" turn
// through the harness so resume/usage/protocol are measured, not assumed.
package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// Options configure Run.
type Options struct {
	DaemonVersion string
	Hostname      string
	WorkdirRoot   string
	// Turn runs the PONG turn per detected runtime (pairing, daily, probe
	// command). False → static detection only.
	Turn bool
	// Only restricts detection to these runtimes (empty = all).
	Only []contracts.RuntimeKind
	// AllowOnceMissing carries the §4 counter (≥3) from the running daemon.
	AllowOnceMissing map[contracts.RuntimeKind]bool
	// Timeout bounds one PONG turn. Zero → 4 minutes (npx cold start).
	Timeout time.Duration
	// Command overrides the adapter command (tests → acpfake). Nil → acp.Command.
	Command func(kind contracts.RuntimeKind) (string, []string, []string, bool)
	Clock   clock.Clock
	// Log receives progress lines (may be nil).
	Log func(string)
}

// PongPrompt is the one-turn capability check.
const PongPrompt = "Reply with exactly the single word PONG and nothing else. Do not use any tools."

// Run builds the probe body.
func Run(ctx context.Context, o Options) contracts.Probe {
	if o.Hostname == "" {
		o.Hostname, _ = os.Hostname()
	}
	p := contracts.Probe{DaemonVersion: o.DaemonVersion, Hostname: o.Hostname, WorkdirRoot: o.WorkdirRoot, Capabilities: []contracts.Capability{}, Repos: []contracts.Repo{}}
	for _, kind := range []contracts.RuntimeKind{contracts.RuntimeClaudeCode, contracts.RuntimeHermes} {
		if len(o.Only) > 0 && !contains(o.Only, kind) {
			continue
		}
		cap, ok := Detect(ctx, kind, o)
		if !ok {
			continue
		}
		p.Capabilities = append(p.Capabilities, cap)
	}
	if o.WorkdirRoot != "" {
		used, _ := workdir.DiskUsage(o.WorkdirRoot)
		p.Disk = contracts.Disk{UsedBytes: used}
	}
	return p
}

func contains(list []contracts.RuntimeKind, k contracts.RuntimeKind) bool {
	for _, x := range list {
		if x == k {
			return true
		}
	}
	return false
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// CLIVersion runs `<cli> --version` and extracts x.y.z.
func CLIVersion(ctx context.Context, cli string) (string, bool) {
	if _, err := exec.LookPath(cli); err != nil {
		return "", false
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, cli, "--version").CombinedOutput()
	if err != nil {
		return "", true
	}
	return versionRe.FindString(string(out)), true
}

// Detect inspects one runtime. ok=false when its CLI is absent.
func Detect(ctx context.Context, kind contracts.RuntimeKind, o Options) (contracts.Capability, bool) {
	cap := contracts.Capability{Kind: kind, Models: []string{}, ProtocolVersion: contracts.ACPProtocolVersion}
	switch kind {
	case contracts.RuntimeClaudeCode:
		v, ok := CLIVersion(ctx, "claude")
		if !ok {
			return cap, false
		}
		if _, err := exec.LookPath("npx"); err != nil {
			return cap, false
		}
		cap.Version = v
		cap.AdapterVersion = contracts.ClaudeAgentACPPin
		cap.BriefTransport = contracts.BriefACPMetaSystemPrompt
		cap.ToolDisallow = true
		cap.Resume, cap.Usage = true, true
		cap.LoggedIn = claudeLoggedIn()
	case contracts.RuntimeHermes:
		v, ok := CLIVersion(ctx, "hermes")
		if !ok {
			return cap, false
		}
		cap.Version = v
		cap.BriefTransport = contracts.BriefInstructionFile
		cap.Resume, cap.Usage = true, true // G1 F6, session/load
		cap.LoggedIn = hermesConfigured()
	default:
		return cap, false
	}
	cap.AllowOnceMissing = o.AllowOnceMissing[kind]
	if o.Turn {
		Pong(ctx, kind, o, &cap)
	}
	return cap, true
}

func claudeLoggedIn() bool {
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil && strings.Contains(string(b), `"oauthAccount"`) {
		return true
	}
	_, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	return err == nil
}

func hermesConfigured() bool {
	home, _ := os.UserHomeDir()
	_, err := os.Stat(filepath.Join(home, ".hermes"))
	return err == nil
}

// PongResult is what one PONG turn measured.
type PongResult struct {
	Result acp.Result
	Models []string
}

// Pong runs one turn through the harness and folds the measurements into cap.
func Pong(ctx context.Context, kind contracts.RuntimeKind, o Options, cap *contracts.Capability) PongResult {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 4 * time.Minute
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "colab-probe-")
	if err != nil {
		return PongResult{}
	}
	defer os.RemoveAll(dir)

	var cmd string
	var args, env []string
	if o.Command != nil {
		var ok bool
		cmd, args, env, ok = o.Command(kind)
		if !ok {
			return PongResult{}
		}
	} else {
		cmd, args = acp.Command(kind, "", nil)
		env = acp.Env(kind, acp.TaskEnv{ServerURL: "", AgentName: "probe"}, nil)
	}
	transport := contracts.BriefACPMetaSystemPrompt
	if kind == contracts.RuntimeHermes {
		transport = contracts.BriefInstructionFile
	}
	b := contracts.TaskBundle{
		Task:    contracts.BundleTask{ID: "probe", Attempt: 1, AgentName: "probe"},
		Profile: contracts.BundleProfile{RuntimeKind: kind, AdapterPin: contracts.ClaudeAgentACPPin},
		Brief:   contracts.BundleBrief{Transport: transport, Text: "You are a capability probe. Answer exactly as instructed."},
		Prompt:  PongPrompt,
		Limits:  contracts.BundleLimits{StallSeconds: 180},
	}
	var models []string
	sink := &captureSink{}
	r := acp.New(acp.Attempt{
		Bundle: b, Workdir: dir,
		Cmd:            acp.Config{Command: cmd, Args: args, Env: env, StderrPath: filepath.Join(dir, "stderr.txt")},
		Sink:           sink,
		Clock:          o.Clock,
		DaemonVersion:  o.DaemonVersion,
		SetupTimeout:   timeout,
		RawSDKMessages: kind == contracts.RuntimeClaudeCode,
	})
	res := r.Run(tctx)
	if o.Log != nil {
		o.Log(fmt.Sprintf("probe %s: outcome=%s stop=%s text=%q", kind, res.Outcome, res.StopReason, strings.TrimSpace(res.Text)))
	}
	if res.AdapterVersion != "" && kind == contracts.RuntimeClaudeCode {
		cap.AdapterVersion = res.AdapterVersion
	}
	switch {
	case res.Outcome == "completed":
		cap.LoggedIn = true
		cap.Usage = res.Usage.InputTokens > 0 || res.Usage.OutputTokens > 0 || !res.Usage.Estimated
	case res.Failure != nil && res.Failure.Kind == contracts.FailAuth:
		cap.LoggedIn = false
	}
	models = res.Models
	if len(res.AvailableModels) > 0 {
		cap.Models = res.AvailableModels
	} else if len(models) > 0 {
		cap.Models = models
	}
	if res.SessionRef != nil {
		cap.Resume = true
	}
	return PongResult{Result: res, Models: models}
}

type captureSink struct{ events []contracts.TaskEvent }

func (s *captureSink) Emit(ev contracts.TaskEvent) { s.events = append(s.events, ev) }
func (s *captureSink) Preview(string)              {}
