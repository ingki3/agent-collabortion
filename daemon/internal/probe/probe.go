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
	// ColabBin is the colab CLI the attempts register as their MCP server
	// (config.ColabBin). Empty → "colab" on PATH.
	ColabBin string
	Clock    clock.Clock
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
	// §3 v0.5: colab_cli is a MACHINE property, reported once at the top
	// level — one binary serves every runtime, and a machine with zero
	// runtimes still has to report it.
	p.ColabCLI = Colab(ctx, o.ColabBin, o)
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
	// resume·usage·tool_disallow·protocol_version are LEFT ZERO here on
	// purpose: probe §9 advertises what one turn measured, not what the
	// runtime name suggests (backlog D-2). Without Turn they stay false and
	// PRD §8.2.6 degrades the UI accordingly, rather than promising a
	// capability nobody saw.
	cap := contracts.Capability{Kind: kind, Models: []string{}}
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
		// adapter_version is measured by the PONG turn (initialize →
		// agentInfo.version); never filled with the pin (PR #20 R3).
		cap.BriefTransport = contracts.BriefACPMetaSystemPrompt
		cap.LoggedIn = claudeLoggedIn()
	case contracts.RuntimeHermes:
		v, ok := CLIVersion(ctx, "hermes")
		if !ok {
			return cap, false
		}
		cap.Version = v
		cap.BriefTransport = contracts.BriefInstructionFile
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
		Task: contracts.BundleTask{ID: "probe", Attempt: 1, AgentName: "probe"},
		// Tools is a deliberate allow-list: it makes the harness send a
		// non-empty disallowedTools, which is the only way to MEASURE
		// tool_disallow instead of assuming it (backlog D-2). PONG uses no
		// tools, so restricting them costs the probe nothing.
		Profile: contracts.BundleProfile{RuntimeKind: kind, AdapterPin: acp.AdapterPin, Tools: ProbeToolAllowList},
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
	// §9 measured, never constant (backlog D-2).
	cap.ProtocolVersion = res.ProtocolVersion
	// §10 v0.8 tool_surface: what initialize advertised, not what the runtime
	// is called. Empty when initialize never answered — G5 (b) was exactly a
	// probe that went 11/11 green while the agent had no channel at all, so
	// an unmeasured surface stays unadvertised rather than assumed.
	cap.ToolSurface = res.ToolSurface
	cap.ToolDisallow = toolDisallowMeasured(res)
	// §9 supported_options (backlog D-5) is keyed on the MEASURED adapter
	// version, not the pin the profile asked for: advertising the pin's option
	// set for a version that actually answered would tell S10 to offer a
	// control the running adapter discards.
	cap.SupportedOptions = acp.SupportedOptions(kind, cap.AdapterVersion)
	// `resume` = the session advertised loadSession (PRD §8.2.1: judge by the
	// advertised value) AND a second process really loaded the session back.
	cap.Resume = res.Caps.LoadSession
	if cap.Resume && res.SessionRef != nil && res.SessionRef.SessionID != "" {
		cap.Resume = resumeCheck(tctx, kind, cmd, args, env, dir, res.SessionRef, acp.Meta(kind, acp.MetaOptions{Brief: b.Brief.Text, Tools: ProbeToolAllowList}), o)
	}
	return PongResult{Result: res, Models: models}
}

// ProbeToolAllowList is the profile allow-list the PONG turn runs under, so
// the harness derives a real disallowedTools set to measure against (§3, §8
// "disallowedTools 도출").
var ProbeToolAllowList = []string{"Read"}

// toolDisallowMeasured reports whether the disallowedTools we sent actually
// removed tools from the session (harness §3 "효과는 계약 테스트로 확인").
// The evidence is the raw system/init tool list, so this is a claude_code
// measurement; Hermes drops `_meta` entirely (§3) and therefore has no raw
// init and advertises tool_disallow=false — which is the true answer for it.
func toolDisallowMeasured(res acp.Result) bool {
	if res.RawInit == nil || len(res.RawInit.Tools) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, t := range res.RawInit.Tools {
		have[t] = true
	}
	denied := acp.DisallowedTools(ProbeToolAllowList)
	blocked := 0
	for _, d := range denied {
		if have[d] {
			return false // we asked for it to be gone and it is still there
		}
		blocked++
	}
	return blocked > 0
}

// resumeCheck measures probe §9 `resume` for real (backlog D-2): a SECOND
// process loads the session the PONG turn created (harness §6). No prompt is
// sent, so this costs no model turn. Hermes answers `null`, or a provenance
// whose acpSessionId differs, for a session it lost (§6, E8-03) — exactly
// the signal `resume` advertises; Claude Code answers "Session not found".
func resumeCheck(ctx context.Context, kind contracts.RuntimeKind, cmd string, args, env []string, dir string, ref *contracts.RuntimeSessionRef, meta map[string]any, o Options) bool {
	lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	c, err := acp.Spawn(lctx, acp.Config{Command: cmd, Args: args, Env: env, Dir: dir})
	if err != nil {
		logf(o, "probe %s: resume check spawn: %v", kind, err)
		return false
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Initialize(lctx, o.DaemonVersion); err != nil {
		logf(o, "probe %s: resume check initialize: %v", kind, err)
		return false
	}
	res, err := c.LoadSession(lctx, dir, ref.SessionID, nil, meta)
	if err != nil {
		logf(o, "probe %s: resume check session/load: %v", kind, err)
		return false
	}
	if kind == contracts.RuntimeHermes {
		if res == nil {
			logf(o, "probe %s: resume check session/load → null (session lost)", kind)
			return false
		}
		if sid, _, _, _, ok := res.HermesProvenance(); ok && sid != ref.SessionID {
			logf(o, "probe %s: resume check provenance %q != %q", kind, sid, ref.SessionID)
			return false
		}
	}
	return true
}

// Colab runs `<bin> --version` for daemon-protocol §3 `colab_cli` (backlog
// D-1). The agent reaches the platform ONLY through this binary — the MCP
// server the daemon registers (harness §2) and the shell path are the same
// executable — so its absence is advertised, never discovered as a silent
// tool failure mid-turn. A missing binary or a failing run is
// present=false / version="" — the reason is logged, never swallowed.
func Colab(ctx context.Context, bin string, o Options) contracts.ColabCLI {
	if bin == "" {
		bin = acp.DefaultColabBin
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		logf(o, "probe: colab CLI not found (%s): %v — MCP and shell paths are both unavailable", bin, err)
		return contracts.ColabCLI{}
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "--version").CombinedOutput()
	if err != nil {
		logf(o, "probe: colab --version failed (%s): %v", path, err)
		return contracts.ColabCLI{}
	}
	v := versionRe.FindString(string(out))
	if v == "" {
		logf(o, "probe: colab --version had no x.y.z (%q)", strings.TrimSpace(string(out)))
		return contracts.ColabCLI{}
	}
	return contracts.ColabCLI{Present: true, Version: v}
}

func logf(o Options, format string, args ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, args...))
	}
}

type captureSink struct{ events []contracts.TaskEvent }

func (s *captureSink) Emit(ev contracts.TaskEvent) { s.events = append(s.events, ev) }
func (s *captureSink) Preview(string)              {}
