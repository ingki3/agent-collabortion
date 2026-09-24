package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

// colab-cli.md v0.8 §2.4a through the CLI: room list · room read · work
// propose, and room get · room messages as the session commands' aliases.

func TestRoomList(t *testing.T) {
	s := clienttest.New(t)
	code, v, stderr := exec(t, s.Env(t.TempDir()), "room", "list", "--query", "경쟁사", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	items, _ := v["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != clienttest.OtherRoomID || items[0].(map[string]any)["via_link"] != false {
		t.Fatalf("items = %v", v)
	}
	if q := s.Requests[len(s.Requests)-1].URL.Query().Get("q"); q != "경쟁사" {
		t.Fatalf("--query sent as q=%q", q)
	}
	if code, v, _ := exec(t, s.Env(t.TempDir()), "room", "list", "--query", "없는 말"); code != 0 || len(v["items"].([]any)) != 0 {
		t.Fatalf("no match: exit %d %v — want an empty items list", code, v)
	}
}

func TestRoomReadCarriesTruncated(t *testing.T) {
	s := clienttest.New(t)
	s.RoomReadTruncated = true
	code, v, stderr := exec(t, s.Env(t.TempDir()), "room", "read", "--room", clienttest.OtherRoomID, "--tail", "5", "--query", "싸다")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	if v["truncated"] != true || v["summary"] != "세 곳을 비교했다" || len(v["messages"].([]any)) != 1 {
		t.Fatalf("read = %v, want the server's RoomReadResult with truncated:true as sent", v)
	}
	q := s.Requests[len(s.Requests)-1].URL.Query()
	if q.Get("tail") != "5" || q.Get("query") != "싸다" {
		t.Fatalf("query = %v", q)
	}
}

// A refusal is exit 3 room_read_denied with denied_reason top-level (§2.4a).
func TestRoomReadDeniedIsExit3WithReason(t *testing.T) {
	for _, reason := range []string{"originator_not_participant", "originator_left", "agent_not_allowed", "no_originator"} {
		s := clienttest.New(t)
		s.RoomReadDenied = reason
		code, v, stderr := exec(t, s.Env(t.TempDir()), "room", "read", "--room", clienttest.OtherRoomID)
		if code != client.ExitRefused || errCode(v) != "room_read_denied" {
			t.Fatalf("%s: exit %d code %q, want 3 room_read_denied", reason, code, errCode(v))
		}
		e := v["error"].(map[string]any)
		if e["denied_reason"] != reason || e["detail"] != "요청자가 그 방의 참여자가 아닙니다" {
			t.Fatalf("%s: error = %v", reason, e)
		}
		if !strings.Contains(stderr, "room_read_denied") {
			t.Fatalf("stderr = %q", stderr)
		}
	}
}

func TestRoomReadArgs(t *testing.T) {
	s := clienttest.New(t)
	for _, args := range [][]string{
		{"room", "read"}, {"room", "read", "--room", " "},
		{"room", "read", "--room", clienttest.OtherRoomID, "--tail", "0"},
		{"room", "read", "--room", clienttest.OtherRoomID, "--tail", "101"},
		{"room", "read", "--room", clienttest.OtherRoomID, "extra"},
		{"room"}, {"room", "nope"},
	} {
		if code, _, _ := exec(t, s.Env(t.TempDir()), args...); code != client.ExitUsage {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
	if len(s.Requests) != 0 {
		t.Fatalf("argument errors sent %v", paths(s))
	}
}

func TestWorkPropose(t *testing.T) {
	s := clienttest.New(t)
	code, v, stderr := exec(t, s.Env(t.TempDir()), "work", "propose", "--goal", "가격표 정리", "--why", "세 방에서 같은 질문이 나왔다")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	if v["proposal_id"] != clienttest.ProposalID || v["proposal"].(map[string]any)["status"] != "open" {
		t.Fatalf("result = %v", v)
	}
	if len(s.Proposals) != 1 || s.Proposals[0]["goal"] != "가격표 정리" || s.Proposals[0]["rationale"] != "세 방에서 같은 질문이 나왔다" {
		t.Fatalf("body = %v (--why is sent as rationale)", s.Proposals)
	}
	if !reached(s, "POST", "/rooms/"+clienttest.SessionID+"/work-proposals") {
		t.Fatalf("not sent to this turn's room: %v", paths(s))
	}
	for _, args := range [][]string{{"work"}, {"work", "open"}, {"work", "propose", "--goal", "g"}, {"work", "propose", "--why", "w"}, {"work", "propose", "--goal", " ", "--why", "w"}} {
		s := clienttest.New(t)
		if code, _, _ := exec(t, s.Env(t.TempDir()), args...); code != client.ExitUsage || len(s.Requests) != 0 {
			t.Fatalf("%v: exit %d, %d requests — want 2 and none", args, code, len(s.Requests))
		}
	}
}

// colab-cli.md v0.9 (R4): room get · room messages are the commands (the
// session group is gone). room get is getRoom + getWork + listRoomParticipants
// under one JSON; room messages reads /rooms/{R}/messages with --work.
func TestRoomGetAndMessages(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	code, v, _ := exec(t, env, "room", "get")
	if code != 0 || v["work"].(map[string]any)["goal"] != "Find 3 competitors" ||
		v["room"].(map[string]any)["isolation"].(map[string]any)["kind"] != "worktree" || len(v["participants"].([]any)) != 3 {
		t.Fatalf("room get: exit %d %v", code, v)
	}
	for _, p := range []string{"/rooms/" + clienttest.RoomID, "/works/" + clienttest.WorkID, "/rooms/" + clienttest.RoomID + "/participants"} {
		if !reached(s, "GET", p) {
			t.Fatalf("room get did not read %s: %v", p, paths(s))
		}
	}
	s.Messages = []map[string]any{{"id": "m1", "work_id": "w1"}, {"id": "m2", "work_id": "w2"}, {"id": "m3"}}
	code, v, _ = exec(t, env, "room", "messages")
	if code != 0 || v["included"] != float64(3) || v["room_id"] != clienttest.RoomID {
		t.Fatalf("room messages: exit %d %v", code, v)
	}
	code, v, _ = exec(t, env, "room", "messages", "--work", "w2", "--limit", "10")
	if code != 0 || v["included"] != float64(1) || v["items"].([]any)[0].(map[string]any)["id"] != "m2" {
		t.Fatalf("--work: exit %d %v", code, v)
	}
	if q := s.Requests[len(s.Requests)-1].URL.Query(); q.Get("work_id") != "w2" || q.Get("limit") != "10" {
		t.Fatalf("query = %v", q)
	}
	// v0.9.1: thread replies come by default (parent_id shown); --top-only
	// is the main timeline alone.
	s.Messages = []map[string]any{{"id": "m1", "parent_id": nil}, {"id": "r1", "parent_id": "m1"}}
	code, v, _ = exec(t, env, "room", "messages")
	if code != 0 || v["included"] != float64(2) || v["items"].([]any)[1].(map[string]any)["parent_id"] != "m1" {
		t.Fatalf("default must include replies: exit %d %v", code, v)
	}
	if q := s.Requests[len(s.Requests)-1].URL.Query(); q.Get("include_replies") != "true" {
		t.Fatalf("default query = %v", q)
	}
	code, v, _ = exec(t, env, "room", "messages", "--top-only")
	if code != 0 || v["included"] != float64(1) || v["items"].([]any)[0].(map[string]any)["id"] != "m1" {
		t.Fatalf("--top-only: exit %d %v", code, v)
	}
	if q := s.Requests[len(s.Requests)-1].URL.Query(); q.Get("include_replies") != "false" {
		t.Fatalf("--top-only query = %v", q)
	}
	// Gated as room_get: a list without it refuses before any request.
	env[client.EnvAllowedCommands] = "room_list"
	n := len(s.Requests)
	code, v, _ = exec(t, env, "room", "get")
	if code != client.ExitRefused || v["error"].(map[string]any)["command"] != "room_get" || len(s.Requests) != n {
		t.Fatalf("room get outside the list: exit %d %v", code, v)
	}
}

// MCP: the room tools call the same actions; a refused read is an isError
// tool result carrying denied_reason.
func TestMCPRoomTools(t *testing.T) {
	s := clienttest.New(t)
	s.RoomReadDenied = "originator_left"
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"colab_room_list","arguments":{}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"colab_room_read","arguments":{"room":"` + clienttest.OtherRoomID + `","tail":3}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"colab_work_propose","arguments":{"goal":"g","why":"w"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"colab_room_messages","arguments":{"work":"w1"}}}
`
	var out, errb bytes.Buffer
	if code := run([]string{"mcp", "serve"}, clienttest.Getenv(s.Env(t.TempDir())), strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 responses:\n%s", out.String())
	}
	var r [4]struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		}
	}
	for i := range lines {
		if err := json.Unmarshal([]byte(lines[i]), &r[i]); err != nil {
			t.Fatal(err)
		}
	}
	if r[0].Result.IsError || len(r[0].Result.StructuredContent["items"].([]any)) != 1 {
		t.Fatalf("room_list: %s", lines[0])
	}
	e, _ := r[1].Result.StructuredContent["error"].(map[string]any)
	if !r[1].Result.IsError || e["code"] != "room_read_denied" || e["denied_reason"] != "originator_left" || e["exit"] != float64(3) {
		t.Fatalf("room_read: %s", lines[1])
	}
	if r[2].Result.IsError || r[2].Result.StructuredContent["proposal_id"] != clienttest.ProposalID {
		t.Fatalf("work_propose: %s", lines[2])
	}
	if r[3].Result.IsError || !reached(s, "GET", "/rooms/"+clienttest.SessionID+"/messages") {
		t.Fatalf("room_messages: %s", lines[3])
	}
	for _, q := range s.Requests {
		if strings.HasSuffix(q.URL.Path, "/messages") && q.URL.Query().Get("work_id") != "w1" {
			t.Fatalf("room_messages work → work_id=%q", q.URL.Query().Get("work_id"))
		}
	}
}
