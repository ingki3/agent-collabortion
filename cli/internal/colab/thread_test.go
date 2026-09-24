package colab_test

// colab-cli v0.9.1 — where `message post` replies: --reply-to, else the
// turn's thread (COLAB_THREAD_ID), else the main timeline; --top-level skips
// the thread; both together is a usage error.
//
// 회귀 주입: MessagePost 의 `parent = c.ThreadID(sid)` 줄을 지우면 (c1) FAIL,
// `&& !a.TopLevel` 을 지우면 (c2) FAIL, 충돌 검사를 지우면 (c3) FAIL.

import (
	"context"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

const threadRoot = "55555555-5555-4555-8555-555555555555"

func threadClient(t *testing.T, s *clienttest.Server, thread string) *client.Client {
	t.Helper()
	env := s.Env(t.TempDir())
	if thread != "" {
		env["COLAB_THREAD_ID"] = thread
	}
	return client.New(client.FromEnv(clienttest.Getenv(env)))
}

// (c1) A thread turn's post defaults to that thread; a top-level turn's to
// the main timeline; --reply-to wins over both.
func TestMessagePostDefaultsToTheTurnThread(t *testing.T) {
	for _, tc := range []struct {
		name, env, replyTo string
		want               any
	}{
		{"thread turn", threadRoot, "", threadRoot},
		{"top-level turn", "", "", nil},
		{"explicit reply wins", threadRoot, "root-9", "root-9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clienttest.New(t)
			if _, err := colab.MessagePost(context.Background(), threadClient(t, s, tc.env), colab.MessagePostArgs{Body: "답", ReplyTo: tc.replyTo}); err != nil {
				t.Fatal(err)
			}
			if got := s.Posted[0].Body["parent_id"]; got != tc.want {
				t.Fatalf("parent_id = %v, want %v", got, tc.want)
			}
		})
	}
}

// (c2) --top-level posts to the main timeline even in a thread turn.
func TestMessagePostTopLevel(t *testing.T) {
	s := clienttest.New(t)
	if _, err := colab.MessagePost(context.Background(), threadClient(t, s, threadRoot), colab.MessagePostArgs{Body: "공지", TopLevel: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Posted[0].Body["parent_id"]; ok {
		t.Fatalf("--top-level sent parent_id %v", s.Posted[0].Body["parent_id"])
	}
}

// (c3) --reply-to with --top-level: exit 2 and nothing posted.
func TestMessagePostReplyAndTopLevelConflict(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.MessagePost(context.Background(), threadClient(t, s, threadRoot), colab.MessagePostArgs{Body: "x", ReplyTo: "root-1", TopLevel: true})
	if client.ExitCode(err) != client.ExitUsage {
		t.Fatalf("err = %v, want exit %d", err, client.ExitUsage)
	}
	if len(s.Posted) != 0 {
		t.Fatalf("a contradictory post reached the server")
	}
}

// The same body posted to the thread and then to the main timeline is two
// posts, not a retry: the derived key is per post (task:<id>:<seq>), so the
// second does not collide with — or replay — the first.
func TestSameBodyThreadThenTopLevelTwoKeys(t *testing.T) {
	s := clienttest.New(t)
	c := threadClient(t, s, threadRoot)
	a, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{Body: "같은 본문"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{Body: "같은 본문", TopLevel: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.IdempotencyKey == b.IdempotencyKey || b.Replayed || len(s.Posted) != 2 {
		t.Fatalf("keys %q/%q replayed=%v posted=%d, want two distinct posts", a.IdempotencyKey, b.IdempotencyKey, b.Replayed, len(s.Posted))
	}
	if s.Posted[0].Body["parent_id"] != threadRoot || s.Posted[1].Body["parent_id"] != nil {
		t.Fatalf("parents = %v / %v", s.Posted[0].Body["parent_id"], s.Posted[1].Body["parent_id"])
	}
}

// --session naming another room does not carry this turn's thread there (the
// fake answers another room's post with 404, so the decision is read off the
// client directly).
func TestThreadNotCarriedToAnotherRoom(t *testing.T) {
	c := threadClient(t, clienttest.New(t), threadRoot)
	if got := c.ThreadID(clienttest.RoomID); got != threadRoot {
		t.Fatalf("own room thread = %q, want %q", got, threadRoot)
	}
	if got := c.ThreadID(clienttest.OtherRoomID); got != "" {
		t.Fatalf("another room got this turn's thread %q", got)
	}
}
