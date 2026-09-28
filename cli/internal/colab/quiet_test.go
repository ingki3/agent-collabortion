package colab

// T-QUIET (harness v0.9.15, Lead 판정 2026-09-28): the CLI stdout and the MCP
// response of a post whose trigger was held carry one line — whom it did not
// wake — derived from warnings[] code approval_pending; a delegation from the
// task's queued_reason (DelegateResult has no warnings[]).
//
// 회귀 주입: summarize 의 approval_pending 가지를 빼면 (post) FAIL;
// LaneDelegate 의 queued_reason 줄을 빼면 (delegate) FAIL.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

const quietWant = "This mission is waiting for the Director's approval, so @Writer was not woken. If work remains after approval, tell the Director."

func TestQuietNoticeFromWarnings(t *testing.T) {
	wid := "agent-w"
	res := &client.MessagePostResult{
		Triggers: []client.Trigger{{AgentID: wid, TaskID: "t1", LaneID: "l1"}},
		Warnings: []client.Warning{{Code: client.WarningApprovalPending, Message: quietWant, AgentID: &wid}},
	}
	out := summarize(res, "k", false, map[string]string{wid: "Writer"})
	if out.Notice != quietWant {
		t.Fatalf("(post) notice = %q\nwant %q", out.Notice, quietWant)
	}
	// Without the roster the name is read back from the server's sentence.
	out = summarize(res, "k", false, map[string]string{})
	if out.Notice != quietWant {
		t.Fatalf("(post, no roster) notice = %q", out.Notice)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"notice":`) {
		t.Fatalf("(post) JSON has no notice: %s", b)
	}
	// Nothing held → no notice key at all.
	out = summarize(&client.MessagePostResult{Triggers: res.Triggers}, "k", false, nil)
	if b, _ := json.Marshal(out); strings.Contains(string(b), `"notice"`) {
		t.Fatalf("(post) notice on a post that held nothing: %s", b)
	}
}

func TestQuietNoticeName(t *testing.T) {
	if got := QuietNotice("@Writer"); got != quietWant {
		t.Fatalf("QuietNotice = %q", got)
	}
	if got := QuietNotice(); got != "" {
		t.Fatalf("QuietNotice() = %q, want empty", got)
	}
}
