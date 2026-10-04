package main

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

// colab memory note|supersede|retire|get — colab-cli v0.9.12 §2.3 (the
// mission ledger, PRD FR-4.6) against the clienttest fake.

// memoryRequests counts the requests that reached a ledger path (anything
// but /cli/context).
func memoryRequests(s *clienttest.Server) int {
	n := 0
	for _, r := range s.Requests {
		if strings.Contains(r.URL.Path, "/memory") {
			n++
		}
	}
	return n
}

// Argument errors are exit 2 and send nothing: a kind outside the enum,
// --certainty on a non-fact, --outcome on a non-lesson, a missing content,
// and supersede --kind (kind is the target's — not a flag at all).
// 회귀 주입: MemoryNote 의 certainty·fact 검사를 지우면 "certainty on lesson"
// 행이 FAIL(서버로 나가 exit 0); outcome·lesson 검사를 지우면 "outcome on fact"
// 행이 FAIL; kind enum 검사를 지우면 "kind outside the enum" 행이 FAIL.
func TestMemoryNoteArgumentErrorsAreExit2(t *testing.T) {
	for name, argv := range map[string][]string{
		"kind outside the enum": {"memory", "note", "--kind", "rumour", "--content", "c"},
		"kind missing":          {"memory", "note", "--content", "c"},
		"content missing":       {"memory", "note", "--kind", "fact"},
		"certainty on lesson":   {"memory", "note", "--kind", "lesson", "--content", "c", "--certainty", "given"},
		"certainty on plan":     {"memory", "note", "--kind", "plan", "--content", "c", "--certainty", "guess"},
		"certainty not in enum": {"memory", "note", "--kind", "fact", "--content", "c", "--certainty", "sure"},
		"certainty empty":       {"memory", "note", "--kind", "fact", "--content", "c", "--certainty", ""},
		"outcome on fact":       {"memory", "note", "--kind", "fact", "--content", "c", "--outcome", "useful"},
		"outcome not in enum":   {"memory", "note", "--kind", "lesson", "--content", "c", "--outcome", "great"},
		"supersede --kind":      {"memory", "supersede", clienttest.MemoryID, "--content", "c", "--kind", "fact"},
		"supersede no id":       {"memory", "supersede", "--content", "c"},
		"supersede no content":  {"memory", "supersede", clienttest.MemoryID},
		"retire no reason":      {"memory", "retire", clienttest.MemoryID},
		"get bad status":        {"memory", "get", "--status", "gone"},
		"get bad kind":          {"memory", "get", "--kind", "rumour"},
		"unknown subcommand":    {"memory", "forget"},
		"no subcommand":         {"memory"},
	} {
		t.Run(name, func(t *testing.T) {
			s := clienttest.New(t)
			code, v, stderr := exec(t, s.Env(t.TempDir()), argv...)
			if code != client.ExitUsage {
				t.Fatalf("exit %d (%v %s), want 2", code, v, stderr)
			}
			if memoryRequests(s) != 0 {
				t.Fatalf("an argument error reached the server: %v", paths(s))
			}
		})
	}
}

// A valid note: the body is MemoryItemInput — certainty only on a fact,
// outcome only on a lesson, --source split into source_message_ids — and
// --idempotency-key is sent only when given.
func TestMemoryNoteSendsMemoryItemInput(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	code, v, stderr := exec(t, env, "memory", "note", "--kind", "fact", "--content", "경쟁사는 셋이다",
		"--certainty", "derived", "--source", "m1,m2", "--source", "m3")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	if v["kind"] != "fact" || v["certainty"] != "derived" || v["status"] != "active" {
		t.Fatalf("output = %v, want the MemoryItem", v)
	}
	if len(s.MemoryCalls) != 1 {
		t.Fatalf("calls = %+v", s.MemoryCalls)
	}
	call := s.MemoryCalls[0]
	if call.Op != "note" || call.Path != "/works/"+clienttest.WorkID+"/memory" || call.Key != "" {
		t.Fatalf("call = %+v (no --idempotency-key → no header)", call)
	}
	if call.Body["certainty"] != "derived" || call.Body["outcome"] != nil {
		t.Fatalf("body = %v", call.Body)
	}
	if src, _ := call.Body["source_message_ids"].([]any); len(src) != 3 || src[0] != "m1" || src[2] != "m3" {
		t.Fatalf("source_message_ids = %v", call.Body["source_message_ids"])
	}

	code, v, stderr = exec(t, env, "memory", "note", "--kind", "lesson", "--content", "캐시를 먼저 본다",
		"--outcome", "dead_end", "--idempotency-key", "k-1")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	call = s.MemoryCalls[1]
	if call.Body["outcome"] != "dead_end" || call.Body["certainty"] != nil || call.Key != "k-1" {
		t.Fatalf("lesson call = %+v", call)
	}
	if src, ok := call.Body["source_message_ids"].([]any); !ok || len(src) != 0 {
		t.Fatalf("source_message_ids without --source = %v, want []", call.Body["source_message_ids"])
	}
}

