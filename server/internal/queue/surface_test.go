package queue

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// shellCommand is a colab shell command in command position — a backtick
// then `colab ` then a word (harness §10's rewrite anchor, v0.8.1).
var shellCommand = regexp.MustCompile("`colab [a-z]")

// surfaceTexts is every sentence a Surface produces, by name — the struct's
// string fields (read by reflection, so a field added later is counted
// without editing this list) plus the rendered helpers that take the
// surface: Section2, ThreadRead, truncationNote and sessions.ReuseSection.
func surfaceTexts(t *testing.T, s Surface) map[string]string {
	t.Helper()
	out := map[string]string{}
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if f.Name == "Kind" || f.Name == "Header" || f.Type.Kind() != reflect.String {
			continue
		}
		out[f.Name] = v.Field(i).String()
	}
	out["Section2()"] = s.Section2()
	out["ThreadRead()"] = s.ThreadRead("m-1")
	out["truncationNote"] = truncationNote(3, roomHistory{}, false, s)
	out["ReuseSection"] = sessions.ReuseSection("t", strings.Repeat("요약 ", 50), sessions.ContextReusePlan{InjectedTokens: 5, TruncationDisclosed: true}, s.RoomMessages)
	if len(out) < 14 {
		t.Fatalf("surface census found %d texts — the struct or the helpers moved", len(out))
	}
	return out
}

// T-SURFACE (harness §10 v0.9.6): on the mcp surface (claude_code) no
// server-written sentence names a colab shell command — every one names a
// `colab_…` tool; on the cli_wrapper surface (hermes) every one names the
// shell command (the daemon rewrites it to the wrapper) and none a tool.
//
// 회귀 주입: mcpSurface 의 아무 칸이나 셸 문장으로 되돌리면 FAIL (예: 강등 줄
// threadRead 를 "`colab room messages --thread %s`" 로).
func TestSurfaceTextsSpeakTheirSurface(t *testing.T) {
	// DetailRule names arguments, not a command (the post line before it
	// names the command): `--flag` on the shell, the bare argument for mcp.
	argsOnly := map[string]bool{"DetailRule": true}
	for name, text := range surfaceTexts(t, SurfaceFor("claude_code")) {
		if shellCommand.MatchString(text) || strings.Contains(text, "`--") {
			t.Errorf("mcp %s names a shell command or flag: %q", name, text)
		}
		if argsOnly[name] {
			if !strings.Contains(text, "`body`") || !strings.Contains(text, "`detail_file`") {
				t.Errorf("mcp %s lacks the tool's arguments: %q", name, text)
			}
			continue
		}
		if !strings.Contains(text, "colab_") {
			t.Errorf("mcp %s names no colab tool: %q", name, text)
		}
	}
	for name, text := range surfaceTexts(t, SurfaceFor("hermes")) {
		if argsOnly[name] {
			if !strings.Contains(text, "`--body`") || !strings.Contains(text, "`--detail-file") || strings.Contains(text, "colab_") {
				t.Errorf("cli_wrapper %s lacks the flags or names a tool: %q", name, text)
			}
			continue
		}
		if !shellCommand.MatchString(text) {
			t.Errorf("cli_wrapper %s names no shell command: %q", name, text)
		}
		if strings.Contains(text, "colab_") {
			t.Errorf("cli_wrapper %s names an MCP tool the hermes agent does not have: %q", name, text)
		}
	}
}

// The same surface renders byte for byte (E12-11: [2] is part of the cached
// prefix), and the two surfaces differ.
func TestSurfaceSection2Stable(t *testing.T) {
	a, b := SurfaceFor("claude_code").Section2(), SurfaceFor("claude_code").Section2()
	if a != b || a == SurfaceFor("hermes").Section2() {
		t.Fatal("Section2 not deterministic per surface, or the surfaces do not differ")
	}
	if !strings.HasPrefix(a, "[2] ") || !strings.HasSuffix(a, "\n\n") {
		t.Fatalf("Section2 shape: %q", a)
	}
	_ = fmt.Sprint
}
