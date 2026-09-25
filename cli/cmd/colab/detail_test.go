package main

// colab-cli v0.9.2 through the CLI: --detail · --detail-file (as is), both
// together exit 2, and room messages · room read carrying detail.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func TestMessagePostDetailFlags(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())

	code, v, stderr := exec(t, env, "message", "post", "--body", "요약", "--detail", "| A | 10 |")
	if code != 0 {
		t.Fatalf("--detail: exit %d %v %s", code, v, stderr)
	}
	if got := s.Posted[0].Body["detail"]; got != "| A | 10 |" {
		t.Fatalf("--detail sent %q", got)
	}
	if d := v["message"].(map[string]any)["detail"]; d != "| A | 10 |" {
		t.Fatalf("result message.detail = %v", d)
	}

	// --detail-file: the file's bytes, not trimmed.
	f := filepath.Join(t.TempDir(), "draft.md")
	want := "\n# 초안 전문\n\n표 | 값\n--|--\nx | 1\n\n\n"
	if err := os.WriteFile(f, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, v, stderr := exec(t, env, "message", "post", "--body", "초안 올림", "--detail-file", f); code != 0 {
		t.Fatalf("--detail-file: exit %d %v %s", code, v, stderr)
	}
	if got := s.Posted[1].Body["detail"]; got != want {
		t.Fatalf("--detail-file sent %q, want %q", got, want)
	}

	// No flag: no detail key.
	if code, _, _ := exec(t, env, "message", "post", "--body", "짧은 말"); code != 0 {
		t.Fatalf("plain post exit %d", code)
	}
	if _, ok := s.Posted[2].Body["detail"]; ok {
		t.Fatalf("plain post sent detail %v", s.Posted[2].Body["detail"])
	}

	// Exit 2, nothing posted: both flags · empty --detail · empty file ·
	// missing file.
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--detail", "a", "--detail-file", f},
		{"--detail", ""},
		{"--detail-file", empty},
		{"--detail-file", filepath.Join(t.TempDir(), "none.md")},
	} {
		code, v, stderr := exec(t, env, append([]string{"message", "post", "--body", "x"}, args...)...)
		if code != client.ExitUsage || !strings.Contains(stderr, "--detail") {
			t.Fatalf("%v: exit %d %v %s, want %d", args, code, v, stderr, client.ExitUsage)
		}
	}
	if len(s.Posted) != 3 {
		t.Fatalf("a refused post reached the server (%d posts)", len(s.Posted))
	}
}

// room messages --thread and room read put detail in the JSON whole; a
// posted detail comes back on the thread read.
func TestRoomReadsCarryDetail(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	long := strings.Repeat("조사 결과 줄\n", 5000)
	code, v, _ := exec(t, env, "message", "post", "--body", "결과 올림", "--detail", long)
	if code != 0 {
		t.Fatalf("post exit %d %v", code, v)
	}
	id := v["message_id"].(string)
	code, v, _ = exec(t, env, "room", "messages", "--thread", id)
	if code != 0 {
		t.Fatalf("room messages exit %d %v", code, v)
	}
	items := v["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["detail"] != long {
		t.Fatalf("room messages --thread lost or cut the detail")
	}

	code, v, _ = exec(t, env, "room", "read", "--room", clienttest.OtherRoomID)
	if code != 0 {
		t.Fatalf("room read exit %d %v", code, v)
	}
	if d := v["messages"].([]any)[0].(map[string]any)["detail"]; d != clienttest.RoomReadDetail {
		t.Fatalf("room read detail = %v", d)
	}
}
