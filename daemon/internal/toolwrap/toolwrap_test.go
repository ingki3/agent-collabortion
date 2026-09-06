package toolwrap_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

var attemptEnv = []string{
	"COLAB_AGENT_NAME=Lead",
	"COLAB_LANE_ID=lane-1",
	"COLAB_SERVER_URL=http://127.0.0.1:8080",
	"COLAB_SESSION_ID=sess-A",
	"COLAB_TASK_ATTEMPT=2",
	"COLAB_TASK_ID=t-1",
	"COLAB_TASK_TOKEN=ctk_secret",
	"HOME=/home/x",
	"PATH=/usr/bin",
}

// The wrapper is the whole channel for a cli_wrapper runtime: right path,
// 0700, every COLAB_* exported, real colab exec'd by absolute path.
func TestWriteContentAndMode(t *testing.T) {
	root := t.TempDir()
	path, err := toolwrap.Write(root, "t-1", 2, "/opt/colab/bin/colab", attemptEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".colab", "bin", "t-1.2", "colab")
	if path != want {
		t.Fatalf("path %q want %q", path, want)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v want 0700 (the file holds the attempt token)", st.Mode().Perm())
	}
	if dst, err := os.Stat(filepath.Dir(path)); err != nil || dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v err %v", dst.Mode().Perm(), err)
	}
	body := read(t, path)
	if !strings.HasPrefix(body, "#!/bin/sh\n") {
		t.Fatalf("no shebang:\n%s", body)
	}
	for _, k := range []string{"COLAB_TASK_TOKEN", "COLAB_SERVER_URL", "COLAB_TASK_ID", "COLAB_TASK_ATTEMPT", "COLAB_LANE_ID", "COLAB_SESSION_ID", "COLAB_AGENT_NAME"} {
		if !strings.Contains(body, "export "+k+"=") {
			t.Fatalf("%s not exported:\n%s", k, body)
		}
	}
	if strings.Contains(body, "export PATH=") || strings.Contains(body, "export HOME=") {
		t.Fatalf("non-COLAB_ variable leaked into the wrapper:\n%s", body)
	}
	if !strings.Contains(body, `exec '/opt/colab/bin/colab' "$@"`) {
		t.Fatalf("no absolute exec:\n%s", body)
	}
}

// A value with a quote in it must not break out of the script.
func TestWriteQuoting(t *testing.T) {
	root := t.TempDir()
	path, err := toolwrap.Write(root, "t-1", 1, "/bin/echo", []string{`COLAB_AGENT_NAME=O'Neil; rm -rf /`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, path), `export COLAB_AGENT_NAME='O'\''Neil; rm -rf /'`) {
		t.Fatalf("bad quoting:\n%s", read(t, path))
	}
}

// The point of the whole exercise: run the wrapper with an EMPTY environment
// (what a sanitising runtime gives its shell tools) and see COLAB_* arrive.
func TestWrapperRunsWithSanitisedEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh wrapper")
	}
	root := t.TempDir()
	path, err := toolwrap.Write(root, "t-1", 1, "/bin/sh", append(attemptEnv, "COLAB_EXTRA=x"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-c", `printf '%s|%s' "$COLAB_TASK_TOKEN" "$COLAB_TASK_ID"`)
	cmd.Env = []string{} // the sanitised environment: nothing at all
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "ctk_secret|t-1" {
		t.Fatalf("got %q — the wrapper did not carry COLAB_* through a sanitised env", out)
	}
}

func TestRemoveAndSweep(t *testing.T) {
	root := t.TempDir()
	if _, err := toolwrap.Write(root, "t-1", 1, "colab", attemptEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := toolwrap.Write(root, "t-2", 1, "colab", attemptEnv); err != nil {
		t.Fatal(err)
	}
	if err := toolwrap.Remove(root, "t-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(toolwrap.Dir(root, "t-1", 1)); !os.IsNotExist(err) {
		t.Fatal("t-1 wrapper survived Remove")
	}
	if _, err := os.Stat(toolwrap.Path(root, "t-2", 1)); err != nil {
		t.Fatal("Remove hit the wrong attempt")
	}
	if err := toolwrap.Remove(root, "t-1", 1); err != nil {
		t.Fatalf("Remove must be idempotent: %v", err)
	}
	if err := toolwrap.SweepAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(toolwrap.Root(root)); !os.IsNotExist(err) {
		t.Fatal("SweepAll left a wrapper behind")
	}
}

// §10 v0.8.1 rewrite rule: commands yes, prose no.
func TestRewriteCLI(t *testing.T) {
	const w = "/root/.colab/bin/t-1.1/colab"
	cases := []struct{ name, in, want string }{
		{"backtick command",
			"- Post every reply with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool).",
			"- Post every reply with `" + w + " message post --body \"<text>\"` (or the colab_message_post MCP tool)."},
		{"line start",
			"colab session get\n",
			w + " session get\n"},
		{"shell prompt",
			"$ colab session messages",
			"$ " + w + " session messages"},
		{"prose untouched",
			"[2] Workspace rules and colab CLI\nYour COLAB_TASK_TOKEN is valid for this attempt only.",
			"[2] Workspace rules and colab CLI\nYour COLAB_TASK_TOKEN is valid for this attempt only."},
		{"mcp tool name untouched",
			"use colab_message_post",
			"use colab_message_post"},
		{"fenced block",
			"```sh\ncolab message post --body hi\n```",
			"```sh\n" + w + " message post --body hi\n```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolwrap.RewriteCLI(c.in, w); got != c.want {
				t.Fatalf("got %q\nwant %q", got, c.want)
			}
		})
	}
	// No wrapper (an mcp runtime) → the text is handed over untouched.
	in := "run `colab message post --body hi`"
	if got := toolwrap.RewriteCLI(in, ""); got != in {
		t.Fatalf("rewrote without a wrapper: %q", got)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
