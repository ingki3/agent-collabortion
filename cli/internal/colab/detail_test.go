package colab_test

// colab-cli v0.9.2 — the work layer: `message post --detail` sends
// MessageCreate.detail as is (openapi v0.3.1), an absent one sends no key, a
// given-but-blank one is exit 2, and `room messages` carries Message.detail
// whole so an agent can read what its turn prompt cut to 400 characters.
//
// 회귀 주입: MessagePost 의 `Detail: a.Detail` 을 지우면 (d1) FAIL(CLI·MCP
// 테스트도 FAIL). client.Message 의 Detail 칸을 JSON 에서 빼면 이 패키지는
// 빌드가 깨지고 cmd/colab TestRoomReadsCarryDetail 이 FAIL.

import (
	"context"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// (d1) --detail reaches the request byte for byte (no trimming); body stays
// the content.
func TestMessagePostDetail(t *testing.T) {
	s := clienttest.New(t)
	d := "\n## 조사\n| A | 10 |\n\n"
	if _, err := colab.MessagePost(context.Background(), threadClient(t, s, ""), colab.MessagePostArgs{Body: "세 곳 비교 끝", Detail: &d}); err != nil {
		t.Fatal(err)
	}
	if got := s.Posted[0].Body["detail"]; got != d {
		t.Fatalf("detail = %q, want %q", got, d)
	}
	if got := s.Posted[0].Body["content"]; got != "세 곳 비교 끝" {
		t.Fatalf("content = %q", got)
	}
}

// (d2) No detail: no key at all (the server's minLength is 1); a blank one
// is exit 2 and nothing is posted.
func TestMessagePostDetailAbsentOrBlank(t *testing.T) {
	s := clienttest.New(t)
	if _, err := colab.MessagePost(context.Background(), threadClient(t, s, ""), colab.MessagePostArgs{Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Posted[0].Body["detail"]; ok {
		t.Fatalf("absent detail sent %v", s.Posted[0].Body["detail"])
	}
	for _, blank := range []string{"", " \n\t"} {
		b := blank
		_, err := colab.MessagePost(context.Background(), threadClient(t, s, ""), colab.MessagePostArgs{Body: "y", Detail: &b})
		if client.ExitCode(err) != client.ExitUsage || !strings.Contains(err.Error(), "--detail") {
			t.Fatalf("blank %q: err = %v, want exit %d", blank, err, client.ExitUsage)
		}
	}
	if len(s.Posted) != 1 {
		t.Fatalf("a blank detail reached the server (%d posts)", len(s.Posted))
	}
}

// (d3) room messages --thread returns each message's detail whole — a long
// one included (the turn prompt shows 400 characters and points here).
func TestRoomMessagesThreadCarriesDetail(t *testing.T) {
	s := clienttest.New(t)
	long := strings.Repeat("가나다라마바사 ", 3000) + "끝"
	s.Messages = []map[string]any{
		{"id": "root", "parent_id": nil, "content": "질문", "detail": nil},
		{"id": "r1", "parent_id": "root", "content": "답", "detail": long},
		{"id": "other", "parent_id": nil, "content": "딴 얘기"},
	}
	v, err := colab.RoomMessages(context.Background(), threadClient(t, s, ""), colab.RoomMessagesArgs{Thread: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Items) != 2 || v.Items[0].Detail != nil || v.Items[1].Detail == nil || *v.Items[1].Detail != long {
		t.Fatalf("items = %d, detail lost or cut", len(v.Items))
	}
	out := string(colab.MarshalIndent(v))
	if !strings.Contains(out, "끝") || strings.Count(out, "가나다라마바사") != 3000 {
		t.Fatalf("--json output does not carry the whole detail")
	}
}
