package acp

import (
	"encoding/json"
	"strings"
)

// VerbFor maps an ACP tool_call kind to a task_event verb (harness §7).
func VerbFor(kind string) string {
	switch kind {
	case "edit":
		return "edit_file"
	case "execute":
		return "run_shell"
	case "read":
		return "read"
	case "search", "fetch":
		return "search"
	}
	return "use_tool"
}

// payloadKind maps an ACP kind onto the task_event tool.kind enum.
func payloadKind(kind string) string {
	switch kind {
	case "edit", "execute", "read", "search", "fetch", "think":
		return kind
	}
	return "other"
}

// toolState is what the runner remembers per toolCallId between tool_call
// and its tool_call_update(s).
type toolState struct {
	id       string
	kind     string
	title    string
	path     string
	command  string
	added    int
	removed  int
	exitCode *int
	summary  string
	done     bool
	startSeq int
}

func (t *toolState) absorb(u *Update) {
	if u.Kind != "" {
		t.kind = u.Kind
	}
	if u.Title != "" {
		t.title = u.Title
	}
	if len(u.Locations) > 0 && u.Locations[0].Path != "" {
		t.path = u.Locations[0].Path
	}
	if len(u.RawInput) > 0 {
		var in struct {
			Command  string `json:"command"`
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
		}
		if json.Unmarshal(u.RawInput, &in) == nil {
			if in.Command != "" {
				t.command = in.Command
			}
			if t.path == "" {
				if in.FilePath != "" {
					t.path = in.FilePath
				} else if in.Path != "" {
					t.path = in.Path
				}
			}
		}
	}
	for _, c := range u.ToolContents() {
		switch c.Type {
		case "diff":
			if c.Path != "" && t.path == "" {
				t.path = c.Path
			}
			t.added = countLines(c.NewText)
			if c.OldText != nil {
				t.removed = countLines(*c.OldText)
			} else {
				t.removed = 0
			}
		case "content":
			if c.Content != nil && c.Content.Text != "" {
				t.summary = clip(c.Content.Text, 2000)
			}
		}
	}
	if len(u.RawOutput) > 0 {
		var out struct {
			ExitCode *int `json:"exitCode"`
			Code     *int `json:"exit_code"`
		}
		if json.Unmarshal(u.RawOutput, &out) == nil {
			if out.ExitCode != nil {
				t.exitCode = out.ExitCode
			} else if out.Code != nil {
				t.exitCode = out.Code
			}
		}
	}
}

func (t *toolState) objectRef() string {
	if t.path != "" {
		return t.path
	}
	if t.command != "" {
		f := strings.Fields(t.command)
		if len(f) > 0 {
			return f[0]
		}
	}
	return clip(t.title, 512)
}

func (t *toolState) payload() map[string]any {
	p := map[string]any{"tool_call_id": t.id, "kind": payloadKind(t.kind)}
	if t.title != "" {
		p["title"] = clip(t.title, 512)
	}
	if t.path != "" {
		p["path"] = t.path
	}
	if t.kind == "edit" && (t.added > 0 || t.removed > 0) {
		p["lines_added"] = t.added
		p["lines_removed"] = t.removed
	}
	if t.command != "" {
		p["command"] = clip(t.command, 2000)
	}
	if t.exitCode != nil {
		p["exit_code"] = *t.exitCode
	}
	if t.summary != "" {
		p["summary"] = t.summary
	}
	return p
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ToolName extracts the Claude Code tool name from a permission request's
// toolCall (_meta.claudeCode.toolName), falling back to the title's first
// word. Used for the profile allow-list check (§4).
func ToolName(tc ToolCallRef) string {
	if len(tc.Meta) > 0 {
		var m struct {
			ClaudeCode struct {
				ToolName string `json:"toolName"`
			} `json:"claudeCode"`
		}
		if json.Unmarshal(tc.Meta, &m) == nil && m.ClaudeCode.ToolName != "" {
			return m.ClaudeCode.ToolName
		}
	}
	f := strings.Fields(tc.Title)
	if len(f) > 0 {
		return f[0]
	}
	return ""
}

// ModelMatches compares the profile model with the model the adapter
// reports: exact match after lower-casing and stripping a Hermes provider
// prefix ("anthropic:"), or an alias from ModelAliases. No substring
// matching — model_drift (harness §7) must not be masked (PR #20 N5).
func ModelMatches(profile, actual string) bool {
	p := stripProvider(profile)
	a := stripProvider(actual)
	if p == a {
		return true
	}
	if p == "" || a == "" {
		return false
	}
	for _, id := range ModelAliases[p] {
		if id == a {
			return true
		}
	}
	for _, id := range ModelAliases[a] {
		if id == p {
			return true
		}
	}
	return false
}

func stripProvider(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
