package colab_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

func newClient(t *testing.T, s *clienttest.Server) *client.Client {
	t.Helper()
	return client.New(client.FromEnv(clienttest.Getenv(s.Env(t.TempDir()))))
}

func TestSessionGet(t *testing.T) {
	s := clienttest.New(t)
	v, err := colab.SessionGet(context.Background(), newClient(t, s), colab.SessionGetArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if v["goal"] != "Find 3 competitors" || v["my_role"] != "member" {
		t.Fatalf("session = %v", v)
	}
	if parts, _ := v["participants"].([]any); len(parts) != 2 {
		t.Fatalf("participants = %v", v["participants"])
	}
}

// colab-cli.md §2.2: triggered/suppressed as agent names, derived from the
// openapi triggers[]/warnings[] shape.
func TestMessagePostMentionsTriggeredSuppressed(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	res, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{
		Body: "please review", Mention: []string{"@Reviewer,@Lead"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID == "" || res.Replayed {
		t.Fatalf("res = %+v", res)
	}
	if strings.Join(res.Triggered, ",") != "Reviewer" {
		t.Fatalf("triggered = %v", res.Triggered)
	}
	if strings.Join(res.Suppressed, ",") != "Lead" {
		t.Fatalf("suppressed = %v", res.Suppressed)
	}
	if res.IdempotencyKey != client.IdempotencyKey(clienttest.TaskID, clienttest.Attempt, 1) {
		t.Fatalf("key = %q", res.IdempotencyKey)
	}
	content, _ := s.Posted[0].Body["content"].(string)
	if !strings.HasPrefix(content, "[@Reviewer](mention://agent/"+clienttest.ReviewerID+") [@Lead](mention://agent/"+clienttest.DelegatorID+") please review") {
		t.Fatalf("content = %q", content)
	}
	if s.Posted[0].Body["parent_id"] != nil {
		t.Fatalf("parent_id should be absent, got %v", s.Posted[0].Body["parent_id"])
	}
}

func TestMessagePostReplyAndNoMention(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	env["COLAB_TASK_ATTEMPT"] = "2" // daemon-provided attempt: no /cli/context needed
	c := client.New(client.FromEnv(clienttest.Getenv(env)))
	res, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{Body: "status update", ReplyTo: "root-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Triggered) != 0 || len(res.Suppressed) != 0 || len(res.Triggers) != 0 {
		t.Fatalf("no mention must trigger nobody (rule 4): %+v", res)
	}
	if s.Posted[0].Body["parent_id"] != "root-1" {
		t.Fatalf("parent_id = %v", s.Posted[0].Body["parent_id"])
	}
	// No mention → no /cli/context round trip when env has session+task+attempt.
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			t.Fatalf("unexpected /cli/context call")
		}
	}
}

func TestMessagePostUnknownMention(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.MessagePost(context.Background(), newClient(t, s), colab.MessagePostArgs{Body: "x", Mention: []string{"@Nobody"}})
	e := client.AsError(err)
	if e.Exit != client.ExitUsage || e.Code != "unknown_mention" || !strings.Contains(e.Detail, "Reviewer") {
		t.Fatalf("err = %+v", e)
	}
	if len(s.Posted) != 0 {
		t.Fatalf("nothing should be posted")
	}
}

func TestMessagePostEmptyBody(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.MessagePost(context.Background(), newClient(t, s), colab.MessagePostArgs{Body: "  "})
	if client.ExitCode(err) != client.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionMessagesTruncated(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := colab.MessagePost(ctx, c, colab.MessagePostArgs{Body: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := colab.SessionMessages(ctx, c, colab.SessionMessagesArgs{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Included != 2 || res.Total == nil || *res.Total != 3 || !res.Truncated {
		t.Fatalf("res = %+v", res)
	}
	res, err = colab.SessionMessages(ctx, c, colab.SessionMessagesArgs{})
	if err != nil || res.Included != 3 || res.Truncated {
		t.Fatalf("res = %+v err=%v", res, err)
	}
}
