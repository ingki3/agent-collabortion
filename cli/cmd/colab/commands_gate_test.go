package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

// K-19 (colab-cli.md v0.6 §2.5): the role's command subset is enforced
// before any request. This file drives every one of the 13 commands through
// the CLI against the §2.5 table PARSED OUT OF THE CONTRACT FILE — a row
// changed in the contract without a change here fails, and the other way
// round — and checks, for each (role, command): allowed → the command's own
// request reaches the server; not allowed → exit 3 `command_not_allowed`,
// the contract's sentence, and no request but the one cached /cli/context.

const contractPath = "../../../contracts/colab-cli.md"

// section25 parses the §2.5 table: role → set of allowed command names. The
// header row's role cells are split on "·" (researcher·writer·engineer is one
// column); a body row's first cell lists the commands it covers.
func section25(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	i := strings.Index(text, "### 2.5 ")
	if i < 0 {
		t.Fatal("colab-cli.md has no §2.5")
	}
	text = text[i:]
	if j := strings.Index(text, "\n## "); j > 0 {
		text = text[:j]
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "|") {
			lines = append(lines, l)
		}
	}
	if len(lines) < 3 {
		t.Fatalf("§2.5 table not found; got %d table lines", len(lines))
	}
	cells := func(l string) []string {
		parts := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	header := cells(lines[0])
	cmdRe := regexp.MustCompile("`([a-z_]+)`")
	out := map[string]map[string]bool{}
	for _, l := range lines[2:] { // skip header and |---| row
		row := cells(l)
		if len(row) != len(header) {
			t.Fatalf("§2.5 row has %d cells, header %d: %q", len(row), len(header), l)
		}
		var cmds []string
		for _, m := range cmdRe.FindAllStringSubmatch(row[0], -1) {
			cmds = append(cmds, m[1])
		}
		if len(cmds) == 0 {
			t.Fatalf("§2.5 row names no command: %q", l)
		}
		for col := 1; col < len(header); col++ {
			for _, role := range strings.Split(header[col], "·") {
				role = strings.TrimSpace(role)
				if out[role] == nil {
					out[role] = map[string]bool{}
				}
				switch row[col] {
				case "✓":
					for _, c := range cmds {
						out[role][c] = true
					}
				case "—":
				default:
					t.Fatalf("§2.5 cell %q for %s is neither ✓ nor —", row[col], role)
				}
			}
		}
	}
	return out
}

// invocation is how each ColabCommand is run and what request proves it
// reached the server (method + path suffix), all against the clienttest fake.
type invocation struct {
	args   []string
	method string
	path   string
}