// Outside a mission (no COLAB_WORK_ID) note · get are exit 3 no_mission and
// send nothing — not even /cli/context; get --work names the mission itself.
// 회귀 주입: workOf 의 빈 W 검사를 지우면 FAIL(/works//memory 로 나간다).
func TestMemoryNoMission(t *testing.T) {
	for _, argv := range [][]string{
		{"memory", "note", "--kind", "fact", "--content", "c"},
		{"memory", "get"},
	} {
		s := clienttest.New(t)
		env := s.Env(t.TempDir())
		delete(env, "COLAB_WORK_ID")
		code, v, _ := exec(t, env, argv...)
		if code != client.ExitRefused || errCode(v) != "no_mission" {
			t.Fatalf("%v: exit %d code %q, want 3 no_mission", argv, code, errCode(v))
		}
		if len(s.Requests) != 0 {
			t.Fatalf("%v: no_mission sent %v", argv, paths(s))
		}
	}
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	delete(env, "COLAB_WORK_ID")
	code, v, stderr := exec(t, env, "memory", "get", "--work", clienttest.OtherWorkID, "--status", "all", "--kind", "fact")
	if code != 0 {
		t.Fatalf("get --work: exit %d %v %s", code, v, stderr)
	}
	call := s.MemoryCalls[0]
	if call.Path != "/works/"+clienttest.OtherWorkID+"/memory" || call.Query != "kind=fact&status=all" {
		t.Fatalf("call = %+v", call)
	}
	if v["work_id"] != clienttest.OtherWorkID || v["status"] != "all" {
		t.Fatalf("output = %v", v)
	}
	if items, _ := v["items"].([]any); len(items) != 1 {
		t.Fatalf("items = %v", v["items"])
	}
}

// memory get defaults to the turn's mission and status=active.
func TestMemoryGetDefaults(t *testing.T) {
	s := clienttest.New(t)
	code, v, stderr := exec(t, s.Env(t.TempDir()), "memory", "get")
	if code != 0 {
		t.Fatalf("exit %d %v %s", code, v, stderr)
	}
	if call := s.MemoryCalls[0]; call.Path != "/works/"+clienttest.WorkID+"/memory" || call.Query != "status=active" {
		t.Fatalf("call = %+v", call)
	}
}

// kind plan|progress: with the role KNOWN (context mode — getCliContext
// names it) and not lead, exit 3 memory_kind_forbidden before the POST; lead
// sends. 회귀 주입: MemoryNote 의 role 검사를 지우면 researcher 행이 FAIL.
func TestMemoryNotePlanKnownRole(t *testing.T) {
	for _, kind := range []string{"plan", "progress"} {
		s := clienttest.New(t)
		s.Role = "researcher"
		code, v, _ := exec(t, s.Env(t.TempDir()), "memory", "note", "--kind", kind, "--content", "다음은 B")
		if code != client.ExitRefused || errCode(v) != "memory_kind_forbidden" {
			t.Fatalf("%s as researcher: exit %d code %q, want 3 memory_kind_forbidden", kind, code, errCode(v))
		}
		if !onlyContext(s) || memoryRequests(s) != 0 {
			t.Fatalf("%s as researcher reached the server: %v", kind, paths(s))
		}
		e := v["error"].(map[string]any)
		if e["role"] != "researcher" || e["kind"] != kind || !strings.Contains(e["detail"].(string), "researcher") {
			t.Fatalf("error = %v", e)
		}

		lead := clienttest.New(t)
		lead.Role = "lead"
		if code, v, stderr := exec(t, lead.Env(t.TempDir()), "memory", "note", "--kind", kind, "--content", "다음은 B"); code != 0 || len(lead.MemoryCalls) != 1 {
			t.Fatalf("%s as lead: exit %d %v %s calls %d", kind, code, v, stderr, len(lead.MemoryCalls))
		}
	}
	// Other kinds never look at the role.
	s := clienttest.New(t)
	s.Role = "reviewer"
	if code, v, _ := exec(t, s.Env(t.TempDir()), "memory", "note", "--kind", "open_question", "--content", "q"); code != 0 {
		t.Fatalf("open_question as reviewer: exit %d %v", code, v)
	}
}

