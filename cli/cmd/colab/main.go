// Command colab is the CLI agents use to talk back to the platform
// (PRD FR-7.4, contracts/colab-cli.md). Authenticated with COLAB_TASK_TOKEN.
// The same commands are exposed as MCP tools via `colab mcp serve`.
//
// P1: session get · session messages · message post · version · mcp serve.
// P2 (colab-cli.md v0.4 §2.2·2.3): lane delegate · status set ·
// decision record · artifact submit/get · review approve/reject.
// P3 (colab-cli.md v0.5 §2.4): hitl ask · hitl approve-request ·
// hitl request-info.
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

  colab session get [--session S] [--json]
  colab session messages [--since <cursor|id>] [--limit N] [--thread <root_id>] [--json]
                             --since is sent as the after= query parameter (messages newer than it)
                             --limit is 1..200 (omit for the server default 50)
  colab message post --body <text> [--reply-to <msg_id>] [--mention @A,@B] [--idempotency-key K] [--json]
                             Idempotency-Key = UUIDv5(task:<task_id>:<seq>), seq continues across attempts;
                             the same seq is sent as X-Colab-Client-Seq (omitted with --idempotency-key)
  colab status set working|blocked|done [--note <text>]
                             blocked needs --note (the question); the reply carries turn_end_required
  colab lane delegate --agent <name> --brief <text> [--depends-on <lane_id>] [--profile <name>]
                             always a new lane; the target must already be a session participant
  colab decision record --summary <s> [--rationale <r>]
  colab artifact submit --type <t> --file <p> [--name <n>] [--description <d>]
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
  colab mcp serve            stdio MCP server exposing the same commands as tools
  colab version            also as the flags --version · -v (the daemon probe runs colab --version)

  The four P2 write commands take an optional --idempotency-key (uuid); it is
  sent only when given (openapi IdempotencyKeyOptional).

env (daemon, contracts/colab-cli.md §1): COLAB_TASK_TOKEN COLAB_SERVER_URL(origin) COLAB_TASK_ID
     COLAB_TASK_ATTEMPT COLAB_LANE_ID COLAB_SESSION_ID COLAB_AGENT_NAME [COLAB_API_PREFIX]
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
	case "session":
		return runSession(args[1:], getenv, stdout, stderr)
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
	case "mcp":
		if len(args) < 2 || args[1] != "serve" {
			return usage(stderr, "usage: colab mcp serve")
		}
		c := client.New(client.FromEnv(getenv))
		if err := mcp.Serve(context.Background(), c, stdin, stdout, version); err != nil {
			fmt.Fprintln(stderr, "colab mcp serve:", err)
			return client.ExitUnreachable
		}
		return client.ExitOK
	}
	return usage(stderr, "colab: unknown command %q "+
		"(session · message · status · lane · decision · artifact · review · hitl · mcp · version)", args[0])
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

func runSession(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: colab session get | colab session messages")
	}
	c := client.New(client.FromEnv(getenv))
	ctx := context.Background()
	switch args[0] {
	case "get":
		fs, _ := newFlagSet("session get", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "session get: unexpected argument %q", fs.Arg(0))
		}
		v, err := colab.SessionGet(ctx, c, colab.SessionGetArgs{Session: *session})
		return emit(stdout, stderr, v, err)
	case "messages":
		fs, _ := newFlagSet("session messages", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
		since := fs.String("since", "", "only messages newer than this cursor / message id (sent as after=)")
		limit := fs.Int("limit", 0, "max messages, 1..200 (omit for the server default 50)")
		thread := fs.String("thread", "", "thread root message id (root + replies)")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "session messages: unexpected argument %q", fs.Arg(0))
		}
		a := colab.SessionMessagesArgs{Session: *session, Since: *since, Thread: *thread}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" { // explicit --limit (even 0) is validated, not ignored
				a.Limit = limit
			}
		})
		v, err := colab.SessionMessages(ctx, c, a)
		return emit(stdout, stderr, v, err)
	}
	return usage(stderr, "colab session: unknown subcommand %q", args[0])
}

func runMessage(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "post" {
		return usage(stderr, "usage: colab message post --body <text> [--reply-to <id>] [--mention @A,@B]")
	}
	fs, _ := newFlagSet("message post", stderr)
	session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
	body := fs.String("body", "", "message text (markdown)")
	replyTo := fs.String("reply-to", "", "parent message id (thread)")
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
	var mentions []string
	if *mention != "" {
		mentions = strings.Split(*mention, ",")
	}
	c := client.New(client.FromEnv(getenv))
	v, err := colab.MessagePost(context.Background(), c, colab.MessagePostArgs{
		Session: *session, Body: *body, ReplyTo: *replyTo, Mention: mentions, IdempotencyKey: *key})
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
