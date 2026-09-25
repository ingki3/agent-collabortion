// Command colab is the CLI agents use to talk back to the platform
// (PRD FR-7.4, contracts/colab-cli.md). Authenticated with COLAB_TASK_TOKEN.
// The same commands are exposed as MCP tools via `colab mcp serve`.
//
// P1: message post · version · mcp serve (and the reads now named room get ·
// room messages).
// P2 (colab-cli.md v0.4 §2.2·2.3): lane delegate · status set ·
// decision record · artifact submit/get · review approve/reject.
// P3 (colab-cli.md v0.5 §2.4): hitl ask · hitl approve-request ·
// hitl request-info.
// P4: `artifact submit --type diff` builds the workdir's own diff (FR-4.3).
// v1.1 (colab-cli.md v0.6 §2.5, K-19): a command outside the role's
// allowed_commands is refused before any request with exit 3
// command_not_allowed; `mcp serve --allow` registers only the allowed tools.
// v0.19 R3 (colab-cli.md v0.8 §2.4a): room list · room read · work propose.
// v0.19 R4 (colab-cli.md v0.9): `colab session get|messages` are gone —
// room get · room messages are the commands, and every path is /rooms/{R}/….
// Output is always JSON on stdout (agents parse it); --json is accepted for
// clarity. Exit codes: 0 ok · 2 args · 3 refused · 4 no/revoked token ·
// 5 server unreachable.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
	"github.com/ingki3/agent-collabortion/cli/internal/mcp"
	"github.com/ingki3/agent-collabortion/contracts"
)

// version is the colab CLI's own version. Release builds set it with
// -ldflags "-X main.version=<x.y.z>" (Makefile COLAB_VERSION); the default
// is a semver too, and deliberately so: the daemon probe reads
// `colab --version` with the regexp \d+\.\d+\.\d+ and takes the FIRST
// match (daemon/internal/probe.CLIVersion). While this said "dev" the first
// x.y.z in the line was the contracts version, so probe reported the
// contract set as the CLI's version and S11 showed "colab CLI 0.1.0"
// (backlog C-3). The CLI version stays leftmost in the output for the same
// reason.
var version = "0.3.0-dev"

