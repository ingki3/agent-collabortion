// Package acpfake is a scripted fake ACP agent for the harness contract tests
// (contracts/harness.md §11, §12 (a)(b)(c)). It runs as a subprocess (the
// test binary re-executed with ACPFAKE=1, see MaybeMain) so process-group
// semantics — pgid, SIGTERM/SIGKILL, orphan cleanup — are exercised for real.
//
// The Script is JSON in ACPFAKE_SCRIPT. Every request the fake receives is
// appended to ACPFAKE_RECORD as JSONL so tests can assert what the harness
// sent (e.g. `_meta` present exactly once, model set after load).
package acpfake

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// Aliases for the harness types that appear in this package's PUBLIC API.
//
// acpfake lives outside `internal/` (backlog D-9) so the server module's
// partial-execution simulator (`server/test/sim`, EVAL E8-04·05) can drive a
// real ACP peer instead of a hand-rolled stand-in. Go's internal rule is
// judged on the IMPORTER's path, so this package may keep importing
// `daemon/internal/harness/acp` — but a caller outside `daemon/` cannot name
// `acp.RPCError` to fill in a Script. A type ALIAS is the same type, and
// naming it through this package requires no import of the internal one, so
// the API stays byte-identical for existing callers while becoming reachable
// from the other module.
type (
	// RPCError is a JSON-RPC error the fake replies with (Turn.Error).
	RPCError = acp.RPCError
	// PromptUsage is the `session/prompt` response `usage` (Turn.Usage).
	PromptUsage = acp.PromptUsage
	// PlanEntry is one `plan` update entry (Step.Plan).
	PlanEntry = acp.PlanEntry
	// RateLimitMeta is `_meta["_claude/rateLimit"]` (UsageStep.RateLimit).
	RateLimitMeta = acp.RateLimitMeta
	// MCPServer is one `mcpServers` entry as the fake received it.
	MCPServer = acp.MCPServer
)

// AdapterPin is the pinned claude_code adapter version the fake reports by
// default (harness §1) — re-exported so a caller outside `daemon/` can assert
// on it without reaching into the harness package.
const AdapterPin = acp.AdapterPin

// Script drives the fake.
type Script struct {
	ProtocolVersion int `json:"protocol_version,omitempty"` // 0 → 1
	// AgentVersion is initialize.agentInfo.version. "" → acp.AdapterPin
	// (the runner rejects a claude_code adapter whose version ≠ pin, §8 config).
	AgentVersion string `json:"agent_version,omitempty"`
	// Kind: "claude" answers session/set_config_option and reports
	// configOptions; "hermes" answers session/set_model, returns null on
	// unknown session/load and _meta.hermes.sessionProvenance.
	Kind         string   `json:"kind,omitempty"`
	SessionID    string   `json:"session_id,omitempty"` // default "sess-1"
	DefaultModel string   `json:"default_model,omitempty"`
	Models       []string `json:"models,omitempty"`
	// KnownSessions succeed on session/load; others → claude: the error shape
	// LoadErrorKind picks, hermes: null.
	KnownSessions []string `json:"known_sessions,omitempty"`
	// LoadErrorKind selects the claude_code `session/load` error for an unknown
	// session. "" (default) is what the real adapter 0.74.0 answers —
	// -32002 "Resource not found: <id>" (spike 4c). "legacy" is the older
	// -32000 "Session not found" that harness §6 used to quote; both must be
	// read as resume_rejected.
	LoadErrorKind string `json:"load_error_kind,omitempty"`
	// LoadNoProvenance makes a hermes `session/load` on a KNOWN session answer
	// a bare `{}` — no `_meta.hermes.sessionProvenance`. That is what Hermes
	// 0.20.6 answers for a session deleted from `~/.hermes/state.db`
	// (spike 4c): the result is not null, so §6 (a) does not catch it.
	LoadNoProvenance bool `json:"load_no_provenance,omitempty"`
	// NoLoadSession drops agentCapabilities.loadSession (probe §9 `resume`
	// is the advertised value, PRD §8.2.1).
	NoLoadSession bool `json:"no_load_session,omitempty"`
	// MCPHTTP/MCPSSE advertise agentCapabilities.mcpCapabilities (§8.2.3
	// filter). stdio is the baseline and never advertised.
	MCPHTTP bool `json:"mcp_http,omitempty"`
	MCPSSE  bool `json:"mcp_sse,omitempty"`
	// NoMCPCapabilities drops the `mcpCapabilities` KEY from initialize —
	// the real Hermes shape (G5): mcpServers are then accepted on the wire
	// and silently ignored, which is harness §10 tool_surface=cli_wrapper.
	// The fake used to always advertise the key and to honour mcpServers,
	// i.e. it shared the implementation's assumption, so the blocker was
	// invisible until e2e. Turning this on makes the fake tell the truth.
	NoMCPCapabilities bool `json:"no_mcp_capabilities,omitempty"`
	// LoadProvenance is returned in _meta.hermes.sessionProvenance on load
	// (hermes). Nil → provenance echoing the requested id.
	LoadProvenance *Provenance `json:"load_provenance,omitempty"`
	ReplayChunks   int         `json:"replay_chunks,omitempty"`
	Turns          []Turn      `json:"turns,omitempty"`
	// ExitOnStdinEOF: default true. False keeps the process alive after stdin
	// closes (SIGTERM/SIGKILL path).
	IgnoreSigterm bool `json:"ignore_sigterm,omitempty"`
	StayAlive     bool `json:"stay_alive,omitempty"`
}

