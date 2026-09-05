package acpprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Client drives one ACP agent subprocess. One Client == one process. Sessions
// live inside the process; to "resume in a fresh process" (what the daemon
// will do after a crash/restart) create a new Client and call LoadSession /
// ResumeSession with the old session id.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *os.File
	rec    *Recorder
	policy PermissionPolicy

	nextID   atomic.Int64
	mu       sync.Mutex
	pending  map[int64]chan message
	writeMu  sync.Mutex
	closed   chan struct{}
	exitErr  error
	exitOnce sync.Once

	// Stats accumulates counters the spikes report.
	Stats Stats

	// updates receives every session/update (after it is recorded). Prompt
	// waits on it to observe activity; scenarios read Stats instead.
	updateHooksMu sync.Mutex
	updateHooks   []func(SessionUpdateParams)
	notifyHooks   []func(method string, params json.RawMessage)

	// cancelling marks a session id whose in-flight turn is being cancelled;
	// pending permission requests for it are answered "cancelled" (PRD §8.2.2).
	cancellingMu sync.Mutex
	cancelling   map[string]bool
}

// Stats are the raw numbers the spike reports use.
type Stats struct {
	PermissionRequests  int64
	AllowOnceMissing    int64 // request_permission with no kind=="allow_once" option → reject_once
	PermissionCancelled int64 // answered "cancelled" because a cancel was in flight
	Updates             int64
	ToolCalls           int64
	ToolCallUpdates     int64
	AgentMessageChunks  int64
	UnexpectedExit      int64 // process died while we still had pending requests
	OtherClientRequests int64 // fs/terminal requests we refused (we do not advertise them)
	OptionKindsSeen     map[string]int64
}

// Config configures Spawn.
type Config struct {
	// Command and Args, e.g. "npx", ["-y", "@zed-industries/claude-code-acp"]
	// or "hermes", ["acp"].
	Command string
	Args    []string
	// Dir is the subprocess working directory (session cwd is passed separately).
	Dir string
	// Env overrides (KEY=VALUE). The child inherits os.Environ() minus every
	// CLAUDE* variable — this probe itself may run inside a Claude Code
	// session, and a nested `claude` refuses to start if it sees CLAUDECODE=1.
	Env []string
	// Recorder receives every wire message as JSONL. Nil → no recording.
	Recorder *Recorder
	// StderrPath captures the agent's stderr. Empty → discarded.
	StderrPath string
	// Policy answers session/request_permission. Nil → DefaultPolicy.
	Policy PermissionPolicy
	// Label is written into the JSONL records (e.g. "claude", "hermes").
	Label string
}

// Spawn starts the agent in its own process group.
func Spawn(ctx context.Context, cfg Config) (*Client, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = childEnv(cfg.Env)
	// exec.CommandContext's default Cancel kills only the direct child; we
	// want the whole group on every exit path (PRD §8.2.2 process hygiene).
	cmd.Cancel = func() error { return killGroup(cmd, syscall.SIGKILL) }

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{
		cmd:        cmd,
		stdin:      stdin,
		rec:        cfg.Recorder,
		policy:     cfg.Policy,
		pending:    map[int64]chan message{},
		closed:     make(chan struct{}),
		cancelling: map[string]bool{},
	}
	c.Stats.OptionKindsSeen = map[string]int64{}
	if c.policy == nil {
		c.policy = DefaultPolicy{}
	}
	if cfg.StderrPath != "" {
		f, err := os.OpenFile(cfg.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		c.stderr = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.record("spawn", map[string]any{"pid": cmd.Process.Pid, "command": cfg.Command, "args": cfg.Args, "label": cfg.Label})
	go c.readLoop(stdout)
	go c.waitLoop()
	return c, nil
}

// PID returns the child's pid (== pgid, since Setpgid).
func (c *Client) PID() int { return c.cmd.Process.Pid }

func childEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if strings.HasPrefix(k, "CLAUDE") && k != "CLAUDE_CONFIG_DIR" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func killGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}

// Close terminates the process group: SIGTERM, 3s grace, SIGKILL. Idempotent.
func (c *Client) Close() error {
	select {
	case <-c.closed:
		return c.exitErr
	default:
	}
	_ = c.stdin.Close()
	_ = killGroup(c.cmd, syscall.SIGTERM)
	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		_ = killGroup(c.cmd, syscall.SIGKILL)
		<-c.closed
	}
	// Belt and braces: the group may have re-parented children (npx → node → claude).
	_ = killGroup(c.cmd, syscall.SIGKILL)
	if c.stderr != nil {
		_ = c.stderr.Close()
	}
	return c.exitErr
}