const usageText = `colab — agent → platform CLI (contracts/colab-cli.md)

  colab room get [--room R] [--json]
                             {"room", "work", "participants"}: the room (name, description, isolation),
                             this turn's mission (goal, acceptance_criteria, completion_progress,
                             director — null outside a mission, i.e. without COLAB_WORK_ID) and
                             the roster (name, role description, derived status)
  colab room messages [--since <cursor|id>] [--limit N] [--thread <root_id>] [--work <mission_id>] [--json]
                             --since is sent as the after= query parameter (messages newer than it)
                             --limit is 1..200 (omit for the server default 50)
                             --work keeps one mission's messages
  colab message post --body <text> [--detail <text> | --detail-file <path>] [--reply-to <msg_id> | --top-level] [--mention @A,@B] [--idempotency-key K] [--json]
                             --body is the conversation (to whom · what · conclusion · next, ~5 lines);
                             findings, full drafts and tables go in --detail (or --detail-file, sent
                             byte for byte). Deliverables are artifact submit
                             Idempotency-Key = UUIDv5(task:<task_id>:<seq>), seq continues across attempts;
                             the same seq is sent as X-Colab-Client-Seq (omitted with --idempotency-key)
  colab status set working|blocked|done [--note <text>]
                             blocked needs --note (the question); the reply carries turn_end_required
  colab lane delegate --agent <name> --brief <text> [--depends-on <lane_id>] [--profile <name>]
                             always a new lane; the target must already be a room participant
  colab decision record --summary <s> [--rationale <r>]
  colab artifact submit --type <t> --file <p> [--name <n>] [--description <d>]
  colab artifact submit --type diff [--base <rev>] [--name <n>] [--description <d>]
                             --type diff may omit --file: the CLI builds the unified diff of THIS
                             workdir (commits since --base, staged and unstaged changes, in one
                             patch). Untracked files are not in a diff — git add them first.
                             The description's first line is "diff <branch>@<commit> vs <base>" and
                             the body starts with a "# colab-diff:" comment (git apply skips it).
                             The patch is for "git apply", NOT "git am" — it is a plain diff with no
                             commit metadata. Binary files are included (git diff --binary).
                             Default --name is the branch's last segment (colab/<s>/frontend →
                             frontend.diff), so re-submitting from the same branch is version+1
  colab artifact get <id> [--out <path>]
  colab review approve --artifact <id> [--note <t>]
  colab review reject  --artifact <id> --reason <text>
  colab hitl ask --question <text> --default <text> [--choices a,b,c] [--context <text>]
                             asks the Director. --default is REQUIRED for both question and choice
                             (FR-5.1); --choices makes it a choice (2+ options, --default one of them)
  colab hitl approve-request --summary <text> [--artifact <id>]
                             asks for approval; no default — an approval never auto-proceeds (FR-5.4)
  colab hitl request-info --what <text> [--why <text>]
                             asks a human for information (--question is an alias of --what)
                             All three return turn_end_required:true — register it and END YOUR TURN.
                             A task holds one open request at a time; a second is exit 3 hitl_already_open.
  colab room list [--query <text>]
                             only the rooms THIS turn may read (the requesting person and you
                             must both have access — judged by the server at the call)
  colab room read --room <id> [--tail N] [--query <text>]
                             another room's summary + recent messages (--tail 1..100, default 30)
                             + decisions + artifacts. READ-ONLY and for this turn only — to carry
                             something over, colab decision record. "truncated": true means the
                             server cut it to the read limits. Refused → exit 3 room_read_denied
                             with denied_reason (originator_not_participant · originator_left ·
                             agent_not_allowed · no_originator)
  colab work propose --goal <text> --why <text>
                             proposes a mission for this room; a person opens it (agents cannot)
  colab mcp serve [--allow <cmd,…>]
                             stdio MCP server exposing the same commands as tools. --allow registers
                             only the listed commands' tools (names as in COLAB_ALLOWED_COMMANDS)
  colab version            also as the flags --version · -v (the daemon probe runs colab --version)

  The four P2 write commands take an optional --idempotency-key (uuid); it is
  sent only when given (openapi IdempotencyKeyOptional).

env (daemon, contracts/colab-cli.md §1): COLAB_TASK_TOKEN COLAB_SERVER_URL(origin) COLAB_TASK_ID
     COLAB_TASK_ATTEMPT COLAB_LANE_ID COLAB_ROOM_ID (old name COLAB_SESSION_ID, same value)
     [COLAB_WORK_ID] COLAB_AGENT_NAME [COLAB_API_PREFIX]
     [COLAB_ALLOWED_COMMANDS=room_get,message_post,…]  the role's command subset (harness §10);
     without it the CLI reads getCliContext.allowed_commands once. A command outside the subset
     is refused BEFORE any request: exit 3 command_not_allowed (colab-cli.md §2.5)
exit: 0 ok · 2 args · 3 refused · 4 no/revoked token · 5 server unreachable
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run is main without os.Exit so tests can drive it.
func run(args []string, getenv client.Getenv, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, usageText)
		return client.ExitUsage
	}
	switch args[0] {
	// --version/-v are the same output as the subcommand: the daemon probe
	// runs `colab --version` (daemon-protocol.md §3) and reads x.y.z out of
	// it, so without the flag every probe reports colab_cli.present=false.
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "colab %s (contracts %s)\n", version, contracts.Version)
		return client.ExitOK
	case "message":
		return runMessage(args[1:], getenv, stdout, stderr)
	case "lane":
		return runLane(args[1:], getenv, stdout, stderr)
	case "status":
		return runStatus(args[1:], getenv, stdout, stderr)
	case "decision":
		return runDecision(args[1:], getenv, stdout, stderr)
	case "artifact":
		return runArtifact(args[1:], getenv, stdout, stderr)
	case "review":
		return runReview(args[1:], getenv, stdout, stderr)
	case "hitl":
		return runHitl(args[1:], getenv, stdout, stderr)
	case "room":
		return runRoom(args[1:], getenv, stdout, stderr)
	case "work":
		return runWork(args[1:], getenv, stdout, stderr)
	case "mcp":
		if len(args) < 2 || args[1] != "serve" {
			return usage(stderr, "usage: colab mcp serve [--allow <cmd,…>]")
		}
		fs, _ := newFlagSet("mcp serve", stderr)
		allow := fs.String("allow", "", "register only these commands' tools (comma-separated ColabCommand names, e.g. room_get,message_post); default: all")
		if err := fs.Parse(args[2:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "mcp serve: unexpected argument %q", fs.Arg(0))
		}
		cfg := client.FromEnv(getenv)
		var o mcp.Options
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "allow" {
				// --allow is the daemon's copy of the bundle's allowed_commands
				// (harness §10): it is the tool table AND the gate list, so no
				// tool call ever spends a /cli/context read just to be refused.
				o.Allow = client.SplitCommands(*allow)
				cfg.AllowedCommands = o.Allow
				o.Unknown = func(name string) {
					fmt.Fprintf(stderr, "colab mcp serve: --allow: %q is not a colab command; ignored\n", name)
				}
			}
		})
		c := client.New(cfg)
		if err := mcp.ServeWith(context.Background(), c, stdin, stdout, version, o); err != nil {
			fmt.Fprintln(stderr, "colab mcp serve:", err)
			return client.ExitUnreachable
		}
		return client.ExitOK
	}
	return usage(stderr, "colab: unknown command %q "+
		"(room · message · status · lane · decision · artifact · review · hitl · work · mcp · version)", args[0])
}

func usage(stderr io.Writer, format string, a ...any) int {
	fmt.Fprintf(stderr, format+"\n", a...)
	fmt.Fprint(stderr, usageText)
	return client.ExitUsage
}

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "JSON output (always on; accepted for clarity)")
	return fs, jsonOut
}

func runMessage(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "post" {
		return usage(stderr, "usage: colab message post --body <text> [--detail <text> | --detail-file <path>] [--reply-to <id> | --top-level] [--mention @A,@B]")
	}
	fs, _ := newFlagSet("message post", stderr)
	session := fs.String("session", "", "room id override (default COLAB_ROOM_ID / token scope)")
	body := fs.String("body", "", "message text (markdown): the conversation — to whom, what, conclusion, next")
	detail := fs.String("detail", "", "work text (markdown): findings, full drafts, tables — folded on screen")
	detailFile := fs.String("detail-file", "", "read the work text from this file, as is (not with --detail)")
	replyTo := fs.String("reply-to", "", "parent message id (thread; default: COLAB_THREAD_ID, the thread the turn was asked in)")
	topLevel := fs.Bool("top-level", false, "post to the main timeline even when the turn was asked in a thread")
	mention := fs.String("mention", "", "comma-separated agent names to mention, e.g. @Reviewer,@Writer")
	key := fs.String("idempotency-key", "", "reuse a previous key to retry the same post (default: UUIDv5 of task:<task_id>:<seq>)")
	if err := fs.Parse(args[1:]); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "message post: unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(*body) == "" {
		return emit(stdout, stderr, nil, client.Usage("--body is required"))
	}
	// --detail / --detail-file: given (even empty) is told apart from absent,
	// so an empty one is exit 2 in colab.MessagePost rather than silently
	// dropped; both together is exit 2 like --reply-to with --top-level.
	var detailArg *string
	var detailGiven, fileGiven bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "detail":
			detailGiven = true
		case "detail-file":
			fileGiven = true
		}
	})
	if detailGiven && fileGiven {
		return emit(stdout, stderr, nil, client.Usage("--detail and --detail-file contradict each other: give one"))
	}
	if detailGiven {
		detailArg = detail
	}
	// --detail-file is read by colab.MessagePost (colab.ReadDetailFile), the
	// same code the MCP tool's detail_file goes through (colab-cli v0.9.3).
	if fileGiven && *detailFile == "" {
		return emit(stdout, stderr, nil, client.Usage("--detail-file is empty: give a path"))
	}
	var mentions []string
	if *mention != "" {
		mentions = strings.Split(*mention, ",")
	}
	c := client.New(client.FromEnv(getenv))
	v, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{
		Session: *session, Body: *body, Detail: detailArg, DetailFile: *detailFile, ReplyTo: *replyTo, TopLevel: *topLevel, Mention: mentions, IdempotencyKey: *key})
	return emit(stdout, stderr, v, err)
}

// emit writes the result (or the error object) as JSON to stdout and returns
// the exit code. Errors also get a one-line human message on stderr.
func emit(stdout, stderr io.Writer, v any, err error) int {
	if err != nil {
		e := client.AsError(err)
		fmt.Fprintln(stderr, "colab:", e.Error())
		stdout.Write(colab.MarshalIndent(colab.ErrorJSON(e)))
		fmt.Fprintln(stdout)
		return e.Exit
	}
	stdout.Write(colab.MarshalIndent(v))
	fmt.Fprintln(stdout)
	return client.ExitOK
}
