package main

// P2 command tests (contracts/colab-cli.md v0.4 §2.2·2.3) at the process
// boundary: argument parsing, exit codes and the JSON an agent parses.
//
// TestP2CommandAndMCPToolAgree is the DoD's MCP round trip: every P2 command
// is run twice against the same fake — once through `run()` and once as the
// same-named MCP tool over stdio — and the two JSON documents must be equal.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// ───────────────────────────── lane delegate ─────────────────────────────

func TestCLILaneDelegate(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()),
		"lane", "delegate", "--agent", "@Reviewer", "--brief", "check the numbers",
		"--depends-on", "lane-a,lane-b", "--depends-on", "lane-c")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["agent_id"] != clienttest.ReviewerID || v["agent_name"] != "Reviewer" || v["lane_id"] == "" {
		t.Fatalf("v = %v", v)
	}
	dep, _ := s.Delegations[0].Body["depends_on"].([]any)
	if len(dep) != 3 {
		t.Fatalf("depends_on = %v (repeatable and comma-separated)", dep)
	}
}

// E15-02 at the CLI boundary: exit 3, machine code, and the alternative
// route named in the JSON the agent reads.
func TestCLILaneDelegateNonParticipantExit3(t *testing.T) {
	s := clienttest.New(t)
	code, v, stderr := exec(t, s.Env(t.TempDir()), "lane", "delegate", "--agent", "Nobody", "--brief", "b")
	if code != client.ExitRefused {
		t.Fatalf("code = %d, want 3", code)
	}
	if errCode(v) != "not_participant" {
		t.Fatalf("code = %q, v = %v", errCode(v), v)
	}
	e, _ := v["error"].(map[string]any)
	detail, _ := e["detail"].(string)
	if !strings.Contains(detail, "hitl ask") {
		t.Fatalf("detail must name `colab hitl ask`, got %q", detail)
	}
	if !strings.Contains(stderr, "not_participant") {
		t.Fatalf("stderr should carry the one-line reason, got %q", stderr)
	}
	if len(s.Delegations) != 0 {
		t.Fatal("a non-participant delegation must not reach the server")
	}
}

func TestCLILaneDelegateUsage(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	for _, args := range [][]string{
		{"lane"},
		{"lane", "bogus"},
		{"lane", "delegate", "--brief", "b"},
		{"lane", "delegate", "--agent", "Reviewer"},
		{"lane", "delegate", "--agent", "Reviewer", "--brief", "b", "extra"},
	} {
		if code, _, _ := exec(t, env, args...); code != client.ExitUsage {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
	}
}

// ───────────────────────────── status set ─────────────────────────────

// E3-05 at the CLI boundary: `blocked` reports turn_end_required verbatim.
func TestCLIStatusSetBlocked(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()), "status", "set", "blocked", "--note", "what is the scope?")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["turn_end_required"] != true {
		t.Fatalf("turn_end_required = %v, want true", v["turn_end_required"])
	}
	if _, dup := v["end_turn"]; dup {
		t.Fatalf("the flag must have exactly one name, got both: %v", v)
	}
	if v["question_message_id"] == nil {
		t.Fatalf("question_message_id = nil, want the posted card (E3-05)")
	}
	if s.StatusCalls[0].Body["note"] != "what is the scope?" {
		t.Fatalf("note = %v", s.StatusCalls[0].Body["note"])
	}
}

// The status word may also come from --status, and `working`/`done` work.
func TestCLIStatusSetWordForms(t *testing.T) {
	for _, args := range [][]string{
		{"status", "set", "done"},
		{"status", "set", "--status", "done"},
		{"status", "set", "working", "--note", "still reading"},
	} {
		s := clienttest.New(t)
		if code, v, _ := exec(t, s.Env(t.TempDir()), args...); code != client.ExitOK {
			t.Fatalf("%v: code=%d v=%v", args, code, v)
		}
		if len(s.StatusCalls) != 1 {
			t.Fatalf("%v: status calls = %d", args, len(s.StatusCalls))
		}
	}
}

