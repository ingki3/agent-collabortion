package main

// colab-cli v0.9.5 through the CLI: `message post --parts-file <json>` —
// one request to /message-groups under one Idempotency-Key, `to` resolved
// like --mention (agent first, then a person, @all), a part's detail_file
// read as is, the single-body flags beside it exit 2, and `room messages
// --group` sends group= and prints group_id · group_index.
//
// 회귀 주입: runMessage 의 single-flag 검사를 끄면 (combo) FAIL; resolvePartTo 의
// @all 분기를 지우면 (all) FAIL(unknown_mention); PostMessageGroup 의 group 경로를
// /messages 로 바꾸면 (endpoint) FAIL; MessagesQuery.Group 의 v.Set 을 지우면
// (group query) FAIL.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func writeParts(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	p := filepath.Join(t.TempDir(), "parts.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMessagePostPartsFile(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	detail := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(detail, []byte("| 표 | 1 |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pf := writeParts(t, []map[string]any{
		{"to": []string{"@Simplist"}, "body": "v9 올렸습니다", "detail_file": detail},
		{"to": []string{"@Reviewer"}, "body": "스프라이트 검토 부탁"},
		{"to": []string{"@all"}, "body": "오늘 저녁 배포"},
	})
	code, v, stderr := exec(t, env, "message", "post", "--parts-file", pf)
	if code != 0 {
		t.Fatalf("exit %d %v %s", code, v, stderr)
	}
	// (endpoint) one group post, one key.
	if len(s.Posted) != 1 || !strings.HasSuffix(s.Requests[len(s.Requests)-1].URL.Path, "/message-groups") {
		t.Fatalf("posted %d, last %s", len(s.Posted), s.Requests[len(s.Requests)-1].URL.Path)
	}
	if s.Posted[0].Key != clienttest.Key(1) || s.Posted[0].ClientSeq != 1 {
		t.Fatalf("key %s seq %d, want one derived key for the whole post", s.Posted[0].Key, s.Posted[0].ClientSeq)
	}
	parts := s.Posted[0].Body["parts"].([]any)
	to := func(i int) string { b, _ := json.Marshal(parts[i].(map[string]any)["to"]); return string(b) }
	if !strings.Contains(to(0), "mention://user/"+clienttest.HumanID) {
		t.Fatalf("part 0 to = %s, want the person Simplist", to(0))
	}
	// Reviewer is both an agent and a person's name — the agent wins.
	if !strings.Contains(to(1), "mention://agent/"+clienttest.ReviewerID) {
		t.Fatalf("part 1 to = %s, want the agent Reviewer", to(1))
	}
	// (all)
	if !strings.Contains(to(2), "mention://all/all") {
		t.Fatalf("part 2 to = %s, want @all", to(2))
	}
	if parts[0].(map[string]any)["detail"] != "| 표 | 1 |\n" || parts[0].(map[string]any)["content"] != "v9 올렸습니다" {
		t.Fatalf("part 0 = %v", parts[0])
	}
	if _, ok := parts[1].(map[string]any)["detail"]; ok {
		t.Fatalf("part 1 sent a detail it did not have")
	}
	if v["group_id"] == "" || len(v["parts"].([]any)) != 3 {
		t.Fatalf("output = %v", v)
	}
	p1 := v["parts"].([]any)[1].(map[string]any)
	if tr := p1["triggered"].([]any); len(tr) != 1 || tr[0] != "Reviewer" {
		t.Fatalf("part 1 triggered = %v", p1["triggered"])
	}

	// room messages --group: group= is sent, group_id · group_index printed.
	gid := v["group_id"].(string)
	code, rv, stderr := exec(t, env, "room", "messages", "--group", gid)
	if code != 0 {
		t.Fatalf("room messages --group: %d %s", code, stderr)
	}
	// (group query)
	if q := s.Requests[len(s.Requests)-1].URL.Query(); q.Get("group") != gid {
		t.Fatalf("group query = %v", q)
	}
	its := rv["items"].([]any)
	if len(its) != 3 {
		t.Fatalf("items = %d", len(its))
	}
	for i, it := range its {
		m := it.(map[string]any)
		if m["group_id"] != gid || m["group_index"] != float64(i) {
			t.Fatalf("item %d group = %v/%v", i, m["group_id"], m["group_index"])
		}
	}
}

func TestMessagePostPartsUsage(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	good := writeParts(t, []map[string]any{{"to": []string{"@Reviewer"}, "body": "a"}, {"to": []string{"@Lead"}, "body": "b"}})
	one := writeParts(t, []map[string]any{{"to": []string{"@Reviewer"}, "body": "a"}})
	noTo := writeParts(t, []map[string]any{{"to": []string{}, "body": "a"}, {"to": []string{"@Lead"}, "body": "b"}})
	both := writeParts(t, []map[string]any{{"to": []string{"@Reviewer"}, "body": "a", "detail": "x", "detail_file": "y"}, {"to": []string{"@Lead"}, "body": "b"}})
	unknown := writeParts(t, []map[string]any{{"to": []string{"@Nobody"}, "body": "a"}, {"to": []string{"@Lead"}, "body": "b"}})
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte(`{"to":"x"}`), 0o600)
	for _, args := range [][]string{
		// (combo) the single-body flags are exit 2 beside --parts-file.
		{"--parts-file", good, "--body", "x"},
		{"--parts-file", good, "--detail", "x"},
		{"--parts-file", good, "--mention", "@Lead"},
		{"--parts-file", good, "--detail-file", good},
		{"--parts-file", good, "--reply-to", "m1", "--top-level"},
		{"--parts-file", ""},
		{"--parts-file", one},
		{"--parts-file", noTo},
		{"--parts-file", both},
		{"--parts-file", bad},
		{"--parts-file", filepath.Join(t.TempDir(), "none.json")},
		{"--parts-file", unknown},
	} {
		code, v, stderr := exec(t, env, append([]string{"message", "post"}, args...)...)
		if code != client.ExitUsage {
			t.Errorf("%v: exit %d %v %s, want %d", args, code, v, stderr, client.ExitUsage)
		}
	}
	if len(s.Posted) != 0 {
		t.Fatalf("a refused post reached the server (%d posts)", len(s.Posted))
	}
}
