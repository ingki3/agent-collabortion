package colab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// PartArg is one part of a part message (colab-cli v0.9.5): the `--parts-file`
// JSON array's element and MCP `colab_message_post`'s `parts[]` element.
// `to` names the recipients the way `--mention` does (agent first, then the
// room's people, a mention link as is, `@all`); `body` is what is said to
// them; `detail` / `detail_file` are that part's work text (optional, not
// both — `detail_file` follows `--detail-file`'s rules).
// `attach` is that part's files (v0.9.6): artifact ids of the same room, at
// most MaxAttach — only that part's recipients see them, so the limit is per
// part, not per group.
type PartArg struct {
	To         []string `json:"to"`
	Body       string   `json:"body"`
	Detail     *string  `json:"detail,omitempty"`
	DetailFile string   `json:"detail_file,omitempty"`
	Attach     []string `json:"attach,omitempty"`
}

// Parts limits (openapi v0.3.6 MessageGroupCreate.parts minItems · maxItems).
const (
	MinParts = 2
	MaxParts = 6
)

// AllMentionLink is FR-3.2's `@all` link.
const AllMentionLink = "[@all](mention://all/all)"

// ReadPartsFile reads `--parts-file`: a JSON array of PartArg. A relative
// path is taken from the working folder, like `--detail-file`. Every failure
// is a usage error (exit 2) naming the path.
func ReadPartsFile(path string) ([]PartArg, error) {
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, client.Usage("--parts-file %s: working folder: %v", path, err)
		}
		path = filepath.Join(wd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, client.Usage("--parts-file: %v", err)
	}
	var parts []PartArg
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parts); err != nil {
		return nil, client.Usage("--parts-file %s: not a JSON array of {\"to\":[\"@Name\"],\"body\":\"…\",\"detail\"?,\"detail_file\"?,\"attach\"?}: %v", path, err)
	}
	return parts, nil
}

// MessageGroupResult is `message post --parts-file` / MCP `parts` output:
// the group id and, per part in order, the same summary a single post gives
// (message_id · triggered · suppressed · triggers · warnings).
type MessageGroupResult struct {
	GroupID        string              `json:"group_id"`
	Parts          []MessagePostResult `json:"parts"`
	IdempotencyKey string              `json:"idempotency_key"`
	Replayed       bool                `json:"replayed"`
}

// validatePartsArgs is the argument rules that need no server: 2~6 parts,
// each with a body and at least one recipient, detail and detail_file not
// both. The CLI and the MCP tool share it.
func validatePartsArgs(parts []PartArg) error {
	if len(parts) < MinParts || len(parts) > MaxParts {
		return client.Usage("parts: %d~%d parts, got %d — to one set of recipients, send one --body", MinParts, MaxParts, len(parts))
	}
	for i, p := range parts {
		if strings.TrimSpace(p.Body) == "" {
			return client.Usage("parts[%d].body is required", i)
		}
		if len(p.To) == 0 {
			return client.Usage("parts[%d].to is required: who this part is for, e.g. [\"@Designer\"]", i)
		}
		if p.Detail != nil && p.DetailFile != "" {
			return client.Usage("parts[%d]: detail and detail_file contradict each other: give one", i)
		}
		if p.Detail != nil && strings.TrimSpace(*p.Detail) == "" {
			return client.Usage("parts[%d].detail is empty: omit it or give the work text", i)
		}
	}
	return nil
}

// resolvePartTo turns one part's `to` into mention links: `@all` is FR-3.2's
// all link, anything else goes through resolveMentions (agent first, then a
// person, a link as is — the `--mention` rule, colab-cli v0.9.4).
func resolvePartTo(cc *client.CliContext, to []string) ([]string, error) {
	var out []string
	for _, raw := range to {
		name := strings.TrimPrefix(strings.TrimSpace(raw), "@")
		if name == "all" || strings.Contains(raw, "(mention://all/") {
			out = append(out, AllMentionLink)
			continue
		}
		links, err := resolveMentions(cc, []string{raw})
		if err != nil {
			return nil, err
		}
		out = append(out, links...)
	}
	return out, nil
}

