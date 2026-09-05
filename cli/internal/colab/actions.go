// Package colab implements the P1 commands of contracts/colab-cli.md §2 on top
// of client. The CLI (cmd/colab) and the MCP server (internal/mcp) both call
// these, so a tool's arguments and result are exactly the command's.
package colab

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// SessionGetArgs — `colab session get [--session S]` / colab_session_get.
type SessionGetArgs struct {
	Session string `json:"session,omitempty"`
}

// SessionGet — GET /sessions/{S}. The result is the Session object as the
// server sent it (goal · acceptance_criteria · completion_progress ·
// participants with derived status · isolation · director).
func SessionGet(ctx context.Context, c *client.Client, a SessionGetArgs) (map[string]any, error) {
	sid, err := c.SessionID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	return c.GetSession(ctx, sid)
}

// SessionMessagesArgs — `colab session messages [--since --limit --thread]`.
type SessionMessagesArgs struct {
	Session string `json:"session,omitempty"`
	Since   string `json:"since,omitempty"`  // after=<cursor|message id>
	Limit   int    `json:"limit,omitempty"`  // 1..200 (server default 50)
	Thread  string `json:"thread,omitempty"` // thread root id
}

// SessionMessagesResult adds the E8-12 included/total/truncated view.
type SessionMessagesResult struct {
	SessionID     string           `json:"session_id"`
	Items         []client.Message `json:"items"`
	Included      int              `json:"included"`
	Total         *int             `json:"total"`
	Truncated     bool             `json:"truncated"`
	BeforeCursor  *string          `json:"before_cursor"`
	AfterCursor   *string          `json:"after_cursor"`
	HasMoreBefore bool             `json:"has_more_before"`
	HasMoreAfter  bool             `json:"has_more_after"`
}

// SessionMessages — GET /sessions/{S}/messages.
func SessionMessages(ctx context.Context, c *client.Client, a SessionMessagesArgs) (*SessionMessagesResult, error) {
	sid, err := c.SessionID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	page, err := c.ListMessages(ctx, sid, client.MessagesQuery{Since: a.Since, Limit: a.Limit, Thread: a.Thread})
	if err != nil {
		return nil, err
	}
	items := page.Items
	if items == nil {
		items = []client.Message{}
	}
	return &SessionMessagesResult{
		SessionID: sid, Items: items, Included: len(items), Total: page.Total,
		Truncated:    page.HasMoreBefore || page.HasMoreAfter || (page.Total != nil && *page.Total > len(items)),
		BeforeCursor: page.BeforeCursor, AfterCursor: page.AfterCursor,
		HasMoreBefore: page.HasMoreBefore, HasMoreAfter: page.HasMoreAfter,
	}, nil
}

// MessagePostArgs — `colab message post --body [--reply-to --mention]`.
type MessagePostArgs struct {
	Session string   `json:"session,omitempty"`
	Body    string   `json:"body"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Mention []string `json:"mention,omitempty"` // agent names, with or without '@'
	// IdempotencyKey overrides the auto key (task:attempt:seq). Use it to
	// retry the *same* post after a network error.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
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

// MessagePost — POST /sessions/{S}/messages. Mentions are resolved to the
// participant's mention_link from /cli/context and prepended to the body;
// routing (rules 4 · 8) is the server's.
func MessagePost(ctx context.Context, c *client.Client, a MessagePostArgs) (*MessagePostResult, error) {
	if strings.TrimSpace(a.Body) == "" {
		return nil, client.Usage("--body is required")
	}
	sid, err := c.SessionID(ctx, a.Session)
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
		content = strings.Join(links, " ") + " " + content
		names = nameIndex(cc)
	}
	body := client.MessageCreate{Content: content}
	if a.ReplyTo != "" {
		r := a.ReplyTo
		body.ParentID = &r
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
					Detail: "not a session participant (FR-1.5). participants: " + strings.Join(known, ", ") +
						". Ask the Director to add them via `colab hitl ask`."}
			}
			links = append(links, link)
		}
	}
	return links, nil
}

// summarize derives triggered/suppressed (colab-cli.md §2.2) from the openapi
// MessagePostResult: triggers[] → triggered; warnings[] whose code mentions
// suppression (rule 8 delegator · non-participant) → suppressed. A server that
// already sends `triggered`/`suppressed` wins.
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
			if !strings.Contains(w.Code, "suppress") {
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