type Provenance struct {
	ACPSessionID        string `json:"acpSessionId"`
	RootHermesSessionID string `json:"rootHermesSessionId"`
	SessionKind         string `json:"sessionKind,omitempty"`
	CompressionDepth    int    `json:"compressionDepth,omitempty"`
}

// Turn scripts one session/prompt. The last turn repeats.
type Turn struct {
	Steps      []Step        `json:"steps,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"` // default end_turn
	Error      *acp.RPCError `json:"error,omitempty"`       // respond with error
	ModelUsage bool          `json:"model_usage,omitempty"` // _meta.quota.model_usage = current model
	// ReportModel overrides the model reported in model_usage (drift test).
	ReportModel string           `json:"report_model,omitempty"`
	Usage       *acp.PromptUsage `json:"usage,omitempty"`
	// LateChunk is sent LateDelayMs after the prompt response (Hermes, §2.2).
	LateChunk   string `json:"late_chunk,omitempty"`
	LateDelayMs int    `json:"late_delay_ms,omitempty"`
}

type Step struct {
	Chunk      string          `json:"chunk,omitempty"`
	Thought    string          `json:"thought,omitempty"`
	SleepMs    int             `json:"sleep_ms,omitempty"`
	ToolCall   *ToolCallStep   `json:"tool_call,omitempty"`
	ToolUpdate *ToolUpdateStep `json:"tool_update,omitempty"`
	Permission *PermissionStep `json:"permission,omitempty"`
	Usage      *UsageStep      `json:"usage,omitempty"`
	Plan       []acp.PlanEntry `json:"plan,omitempty"`
	// EchoBrief emits the last received _meta.systemPrompt.append as a chunk
	// (§12 (a)); EchoModel emits the current model (§12 (b)).
	EchoBrief bool `json:"echo_brief,omitempty"`
	EchoModel bool `json:"echo_model,omitempty"`
	// Hang blocks until session/cancel arrives (stall test); the turn then
	// ends with stopReason cancelled. HangForever never answers at all.
	Hang        bool `json:"hang,omitempty"`
	HangForever bool `json:"hang_forever,omitempty"`
}

type ToolCallStep struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Status  string `json:"status,omitempty"` // default pending
}

type ToolUpdateStep struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"` // completed | failed
	OldText  *string `json:"old_text,omitempty"`
	NewText  string  `json:"new_text,omitempty"`
	Path     string  `json:"path,omitempty"`
	ExitCode *int    `json:"exit_code,omitempty"`
	Text     string  `json:"text,omitempty"`
}

type PermissionStep struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	ToolName string   `json:"tool_name,omitempty"`
	Kinds    []string `json:"kinds"` // option kinds offered, in order
	IDPrefix string   `json:"id_prefix,omitempty"`
}

type UsageStep struct {
	Used      int64              `json:"used"`
	RateLimit *acp.RateLimitMeta `json:"rate_limit,omitempty"`
}

