// Package colab implements the P1 commands of contracts/colab-cli.md §2 on top
// of client. The CLI (cmd/colab) and the MCP server (internal/mcp) both call
// these, so a tool's arguments and result are exactly the command's.
package colab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// MessagePostArgs — `colab message post --body [--detail | --detail-file] [--reply-to | --top-level] [--mention]`.
type MessagePostArgs struct {
	Session string `json:"session,omitempty"`
	Body    string `json:"body"`
	// Detail is the work layer (colab-cli v0.9.2): findings · full drafts ·
	// tables, sent as is (never trimmed). nil = not given; given but blank is
	// a usage error — the server's minLength is 1 and a blank fold is noise.
	Detail *string `json:"detail,omitempty"`
	// DetailFile is `--detail-file` / MCP `detail_file` (colab-cli v0.9.3): a
	// path — relative to the working folder, or absolute — whose UTF-8 text
	// (at most MaxDetailChars characters) becomes Detail. With Detail it is a
	// usage error. Both surfaces read it through ReadDetailFile.
	DetailFile string `json:"detail_file,omitempty"`
	ReplyTo    string `json:"reply_to,omitempty"`
	// TopLevel posts to the main timeline even when the turn was asked in a
	// thread (colab-cli v0.9.1). With ReplyTo it is a usage error.
	TopLevel bool     `json:"top_level,omitempty"`
	Mention  []string `json:"mention,omitempty"` // agent names, with or without '@'
	// IdempotencyKey overrides the derived key (UUIDv5 of task:<task_id>:<seq>,
	// colab-cli.md §1). Use it to retry the *same* post after a network error.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// MaxDetailChars is MessageCreate.detail's maxLength (openapi v0.3.1): the
// work text is at most 200,000 characters.
const MaxDetailChars = 200000

// ReadDetailFile is the one reader of `--detail-file` (CLI) and `detail_file`
// (MCP colab_message_post), colab-cli v0.9.3: a relative path is taken from
// the working folder (the process's — the agent's workdir for both the CLI
// and the MCP server the runtime starts there), the bytes must be UTF-8 text
// and at most MaxDetailChars characters, and they are sent as is (no
// trimming — the file is the work text). Every failure is a usage error (exit
// 2) naming the path, so the agent can fix the argument.
func ReadDetailFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", client.Usage("--detail-file %s: working folder: %v", path, err)
		}
		path = filepath.Join(wd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", client.Usage("--detail-file: %v", err)
	}
	if !utf8.Valid(b) {
		return "", client.Usage("--detail-file %s: not UTF-8 text", path)
	}
	if n := utf8.RuneCount(b); n > MaxDetailChars {
		return "", client.Usage("--detail-file %s: %d characters, the limit is %d — put the full text in an artifact (artifact submit) and summarise it here", path, n, MaxDetailChars)
	}
	return string(b), nil
}

// MessagePostResult — colab-cli.md §2.2: `triggered`/`suppressed` are agent
// names; `triggers`/`warnings` are the raw openapi MessagePostResult fields.
type MessagePostResult struct {
	MessageID      string           `json:"message_id"`
	Message        client.Message   `json:"message"`
	Triggered      []string         `json:"triggered"`
	Suppressed     []string         `json:"suppressed"`
	Triggers       []client.Trigger `json:"triggers"`
	Warnings       []client.Warning `json:"warnings"`
	SessionPaused  *string          `json:"session_paused,omitempty"`
	IdempotencyKey string           `json:"idempotency_key"`
	Replayed       bool             `json:"replayed"`
}

