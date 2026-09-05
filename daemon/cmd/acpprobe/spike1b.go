package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ingki3/agent-collabortion/daemon/internal/acpprobe"
)

// ---- spike 1b: claude-agent-acp 0.74.0 re-check + daemon session isolation --
//
// PLAN P0-b. Five questions against @agentclientprotocol/claude-agent-acp@0.74.0:
//   E1 model selection without `models` in session/new (session/set_config_option)
//   E2 `_meta.systemPrompt {append}` still works ×3, and across session/load
//   E3 isolation: settingSources / strictMcpConfig keep user MCP + hooks out
//   E4 permissions.deny via `_meta.claudeCode.options.settings`, subagent included
//   E5 rate-limit wording — source only (TestRateLimitRegexCoversSDKPrefixes)
//
// Evidence is taken from the wire, not from the model's self-report where
// possible: `_meta.quota.model_usage` for the model, the raw SDK `system/init`
// message (tools, mcp_servers) and `hook_started` events for isolation, and
// tool_use/tool_result blocks for the subagent deny.

// rawCapture collects `_claude/sdkMessage` notifications
// (`_meta.claudeCode.emitRawSDKMessages: true`).
type rawCapture struct {
	mu sync.Mutex
	rawSnapshot
}

// rawSnapshot is the lock-free copy written into the summary.
type rawSnapshot struct {
	Count       int               `json:"rawMessages"`
	Init        *sdkInit          `json:"init,omitempty"`
	Hooks       []sdkHookEvent    `json:"hookEvents,omitempty"`
	ToolUses    []sdkToolUse      `json:"toolUses,omitempty"`
	ToolResults []sdkToolResult   `json:"toolResults,omitempty"`
	Errors      []json.RawMessage `json:"assistantErrors,omitempty"`
}

type sdkInit struct {
	Model          string   `json:"model"`
	PermissionMode string   `json:"permissionMode"`
	Tools          []string `json:"tools"`
	MCPServers     []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Agents        []string `json:"agents,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	SlashCommands []string `json:"slash_commands,omitempty"`
}

type sdkHookEvent struct {
	Name  string `json:"hook_name"`
	Event string `json:"hook_event"`
}

type sdkToolUse struct {
	Name            string          `json:"name"`
	Input           json.RawMessage `json:"input,omitempty"`
	ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
	ID              string          `json:"id"`
}

type sdkToolResult struct {
	ToolUseID       string `json:"tool_use_id"`
	IsError         bool   `json:"is_error"`
	Text            string `json:"text"`
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
}

func captureRaw(c *acpprobe.Client) *rawCapture {
	rc := &rawCapture{}
	c.OnNotification(func(method string, params json.RawMessage) {
		if method != acpprobe.ExtNotificationSDKMessage {
			return
		}
		var p struct {
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		var head struct {
			Type            string `json:"type"`
			Subtype         string `json:"subtype"`
			ParentToolUseID string `json:"parent_tool_use_id"`
			Error           any    `json:"error"`
		}
		_ = json.Unmarshal(p.Message, &head)
		rc.mu.Lock()
		defer rc.mu.Unlock()
		rc.Count++
		switch {
		case head.Type == "system" && head.Subtype == "init":
			var in sdkInit
			if json.Unmarshal(p.Message, &in) == nil {
				rc.Init = &in
			}
		case head.Type == "system" && head.Subtype == "hook_started":
			var h sdkHookEvent
			if json.Unmarshal(p.Message, &h) == nil {
				rc.Hooks = append(rc.Hooks, h)
			}
		case head.Type == "assistant":
			if head.Error != nil {
				rc.Errors = append(rc.Errors, p.Message)
			}
			var m struct {
				Message struct {
					Content []struct {
						Type  string          `json:"type"`
						ID    string          `json:"id"`
						Name  string          `json:"name"`
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(p.Message, &m) == nil {
				for _, b := range m.Message.Content {
					if b.Type == "tool_use" {
						rc.ToolUses = append(rc.ToolUses, sdkToolUse{Name: b.Name, Input: truncateRaw(b.Input, 300), ParentToolUseID: head.ParentToolUseID, ID: b.ID})
					}
				}
			}
		case head.Type == "user":
			var m struct {
				Message struct {
					Content []struct {
						Type      string          `json:"type"`
						ToolUseID string          `json:"tool_use_id"`
						IsError   bool            `json:"is_error"`
						Content   json.RawMessage `json:"content"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(p.Message, &m) == nil {
				for _, b := range m.Message.Content {
					if b.Type == "tool_result" {
						rc.ToolResults = append(rc.ToolResults, sdkToolResult{ToolUseID: b.ToolUseID, IsError: b.IsError, Text: firstLine(rawText(b.Content)), ParentToolUseID: head.ParentToolUseID})
					}
				}
			}
		}
	})
	return rc
}