// Exited reports whether the process has exited and with what error.
func (c *Client) Exited() (bool, error) {
	select {
	case <-c.closed:
		return true, c.exitErr
	default:
		return false, nil
	}
}

func (c *Client) waitLoop() {
	err := c.cmd.Wait()
	c.exitOnce.Do(func() {
		c.exitErr = err
		c.mu.Lock()
		n := len(c.pending)
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if n > 0 {
			atomic.AddInt64(&c.Stats.UnexpectedExit, 1)
		}
		c.record("exit", map[string]any{"error": errString(err), "pending_requests": n})
		close(c.closed)
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// OnUpdate registers a hook called for each session/update notification.
func (c *Client) OnUpdate(fn func(SessionUpdateParams)) {
	c.updateHooksMu.Lock()
	c.updateHooks = append(c.updateHooks, fn)
	c.updateHooksMu.Unlock()
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			c.record("recv_garbage", map[string]any{"line": string(line)})
			continue
		}
		c.record("recv", json.RawMessage(append([]byte(nil), line...)))
		switch {
		case m.Method != "" && m.ID != nil:
			go c.handleRequest(m)
		case m.Method != "":
			c.handleNotification(m)
		case m.ID != nil:
			var id int64
			if err := json.Unmarshal(*m.ID, &id); err != nil {
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			if ok {
				delete(c.pending, id)
			}
			c.mu.Unlock()
			if ok {
				ch <- m
			}
		}
	}
}

// OnNotification registers a hook called for every notification (session/update
// and extension notifications such as _claude/sdkMessage).
func (c *Client) OnNotification(fn func(method string, params json.RawMessage)) {
	c.updateHooksMu.Lock()
	c.notifyHooks = append(c.notifyHooks, fn)
	c.updateHooksMu.Unlock()
}

func (c *Client) handleNotification(m message) {
	c.updateHooksMu.Lock()
	nh := append([]func(string, json.RawMessage){}, c.notifyHooks...)
	c.updateHooksMu.Unlock()
	for _, h := range nh {
		h(m.Method, m.Params)
	}
	if m.Method != MethodSessionUpdate {
		return
	}
	var p SessionUpdateParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	atomic.AddInt64(&c.Stats.Updates, 1)
	var k UpdateKind
	_ = json.Unmarshal(p.Update, &k)
	switch k.SessionUpdate {
	case "tool_call":
		atomic.AddInt64(&c.Stats.ToolCalls, 1)
	case "tool_call_update":
		atomic.AddInt64(&c.Stats.ToolCallUpdates, 1)
	case "agent_message_chunk":
		atomic.AddInt64(&c.Stats.AgentMessageChunks, 1)
	}
	c.updateHooksMu.Lock()
	hooks := append([]func(SessionUpdateParams){}, c.updateHooks...)
	c.updateHooksMu.Unlock()
	for _, h := range hooks {
		h(p)
	}
}

func (c *Client) handleRequest(m message) {
	switch m.Method {
	case MethodRequestPermission:
		var p RequestPermissionParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.respondError(m.ID, -32602, "bad params")
			return
		}
		atomic.AddInt64(&c.Stats.PermissionRequests, 1)
		c.mu.Lock()
		for _, o := range p.Options {
			c.Stats.OptionKindsSeen[o.Kind]++
		}
		c.mu.Unlock()
		c.cancellingMu.Lock()
		cancelling := c.cancelling[p.SessionID]
		c.cancellingMu.Unlock()
		var out PermissionOutcome
		if cancelling {
			out = PermissionOutcome{Outcome: "cancelled"}
			atomic.AddInt64(&c.Stats.PermissionCancelled, 1)
		} else {
			d := c.policy.Decide(p)
			out = d.Outcome
			if d.AllowOnceMissing {
				atomic.AddInt64(&c.Stats.AllowOnceMissing, 1)
			}
		}
		c.record("permission_decision", map[string]any{"sessionId": p.SessionID, "options": p.Options, "outcome": out, "toolCall": p.ToolCall})
		c.respond(m.ID, RequestPermissionResult{Outcome: out})
	default:
		// fs/* and terminal/* — we did not advertise them; refuse loudly so
		// the log shows if an adapter ignores clientCapabilities.
		atomic.AddInt64(&c.Stats.OtherClientRequests, 1)
		c.respondError(m.ID, -32601, "method not supported by probe: "+m.Method)
	}
}

func (c *Client) respond(id *json.RawMessage, result any) {
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) respondError(id *json.RawMessage, code int, msg string) {
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": RPCError{Code: code, Message: msg}})
}

func (c *Client) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.record("send", json.RawMessage(b))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// ErrProcessExited is returned by Call when the agent died mid-request.
var ErrProcessExited = errors.New("acp agent process exited")

// Call sends a request and waits for its response (or ctx / process exit).
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	ch := make(chan message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.send(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: write: %w", method, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: %w", method, ErrProcessExited)
		}
		if m.Error != nil {
			// %w so callers can errors.As into *RPCError and read Code / Data.
			return fmt.Errorf("%s: rpc error %d: %w", method, m.Error.Code, m.Error)
		}
		if result != nil && len(m.Result) > 0 && string(m.Result) != "null" {
			if err := json.Unmarshal(m.Result, result); err != nil {
				return fmt.Errorf("%s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.closed:
		return fmt.Errorf("%s: %w", method, ErrProcessExited)
	}
}

// Notify sends a notification (no id, no response).
func (c *Client) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// ---- typed helpers ----------------------------------------------------------

func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	var res InitializeResult
	err := c.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{},
		ClientInfo:         &Implementation{Name: "colab-acpprobe", Title: "Colab ACP probe", Version: "0.0.1"},
	}, &res)
	if err != nil {
		return nil, err
	}
	if res.ProtocolVersion != ProtocolVersion {
		return &res, fmt.Errorf("protocol version mismatch: want %d got %d", ProtocolVersion, res.ProtocolVersion)
	}
	return &res, nil
}