// Record is one JSONL line of ACPFAKE_RECORD.
type Record struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"` // for responses we received (permission answers)
}

// MaybeMain runs the fake and exits when ACPFAKE=1. Call from TestMain.
func MaybeMain() {
	if os.Getenv("ACPFAKE") != "1" {
		return
	}
	var s Script
	if err := json.Unmarshal([]byte(os.Getenv("ACPFAKE_SCRIPT")), &s); err != nil {
		fmt.Fprintln(os.Stderr, "acpfake: bad script:", err)
		os.Exit(2)
	}
	var rec io.Writer
	if p := os.Getenv("ACPFAKE_RECORD"); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "acpfake: record:", err)
			os.Exit(2)
		}
		defer f.Close()
		rec = f
	}
	if s.IgnoreSigterm {
		ignoreSigterm()
	}
	Serve(os.Stdin, os.Stdout, s, rec)
	if s.StayAlive {
		select {}
	}
	os.Exit(0)
}

// Command returns how to spawn the fake from a test: the test binary itself
// with the fake environment. record may be "".
func Command(script Script, record string) (cmd string, args []string, env []string) {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	b, _ := json.Marshal(script)
	env = []string{"ACPFAKE=1", "ACPFAKE_SCRIPT=" + string(b), "ACPFAKE_RECORD=" + record, "PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return exe, []string{"-test.run=^$"}, env
}

// ReadRecords parses an ACPFAKE_RECORD file.
func ReadRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acp.RPCError    `json:"error,omitempty"`
}

type server struct {
	s      Script
	out    *bufio.Writer
	outMu  sync.Mutex
	rec    io.Writer
	nextID int64

	inbox     chan message // responses to our requests
	cancelled atomic.Bool
	cancelCh  chan struct{}

	model     string
	lastBrief string
	lastMeta  map[string]any
	lastMCP   []acp.MCPServer
	turn      int
	// refuseSession is the session a LoadNoProvenance load handed back. Hermes
	// answers the next session/prompt on it with `stopReason: "refusal"` and
	// does nothing (spike 4c wire log) — that is what makes a missed loss end
	// as a `completed` attempt with no work.
	refuseSession string
}

// Serve runs the fake over in/out until in is closed.
func Serve(in io.Reader, out io.Writer, s Script, rec io.Writer) {
	if s.ProtocolVersion == 0 {
		s.ProtocolVersion = 1
	}
	if s.SessionID == "" {
		s.SessionID = "sess-1"
	}
	if s.Kind == "" {
		s.Kind = "claude"
	}
	if s.DefaultModel == "" {
		s.DefaultModel = "default-model"
	}
	if len(s.Turns) == 0 {
		s.Turns = []Turn{{Steps: []Step{{Chunk: "PONG"}}}}
	}
	sv := &server{s: s, out: bufio.NewWriter(out), rec: rec, nextID: 1000, inbox: make(chan message, 16), cancelCh: make(chan struct{}, 1), model: s.DefaultModel}
	reqs := make(chan message, 16)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var m message
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			sv.record(m)
			switch {
			case m.Method == acp.MethodSessionCancel:
				sv.cancelled.Store(true)
				select {
				case sv.cancelCh <- struct{}{}:
				default:
				}
			case m.Method != "":
				reqs <- m
			default:
				sv.inbox <- m
			}
		}
		close(reqs)
	}()
	for m := range reqs {
		sv.handle(m)
	}
}

func (sv *server) record(m message) {
	if sv.rec == nil {
		return
	}
	b, _ := json.Marshal(Record{Method: m.Method, Params: m.Params, Result: m.Result})
	_, _ = sv.rec.Write(append(b, '\n'))
}

func (sv *server) send(v any) {
	b, _ := json.Marshal(v)
	sv.outMu.Lock()
	defer sv.outMu.Unlock()
	_, _ = sv.out.Write(append(b, '\n'))
	_ = sv.out.Flush()
}

func (sv *server) reply(id *json.RawMessage, result any) {
	sv.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (sv *server) replyErr(id *json.RawMessage, e *acp.RPCError) {
	sv.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (sv *server) update(sessionID string, u map[string]any) {
	sv.send(map[string]any{"jsonrpc": "2.0", "method": acp.MethodSessionUpdate, "params": map[string]any{"sessionId": sessionID, "update": u}})
}

func (sv *server) chunk(sid, kind, text string) {
	sv.update(sid, map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}})
}