func truncateRaw(b json.RawMessage, n int) json.RawMessage {
	if len(b) <= n {
		return b
	}
	s, _ := json.Marshal(string(b[:n]) + "…")
	return s
}

// rawText flattens a tool_result content (string or [{type:text,text}]).
func rawText(b json.RawMessage) string {
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(b, &arr) == nil {
		var sb strings.Builder
		for _, a := range arr {
			sb.WriteString(a.Text)
		}
		return sb.String()
	}
	return string(b)
}

func (rc *rawCapture) snapshot() rawSnapshot {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.rawSnapshot
}

func metaRaw(extra map[string]any) map[string]any {
	cc := map[string]any{"emitRawSDKMessages": true}
	for k, v := range extra {
		cc[k] = v
	}
	return map[string]any{"claudeCode": cc}
}

func hasHaiku(models []string) bool {
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), "haiku") {
			return true
		}
	}
	return false
}

func mcpTools(tools []string) []string {
	var out []string
	for _, t := range tools {
		if strings.HasPrefix(t, "mcp__") {
			out = append(out, t)
		}
	}
	return out
}

func (r *runner) spike1b(ctx context.Context) error {
	if err := r.spike1bModel(ctx); err != nil {
		return err
	}
	if err := r.spike1bSystemPrompt(ctx); err != nil {
		return err
	}
	if err := r.spike1bIsolation(ctx); err != nil {
		return err
	}
	return r.spike1bDeny(ctx)
}

