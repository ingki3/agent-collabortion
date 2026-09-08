package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// S-63 (Lead 판정 2026-09-08) — the installer places BOTH binaries
// ---------------------------------------------------------------------------
//
// openapi `Pairing.install_commands` now says the script installs "데몬과
// `colab` CLI 둘 다" and names the acceptance test: the first probe after
// pairing reports `colab_cli.present == true`. The daemon alone leaves the
// agent with no way to talk to the platform at all — `colab` IS the MCP server
// the daemon registers and the shell path agents call (colab-cli.md §1), and
// `probe.Colab` finds it with a plain PATH lookup — so a machine with only
// `colab-daemon` goes quiet without ever erroring, which is the F1 G8 measures.
//
// This test RUNS the script rather than reading it. A grep for `./cmd/colab`
// would have passed on every broken variant we actually hit while writing it
// (wrong module directory, `-ldflags` quoting that dash eats, a `for` loop that
// moved one file and left the other behind) — the only check that separates a
// working installer from a plausible-looking one is the binary being there and
// answering afterwards.

// repoRoot walks up from the test's directory to the checkout that holds the
// daemon and cli modules the script builds.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("no checkout root above %s", dir)
		}
		dir = parent
	}
}

// seedSourceRepo makes the throwaway git repository the installer clones.
//
// It copies the WORKING TREE (not HEAD): the point of the test is that the
// source in front of us builds two binaries, and a clone of the last commit
// would keep passing for a change that has not been committed yet. Only the
// three modules the script touches are copied — they depend on nothing outside
// the checkout (`replace ../contracts`), so the build needs no network.
func seedSourceRepo(t *testing.T, root string) string {
	t.Helper()
	src := t.TempDir()
	for _, name := range []string{"contracts", "daemon", "cli", "Makefile"} {
		run(t, "", "cp", "-R", filepath.Join(root, name), filepath.Join(src, name))
	}
	run(t, src, "git", "init", "-q", "-b", "main")
	run(t, src, "git", "add", "-A")
	run(t, src, "git",
		"-c", "user.name=colab test", "-c", "user.email=test@colab.invalid",
		"commit", "-q", "-m", "installer fixture")
	return src
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func haveTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not on PATH — the installer needs it and so does this test", name)
	}
}

// goEnv passes the real build caches through to the throwaway HOME. Without
// them the script's `go build` starts from an empty cache under a temp
// directory and the test takes minutes instead of seconds.
func goEnv(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE", "GOPATH").Output()
	if err != nil {
		t.Skipf("go env: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Skipf("go env returned %q", out)
	}
	return []string{"GOCACHE=" + lines[0], "GOMODCACHE=" + lines[1], "GOPATH=" + lines[2]}
}

// probeVersionRe is the expression daemon/internal/probe uses on
// `colab --version` (backlog C-3: it takes the FIRST x.y.z in the line).
var probeVersionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

func TestScriptInstallsBothBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries from source")
	}
	haveTool(t, "go")
	haveTool(t, "git")
	root := repoRoot(t)

	home := t.TempDir()
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "install.sh")
	if err := os.WriteFile(script, []byte(Script("http://colab.test")), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", script)
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + os.Getenv("TMPDIR"),
		// A login shell we can predict, so the PATH block lands in a file the
		// assertions below can name.
		"SHELL=/bin/sh",
		"COLAB_INSTALL_REPO=file://" + seedSourceRepo(t, root),
	}, goEnv(t)...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	t.Logf("installer finished in %s:\n%s", time.Since(start).Round(time.Millisecond), out)
	if err != nil {
		t.Fatalf("the installer a new person pipes into `sh` failed: %v", err)
	}

	binDir := filepath.Join(home, ".colab", "bin")
	daemonBin := filepath.Join(binDir, "colab-daemon")
	cliBin := filepath.Join(binDir, "colab")
	for _, bin := range []string{daemonBin, cliBin} {
		fi, err := os.Stat(bin)
		if err != nil {
			t.Fatalf("%s was not installed: %v — 데몬만 놓으면 첫 probe 가 colab_cli.present=false 로 "+
				"뜨고 세션이 조용히 아무 일도 못 한다 (S-63, Lead 판정)", bin, err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable (%v)", bin, fi.Mode())
		}
	}

	// What probe.Colab actually does: look the CLI up by NAME on PATH, run
	// `--version`, take the first x.y.z. This is `colab_cli.present == true`
	// reproduced without a server. PATH is narrowed to the directory the
	// installer wrote, so a `colab` already on this machine cannot answer for it.
	t.Setenv("PATH", binDir)
	lookedUp, err := exec.LookPath("colab")
	if err != nil {
		t.Fatalf("probe would not find `colab` on a PATH holding only the installed bin dir: %v", err)
	}
	verOut, err := exec.Command(lookedUp, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("`colab --version` failed after install (%v) — probe reports present=false for this: %s", err, verOut)
	}
	got := probeVersionRe.FindString(string(verOut))
	if got == "" {
		t.Fatalf("`colab --version` printed no x.y.z (%q) — probe reads present=false", verOut)
	}
	// The version is the repository Makefile's, stamped by -ldflags. If the
	// script ever writes its own number the two drift and S11 shows the wrong
	// one again (backlog C-3).
	if want := makefileVersion(t, root); got != want {
		t.Errorf("installed colab reports %q, Makefile COLAB_VERSION is %q — the installer is carrying "+
			"its own copy of the number", got, want)
	}
	if _, err := exec.Command(daemonBin, "version").CombinedOutput(); err != nil {
		t.Errorf("installed colab-daemon does not run: %v", err)
	}

	// The PATH guidance covers BOTH binaries: the daemon's own probe resolves
	// `colab` through PATH, so a block that only got the daemon there would
	// still leave colab_cli.present=false.
	profile, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil {
		t.Fatalf("the installer added no PATH block to the new user's profile: %v", err)
	}
	if !strings.Contains(string(profile), binDir) {
		t.Errorf("the PATH block does not put %s on PATH:\n%s", binDir, profile)
	}
	for _, want := range []string{"colab-daemon", "colab"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the installer never mentions %q in what it prints to the person running it", want)
		}
	}
}

func makefileVersion(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "COLAB_VERSION") {
			_, v, ok := strings.Cut(line, "=")
			if ok {
				return strings.TrimSpace(v)
			}
		}
	}
	t.Fatal("Makefile has no COLAB_VERSION")
	return ""
}