func (sv *server) configOptions() []map[string]any {
	opts := []map[string]any{}
	for _, m := range sv.s.Models {
		opts = append(opts, map[string]any{"value": m, "name": m})
	}
	return []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": sv.model, "options": opts}}
}

func (sv *server) absorbMeta(params json.RawMessage) {
	var p struct {
		Meta       map[string]any  `json:"_meta"`
		MCPServers []acp.MCPServer `json:"mcpServers"`
	}
	_ = json.Unmarshal(params, &p)
	sv.lastMeta = p.Meta
	sv.lastMCP = p.MCPServers
	if sp, ok := p.Meta["systemPrompt"].(map[string]any); ok {
		if a, ok := sp["append"].(string); ok {
			sv.lastBrief = a
		}
	}
}

// rawInit emulates claude-agent-acp's `_claude/sdkMessage` system/init when
// emitRawSDKMessages is on: user mcp__ tools and hooks appear unless the §3
// isolation keys (settingSources: [] + strictMcpConfig: true) were sent;
// servers passed in `mcpServers` always appear as mcp__<name>__* tools.
func (sv *server) rawInit(sid string) {
	cc, _ := sv.lastMeta["claudeCode"].(map[string]any)
	if cc == nil || cc["emitRawSDKMessages"] != true {
		return
	}
	opts, _ := cc["options"].(map[string]any)
	isolated := false
	noHooks := false
	if opts != nil {
		if ss, ok := opts["settingSources"].([]any); ok && len(ss) == 0 {
			noHooks = true
			if opts["strictMcpConfig"] == true {
				isolated = true
			}
		}
	}
	tools := []string{"Bash", "Read", "Write", "Task", "WebFetch"}
	if opts != nil {
		if dis, ok := opts["disallowedTools"].([]any); ok {
			blocked := map[string]bool{}
			for _, d := range dis {
				if name, ok := d.(string); ok {
					blocked[name] = true
				}
			}
			kept := tools[:0:0]
			for _, t := range tools {
				if !blocked[t] {
					kept = append(kept, t)
				}
			}
			tools = kept
		}
	}
	mcp := []map[string]any{}
	for _, s := range sv.lastMCP {
		tools = append(tools, "mcp__"+s.Name+"__message_post")
		mcp = append(mcp, map[string]any{"name": s.Name, "status": "connected"})
	}
	if !isolated {
		tools = append(tools, "mcp__user_server__do")
		mcp = append(mcp, map[string]any{"name": "user_server", "status": "connected"})
	}
	sv.send(map[string]any{"jsonrpc": "2.0", "method": acp.ExtNotificationSDKMessage, "params": map[string]any{"sessionId": sid, "message": map[string]any{"type": "system", "subtype": "init", "tools": tools, "mcp_servers": mcp}}})
	if !noHooks {
		sv.send(map[string]any{"jsonrpc": "2.0", "method": acp.ExtNotificationSDKMessage, "params": map[string]any{"sessionId": sid, "message": map[string]any{"type": "system", "subtype": "hook_started", "hook_name": "user-hook"}}})
	}
}