// Wrapper env mode (COLAB_ALLOWED_COMMANDS — no context read, role unknown):
// kind plan goes to the server, and its 403 memory_kind_forbidden is exit 3.
func TestMemoryNotePlanEnvModeLeavesItToTheServer(t *testing.T) {
	s := clienttest.New(t)
	s.Role = "researcher"
	s.MemoryForbidPlan = true
	env := s.Env(t.TempDir())
	env[client.EnvAllowedCommands] = "memory_note,memory_get"
	code, v, _ := exec(t, env, "memory", "note", "--kind", "plan", "--content", "다음은 B")
	if code != client.ExitRefused || errCode(v) != "memory_kind_forbidden" {
		t.Fatalf("exit %d code %q, want 3 memory_kind_forbidden from the server", code, errCode(v))
	}
	if e := v["error"].(map[string]any); e["status"] != float64(403) {
		t.Fatalf("status = %v, want the server's 403", e["status"])
	}
	if len(s.MemoryCalls) != 1 {
		t.Fatalf("env mode must send the note; calls = %+v", s.MemoryCalls)
	}
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			t.Fatal("env mode spent a /cli/context read")
		}
	}
}

// The gate: a ledger command outside the allowed list is exit 3
// command_not_allowed with nothing sent.
func TestMemoryCommandsAreGated(t *testing.T) {
	for _, argv := range [][]string{
		{"memory", "note", "--kind", "fact", "--content", "c"},
		{"memory", "supersede", clienttest.MemoryID, "--content", "c"},
		{"memory", "retire", clienttest.MemoryID, "--reason", "r"},
		{"memory", "get"},
	} {
		s := clienttest.New(t)
		env := s.Env(t.TempDir())
		env[client.EnvAllowedCommands] = "room_get"
		code, v, _ := exec(t, env, argv...)
		if code != client.ExitRefused || errCode(v) != client.ErrCodeCommandNotAllowed {
			t.Fatalf("%v: exit %d code %q", argv, code, errCode(v))
		}
		if len(s.Requests) != 0 {
			t.Fatalf("%v: refused command sent %v", argv, paths(s))
		}
	}
}

// supersede · retire: the paths and bodies of openapi, the output the
// server's; a target that is no longer active is 409 → exit 3
// memory_not_active.
func TestMemorySupersedeAndRetire(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	code, v, stderr := exec(t, env, "memory", "supersede", clienttest.MemoryID, "--content", "경쟁사는 넷이다", "--source", "m9", "--idempotency-key", "k-2")
	if code != 0 {
		t.Fatalf("supersede: exit %d %v %s", code, v, stderr)
	}
	item, _ := v["item"].(map[string]any)
	old, _ := v["superseded"].(map[string]any)
	if item["supersedes"] != clienttest.MemoryID || old["status"] != "superseded" {
		t.Fatalf("output = %v", v)
	}
	call := s.MemoryCalls[0]
	if call.Path != "/memory/"+clienttest.MemoryID+"/supersede" || call.Key != "k-2" || call.Body["content"] != "경쟁사는 넷이다" {
		t.Fatalf("call = %+v", call)
	}
	if _, has := call.Body["kind"]; has {
		t.Fatalf("supersede sent kind (422 kind_immutable): %v", call.Body)
	}

	code, v, stderr = exec(t, env, "memory", "retire", clienttest.MemoryID, "--reason", "틀렸다")
	if code != 0 || v["status"] != "retired" {
		t.Fatalf("retire: exit %d %v %s", code, v, stderr)
	}
	if call := s.MemoryCalls[1]; call.Path != "/memory/"+clienttest.MemoryID+"/retire" || call.Body["reason"] != "틀렸다" || call.Key != "" {
		t.Fatalf("retire call = %+v", call)
	}

	for _, argv := range [][]string{
		{"memory", "supersede", clienttest.RetiredMemoryID, "--content", "c"},
		{"memory", "retire", clienttest.RetiredMemoryID, "--reason", "r"},
	} {
		code, v, _ := exec(t, env, argv...)
		if code != client.ExitRefused || errCode(v) != "memory_not_active" {
			t.Fatalf("%v: exit %d code %q, want 3 memory_not_active", argv, code, errCode(v))
		}
	}
}
