// Package toolwrap implements the harness §10 v0.8 `cli_wrapper` tool surface.
//
// A runtime whose initialize response has no `mcpCapabilities` drops the
// `session/new.mcpServers` we send without a word, and launches its shell and
// python tools under a SANITISED environment — the attempt's COLAB_* and even
// PATH never reach them. An agent that follows the brief and runs `colab
// message post` there gets `command not found` and has no way at all to reach
// the platform (G5 blocker (b): 0 messages, COLAB_TASK_TOKEN=None).
//
// What a sanitised environment cannot take away is a FILE. So the daemon
// writes one executable per attempt,
//
//	<workdir_root>/.colab/bin/<task_id>.<attempt>/colab
//
// which exports the attempt's COLAB_* set and execs the real colab binary by
// absolute path, and rewrites the `colab …` commands in the text it hands the
// agent (brief and turn prompt) to that path — see RewriteCLI. The directory
// is under the workdir ROOT, not inside the lane workdir, so it never lands in
// the agent's repository tree or a commit. It holds the attempt token, so it
// is 0700 and is deleted at finish (completed, failed and cancelled alike)
// and by the daemon's start-up sweep, alongside the pgid record.
package toolwrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Name is the wrapper's file name: the agent must call the binary by the name
// the brief and colab-cli.md use.
const Name = "colab"

// envPrefix selects the variables the wrapper exports (harness §2.1 / §10:
// COLAB_TASK_TOKEN, COLAB_SERVER_URL, COLAB_TASK_ID, COLAB_TASK_ATTEMPT,
// COLAB_LANE_ID, COLAB_SESSION_ID, COLAB_AGENT_NAME).
const envPrefix = "COLAB_"

// Root is <workdir_root>/.colab/bin — every attempt's wrapper lives under it.
func Root(workdirRoot string) string {
	return filepath.Join(workdirRoot, ".colab", "bin")
}

// Dir is the one attempt's wrapper directory.
func Dir(workdirRoot, taskID string, attempt int) string {
	return filepath.Join(Root(workdirRoot), fmt.Sprintf("%s.%d", taskID, attempt))
}

// Path is the wrapper executable of one attempt.
func Path(workdirRoot, taskID string, attempt int) string {
	return filepath.Join(Dir(workdirRoot, taskID, attempt), Name)
}

// Write creates the attempt's wrapper and returns its absolute path. env is
// the attempt process environment (acp.Env); its COLAB_* entries are what the
// wrapper exports. colabBin is config.ColabBin, resolved to an absolute path
// here because the wrapper runs with no usable PATH.
func Write(workdirRoot, taskID string, attempt int, colabBin string, env []string) (string, error) {
	dir := Dir(workdirRoot, taskID, attempt)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, Name)
	if err := os.WriteFile(path, []byte(Script(ResolveBin(colabBin), env)), 0o700); err != nil {
		return "", err
	}
	// WriteFile does not chmod a file that already existed (a retried attempt
	// number reuses the path), so set the mode explicitly.
	if err := os.Chmod(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// Script renders the wrapper. bin must already be absolute.
func Script(bin string, env []string) string {
	vars := map[string]string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, envPrefix) {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			vars[k] = v
		}
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Colab tool surface wrapper (harness §10 cli_wrapper) — generated per attempt.\n")
	b.WriteString("# The runtime sanitises the environment of its shell tools, so the attempt's\n")
	b.WriteString("# COLAB_* values travel in this file instead. Deleted when the attempt ends.\n")
	for _, k := range keys {
		b.WriteString("export " + k + "=" + shQuote(vars[k]) + "\n")
	}
	b.WriteString("exec " + shQuote(bin) + " \"$@\"\n")
	return b.String()
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ResolveBin makes the colab binary absolute: the wrapper execs it with no
// PATH to fall back on. An unresolvable name is returned unchanged — the
// probe already advertises a missing colab CLI (daemon-protocol §3
// `colab_cli`), and a wrapper that fails loudly beats no wrapper at all.
func ResolveBin(bin string) string {
	if bin == "" {
		bin = Name
	}
	if p, err := exec.LookPath(bin); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	if strings.ContainsRune(bin, filepath.Separator) {
		if abs, err := filepath.Abs(bin); err == nil {
			return abs
		}
	}
	return bin
}

// Remove deletes one attempt's wrapper directory (finish, every outcome).
func Remove(workdirRoot, taskID string, attempt int) error {
	return os.RemoveAll(Dir(workdirRoot, taskID, attempt))
}

// SweepAll deletes every wrapper directory under the root. It runs at daemon
// start, next to the orphan pgid sweep and before the first claim: no attempt
// of this daemon is running yet, so anything left is from a process that died
// with its token still on disk.
func SweepAll(workdirRoot string) error {
	return os.RemoveAll(Root(workdirRoot))
}

// cliRe matches `colab ` where it is a COMMAND, not prose: at the start of a
// line, right after a backtick (inline code and fenced blocks alike) or after
// a shell prompt "$ ", and followed by a lower-case subcommand word OR a
// flag. So "`colab message post …`" and "`colab --version`" are rewritten and
// "the colab CLI" or the MCP tool name "colab_message_post" are not.
//
// The flag half is backlog D-8. `([a-z])` alone left "`colab --version`"
// pointing at whatever `colab` the runtime's sanitised PATH resolves to —
// which for a cli_wrapper runtime is either nothing or a binary with none of
// the attempt's COLAB_* env, i.e. exactly the G5 failure the wrapper exists to
// prevent. Nothing in today's brief or turn prompt is a bare flag, so this
// closes a hole rather than fixing a break; the point is that the rule is
// "command position", not "command position and also a subcommand".
var cliRe = regexp.MustCompile("(?m)(^|`|\\$ )colab (-|[a-z])")

// RewriteCLI replaces those command occurrences with the wrapper's absolute
// path (harness §10 v0.8.1: the daemon rewrites every text it hands a
// cli_wrapper runtime — the brief marker block AND the turn prompt — because
// the server cannot know a path the daemon invents per attempt). An empty
// wrapper path leaves the text alone.
func RewriteCLI(text, wrapper string) string {
	if wrapper == "" || text == "" {
		return text
	}
	return cliRe.ReplaceAllStringFunc(text, func(m string) string {
		i := strings.Index(m, Name+" ")
		return m[:i] + wrapper + " " + m[i+len(Name)+1:]
	})
}
