package acp

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

	"github.com/ingki3/agent-collabortion/contracts"
)

// Client drives one ACP agent subprocess. One Client == one process == one
// task attempt (harness §2). Sessions live inside the process.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderrF *os.File
	stderr  *stderrTail

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan message
	writeMu sync.Mutex

	closed   chan struct{}
	exitErr  error
	exitOnce sync.Once
	killWait time.Duration

	hooksMu     sync.Mutex
	updateHooks []func(SessionUpdateParams)
	notifyHooks []func(method string, params json.RawMessage)

	// Permission answers session/request_permission. Nil → DefaultPolicy.
	Permission func(RequestPermissionParams) PermissionOutcome

	// UnexpectedExit counts a process death while requests were pending.
	UnexpectedExit atomic.Int64
	// OtherClientRequests counts fs/terminal requests we refused.
	OtherClientRequests atomic.Int64
}

// Config configures Spawn.
type Config struct {
	Command string
	Args    []string
	// Dir is the process working directory (= workdir).
	Dir string
	// Env is the COMPLETE environment (harness §2.1 allow-list, see Env()).
	// The user shell environment is never inherited.
	Env []string
	// StderrPath captures the agent's stderr (also kept in memory for
	// classification, harness §8 "Hermes 보조 신호"). Empty → memory only.
	StderrPath string
	// KillAfter is the SIGTERM → SIGKILL grace. Zero → contracts.KillAfterTerm.
	KillAfter time.Duration
}

// Spawn starts the agent in its own process group (pgid == pid).
func Spawn(ctx context.Context, cfg Config) (*Client, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		cmd:      cmd,
		stdin:    stdin,
		pending:  map[int64]chan message{},
		closed:   make(chan struct{}),
		killWait: cfg.KillAfter,
		stderr:   &stderrTail{},
	}
	if c.killWait == 0 {
		c.killWait = contracts.KillAfterTerm
	}
	var w io.Writer = c.stderr
	if cfg.StderrPath != "" {
		f, err := os.OpenFile(cfg.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		c.stderrF = f
		w = io.MultiWriter(f, c.stderr)
	}
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		if c.stderrF != nil {
			_ = c.stderrF.Close()
		}
		return nil, err
	}
	go c.readLoop(stdout)
	go c.waitLoop()
	return c, nil
}

// stderrTail keeps the last few KB of stderr for error classification.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 16<<10 {
		t.buf = t.buf[len(t.buf)-16<<10:]
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Stderr returns the captured stderr tail.
func (c *Client) Stderr() string { return c.stderr.String() }

// PID returns the child's pid (== pgid, since Setpgid).
func (c *Client) PID() int { return c.cmd.Process.Pid }

func killGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}

// Close terminates the process group: SIGTERM → KillAfter → SIGKILL (§2).
// Idempotent.
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
	case <-time.After(c.killWait):
		_ = killGroup(c.cmd, syscall.SIGKILL)
		<-c.closed
	}
	// The group may contain re-parented children (npx → node → claude).
	_ = killGroup(c.cmd, syscall.SIGKILL)
	if c.stderrF != nil {
		_ = c.stderrF.Close()
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

// Done is closed once the process has exited.
func (c *Client) Done() <-chan struct{} { return c.closed }

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
			c.UnexpectedExit.Add(1)
		}
		close(c.closed)
	})
}

// OnUpdate registers a hook called for each session/update notification.
func (c *Client) OnUpdate(fn func(SessionUpdateParams)) {
	c.hooksMu.Lock()
	c.updateHooks = append(c.updateHooks, fn)
	c.hooksMu.Unlock()
}

// OnNotification registers a hook for every notification (session/update and
// extension notifications such as _claude/sdkMessage).
func (c *Client) OnNotification(fn func(method string, params json.RawMessage)) {
	c.hooksMu.Lock()
	c.notifyHooks = append(c.notifyHooks, fn)
	c.hooksMu.Unlock()
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
			continue
		}
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