func (sv *server) handle(m message) {
	s := sv.s
	switch m.Method {
	case acp.MethodInitialize:
		caps := map[string]any{"loadSession": !s.NoLoadSession}
		if !s.NoMCPCapabilities {
			caps["mcpCapabilities"] = map[string]any{"http": s.MCPHTTP, "sse": s.MCPSSE}
		}
		sv.reply(m.ID, map[string]any{"protocolVersion": s.ProtocolVersion, "agentCapabilities": caps, "agentInfo": map[string]any{"name": "acpfake", "version": orDefault(s.AgentVersion, acp.AdapterPin)}})
	case acp.MethodSessionNew:
		sv.absorbMeta(m.Params)
		sv.model = s.DefaultModel
		res := map[string]any{"sessionId": s.SessionID}
		if s.Kind == "hermes" {
			res["_meta"] = map[string]any{"hermes": map[string]any{"sessionProvenance": Provenance{ACPSessionID: s.SessionID, RootHermesSessionID: s.SessionID, SessionKind: "root"}}}
			res["models"] = map[string]any{"currentModelId": sv.model, "availableModels": []any{}}
		} else {
			res["configOptions"] = sv.configOptions()
		}
		sv.reply(m.ID, res)
		sv.rawInit(s.SessionID)
	case acp.MethodSessionLoad:
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		known := false
		for _, k := range s.KnownSessions {
			if k == p.SessionID {
				known = true
			}
		}
		if !known {
			if s.Kind == "hermes" {
				sv.reply(m.ID, nil)
			} else {
				if s.LoadErrorKind == "legacy" {
					sv.replyErr(m.ID, &acp.RPCError{Code: -32000, Message: "Session not found"})
				} else {
					sv.replyErr(m.ID, &acp.RPCError{Code: -32002, Message: "Resource not found: " + p.SessionID,
						Data: json.RawMessage(`{"uri":"` + p.SessionID + `"}`)})
				}
			}
			return
		}
		sv.absorbMeta(m.Params)
		sv.model = s.DefaultModel // 1b E1: load resets the model
		for i := 0; i < s.ReplayChunks; i++ {
			sv.chunk(p.SessionID, "user_message_chunk", "replayed user")
			sv.chunk(p.SessionID, "agent_message_chunk", "replayed agent")
		}
		res := map[string]any{}
		if s.Kind == "hermes" {
			// LoadNoProvenance: the bare `{}` Hermes 0.20.6 actually answers for
			// a session it no longer has (spike 4c wire log) — not null, no _meta.
			if s.LoadNoProvenance {
				sv.refuseSession = p.SessionID
			} else {
				prov := Provenance{ACPSessionID: p.SessionID, RootHermesSessionID: p.SessionID, SessionKind: "root"}
				if s.LoadProvenance != nil {
					prov = *s.LoadProvenance
				}
				res["_meta"] = map[string]any{"hermes": map[string]any{"sessionProvenance": prov}}
			}
		} else {
			res["configOptions"] = sv.configOptions()
		}
		sv.reply(m.ID, res)
		sv.rawInit(p.SessionID)
	case acp.MethodSessionSetConfigOption:
		var p acp.SetConfigOptionParams
		_ = json.Unmarshal(m.Params, &p)
		if p.ConfigID == "model" {
			if v, ok := p.Value.(string); ok {
				sv.model = v
			}
		}
		sv.reply(m.ID, map[string]any{"configOptions": sv.configOptions()})
	case acp.MethodSessionSetModel:
		var p acp.SetModelParams
		_ = json.Unmarshal(m.Params, &p)
		sv.model = p.ModelID
		sv.reply(m.ID, map[string]any{})
	case acp.MethodSessionPrompt:
		var p acp.PromptParams
		_ = json.Unmarshal(m.Params, &p)
		sv.prompt(m.ID, p.SessionID)
	default:
		sv.replyErr(m.ID, &acp.RPCError{Code: -32601, Message: "method not found: " + m.Method})
	}
}