func invocations(t *testing.T) map[client.Command]invocation {
	t.Helper()
	doc := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(doc, []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := "/sessions/" + clienttest.SessionID
	return map[client.Command]invocation{
		client.CmdSessionGet:         {[]string{"session", "get"}, "GET", sess},
		client.CmdSessionMessages:    {[]string{"session", "messages"}, "GET", sess + "/messages"},
		client.CmdArtifactGet:        {[]string{"artifact", "get", clienttest.ArtifactID}, "GET", "/artifacts/" + clienttest.ArtifactID},
		client.CmdMessagePost:        {[]string{"message", "post", "--body", "hi"}, "POST", sess + "/messages"},
		client.CmdStatusSet:          {[]string{"status", "set", "working"}, "POST", "/tasks/" + clienttest.TaskID + "/status"},
		client.CmdDecisionRecord:     {[]string{"decision", "record", "--summary", "s"}, "POST", sess + "/decisions"},
		client.CmdLaneDelegate:       {[]string{"lane", "delegate", "--agent", clienttest.ReviewerName, "--brief", "b"}, "POST", sess + "/lanes"},
		client.CmdArtifactSubmit:     {[]string{"artifact", "submit", "--type", "doc", "--file", doc}, "POST", sess + "/artifacts"},
		client.CmdReviewApprove:      {[]string{"review", "approve", "--artifact", clienttest.ArtifactID}, "POST", "/artifacts/" + clienttest.ArtifactID + "/review"},
		client.CmdReviewReject:       {[]string{"review", "reject", "--artifact", clienttest.ArtifactID, "--reason", "r"}, "POST", "/artifacts/" + clienttest.ArtifactID + "/review"},
		client.CmdHitlAsk:            {[]string{"hitl", "ask", "--question", "q", "--default", "d"}, "POST", sess + "/hitl-requests"},
		client.CmdHitlApproveRequest: {[]string{"hitl", "approve-request", "--summary", "s"}, "POST", sess + "/hitl-requests"},
		client.CmdHitlRequestInfo:    {[]string{"hitl", "request-info", "--what", "w"}, "POST", sess + "/hitl-requests"},
	}
}

func reached(s *clienttest.Server, method, suffix string) bool {
	for _, r := range s.Requests {
		if r.Method == method && strings.HasSuffix(r.URL.Path, suffix) {
			return true
		}
	}
	return false
}

// onlyContext reports whether every request the fake saw was GET /cli/context.
func onlyContext(s *clienttest.Server) bool {
	for _, r := range s.Requests {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/cli/context") {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for _, c := range client.AllCommands { // contract order, deterministic
		if m[string(c)] {
			out = append(out, string(c))
		}
	}
	return out
}

// Every role × every command, the list coming from getCliContext.
func TestGateMatchesSection25ViaCliContext(t *testing.T) {
	table := section25(t)
	if len(table) != 6 {
		t.Fatalf("§2.5 has %d roles, want 6 (lead researcher writer engineer reviewer custom): %v", len(table), table)
	}
	inv := invocations(t)
	if len(inv) != len(client.AllCommands) {
		t.Fatalf("%d invocations for %d commands", len(inv), len(client.AllCommands))
	}
	for role, allowed := range table {
		for _, cmd := range client.AllCommands {
			t.Run(role+"/"+string(cmd), func(t *testing.T) {
				s := clienttest.New(t)
				s.Role = role
				s.AllowedCommands = sortedKeys(allowed)
				code, v, stderr := exec(t, s.Env(t.TempDir()), inv[cmd].args...)
				if allowed[string(cmd)] {
					if code != 0 {
						t.Fatalf("allowed but exit %d: %v %s", code, v, stderr)
					}
					if !reached(s, inv[cmd].method, inv[cmd].path) {
						t.Fatalf("allowed but %s %s never reached the server", inv[cmd].method, inv[cmd].path)
					}
					return
				}
				if code != client.ExitRefused || errCode(v) != client.ErrCodeCommandNotAllowed {
					t.Fatalf("exit %d code %q, want 3 command_not_allowed: %v", code, errCode(v), v)
				}
				if !onlyContext(s) {
					t.Fatalf("a refused command reached the server: %v", paths(s))
				}
				e := v["error"].(map[string]any)
				wantDetail := "이 역할(" + role + ")은 " + cmd.CLIName() + " 를 쓸 수 없습니다"
				if e["detail"] != wantDetail {
					t.Fatalf("detail = %q, want %q", e["detail"], wantDetail)
				}
				if e["role"] != role || e["command"] != string(cmd) {
					t.Fatalf("role/command = %v/%v, want %s/%s", e["role"], e["command"], role, cmd)
				}
				got, _ := e["allowed"].([]any)
				if len(got) != len(s.AllowedCommands) {
					t.Fatalf("allowed = %v, want %v", got, s.AllowedCommands)
				}
				if !strings.Contains(stderr, wantDetail) {
					t.Fatalf("stderr %q lacks the sentence", stderr)
				}
			})
		}
	}
}

func paths(s *clienttest.Server) []string {
	var out []string
	for _, r := range s.Requests {
		out = append(out, r.Method+" "+r.URL.Path)
	}
	return out
}

// The §2.5 table names exactly the 13 ColabCommand values — no more (a name
// the enum lacks) and no fewer (a command the table forgot).
func TestSection25NamesEveryCommand(t *testing.T) {
	table := section25(t)
	named := map[string]bool{}
	for _, allowed := range table {
		for c := range allowed {
			named[c] = true
		}
	}
	// lead has everything, so its row is the full set.
	lead := table["lead"]
	for _, c := range client.AllCommands {
		if !lead[string(c)] {
			t.Errorf("§2.5 does not give lead %s (or does not name it)", c)
		}
	}
	for c := range named {
		if !client.IsCommand(c) {
			t.Errorf("§2.5 names %q, which is not a ColabCommand", c)
		}
	}
	if len(lead) != len(client.AllCommands) {
		t.Errorf("§2.5 names %d commands, enum has %d", len(lead), len(client.AllCommands))
	}
}

// COLAB_ALLOWED_COMMANDS (the daemon wrapper, harness §10) is THE list when
// set: it wins over getCliContext.allowed_commands both ways, and a refusal
// from it sends nothing at all — not even /cli/context.
func TestGateEnvWinsOverContext(t *testing.T) {
	inv := invocations(t)
	// env denies what the server would allow → exit 3, zero requests.
	s := clienttest.New(t)
	s.AllowedCommands = []string{"lane_delegate"}
	env := s.Env(t.TempDir())
	env[client.EnvAllowedCommands] = "session_get, message_post"
	code, v, _ := exec(t, env, inv[client.CmdLaneDelegate].args...)
	if code != client.ExitRefused || errCode(v) != client.ErrCodeCommandNotAllowed {
		t.Fatalf("exit %d code %q, want 3 command_not_allowed", code, errCode(v))
	}
	if len(s.Requests) != 0 {
		t.Fatalf("env-refused command sent %v; want none", paths(s))
	}
	e := v["error"].(map[string]any)
	if e["role"] != "" || e["detail"] != "이 역할은 lane delegate 를 쓸 수 없습니다" {
		t.Fatalf("without a context the role is unknown and the sentence drops it; got %v", e)
	}
	if got, _ := e["allowed"].([]any); len(got) != 2 || got[0] != "session_get" || got[1] != "message_post" {
		t.Fatalf("allowed = %v, want the env list, trimmed", got)
	}
	// env allows what the server would deny → the request goes out.
	s2 := clienttest.New(t)
	s2.AllowedCommands = []string{"session_get"}
	env2 := s2.Env(t.TempDir())
	env2[client.EnvAllowedCommands] = "lane_delegate,session_get"
	code, v, stderr := exec(t, env2, inv[client.CmdLaneDelegate].args...)
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	if !reached(s2, "POST", "/lanes") {
		t.Fatalf("env-allowed delegate never reached the server: %v", paths(s2))
	}
}

// A pre-v1.1 server omits allowed_commands, and a server may send an empty
// list: both mean no restriction (colab-cli.md §2.5 · daemon-protocol §4.1
// "비면 전부"). Likewise an env value that is all commas/space.
func TestGateOldServerAndEmptyListAllowEverything(t *testing.T) {
	inv := invocations(t)
	for name, setup := range map[string]func(s *clienttest.Server, env map[string]string){
		"field absent": func(*clienttest.Server, map[string]string) {},
		"empty list":   func(s *clienttest.Server, _ map[string]string) { s.AllowedCommands = []string{} },
		"empty env": func(s *clienttest.Server, env map[string]string) {
			s.AllowedCommands = []string{"session_get"}
			env[client.EnvAllowedCommands] = " , "
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cmd := range client.AllCommands {
				s := clienttest.New(t)
				env := s.Env(t.TempDir())
				setup(s, env)
				code, v, stderr := exec(t, env, inv[cmd].args...)
				if code != 0 || !reached(s, inv[cmd].method, inv[cmd].path) {
					t.Fatalf("%s: exit %d %v %s — want allowed", cmd, code, v, stderr)
				}
			}
		})
	}
}