func TestCLIStatusSetUsage(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	for _, args := range [][]string{
		{"status"},
		{"status", "get"},
		{"status", "set"},
		{"status", "set", "blocked"},          // no --note
		{"status", "set", "sideways"},         // not in the enum
		{"status", "set", "done", "leftover"}, // stray positional
	} {
		if code, _, _ := exec(t, env, args...); code != client.ExitUsage {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
	}
	if len(s.StatusCalls) != 0 {
		t.Fatal("an argument error must not reach the server")
	}
}

// ───────────────────────────── decision record ─────────────────────────────

func TestCLIDecisionRecord(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()),
		"decision", "record", "--summary", "use Postgres", "--rationale", "already operated in-house")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["decision_id"] == "" {
		t.Fatalf("v = %v", v)
	}
	b := s.Decisions[0]
	if b["summary"] != "use Postgres" || b["rationale"] != "already operated in-house" || len(b) != 2 {
		t.Fatalf("body must be exactly {summary, rationale}: %v", b)
	}
}

// --title/--body remain as aliases of --summary/--rationale.
func TestCLIDecisionRecordAliases(t *testing.T) {
	s := clienttest.New(t)
	if code, v, _ := exec(t, s.Env(t.TempDir()),
		"decision", "record", "--title", "t", "--body", "r"); code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if b := s.Decisions[0]; b["summary"] != "t" || b["rationale"] != "r" {
		t.Fatalf("body = %v", b)
	}
}

// --options/--chosen were dropped in v0.4: they must not silently succeed.
func TestCLIDecisionRecordDroppedFlags(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	for _, flag := range []string{"--options", "--chosen"} {
		code, _, _ := exec(t, env, "decision", "record", "--summary", "s", flag, "a")
		if code != client.ExitUsage {
			t.Fatalf("%s: code = %d, want 2 (the flag no longer exists)", flag, code)
		}
	}
	if len(s.Decisions) != 0 {
		t.Fatal("nothing may be recorded")
	}
}

// ───────────────────────────── artifact ─────────────────────────────

func TestCLIArtifactSubmit(t *testing.T) {
	s := clienttest.New(t)
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("# hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, v, _ := exec(t, s.Env(t.TempDir()),
		"artifact", "submit", "--type", "report", "--file", path, "--description", "first cut")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["artifact_id"] != clienttest.ArtifactID || v["name"] != "report.md" {
		t.Fatalf("v = %v", v)
	}
	if f := s.Submissions[0].Fields; f["type"] != "report" || f["description"] != "first cut" {
		t.Fatalf("fields = %v", f)
	}
}

// `--type diff` with no --file diffs the process's OWN working directory —
// the workdir the daemon spawned this attempt in (harness.md §5). There is no
// flag that points it anywhere else, which is what keeps a lane out of
// another lane's worktree (FR-6.1, scenario B step 4).
func TestCLIArtifactSubmitDiffUsesProcessWorkdir(t *testing.T) {
	s := clienttest.New(t)
	dir := cliDiffRepo(t)
	t.Chdir(dir)

	code, v, _ := exec(t, s.Env(t.TempDir()), "artifact", "submit", "--type", "diff", "--description", "탈퇴 화면")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["name"] != "frontend.diff" {
		t.Fatalf("name = %v, want the branch's last segment", v["name"])
	}
	sub := s.Submissions[0]
	if !strings.HasPrefix(sub.Fields["description"], "diff colab/S/frontend@") ||
		!strings.HasSuffix(sub.Fields["description"], "\n탈퇴 화면") {
		t.Fatalf("description = %q", sub.Fields["description"])
	}
	if !strings.HasPrefix(string(sub.Data), "# colab-diff: branch=colab/S/frontend base=main commit=") {
		t.Fatalf("body head = %q", strings.SplitN(string(sub.Data), "\n", 2)[0])
	}
	if !strings.Contains(string(sub.Data), "+worktree change") {
		t.Fatalf("body = %s", sub.Data)
	}
	// A clean workdir has nothing to submit: exit 2 and no second request.
	if _, err := gitCLI(t, dir, "checkout", "--", "."); err != nil {
		t.Fatal(err)
	}
	code, v, _ = exec(t, s.Env(t.TempDir()), "artifact", "submit", "--type", "diff", "--base", "HEAD")
	if code != client.ExitUsage || errCode(v) != "empty_diff" {
		t.Fatalf("clean workdir: code=%d v=%v", code, v)
	}
	if len(s.Submissions) != 1 {
		t.Fatalf("submissions = %d", len(s.Submissions))
	}
}

