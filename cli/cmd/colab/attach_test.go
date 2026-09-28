package main

// colab-cli v0.9.6 through the CLI: `message post --attach` (repeatable,
// uuids only, repeats once, at most 10) and `room messages` carrying
// attachments.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

const (
	attA = "11111111-1111-4111-8111-111111111111"
	attB = "22222222-2222-4222-8222-222222222222"
)

func TestMessagePostAttach(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())

	code, v, stderr := exec(t, env, "message", "post", "--body", "시안 올렸습니다", "--attach", attA, "--attach", attB, "--attach", attA)
	if code != 0 {
		t.Fatalf("--attach: exit %d %v %s", code, v, stderr)
	}
	got := fmt.Sprint(s.Posted[0].Body["attachment_ids"])
	if got != "["+attA+" "+attB+"]" {
		t.Fatalf("attachment_ids sent %s, want [A B] in order with the repeat dropped", got)
	}
	if n := len(v["message"].(map[string]any)["attachments"].([]any)); n != 2 {
		t.Fatalf("result message.attachments = %d, want 2", n)
	}

	// No --attach: no key at all.
	if code, _, _ := exec(t, env, "message", "post", "--body", "짧은 말"); code != 0 {
		t.Fatalf("plain post exit %d", code)
	}
	if _, ok := s.Posted[1].Body["attachment_ids"]; ok {
		t.Fatalf("plain post sent attachment_ids %v", s.Posted[1].Body["attachment_ids"])
	}

	// room messages carries them verbatim.
	code, v, _ = exec(t, env, "room", "messages")
	if code != 0 {
		t.Fatalf("room messages exit %d %v", code, v)
	}
	first := v["items"].([]any)[0].(map[string]any)
	if atts, ok := first["attachments"].([]any); !ok || len(atts) != 2 {
		t.Fatalf("room messages lost attachments: %v", first["attachments"])
	}

	// Exit 2, nothing posted: a name instead of an id · eleven files.
	eleven := []string{"message", "post", "--body", "x"}
	for i := 0; i < 11; i++ {
		eleven = append(eleven, "--attach", fmt.Sprintf("%08d-0000-4000-8000-000000000000", i))
	}
	for _, args := range [][]string{
		{"message", "post", "--body", "x", "--attach", "touge_mock.png"},
		eleven,
	} {
		code, v, stderr := exec(t, env, args...)
		if code != client.ExitUsage || !strings.Contains(stderr, "--attach") {
			t.Fatalf("%v: exit %d %v %s, want %d", args[4:6], code, v, stderr, client.ExitUsage)
		}
	}
	if len(s.Posted) != 2 {
		t.Fatalf("a refused post reached the server (%d posts)", len(s.Posted))
	}
}