// The gate's context read is the one cached read of the process: a command
// that also needs the roster (lane delegate) still makes exactly one.
func TestGateReusesTheOneContextRead(t *testing.T) {
	inv := invocations(t)
	s := clienttest.New(t)
	s.AllowedCommands = []string{"lane_delegate", "session_get"}
	if code, v, stderr := exec(t, s.Env(t.TempDir()), inv[client.CmdLaneDelegate].args...); code != 0 {
		t.Fatalf("exit %d: %v %s", code, v, stderr)
	}
	n := 0
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("/cli/context read %d times, want 1", n)
	}
}

// Server-side defence: a 403 command_not_allowed the server still returns
// (a list the CLI did not see, a newer table) is forwarded as exit 3 with the
// server's code, as any 403 is.
func TestServer403CommandNotAllowedIsExit3(t *testing.T) {
	inv := invocations(t)
	s := clienttest.New(t)
	s.Fail, s.FailCode = 403, client.ErrCodeCommandNotAllowed
	env := s.Env(t.TempDir())
	env[client.EnvAllowedCommands] = "lane_delegate"
	code, v, _ := exec(t, env, inv[client.CmdLaneDelegate].args...)
	if code != client.ExitRefused || errCode(v) != client.ErrCodeCommandNotAllowed {
		t.Fatalf("exit %d code %q, want 3 command_not_allowed from the server", code, errCode(v))
	}
	if e := v["error"].(map[string]any); e["status"] != float64(403) {
		t.Fatalf("status = %v, want 403 (the server's)", e["status"])
	}
}

// `colab mcp serve --allow a,b`: tools/list is the subset, a call outside it
// is the command_not_allowed tool result with nothing sent, an unknown name
// is reported on stderr and ignored, and the flag also feeds the gate (no
// /cli/context read to refuse).
func TestMCPServeAllowViaCLI(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"colab_lane_delegate","arguments":{"agent":"Lead","brief":"b"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"colab_session_get","arguments":{}}}
`
	var out, errb bytes.Buffer
	if code := run([]string{"mcp", "serve", "--allow", "session_get, review_approve,bogus"}, clienttest.Getenv(env), strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), `"bogus" is not a colab command; ignored`) {
		t.Fatalf("stderr = %q", errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 responses, got %d:\n%s", len(lines), out.String())
	}
	var list struct {
		Result struct{ Tools []struct{ Name string } }
	}
	if err := json.Unmarshal([]byte(lines[0]), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Result.Tools) != 2 || list.Result.Tools[0].Name != "colab_session_get" || list.Result.Tools[1].Name != "colab_review_approve" {
		t.Fatalf("tools/list = %+v", list.Result.Tools)
	}
	if !strings.Contains(lines[1], `"code":"command_not_allowed"`) || !strings.Contains(lines[1], `"isError":true`) {
		t.Fatalf("filtered tool call: %s", lines[1])
	}
	if !strings.Contains(lines[2], `"goal":"Find 3 competitors"`) {
		t.Fatalf("allowed tool call: %s", lines[2])
	}
	for _, r := range s.Requests {
		if strings.HasSuffix(r.URL.Path, "/cli/context") {
			t.Fatal("--allow is the gate list; no /cli/context read should be spent")
		}
	}
	// Flag errors are exit 2.
	if code := run([]string{"mcp", "serve", "extra"}, clienttest.Getenv(env), strings.NewReader(""), &out, &errb); code != client.ExitUsage {
		t.Fatalf("mcp serve extra: code=%d", code)
	}
}