func (c *Client) handleNotification(m message) {
	c.hooksMu.Lock()
	nh := append([]func(string, json.RawMessage){}, c.notifyHooks...)
	uh := append([]func(SessionUpdateParams){}, c.updateHooks...)
	c.hooksMu.Unlock()
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
	for _, h := range uh {
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
		var out PermissionOutcome
		if c.Permission != nil {
			out = c.Permission(p)
		} else {
			out = DefaultPolicy{}.Decide(p).Outcome
		}
		c.respond(m.ID, RequestPermissionResult{Outcome: out})
	default:
		// fs/* and terminal/* — not advertised (§2); refuse.
		c.OtherClientRequests.Add(1)
		c.respondError(m.ID, -32601, "method not supported: "+m.Method)
	}
}

func (c *Client) respond(id *json.RawMessage, result any) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) respondError(id *json.RawMessage, code int, msg string) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": RPCError{Code: code, Message: msg}})
}

func (c *Client) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// ErrProcessExited is returned by Call when the agent died mid-request.
var ErrProcessExited = errors.New("acp agent process exited")

// Call sends a request and waits for its response (or ctx / process exit).
// RPC errors are wrapped so callers can errors.As into *RPCError.
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

// ErrProtocolVersion is returned by Initialize on a protocolVersion mismatch
// (harness §2 → failure_kind=config).
var ErrProtocolVersion = errors.New("acp protocol version mismatch")

// Initialize runs the handshake (§2) with no fs/terminal capabilities.
func (c *Client) Initialize(ctx context.Context, daemonVersion string) (*InitializeResult, error) {
	var res InitializeResult
	err := c.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion:    contracts.ACPProtocolVersion,
		ClientCapabilities: ClientCapabilities{},
		ClientInfo:         &Implementation{Name: "colab-daemon", Title: "Colab daemon", Version: daemonVersion},
	}, &res)
	if err != nil {
		return nil, err
	}
	if res.ProtocolVersion != contracts.ACPProtocolVersion {
		return &res, fmt.Errorf("%w: want %d got %d", ErrProtocolVersion, contracts.ACPProtocolVersion, res.ProtocolVersion)
	}
	return &res, nil
}

// NewSession is session/new. meta is the §3 _meta (nil for Hermes).
func (c *Client) NewSession(ctx context.Context, cwd string, mcp []any, meta map[string]any) (*SessionResult, error) {
	if mcp == nil {
		mcp = []any{}
	}
	var res SessionResult
	if err := c.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, MCPServers: mcp, Meta: meta}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// LoadSession is session/load; the agent replays history as session/update
// notifications before responding (the runner discards them, §6).
func (c *Client) LoadSession(ctx context.Context, cwd, sessionID string, mcp []any, meta map[string]any) (*SessionResult, error) {
	if mcp == nil {
		mcp = []any{}
	}
	var raw json.RawMessage
	if err := c.Call(ctx, MethodSessionLoad, LoadSessionParams{Cwd: cwd, SessionID: sessionID, MCPServers: mcp, Meta: meta}, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil // Hermes: unknown session → null result (§6)
	}
	var res SessionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("session/load: decode result: %w", err)
	}
	return &res, nil
}

// SetConfigOption is session/set_config_option (claude_code model, §1).
func (c *Client) SetConfigOption(ctx context.Context, sessionID, configID string, value any) (*SetConfigOptionResult, error) {
	var res SetConfigOptionResult
	if err := c.Call(ctx, MethodSessionSetConfigOption, SetConfigOptionParams{SessionID: sessionID, ConfigID: configID, Value: value}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SetModel is session/set_model (hermes, §1).
func (c *Client) SetModel(ctx context.Context, sessionID, modelID string) error {
	return c.Call(ctx, MethodSessionSetModel, SetModelParams{SessionID: sessionID, ModelID: modelID}, nil)
}

// Prompt sends session/prompt and blocks until the stop reason arrives.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (*PromptResult, error) {
	var res PromptResult
	if err := c.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: text}}}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Cancel sends session/cancel (a notification).
func (c *Client) Cancel(sessionID string) error {
	return c.Notify(MethodSessionCancel, CancelParams{SessionID: sessionID})
}