func (c *Client) NewSession(ctx context.Context, cwd string, meta map[string]any) (*SessionResult, error) {
	var res SessionResult
	err := c.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, MCPServers: []any{}, Meta: meta}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// LoadSession is the stable resume path (session/load): the agent replays
// history as session/update notifications before responding.
func (c *Client) LoadSession(ctx context.Context, cwd, sessionID string, meta map[string]any) (*SessionResult, error) {
	var res SessionResult
	err := c.Call(ctx, MethodSessionLoad, LoadSessionParams{Cwd: cwd, SessionID: sessionID, MCPServers: []any{}, Meta: meta}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// ResumeSession is the unstable session/resume (no replay in the spec draft;
// Hermes replays anyway and silently creates a new session if the id is unknown).
func (c *Client) ResumeSession(ctx context.Context, cwd, sessionID string, meta map[string]any) (*SessionResult, error) {
	var res SessionResult
	err := c.Call(ctx, MethodSessionResume, LoadSessionParams{Cwd: cwd, SessionID: sessionID, MCPServers: []any{}, Meta: meta}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) SetModel(ctx context.Context, sessionID, modelID string) error {
	return c.Call(ctx, MethodSessionSetModel, SetModelParams{SessionID: sessionID, ModelID: modelID}, nil)
}

// SetConfigOption is session/set_config_option (ACP 1.x). For configId
// "model", claude-agent-acp accepts aliases ("haiku") as well as option values.
func (c *Client) SetConfigOption(ctx context.Context, sessionID, configID string, value any) (*SetConfigOptionResult, error) {
	var res SetConfigOptionResult
	err := c.Call(ctx, MethodSessionSetConfigOption, SetConfigOptionParams{SessionID: sessionID, ConfigID: configID, Value: value}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// TurnResult is what Prompt returns: the stop reason plus the text the agent
// produced in this turn (agent_message_chunk text concatenated) and counts.
type TurnResult struct {
	StopReason  string
	Text        string
	ToolCalls   int
	Permissions int
	Duration    time.Duration
	// Models is _meta.quota.model_usage[].model from the prompt response (nil
	// when the adapter does not report it).
	Models []string
	// Meta is the raw prompt-response _meta.
	Meta json.RawMessage
	// TurnEndAt is when the session/prompt response arrived. For Hermes the
	// last chunk may land after it (PRD §8.2.5); Prompt drains for
	// DrainAfterTurn and counts any late chunks in LateChunks.
	LateChunks int
}

// DrainAfterTurn is how long Prompt keeps listening after the prompt response
// (PRD §8.2.5: 250ms static wait for Hermes late chunks).
var DrainAfterTurn = 250 * time.Millisecond

// Prompt runs one turn and blocks until the agent reports a stop reason.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (*TurnResult, error) {
	var (
		mu    sync.Mutex
		buf   strings.Builder
		tools int
		done  bool
		late  int
	)
	permBefore := atomic.LoadInt64(&c.Stats.PermissionRequests)
	c.OnUpdate(func(p SessionUpdateParams) {
		if p.SessionID != sessionID {
			return
		}
		var k UpdateKind
		if err := json.Unmarshal(p.Update, &k); err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if done {
			late++
		}
		switch k.SessionUpdate {
		case "agent_message_chunk":
			// content is an object here; for tool_call it is an array, so
			// decode it only on this branch.
			var u struct {
				Content struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(p.Update, &u); err == nil && u.Content.Type == "text" {
				buf.WriteString(u.Content.Text)
			}
		case "tool_call":
			tools++
		}
	})
	start := time.Now()
	c.record("turn_start", map[string]any{"sessionId": sessionID, "prompt": text})
	var res PromptResult
	err := c.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: text}}}, &res)
	mu.Lock()
	done = true
	mu.Unlock()
	if err != nil {
		c.record("turn_error", map[string]any{"sessionId": sessionID, "error": err.Error()})
		return nil, err
	}
	time.Sleep(DrainAfterTurn)
	mu.Lock()
	tr := &TurnResult{
		StopReason:  res.StopReason,
		Text:        buf.String(),
		ToolCalls:   tools,
		Permissions: int(atomic.LoadInt64(&c.Stats.PermissionRequests) - permBefore),
		Duration:    time.Since(start),
		LateChunks:  late,
		Models:      res.ModelsUsed(),
		Meta:        res.Meta,
	}
	mu.Unlock()
	// turn_end is not an ACP update kind — the session/prompt response is the
	// turn end. We synthesise a record so the log reads like the task_event stream.
	c.record("turn_end", map[string]any{"sessionId": sessionID, "stopReason": tr.StopReason, "toolCalls": tr.ToolCalls, "permissions": tr.Permissions, "lateChunks": tr.LateChunks, "ms": tr.Duration.Milliseconds(), "textLen": len(tr.Text), "models": tr.Models})
	c.cancellingMu.Lock()
	delete(c.cancelling, sessionID)
	c.cancellingMu.Unlock()
	return tr, nil
}

// Cancel marks the session cancelling (pending permission requests get
// "cancelled") and sends session/cancel. The in-flight Prompt then returns
// with stopReason "cancelled".
func (c *Client) Cancel(sessionID string) error {
	c.cancellingMu.Lock()
	c.cancelling[sessionID] = true
	c.cancellingMu.Unlock()
	return c.Notify(MethodSessionCancel, CancelParams{SessionID: sessionID})
}

// record writes a JSONL line if a recorder is attached.
func (c *Client) record(kind string, payload any) {
	if c.rec != nil {
		c.rec.Write(kind, payload)
	}
}