// MessagePostParts — POST /rooms/{R}/message-groups (colab-cli v0.9.5). One
// post in parts under one Idempotency-Key; `--reply-to` / `--top-level` (and
// COLAB_THREAD_ID) apply to every part.
func MessagePostParts(ctx context.Context, c *client.Client, a MessagePostArgs) (*MessageGroupResult, error) {
	if a.TopLevel && a.ReplyTo != "" {
		return nil, client.Usage("--reply-to and --top-level contradict each other: give one")
	}
	parts := a.Parts
	if a.PartsFile != "" {
		if len(parts) > 0 {
			return nil, client.Usage("--parts-file and parts contradict each other: give one")
		}
		var err error
		if parts, err = ReadPartsFile(a.PartsFile); err != nil {
			return nil, err
		}
	}
	if err := validatePartsArgs(parts); err != nil {
		return nil, err
	}
	body := client.MessageGroupCreate{Parts: make([]client.MessagePartCreate, 0, len(parts))}
	for i, p := range parts {
		if p.DetailFile != "" {
			d, err := ReadDetailFile(p.DetailFile)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(d) == "" {
				return nil, client.Usage("parts[%d].detail_file %s is empty", i, p.DetailFile)
			}
			p.Detail = &d
		}
		// 부분마다 제 첨부 — attachIDs 가 단일 게시와 같은 규칙(uuid·중복 한 번·10개)을 적용한다.
		att, err := attachIDs(p.Attach)
		if err != nil {
			return nil, client.Usage("parts[%d].%s", i, strings.TrimPrefix(err.Error(), "--attach: "))
		}
		body.Parts = append(body.Parts, client.MessagePartCreate{Content: p.Body, Detail: p.Detail, AttachmentIDs: att})
	}
	if err := c.Allow(ctx, client.CmdMessagePost); err != nil {
		return nil, err
	}
	sid, err := c.RoomID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return nil, err
	}
	for i, p := range parts {
		links, err := resolvePartTo(cc, p.To)
		if err != nil {
			return nil, err
		}
		body.Parts[i].To = links
	}
	parent := a.ReplyTo
	if parent == "" && !a.TopLevel {
		parent = c.ThreadID(sid)
	}
	if parent != "" {
		body.ParentID = &parent
	}
	res, key, replayed, err := c.PostMessageGroup(ctx, sid, body, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	names := nameIndex(cc)
	out := &MessageGroupResult{GroupID: res.GroupID, IdempotencyKey: key, Replayed: replayed, Parts: make([]MessagePostResult, 0, len(res.Parts))}
	for i := range res.Parts {
		out.Parts = append(out.Parts, *summarize(&res.Parts[i], key, replayed, names))
	}
	return out, nil
}

// Post is `message post` for both shapes: parts (--parts-file / MCP parts)
// or one body. Giving both is a usage error (exit 2).
func Post(ctx context.Context, c *client.Client, a MessagePostArgs) (any, error) {
	if len(a.Parts) > 0 || a.PartsFile != "" {
		// v0.9.6: `attach` joins this list — the message-wide attachment has no
		// meaning when each recipient is sent a different part, so it belongs
		// inside a part (parts[].attach), never beside them.
		if strings.TrimSpace(a.Body) != "" || a.Detail != nil || a.DetailFile != "" || len(a.Mention) > 0 || len(a.Attach) > 0 {
			return nil, client.Usage("--parts-file (MCP parts) cannot be combined with --body, --detail, --detail-file, --mention or --attach: each part carries its own to, body, detail and attach")
		}
		return MessagePostParts(ctx, c, a)
	}
	return MessagePost(ctx, c, a)
}
