package colab_test

// openapi v0.3.2 (D24, PRD FR-3.1.3): `room messages` carries the server's
// speech · addressees · responds_to_message_id · delegated_lane_id as sent, so
// an agent reading its room sees the same 「누가 → 누구에게 · 무엇을」 the web
// timeline does. The CLI does not interpret them.
//
// 회귀 주입: client.Message 의 Speech 칸 JSON 태그를 지우면 이 테스트가 FAIL.

import (
	"context"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

func TestRoomMessagesCarriesSpeech(t *testing.T) {
	s := clienttest.New(t)
	s.Messages = []map[string]any{
		{"id": "d1", "parent_id": nil, "content": "@R A 조사", "speech": "delegate",
			"addressees":        []any{map[string]any{"kind": "agent", "id": "11111111-1111-1111-1111-111111111111", "name": "R"}},
			"delegated_lane_id": "22222222-2222-2222-2222-222222222222"},
		{"id": "r1", "parent_id": nil, "content": "@Lead 끝", "speech": "report",
			"addressees":             []any{map[string]any{"kind": "agent", "id": "33333333-3333-3333-3333-333333333333", "name": "Lead"}},
			"responds_to_message_id": "d1"},
	}
	v, err := colab.RoomMessages(context.Background(), threadClient(t, s, ""), colab.RoomMessagesArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Items) != 2 || v.Items[0].Speech != "delegate" || v.Items[1].Speech != "report" {
		t.Fatalf("speech lost: %+v", v.Items)
	}
	if v.Items[0].DelegatedLaneID == nil || v.Items[1].RespondsToMessageID == nil || *v.Items[1].RespondsToMessageID != "d1" {
		t.Fatalf("delegated_lane_id / responds_to_message_id lost")
	}
	out := string(colab.MarshalIndent(v))
	for _, want := range []string{`"speech": "delegate"`, `"speech": "report"`, `"name": "R"`, `"name": "Lead"`, `"responds_to_message_id": "d1"`, `"delegated_lane_id": "22222222`} {
		if !strings.Contains(out, want) {
			t.Fatalf("--json output misses %s:\n%s", want, out)
		}
	}
}
