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

// colab-cli.md v0.9: room get is getRoom + getWork + listRoomParticipants,
// and nothing the old getSession answer carried is lost — goal · criteria ·
// progress · director (work), isolation (room), roster with status.
func TestRoomGet(t *testing.T) {
	s := clienttest.New(t)
	v, err := colab.RoomGet(context.Background(), newClient(t, s), colab.RoomGetArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Room["name"] != "Market research" || v.Room["isolation"].(map[string]any)["kind"] != "worktree" {
		t.Fatalf("room = %v", v.Room)
	}
	if v.Work == nil || v.Work["goal"] != "Find 3 competitors" || v.Work["completion_progress"] == nil ||
		v.Work["director"].(map[string]any)["display_name"] != "Dana" || len(v.Work["acceptance_criteria"].([]any)) != 2 {
		t.Fatalf("work = %v", v.Work)
	}
	if len(v.Participants) != 3 || v.Participants[1]["status"] != "working" ||
		v.Participants[1]["agent"].(map[string]any)["role_description"] != "digs" {
		t.Fatalf("participants = %v", v.Participants)
	}
	var paths []string
	for _, r := range s.Requests {
		if r.URL.Path != "/api/v1/cli/context" { // the K-19 gate's one cached read
			paths = append(paths, r.URL.Path)
		}
	}
	want := []string{"/api/v1/rooms/" + clienttest.RoomID, "/api/v1/works/" + clienttest.WorkID, "/api/v1/rooms/" + clienttest.RoomID + "/participants"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("requests = %v, want %v", paths, want)
	}
}

// Outside a mission (no COLAB_WORK_ID) work is null and getWork is not
// called; COLAB_SESSION_ID alone still names the room.
func TestRoomGetOutsideMission(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	delete(env, "COLAB_WORK_ID")
	delete(env, "COLAB_ROOM_ID")
	c := client.New(client.FromEnv(clienttest.Getenv(env)))
	v, err := colab.RoomGet(context.Background(), c, colab.RoomGetArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Work != nil || v.Room["id"] != clienttest.RoomID || len(v.Participants) != 3 {
		t.Fatalf("v = %+v", v)
	}
	for _, r := range s.Requests {
		if strings.HasPrefix(r.URL.Path, "/api/v1/works/") {
			t.Fatalf("getWork called outside a mission: %s", r.URL.Path)
		}
	}
	b := string(colab.MarshalIndent(v))
	if !strings.Contains(b, `"work": null`) {
		t.Fatalf("json = %s", b)
	}
}

// --room naming another room does not attach this turn's mission to it.
func TestRoomGetOtherRoomNoWork(t *testing.T) {
	s := clienttest.New(t)
	_, err := colab.RoomGet(context.Background(), newClient(t, s), colab.RoomGetArgs{Room: clienttest.OtherRoomID})
	if client.ExitCode(err) != client.ExitRefused {
		t.Fatalf("err = %v", err)
	}
	for _, r := range s.Requests {
		if strings.HasPrefix(r.URL.Path, "/api/v1/works/") {
			t.Fatalf("getWork called for another room: %s", r.URL.Path)
		}
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
	if res.IdempotencyKey != clienttest.Key(1) {
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
	c := newClient(t, s) // env carries COLAB_TASK_ATTEMPT (daemon-provided)
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
	if _, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{Body: "second"}); err != nil {
		t.Fatal(err)
	}
	// /cli/context once per attempt (last_seq), not once per post.
	n := 0
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("/cli/context called %d times, want 1", n)
	}
}

// N1: suppressed[] is derived only from warnings whose code is exactly
// `suppressed_delegator`; a not_participant warning (E1-04) is not "suppressed".
func TestSuppressedExactCodeOnly(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	body := "[@Ghost](mention://agent/" + clienttest.OutsiderID + ") [@Lead](mention://agent/" + clienttest.DelegatorID + ") hi"
	res, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 2 || res.Warnings[0].Code != client.WarningSuppressedDelegator || res.Warnings[1].Code != client.WarningNotParticipant {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	if strings.Join(res.Suppressed, ",") != clienttest.Delegator {
		t.Fatalf("suppressed = %v (want only the rule-8 delegator)", res.Suppressed)
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

func TestRoomMessagesTruncated(t *testing.T) {
	s := clienttest.New(t)
	c := newClient(t, s)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := colab.MessagePost(ctx, c, colab.MessagePostArgs{Body: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	two := 2
	res, err := colab.RoomMessages(ctx, c, colab.RoomMessagesArgs{Limit: &two})
	if err != nil {
		t.Fatal(err)
	}
	if res.Included != 2 || res.Total == nil || *res.Total != 3 || !res.Truncated {
		t.Fatalf("res = %+v", res)
	}
	res, err = colab.RoomMessages(ctx, c, colab.RoomMessagesArgs{})
	if err != nil || res.Included != 3 || res.Truncated {
		t.Fatalf("res = %+v err=%v", res, err)
	}
	// N4: an explicit limit outside 1..200 (including 0) is exit 2, not "unset".
	for _, bad := range []int{0, -1, 201} {
		n := len(s.Requests)
		_, err := colab.RoomMessages(ctx, c, colab.RoomMessagesArgs{Limit: &bad})
		if client.ExitCode(err) != client.ExitUsage || len(s.Requests) != n {
			t.Fatalf("limit %d: err=%v requests=%d", bad, err, len(s.Requests)-n)
		}
	}
}