func (sv *server) prompt(id *json.RawMessage, sid string) {
	if sv.refuseSession != "" && sid == sv.refuseSession {
		sv.reply(id, map[string]any{"stopReason": "refusal"})
		return
	}
	t := sv.s.Turns[min(sv.turn, len(sv.s.Turns)-1)]
	sv.turn++
	sv.cancelled.Store(false)
	select {
	case <-sv.cancelCh:
	default:
	}
	finish := func(stop string) {
		if t.Error != nil && stop != "cancelled" {
			sv.replyErr(id, t.Error)
			return
		}
		res := map[string]any{"stopReason": stop}
		if t.Usage != nil {
			res["usage"] = t.Usage
		}
		if t.ModelUsage {
			res["_meta"] = map[string]any{"quota": map[string]any{"model_usage": []map[string]any{{"model": orDefault(t.ReportModel, sv.model)}}}}
		}
		sv.reply(id, res)
		if t.LateChunk != "" {
			time.Sleep(time.Duration(t.LateDelayMs) * time.Millisecond)
			sv.chunk(sid, "agent_message_chunk", t.LateChunk)
		}
	}
	stop := t.StopReason
	if stop == "" {
		stop = "end_turn"
	}
	for _, st := range t.Steps {
		// A permission request already in flight still reaches the client
		// after session/cancel (the §5 E10-03 path); other steps stop.
		if sv.cancelled.Load() && st.Permission == nil {
			finish("cancelled")
			return
		}
		switch {
		case st.HangForever:
			select {}
		case st.Hang:
			<-sv.cancelCh
			finish("cancelled")
			return
		case st.SleepMs > 0:
			time.Sleep(time.Duration(st.SleepMs) * time.Millisecond)
		case st.Chunk != "":
			sv.chunk(sid, "agent_message_chunk", st.Chunk)
		case st.Thought != "":
			sv.chunk(sid, "agent_thought_chunk", st.Thought)
		case st.EchoBrief:
			sv.chunk(sid, "agent_message_chunk", sv.lastBrief)
		case st.EchoModel:
			sv.chunk(sid, "agent_message_chunk", sv.model)
		case st.ToolCall != nil:
			tc := st.ToolCall
			u := map[string]any{"sessionUpdate": "tool_call", "toolCallId": tc.ID, "title": tc.Title, "kind": tc.Kind, "status": orDefault(tc.Status, "pending"), "content": []any{}, "locations": []any{}}
			if tc.Path != "" {
				u["locations"] = []map[string]any{{"path": tc.Path}}
			}
			if tc.Command != "" {
				u["rawInput"] = map[string]any{"command": tc.Command}
			}
			sv.update(sid, u)
		case st.ToolUpdate != nil:
			tu := st.ToolUpdate
			u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tu.ID, "status": tu.Status}
			var content []any
			if tu.NewText != "" || tu.OldText != nil {
				content = append(content, map[string]any{"type": "diff", "path": tu.Path, "oldText": tu.OldText, "newText": tu.NewText})
			}
			if tu.Text != "" {
				content = append(content, map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": tu.Text}})
			}
			if content != nil {
				u["content"] = content
			}
			if tu.ExitCode != nil {
				u["rawOutput"] = map[string]any{"exitCode": *tu.ExitCode}
			}
			sv.update(sid, u)
		case st.Permission != nil:
			ps := st.Permission
			prefix := orDefault(ps.IDPrefix, "opt")
			opts := []map[string]any{}
			for i, k := range ps.Kinds {
				opts = append(opts, map[string]any{"optionId": fmt.Sprintf("%s-%d", prefix, i), "name": k, "kind": k})
			}
			tc := map[string]any{"toolCallId": ps.ID, "title": ps.Title, "kind": "execute"}
			if ps.ToolName != "" {
				tc["_meta"] = map[string]any{"claudeCode": map[string]any{"toolName": ps.ToolName}}
			}
			sv.nextID++
			rid := sv.nextID
			// Record the OUTGOING request as well as the answer: a test that
			// has to act while a permission is genuinely outstanding (harness
			// §5 step 2, E10-03) needs to know the moment it was asked, and
			// the client's own events only appear once it has decided.
			params := map[string]any{"sessionId": sid, "toolCall": tc, "options": opts}
			pb, _ := json.Marshal(params)
			sv.record(message{Method: acp.MethodRequestPermission, Params: pb})
			sv.send(map[string]any{"jsonrpc": "2.0", "id": rid, "method": acp.MethodRequestPermission, "params": params})
			ans := <-sv.inbox
			var r acp.RequestPermissionResult
			_ = json.Unmarshal(ans.Result, &r)
			status := "completed"
			if r.Outcome.Outcome != "selected" {
				status = "failed"
			} else {
				for i, k := range ps.Kinds {
					if fmt.Sprintf("%s-%d", prefix, i) == r.Outcome.OptionID && (k == "reject_once" || k == "reject_always") {
						status = "failed"
					}
				}
			}
			sv.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": ps.ID, "title": ps.Title, "kind": "execute", "status": status})
		case st.Usage != nil:
			u := map[string]any{"sessionUpdate": "usage_update", "used": st.Usage.Used, "size": 200000}
			if st.Usage.RateLimit != nil {
				u["_meta"] = map[string]any{"_claude/rateLimit": st.Usage.RateLimit}
			}
			sv.update(sid, u)
		case st.Plan != nil:
			sv.update(sid, map[string]any{"sessionUpdate": "plan", "entries": st.Plan})
		}
	}
	if sv.cancelled.Load() {
		stop = "cancelled"
	}
	finish(stop)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