// cliDiffRepo is a worktree in the state E16-B step 3 finds: an agent branch
// off `main` with an uncommitted change.
func cliDiffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitCLI(t, dir, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	mustGit := func(args ...string) {
		t.Helper()
		if _, err := gitCLI(t, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustGit("symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "a.txt")
	mustGit("commit", "-qm", "base")
	mustGit("checkout", "-q", "-b", "colab/S/frontend")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nworktree change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func gitCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=colab", "GIT_AUTHOR_EMAIL=colab@example.com",
		"GIT_COMMITTER_NAME=colab", "GIT_COMMITTER_EMAIL=colab@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// --path is the alias; --url no longer exists (v0.4: an absent flag tells
// the truth about an absent feature).
func TestCLIArtifactSubmitPathAliasAndNoURL(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, v, _ := exec(t, env, "artifact", "submit", "--type", "doc", "--path", path); code != client.ExitOK {
		t.Fatalf("--path alias: code=%d v=%v", code, v)
	}
	if code, _, _ := exec(t, env, "artifact", "submit", "--type", "link", "--url", "https://x"); code != client.ExitUsage {
		t.Fatalf("--url: code = %d, want 2 (the flag does not exist)", code)
	}
	if len(s.Submissions) != 1 {
		t.Fatalf("submissions = %d, want only the --path one", len(s.Submissions))
	}
}

func TestCLIArtifactGet(t *testing.T) {
	s := clienttest.New(t)
	dest := filepath.Join(t.TempDir(), "saved.md")
	code, v, _ := exec(t, s.Env(t.TempDir()), "artifact", "get", clienttest.ArtifactID, "--out", dest)
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["saved_to"] != dest {
		t.Fatalf("saved_to = %v", v["saved_to"])
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != clienttest.ArtifactBody {
		t.Fatalf("saved = %q (%v)", got, err)
	}
}

func TestCLIArtifactUsage(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	for _, args := range [][]string{
		{"artifact"},
		{"artifact", "bogus"},
		{"artifact", "get"},
		{"artifact", "submit", "--file", "/nope"},
		{"artifact", "get", clienttest.ArtifactID, "extra"},
	} {
		if code, _, _ := exec(t, env, args...); code != client.ExitUsage {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
	}
}

// ───────────────────────────── review ─────────────────────────────

func TestCLIReviewApproveReject(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	if code, v, _ := exec(t, env, "review", "approve", "--artifact", clienttest.ArtifactID, "--note", "ok"); code != client.ExitOK {
		t.Fatalf("approve: code=%d v=%v", code, v)
	}
	code, v, _ := exec(t, env, "review", "reject", "--artifact", clienttest.ArtifactID, "--reason", "stale source")
	if code != client.ExitOK {
		t.Fatalf("reject: code=%d v=%v", code, v)
	}
	if v["verdict"] != "reject" || v["message"] == nil {
		t.Fatalf("v = %v", v)
	}
	// Both flags land in `comments`.
	if s.Reviews[0].Body["comments"] != "ok" || s.Reviews[1].Body["comments"] != "stale source" {
		t.Fatalf("reviews = %+v", s.Reviews)
	}
}

// E6-06 at the CLI boundary.
func TestCLIReviewNotReviewerExit3(t *testing.T) {
	s := clienttest.New(t)
	s.NotDesignatedReviewer = true
	code, v, _ := exec(t, s.Env(t.TempDir()), "review", "approve", "--artifact", clienttest.ArtifactID)
	if code != client.ExitRefused {
		t.Fatalf("code = %d, want 3", code)
	}
	if errCode(v) != "not_reviewer" {
		t.Fatalf("code = %q, v = %v", errCode(v), v)
	}
	if len(s.Reviews) != 0 {
		t.Fatal("nothing may be stored")
	}
}

func TestCLIReviewUsage(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	for _, args := range [][]string{
		{"review"},
		{"review", "maybe", "--artifact", clienttest.ArtifactID},
		{"review", "approve"}, // no --artifact
		{"review", "reject", "--artifact", clienttest.ArtifactID}, // no --reason
		{"review", "reject", "--reason", "r"},                     // no --artifact
	} {
		if code, _, _ := exec(t, env, args...); code != client.ExitUsage {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
	}
}

// ─────────────────── command ↔ MCP tool round trip (DoD) ───────────────────

// mcpCall runs one tools/call over `colab mcp serve`'s stdio and returns the
// tool's structuredContent — the same object the CLI writes to stdout.
func mcpCall(t *testing.T, env map[string]string, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	in := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", tool, argsJSON)
	var out, errb bytes.Buffer
	if code := run([]string{"mcp", "serve"}, clienttest.Getenv(env), strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("mcp serve: code=%d stderr=%s", code, errb.String())
	}
	var r struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("mcp response: %v\n%s", err, out.String())
	}
	if len(r.Error) > 0 {
		t.Fatalf("%s: protocol error %s", tool, r.Error)
	}
	// The text content must be the same document as structuredContent.
	var fromText map[string]any
	if len(r.Result.Content) == 0 || json.Unmarshal([]byte(r.Result.Content[0].Text), &fromText) != nil {
		t.Fatalf("%s: content[0].text is not the result JSON: %+v", tool, r.Result.Content)
	}
	if !equalJSON(t, fromText, r.Result.StructuredContent) {
		t.Fatalf("%s: content text and structuredContent disagree", tool)
	}
	return r.Result.StructuredContent, r.Result.IsError
}

// scrubTimestamps removes wall-clock fields anywhere in the document; every
// other value the fake produces is deterministic and must match.
func scrubTimestamps(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch k {
			case "created_at", "reviewed_at", "edited_at", "expires_at", "met_at":
				delete(t, k)
			default:
				scrubTimestamps(child)
			}
		}
	case []any:
		for _, child := range t {
			scrubTimestamps(child)
		}
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

// contracts/colab-cli.md §3: "인자·반환은 CLI와 동일한 JSON" — each MCP tool
// must produce exactly what its command produces, successes and refusals
// alike. Each case runs against a fresh fake so the two calls see identical
// server state.
func TestP2CommandAndMCPToolAgree(t *testing.T) {
	file := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(file, []byte("# hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outA, outB := filepath.Join(t.TempDir(), "a.md"), filepath.Join(t.TempDir(), "b.md")

	cases := []struct {
		name     string
		argv     []string
		tool     string
		args     map[string]any
		setup    func(*clienttest.Server)
		wantExit int
		// drop names top-level fields that legitimately differ between the
		// two runs (only the --out path, which is deliberately different).
		drop []string
	}{
		{
			name: "lane delegate", tool: "colab_lane_delegate",
			argv: []string{"lane", "delegate", "--agent", "Reviewer", "--brief", "check", "--depends-on", "l1,l2"},
			args: map[string]any{"agent": "Reviewer", "brief": "check", "depends_on": []string{"l1", "l2"}},
		},
		{
			name: "lane delegate refused (E15-02)", tool: "colab_lane_delegate",
			argv:     []string{"lane", "delegate", "--agent", "Nobody", "--brief", "check"},
			args:     map[string]any{"agent": "Nobody", "brief": "check"},
			wantExit: client.ExitRefused,
		},
		{
			name: "status set blocked (E3-05)", tool: "colab_status_set",
			argv: []string{"status", "set", "blocked", "--note", "scope?"},
			args: map[string]any{"status": "blocked", "note": "scope?"},
		},
		{
			name: "status set done", tool: "colab_status_set",
			argv: []string{"status", "set", "done"},
			args: map[string]any{"status": "done"},
		},
		{
			name: "decision record", tool: "colab_decision_record",
			argv: []string{"decision", "record", "--summary", "use Postgres", "--rationale", "in-house"},
			args: map[string]any{"summary": "use Postgres", "rationale": "in-house"},
		},
		{
			name: "artifact submit", tool: "colab_artifact_submit",
			argv: []string{"artifact", "submit", "--type", "report", "--file", file},
			args: map[string]any{"type": "report", "file": file},
		},
		{
			name: "artifact get", tool: "colab_artifact_get",
			argv: []string{"artifact", "get", clienttest.ArtifactID, "--out", outA},
			args: map[string]any{"artifact": clienttest.ArtifactID, "out": outB},
			drop: []string{"saved_to"}, // the two runs write to different paths on purpose
		},
		{
			name: "review approve", tool: "colab_review_approve",
			argv: []string{"review", "approve", "--artifact", clienttest.ArtifactID, "--note", "ok"},
			args: map[string]any{"artifact": clienttest.ArtifactID, "note": "ok"},
		},
		{
			name: "review reject", tool: "colab_review_reject",
			argv: []string{"review", "reject", "--artifact", clienttest.ArtifactID, "--reason", "stale"},
			args: map[string]any{"artifact": clienttest.ArtifactID, "reason": "stale"},
		},
		{
			name: "review refused (E6-06)", tool: "colab_review_approve",
			argv:     []string{"review", "approve", "--artifact", clienttest.ArtifactID},
			args:     map[string]any{"artifact": clienttest.ArtifactID},
			setup:    func(s *clienttest.Server) { s.NotDesignatedReviewer = true },
			wantExit: client.ExitRefused,
		},
		{
			name: "revoked token (E11-04)", tool: "colab_status_set",
			argv:     []string{"status", "set", "done"},
			args:     map[string]any{"status": "done"},
			setup:    func(s *clienttest.Server) { s.Revoked = true },
			wantExit: client.ExitNoToken,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sCLI := clienttest.New(t)
			if tc.setup != nil {
				tc.setup(sCLI)
			}
			code, cli, _ := exec(t, sCLI.Env(t.TempDir()), tc.argv...)
			if code != tc.wantExit {
				t.Fatalf("CLI exit = %d, want %d: %v", code, tc.wantExit, cli)
			}

			sMCP := clienttest.New(t)
			if tc.setup != nil {
				tc.setup(sMCP)
			}
			tool, isErr := mcpCall(t, sMCP.Env(t.TempDir()), tc.tool, tc.args)
			if isErr != (tc.wantExit != client.ExitOK) {
				t.Fatalf("tool isError = %v, want %v", isErr, tc.wantExit != client.ExitOK)
			}
			// A refusal carries the same error object, exit code included.
			if tc.wantExit != client.ExitOK {
				e, _ := tool["error"].(map[string]any)
				if e == nil || e["exit"] != float64(tc.wantExit) {
					t.Fatalf("tool error = %v, want exit %d", tool, tc.wantExit)
				}
			}
			for _, f := range tc.drop {
				delete(cli, f)
				delete(tool, f)
			}
			// The fake mints ids from a per-server counter, so two fresh
			// servers produce identical ids; only wall-clock stamps differ.
			scrubTimestamps(cli)
			scrubTimestamps(tool)
			if len(cli) == 0 || len(tool) == 0 {
				t.Fatalf("nothing left to compare — cli=%v tool=%v", cli, tool)
			}
			if !equalJSON(t, cli, tool) {
				a, _ := json.Marshal(cli)
				b, _ := json.Marshal(tool)
				t.Fatalf("command and tool disagree\n  cmd:  %s\n  tool: %s", a, b)
			}
		})
	}
}

// ─────────────────────── help text must not lie (R2) ───────────────────────

// A usage string that advertises a flag the CLI rejects is worse than no
// usage string: `decision record`'s help still named v0.3's --options and
// --chosen, and brief section [2] puts these commands in front of the agent,
// so it would read the suggestion and get exit 2. Every subcommand's usage
// line is checked against the flags that actually exist.
func TestUsageTextAdvertisesOnlyRealFlags(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	// Flag spellings removed from the contract in v0.4.
	gone := []string{"--options", "--chosen", "--url"}

	// Each subcommand's own usage line, reached by invoking it wrongly, plus
	// the top-level help.
	for _, args := range [][]string{
		{"help"},
		{"lane"},
		{"status"},
		{"decision"},
		{"artifact"},
		{"review"},
	} {
		var out, errb bytes.Buffer
		run(args, clienttest.Getenv(env), strings.NewReader(""), &out, &errb)
		text := errb.String()
		for _, g := range gone {
			if strings.Contains(text, g) {
				t.Errorf("%v help advertises %s, which no longer exists:\n%s", args, g, text)
			}
		}
	}

	// And the canonical names are the ones offered.
	var out, errb bytes.Buffer
	run([]string{"decision"}, clienttest.Getenv(env), strings.NewReader(""), &out, &errb)
	if !strings.Contains(errb.String(), "--summary") || !strings.Contains(errb.String(), "--rationale") {
		t.Errorf("decision record usage should name --summary/--rationale:\n%s", errb.String())
	}
}

// Every flag a usage line advertises must actually parse. R2 was a usage
// line naming flags that had been removed; this catches the mirror image too
// — a usage line naming a flag that was never added.
//
// The usage line is obtained by invoking the command group with a bogus
// subcommand, which is what prints it. (Passing a bogus *flag* would print
// the flag package's own listing of real flags instead, which can never
// disagree with itself.)
func TestAdvertisedFlagsAllParse(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	groups := []struct {
		probe []string   // prints the group's usage line
		subs  [][]string // the flag must be defined by one of these
	}{
		{probe: []string{"lane", "bogus"}, subs: [][]string{{"lane", "delegate"}}},
		{probe: []string{"status", "bogus"}, subs: [][]string{{"status", "set"}}},
		{probe: []string{"decision", "bogus"}, subs: [][]string{{"decision", "record"}}},
		{probe: []string{"artifact"}, subs: [][]string{{"artifact", "submit"}, {"artifact", "get"}}},
		{probe: []string{"review", "bogus"}, subs: [][]string{{"review", "approve"}, {"review", "reject"}}},
	}
	for _, g := range groups {
		t.Run(strings.Join(g.probe, " "), func(t *testing.T) {
			var out, errb bytes.Buffer
			run(g.probe, clienttest.Getenv(env), strings.NewReader(""), &out, &errb)
			usageLine := errb.String()
			// Only the "usage:" lines this group printed, not the shared
			// bottom-of-help text.
			if i := strings.Index(usageLine, "colab — agent → platform CLI"); i >= 0 {
				usageLine = usageLine[:i]
			}
			flags := advertisedFlags(usageLine)
			if len(flags) == 0 {
				t.Fatalf("no flags found in the usage line — the probe stopped printing it:\n%s", errb.String())
			}
			for _, f := range flags {
				if !anyDefines(t, env, g.subs, f) {
					t.Errorf("usage advertises %s but no subcommand of %v defines it:\n%s", f, g.probe, usageLine)
				}
			}
		})
	}
}

// advertisedFlags pulls the --flag words out of a usage line.
func advertisedFlags(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, word := range strings.Fields(text) {
		word = strings.Trim(word, "[]|`,.()")
		if !strings.HasPrefix(word, "--") || len(word) < 4 || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

// anyDefines reports whether one of the subcommands defines the flag. The
// flag package answers "flag provided but not defined: -x" for one it does
// not know — note the single dash, whatever the caller typed — and any other
// outcome means it parsed.
func anyDefines(t *testing.T, env map[string]string, subs [][]string, flag string) bool {
	t.Helper()
	rejected := "not defined: -" + strings.TrimLeft(flag, "-")
	for _, sub := range subs {
		var out, errb bytes.Buffer
		run(append(append([]string{}, sub...), flag, "x"), clienttest.Getenv(env), strings.NewReader(""), &out, &errb)
		if !strings.Contains(errb.String(), rejected) {
			return true
		}
	}
	return false
}

// ─────────────────────────── NN1: submit size cap ───────────────────────────

// The 50 MB submitArtifact ceiling is enforced locally, before the upload:
// relying on the server's 413 would mean pushing 50 MB up the wire to be told
// no. The file is created sparse so the test costs no disk.
func TestArtifactSubmitRefusesOversizeBeforeUploading(t *testing.T) {
	s := clienttest.New(t)
	path := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(colab.MaxArtifactBytes + 1); err != nil { // sparse
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	code, v, _ := exec(t, s.Env(t.TempDir()), "artifact", "submit", "--type", "diff", "--file", path)
	if code != client.ExitUsage {
		t.Fatalf("code = %d, want 2 (refused locally), v = %v", code, v)
	}
	if len(s.Requests) != 0 {
		t.Fatalf("%d requests reached the server; the cap must be checked before uploading", len(s.Requests))
	}
	// Exactly at the ceiling is allowed.
	ok := filepath.Join(t.TempDir(), "atlimit.bin")
	f2, err := os.Create(ok)
	if err != nil {
		t.Fatal(err)
	}
	if err := f2.Truncate(colab.MaxArtifactBytes); err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if code, v, _ := exec(t, s.Env(t.TempDir()), "artifact", "submit", "--type", "diff", "--file", ok); code != client.ExitOK {
		t.Fatalf("a file exactly at the limit must be accepted: code=%d v=%v", code, v)
	}
}