func (r *runner) subdir(name string) string {
	d := filepath.Join(r.o.workdir, name)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// E1 — model selection. session/new (no model) → set_config_option{model:haiku}
// → turn → kill → session/load → turn → (re-assert if reverted) → turn. Then
// session/new with `_meta.claudeCode.options.model` as the alternative.
func (r *runner) spike1bModel(ctx context.Context) error {
	e := map[string]any{}
	r.summary["e1_model"] = e
	cwd := r.subdir("e1")
	r.logf("E1 model: session/new without model")
	c, _, err := r.spawn(ctx, "e1")
	if err != nil {
		return err
	}
	rc := captureRaw(c)
	sctx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	s, err := c.NewSession(sctx, cwd, metaRaw(nil))
	if err != nil {
		return err
	}
	e["session_id"] = s.SessionID
	e["new_models_field_present"] = s.Models != nil
	e["new_model_current"] = acpprobe.ConfigOptionValue(s.ConfigOptions, "model")
	for _, o := range s.ConfigOptions {
		if o.ID == "model" {
			e["model_option"] = o
		}
	}
	res, err := c.SetConfigOption(sctx, s.SessionID, "model", r.o.model)
	if err != nil {
		e["set_config_option_error"] = err.Error()
	} else {
		e["set_config_option_current"] = acpprobe.ConfigOptionValue(res.ConfigOptions, "model")
	}
	tr, err := r.turn(ctx, c, s.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
	if err != nil {
		e["turn1_error"] = err.Error()
		return err
	}
	e["turn1_models"] = tr.Models
	e["turn1_text"] = firstLine(tr.Text)
	e["turn1_init_model"] = ""
	if snap := rc.snapshot(); snap.Init != nil {
		e["turn1_init_model"] = snap.Init.Model
	}
	r.retire(c)

	r.logf("E1 model: session/load in fresh process (no _meta)")
	c2, _, err := r.spawn(ctx, "e1:load")
	if err != nil {
		return err
	}
	lctx, lcancel := context.WithTimeout(ctx, r.o.timeout)
	defer lcancel()
	lres, err := c2.LoadSession(lctx, cwd, s.SessionID, metaRaw(nil))
	if err != nil {
		e["load_error"] = err.Error()
		r.retire(c2)
		return err
	}
	e["load_model_current"] = acpprobe.ConfigOptionValue(lres.ConfigOptions, "model")
	tr2, err := r.turn(ctx, c2, s.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
	if err != nil {
		e["turn2_error"] = err.Error()
		return err
	}
	e["turn2_models_after_load"] = tr2.Models
	if !hasHaiku(tr2.Models) {
		res2, err := c2.SetConfigOption(lctx, s.SessionID, "model", r.o.model)
		if err != nil {
			e["reassert_error"] = err.Error()
		} else {
			e["reassert_current"] = acpprobe.ConfigOptionValue(res2.ConfigOptions, "model")
		}
		tr3, err := r.turn(ctx, c2, s.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
		if err != nil {
			e["turn3_error"] = err.Error()
			return err
		}
		e["turn3_models_after_reassert"] = tr3.Models
	}
	r.retire(c2)

	r.logf("E1 model: session/new with _meta.claudeCode.options.model")
	c3, _, err := r.spawn(ctx, "e1:options.model")
	if err != nil {
		return err
	}
	s3, err := c3.NewSession(sctx, cwd, metaRaw(map[string]any{"options": map[string]any{"model": r.o.model}}))
	if err != nil {
		e["options_model_new_error"] = err.Error()
		r.retire(c3)
		return err
	}
	e["options_model_new_current"] = acpprobe.ConfigOptionValue(s3.ConfigOptions, "model")
	tr4, err := r.turn(ctx, c3, s3.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
	if err != nil {
		e["options_model_turn_error"] = err.Error()
		return err
	}
	e["options_model_turn_models"] = tr4.Models
	r.retire(c3)
	e["verdict_set_config_option"] = hasHaiku(tr.Models)
	e["verdict_survives_load"] = hasHaiku(tr2.Models)
	e["verdict_options_model"] = hasHaiku(tr4.Models)
	return nil
}

// E2 — `_meta.systemPrompt {append}` ×3 on session/new, then session/load
// without and with the same _meta.
func (r *runner) spike1bSystemPrompt(ctx context.Context) error {
	brief := "Your name is Zorblax-7. You are a probe agent for the Colab project. When asked your name, answer with exactly 'Zorblax-7'. When asked your secret word, answer 'pomegranate'."
	q := "Two questions, answer on two lines and nothing else: 1) What is your name? 2) What is your secret word? Do not use tools."
	ok := func(t string) bool {
		return strings.Contains(t, "Zorblax-7") && strings.Contains(strings.ToLower(t), "pomegranate")
	}
	meta := func() map[string]any {
		m := metaRaw(nil)
		m["systemPrompt"] = map[string]any{"append": brief}
		return m
	}
	e := map[string]any{}
	r.summary["e2_system_prompt"] = e
	cwd := r.subdir("e2")
	var runs []map[string]any
	var lastSID string
	for i := 1; i <= 3; i++ {
		r.logf("E2 systemPrompt append %d/3", i)
		rec := map[string]any{"i": i}
		c, _, err := r.spawn(ctx, fmt.Sprintf("e2:%d", i))
		if err != nil {
			return err
		}
		s, err := r.newSessionIn(ctx, c, cwd, meta())
		if err != nil {
			rec["error"] = err.Error()
			runs = append(runs, rec)
			r.retire(c)
			return err
		}
		lastSID = s.SessionID
		tr, err := r.turn(ctx, c, s.SessionID, q)
		if err != nil {
			rec["error"] = err.Error()
			runs = append(runs, rec)
			e["runs"] = runs
			return err
		}
		rec["text"], rec["models"], rec["ok"] = strings.TrimSpace(tr.Text), tr.Models, ok(tr.Text)
		runs = append(runs, rec)
		r.retire(c)
	}
	e["runs"] = runs
	passed := 0
	for _, x := range runs {
		if x["ok"] == true {
			passed++
		}
	}
	e["passed"] = passed

	load := func(label string, m map[string]any) (map[string]any, error) {
		rec := map[string]any{"label": label, "meta_systemPrompt": m["systemPrompt"] != nil}
		c, _, err := r.spawn(ctx, "e2:"+label)
		if err != nil {
			return rec, err
		}
		lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
		defer cancel()
		res, err := c.LoadSession(lctx, cwd, lastSID, m)
		if err != nil {
			rec["load_error"] = err.Error()
			r.retire(c)
			return rec, err
		}
		if len(res.ConfigOptions) > 0 {
			r.ensureModel(lctx, c, lastSID, res.ConfigOptions)
		}
		tr, err := r.turn(ctx, c, lastSID, q)
		if err != nil {
			rec["error"] = err.Error()
			return rec, err
		}
		rec["text"], rec["models"], rec["ok"] = strings.TrimSpace(tr.Text), tr.Models, ok(tr.Text)
		r.retire(c)
		return rec, nil
	}
	r.logf("E2 session/load without _meta.systemPrompt")
	a, err := load("load_without_meta", metaRaw(nil))
	e["load_without_meta"] = a
	if err != nil {
		return err
	}
	r.logf("E2 session/load with _meta.systemPrompt")
	b, err := load("load_with_meta", meta())
	e["load_with_meta"] = b
	if err != nil {
		return err
	}
	e["verdict_append_3of3"] = passed == 3
	e["verdict_load_needs_meta"] = a["ok"] != true && b["ok"] == true
	return nil
}

// newSessionIn is newSession with an explicit cwd (spike1b uses one subdir per
// experiment so E3's settingSources:["project"] sees an empty project).
func (r *runner) newSessionIn(ctx context.Context, c *acpprobe.Client, cwd string, meta map[string]any) (*acpprobe.SessionResult, error) {
	sctx, cancel := context.WithTimeout(ctx, r.o.timeout)
	defer cancel()
	s, err := c.NewSession(sctx, cwd, meta)
	if err != nil {
		return nil, err
	}
	if r.o.model != "" && len(s.ConfigOptions) > 0 {
		r.ensureModel(sctx, c, s.SessionID, s.ConfigOptions)
	}
	return s, nil
}

// E3 — isolation. Three sessions: control, settingSources:["project"] only,
// settingSources:[] + strictMcpConfig:true. Evidence: raw `system/init`
// tools + mcp_servers, `hook_started` events, and the agent's own tool list.
func (r *runner) spike1bIsolation(ctx context.Context) error {
	prompt := "List every tool you currently have available, one tool name per line, exactly as the tool is named (for example Bash, Read, mcp__server__tool). Include MCP tools if you have any. Do not call any tools. No commentary."
	e := map[string]any{}
	r.summary["e3_isolation"] = e
	cwd := r.subdir("e3")
	type variant struct {
		name string
		opts map[string]any
	}
	variants := []variant{
		{"a_control", nil},
		{"b_settingSources_project", map[string]any{"settingSources": []string{"project"}}},
		{"c_settingSources_empty_strictMcp", map[string]any{"settingSources": []string{}, "strictMcpConfig": true}},
	}
	var results []map[string]any
	for _, v := range variants {
		r.logf("E3 isolation: %s", v.name)
		rec := map[string]any{"name": v.name, "options": v.opts}
		c, _, err := r.spawn(ctx, "e3:"+v.name)
		if err != nil {
			return err
		}
		rc := captureRaw(c)
		var meta map[string]any
		if v.opts != nil {
			meta = metaRaw(map[string]any{"options": v.opts})
		} else {
			meta = metaRaw(nil)
		}
		s, err := r.newSessionIn(ctx, c, cwd, meta)
		if err != nil {
			rec["error"] = err.Error()
			results = append(results, rec)
			e["variants"] = results
			r.retire(c)
			return err
		}
		tr, err := r.turn(ctx, c, s.SessionID, prompt)
		if err != nil {
			rec["error"] = err.Error()
			results = append(results, rec)
			e["variants"] = results
			return err
		}
		snap := rc.snapshot()
		rec["text"] = strings.TrimSpace(tr.Text)
		rec["models"] = tr.Models
		rec["raw"] = snap
		if snap.Init != nil {
			rec["init_tools"] = snap.Init.Tools
			rec["init_mcp_tools"] = mcpTools(snap.Init.Tools)
			rec["init_mcp_servers"] = snap.Init.MCPServers
		}
		rec["hook_events"] = snap.Hooks
		rec["text_mentions_mcp"] = strings.Contains(tr.Text, "mcp__")
		rec["text_mentions_pencil"] = strings.Contains(strings.ToLower(tr.Text), "pencil")
		results = append(results, rec)
		r.retire(c)
	}
	e["variants"] = results
	if len(results) == 3 {
		ctrl, full := results[0], results[2]
		e["verdict_control_leaks_mcp"] = len(asStrings(ctrl["init_mcp_tools"])) > 0 || ctrl["text_mentions_mcp"] == true
		e["verdict_isolated_no_mcp"] = len(asStrings(full["init_mcp_tools"])) == 0 && full["text_mentions_mcp"] != true
		e["verdict_control_hooks"] = len(asHooks(ctrl["hook_events"]))
		e["verdict_isolated_hooks"] = len(asHooks(full["hook_events"]))
	}
	return nil
}

func asStrings(v any) []string {
	s, _ := v.([]string)
	return s
}

func asHooks(v any) []sdkHookEvent {
	s, _ := v.([]sdkHookEvent)
	return s
}

// E4 — permissions.deny through `_meta.claudeCode.options.settings` (the SDK
// `--settings` inline layer), exercised from inside a Task subagent.
func (r *runner) spike1bDeny(ctx context.Context) error {
	e := map[string]any{}
	r.summary["e4_deny"] = e
	cwd := r.subdir("e4")
	settings := map[string]any{"permissions": map[string]any{"deny": []string{"Bash(date:*)", "Bash(date)"}}}
	e["settings"] = settings
	prompt := "Use the Task tool to launch exactly one subagent (subagent_type: general-purpose). The subagent's only job is to run the shell command `date` using the Bash tool and return its output verbatim. Do not run `date` yourself. When the subagent returns, reply with exactly what it returned. If any tool call was blocked or failed (yours or the subagent's), reply with 'BLOCKED: ' followed by the error text verbatim."
	r.logf("E4 deny via options.settings inside Task subagent")
	c, _, err := r.spawn(ctx, "e4")
	if err != nil {
		return err
	}
	rc := captureRaw(c)
	var toolUpdates []json.RawMessage
	c.OnUpdate(func(p acpprobe.SessionUpdateParams) {
		var k acpprobe.UpdateKind
		_ = json.Unmarshal(p.Update, &k)
		if k.SessionUpdate == "tool_call" || k.SessionUpdate == "tool_call_update" {
			toolUpdates = append(toolUpdates, truncateRaw(append(json.RawMessage(nil), p.Update...), 600))
		}
	})
	s, err := r.newSessionIn(ctx, c, cwd, metaRaw(map[string]any{"options": map[string]any{"settings": settings}}))
	if err != nil {
		e["error"] = err.Error()
		r.retire(c)
		return err
	}
	tr, err := r.turn(ctx, c, s.SessionID, prompt)
	if err != nil {
		e["error"] = err.Error()
		return err
	}
	snap := rc.snapshot()
	e["text"] = strings.TrimSpace(tr.Text)
	e["models"] = tr.Models
	e["permission_requests"] = tr.Permissions
	e["tool_updates"] = toolUpdates
	e["raw"] = snap
	var subagentBash, subagentBashDenied, mainBash bool
	for _, u := range snap.ToolUses {
		if u.Name == "Bash" && strings.Contains(string(u.Input), "date") {
			if u.ParentToolUseID != "" {
				subagentBash = true
			} else {
				mainBash = true
			}
		}
	}
	for _, t := range snap.ToolResults {
		if t.ParentToolUseID != "" && t.IsError && strings.Contains(strings.ToLower(t.Text), "denied") {
			subagentBashDenied = true
		}
	}
	e["subagent_bash_date_attempted"] = subagentBash
	e["subagent_bash_date_denied"] = subagentBashDenied
	e["main_agent_bash_date_attempted"] = mainBash
	e["text_says_blocked"] = strings.Contains(tr.Text, "BLOCKED")
	e["verdict_deny_reaches_subagent"] = subagentBash && subagentBashDenied
	r.retire(c)
	return nil
}

// E2b — separates "history not restored" from "system prompt not re-applied"
// after session/load: session/new{append} → q → kill → session/load{append}
// → "repeat your previous answer" (history) → q (system prompt) → then
// session/load with the string (replace) form → q.
func (r *runner) spike1bLoad(ctx context.Context) error {
	brief := "Your name is Zorblax-7. You are a probe agent for the Colab project. When asked your name, answer with exactly 'Zorblax-7'. When asked your secret word, answer 'pomegranate'."
	q := "Two questions, answer on two lines and nothing else: 1) What is your name? 2) What is your secret word? Do not use tools."
	ok := func(t string) bool {
		return strings.Contains(t, "Zorblax-7") && strings.Contains(strings.ToLower(t), "pomegranate")
	}
	e := map[string]any{}
	r.summary["e2b_load"] = e
	cwd := r.subdir("e2b")
	m := metaRaw(nil)
	m["systemPrompt"] = map[string]any{"append": brief}

	// (A) clean history: session/new WITHOUT the brief → neutral turn → kill →
	// session/load WITH the brief → q. A Zorblax answer here can only come from
	// the _meta.systemPrompt given at load time.
	{
		rec := map[string]any{}
		e["clean_new_without_meta_then_load_with_meta"] = rec
		c0, _, err := r.spawn(ctx, "e2b:clean-new")
		if err != nil {
			return err
		}
		s0, err := r.newSessionIn(ctx, c0, cwd, metaRaw(nil))
		if err != nil {
			return err
		}
		tr0, err := r.turn(ctx, c0, s0.SessionID, "Reply with exactly the single word PONG and nothing else. Do not use any tools.")
		if err != nil {
			return err
		}
		rec["new_text"] = firstLine(tr0.Text)
		r.retire(c0)
		c1, _, err := r.spawn(ctx, "e2b:clean-load")
		if err != nil {
			return err
		}
		lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
		res, err := c1.LoadSession(lctx, cwd, s0.SessionID, m)
		if err != nil {
			cancel()
			rec["load_error"] = err.Error()
			return err
		}
		r.ensureModel(lctx, c1, s0.SessionID, res.ConfigOptions)
		cancel()
		tr1, err := r.turn(ctx, c1, s0.SessionID, q)
		if err != nil {
			return err
		}
		rec["load_text"], rec["load_ok"], rec["load_models"] = strings.TrimSpace(tr1.Text), ok(tr1.Text), tr1.Models
		r.retire(c1)
	}

	c, _, err := r.spawn(ctx, "e2b:new")
	if err != nil {
		return err
	}
	s, err := r.newSessionIn(ctx, c, cwd, m)
	if err != nil {
		return err
	}
	tr, err := r.turn(ctx, c, s.SessionID, q)
	if err != nil {
		return err
	}
	e["new_text"], e["new_ok"] = strings.TrimSpace(tr.Text), ok(tr.Text)
	r.retire(c)

	load := func(label string, meta map[string]any, prompts ...string) error {
		rec := map[string]any{}
		e[label] = rec
		c, _, err := r.spawn(ctx, "e2b:"+label)
		if err != nil {
			return err
		}
		lctx, cancel := context.WithTimeout(ctx, r.o.timeout)
		defer cancel()
		res, err := c.LoadSession(lctx, cwd, s.SessionID, meta)
		if err != nil {
			rec["load_error"] = err.Error()
			r.retire(c)
			return err
		}
		r.ensureModel(lctx, c, s.SessionID, res.ConfigOptions)
		for i, p := range prompts {
			tr, err := r.turn(ctx, c, s.SessionID, p)
			if err != nil {
				rec[fmt.Sprintf("turn%d_error", i+1)] = err.Error()
				return err
			}
			rec[fmt.Sprintf("turn%d_text", i+1)] = strings.TrimSpace(tr.Text)
			rec[fmt.Sprintf("turn%d_ok", i+1)] = ok(tr.Text)
			rec[fmt.Sprintf("turn%d_models", i+1)] = tr.Models
		}
		r.retire(c)
		return nil
	}
	if err := load("load_append", m, "Without using tools: repeat, verbatim, the two lines of your previous answer in this conversation.", q); err != nil {
		return err
	}
	m2 := metaRaw(nil)
	m2["systemPrompt"] = brief // replace form
	return load("load_replace_string", m2, q)
}