// MessagePost — POST /rooms/{R}/messages. Mentions are resolved to the
// participant's mention_link from /cli/context and prepended to the body;
// routing (rules 4 · 8) is the server's.
func MessagePost(ctx context.Context, c *client.Client, a MessagePostArgs) (*MessagePostResult, error) {
	if strings.TrimSpace(a.Body) == "" {
		return nil, client.Usage("--body is required")
	}
	if a.TopLevel && a.ReplyTo != "" {
		return nil, client.Usage("--reply-to and --top-level contradict each other: give one")
	}
	if a.DetailFile != "" {
		if a.Detail != nil {
			return nil, client.Usage("--detail and --detail-file (MCP detail · detail_file) contradict each other: give one")
		}
		d, err := ReadDetailFile(a.DetailFile)
		if err != nil {
			return nil, err
		}
		a.Detail = &d
	}
	if a.Detail != nil && strings.TrimSpace(*a.Detail) == "" {
		return nil, client.Usage("--detail is empty: omit it or give the work text")
	}
	if err := c.Allow(ctx, client.CmdMessagePost); err != nil {
		return nil, err
	}
	sid, err := c.RoomID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	content := a.Body
	var names map[string]string // agent_id → name (for triggered/suppressed)
	if len(a.Mention) > 0 {
		cc, err := c.Context(ctx)
		if err != nil {
			return nil, err
		}
		links, err := resolveMentions(cc, a.Mention)
		if err != nil {
			return nil, err
		}
		if links = missingMentions(links, content); len(links) > 0 {
			content = strings.Join(links, " ") + " " + content
		}
		names = nameIndex(cc)
	}
	body := client.MessageCreate{Content: content, Detail: a.Detail}
	// Where the reply goes (colab-cli v0.9.1): the thread named, else the
	// thread the turn was asked in (COLAB_THREAD_ID), else the main timeline.
	// A question asked in a thread is answered there — an answer on the main
	// timeline is one the asker does not see (STO 방, 2026-09-25).
	parent := a.ReplyTo
	if parent == "" && !a.TopLevel {
		parent = c.ThreadID(sid)
	}
	if parent != "" {
		body.ParentID = &parent
	}
	res, key, replayed, err := c.PostMessage(ctx, sid, body, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if names == nil {
		// Best effort: name resolution only if the context is already cached
		// (no extra round trip just for display).
		names = map[string]string{}
		if cc := c.CachedContext(); cc != nil {
			names = nameIndex(cc)
		}
	}
	return summarize(res, key, replayed, names), nil
}

// mentionTargetRe is the target of an FR-3.2 mention link,
// `[@Name](mention://agent/<id>)` → `agent/<id>`.
var mentionTargetRe = regexp.MustCompile(`\(mention://((?:agent|user|all)/[^)\s]+)\)`)

// missingMentions is links minus the ones the body already carries and minus
// repeats: `mention` names who to wake, and an agent that wrote the link into
// its body AND named the same agent in `mention` must not get
// 「[@Researcher](…) [@Researcher](…) 삼성전자…」 (실사용 message abe08c9c,
// 2026-09-25 — the Lead's body opened with the link and `mention` said
// @Researcher; the prepend doubled it). The same target is judged by its id,
// not the display name, so a renamed agent still counts as present.
func missingMentions(links []string, body string) []string {
	have := map[string]bool{}
	for _, m := range mentionTargetRe.FindAllStringSubmatch(body, -1) {
		have[m[1]] = true
	}
	var out []string
	for _, l := range links {
		m := mentionTargetRe.FindStringSubmatch(l)
		if m == nil {
			out = append(out, l)
			continue
		}
		if have[m[1]] {
			continue
		}
		have[m[1]] = true
		out = append(out, l)
	}
	return out
}

func nameIndex(cc *client.CliContext) map[string]string {
	m := map[string]string{}
	for _, p := range cc.Participants {
		m[p.AgentID] = p.Name
	}
	return m
}

func resolveMentions(cc *client.CliContext, mention []string) ([]string, error) {
	var links []string
	for _, raw := range mention {
		for _, m := range strings.Split(raw, ",") {
			name := strings.TrimPrefix(strings.TrimSpace(m), "@")
			if name == "" {
				continue
			}
			// A mention link given as the name (「[@Writer](mention://agent/<id>)」
			// — the roster in the brief shows agents that way, and a real
			// claude_code turn passed it verbatim, T-SURFACE 실기) names the
			// agent by its id.
			if l := mentionTargetRe.FindStringSubmatch(name); l != nil && strings.HasPrefix(l[1], "agent/") {
				name = strings.TrimPrefix(l[1], "agent/")
			}
			var link string
			for _, p := range cc.Participants {
				if strings.EqualFold(p.Name, name) || p.AgentID == name {
					link = p.MentionLink
					if link == "" {
						link = "[@" + p.Name + "](mention://agent/" + p.AgentID + ")"
					}
					break
				}
			}
			if link == "" {
				var known []string
				for _, p := range cc.Participants {
					known = append(known, p.Name)
				}
				return nil, &client.Error{Exit: client.ExitUsage, Code: "unknown_mention", Title: "unknown mention @" + name,
					Detail: "not a room participant (FR-1.5). participants: " + strings.Join(known, ", ") +
						". Ask the Director to add them via `colab hitl ask`."}
			}
			links = append(links, link)
		}
	}
	return links, nil
}

// summarize derives triggered/suppressed (colab-cli.md §2.2) from the openapi
// MessagePostResult: triggers[] → triggered; warnings[] whose code is exactly
// `suppressed_delegator` (rule 8) → suppressed. Other warning codes
// (not_participant · loop_limit_near · agent_disabled) stay in warnings[]
// only. A server that already sends `triggered`/`suppressed` wins.
func summarize(res *client.MessagePostResult, key string, replayed bool, names map[string]string) *MessagePostResult {
	out := &MessagePostResult{
		MessageID: res.Message.ID, Message: res.Message,
		Triggers: res.Triggers, Warnings: res.Warnings, SessionPaused: res.SessionPaused,
		IdempotencyKey: key, Replayed: replayed,
		Triggered: []string{}, Suppressed: []string{},
	}
	if out.Triggers == nil {
		out.Triggers = []client.Trigger{}
	}
	if out.Warnings == nil {
		out.Warnings = []client.Warning{}
	}
	label := func(id string) string {
		if n, ok := names[id]; ok && n != "" {
			return n
		}
		return id
	}
	if res.Triggered != nil {
		out.Triggered = res.Triggered
	} else {
		for _, t := range res.Triggers {
			out.Triggered = append(out.Triggered, label(t.AgentID))
		}
	}
	if res.Suppressed != nil {
		out.Suppressed = res.Suppressed
	} else {
		for _, w := range res.Warnings {
			if w.Code != client.WarningSuppressedDelegator {
				continue
			}
			if w.AgentID != nil && *w.AgentID != "" {
				out.Suppressed = append(out.Suppressed, label(*w.AgentID))
			} else {
				out.Suppressed = append(out.Suppressed, w.Message)
			}
		}
	}
	return out
}

// ErrorJSON renders an error as the JSON object the CLI/MCP emit.
func ErrorJSON(err error) map[string]any {
	e := client.AsError(err)
	m := map[string]any{"ok": false, "exit": e.Exit, "code": e.Code, "title": e.Title}
	if e.Detail != "" {
		m["detail"] = e.Detail
	}
	if e.Status != 0 {
		m["status"] = e.Status
	}
	if e.Problem != nil {
		m["problem"] = e.Problem
	}
	for k, v := range e.Extra {
		m[k] = v
	}
	return map[string]any{"error": m}
}

// MarshalIndent is a tiny helper shared by CLI and MCP output.
func MarshalIndent(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte(`{"error":{"code":"encode","title":"` + err.Error() + `"}}`)
	}
	return b
}
