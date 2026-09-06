package main

import (
	"context"
	"io"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// P2 commands — contracts/colab-cli.md §2.2·2.3: lane delegate ·
// status set · decision record · artifact submit/get · review approve/reject.
// Exit codes are the §2 convention shared with P1 (0 · 2 · 3 · 4 · 5).

// repeatable collects a flag that may be given more than once and/or with
// comma-separated values (--depends-on a,b --depends-on c).
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func runLane(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "delegate" {
		return usage(stderr, "usage: colab lane delegate --agent <name> --brief <text> [--depends-on <lane_id>] [--profile <name>]")
	}
	fs, _ := newFlagSet("lane delegate", stderr)
	session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
	agent := fs.String("agent", "", "target agent name — must already be a session participant (FR-1.5)")
	brief := fs.String("brief", "", "delegation brief; goes into the delegate's turn prompt verbatim")
	profile := fs.String("profile", "", "profile name (default: the participant's registered profile)")
	key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
	var dependsOn repeatable
	fs.Var(&dependsOn, "depends-on", "lane id this lane waits for; repeatable / comma-separated (v1 stores it, DAG execution is v1.1)")
	if err := fs.Parse(args[1:]); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "lane delegate: unexpected argument %q", fs.Arg(0))
	}
	v, err := colab.LaneDelegate(context.Background(), client.New(client.FromEnv(getenv)), colab.LaneDelegateArgs{
		Session: *session, Agent: *agent, Brief: *brief, DependsOn: dependsOn,
		Profile: *profile, IdempotencyKey: *key})
	return emit(stdout, stderr, v, err)
}

func runStatus(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		return usage(stderr, "usage: colab status set working|blocked|done [--note <text>]")
	}
	// The status is a positional word: `colab status set blocked --note …`.
	rest := args[1:]
	status := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		status, rest = rest[0], rest[1:]
	}
	fs, _ := newFlagSet("status set", stderr)
	task := fs.String("task", "", "task id (default COLAB_TASK_ID / token scope)")
	note := fs.String("note", "", "feed note; required for blocked — it is the question the delegator answers")
	statusFlag := fs.String("status", "", "alternative to the positional word: working | blocked | done")
	if err := fs.Parse(rest); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "status set: unexpected argument %q", fs.Arg(0))
	}
	if status == "" {
		status = *statusFlag
	}
	v, err := colab.StatusSet(context.Background(), client.New(client.FromEnv(getenv)),
		colab.StatusSetArgs{Task: *task, Status: status, Note: *note})
	return emit(stdout, stderr, v, err)
}

func runDecision(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "record" {
		return usage(stderr, "usage: colab decision record --summary <s> [--rationale <r>]")
	}
	fs, _ := newFlagSet("decision record", stderr)
	session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
	summary := fs.String("summary", "", "what was decided (openapi Decision.summary)")
	rationale := fs.String("rationale", "", "why (openapi Decision.rationale)")
	title := fs.String("title", "", "alias of --summary")
	body := fs.String("body", "", "alias of --rationale")
	key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
	if err := fs.Parse(args[1:]); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "decision record: unexpected argument %q", fs.Arg(0))
	}
	sum, rat := *summary, *rationale
	if sum == "" {
		sum = *title
	}
	if rat == "" {
		rat = *body
	}
	v, err := colab.DecisionRecord(context.Background(), client.New(client.FromEnv(getenv)), colab.DecisionRecordArgs{
		Session: *session, Summary: sum, Rationale: rat, IdempotencyKey: *key})
	return emit(stdout, stderr, v, err)
}

func runArtifact(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: colab artifact submit --type <t> --file <p> [--name <n>] [--description <d>]\n"+
			"       colab artifact get <id> [--out <path>]")
	}
	c := client.New(client.FromEnv(getenv))
	ctx := context.Background()
	switch args[0] {
	case "submit":
		fs, _ := newFlagSet("artifact submit", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / token scope)")
		name := fs.String("name", "", "artifact name; re-submitting the same name is version+1 (default: the file's base name)")
		typ := fs.String("type", "", "artifact type — open set: file · diff · branch · doc · report …")
		file := fs.String("file", "", "file to upload, max 50 MB (openapi's multipart part name)")
		path := fs.String("path", "", "alias of --file")
		desc := fs.String("description", "", "optional description")
		key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "artifact submit: unexpected argument %q", fs.Arg(0))
		}
		f := *file
		if f == "" {
			f = *path
		}
		v, err := colab.ArtifactSubmit(ctx, c, colab.ArtifactSubmitArgs{
			Session: *session, Name: *name, Type: *typ, File: f,
			Description: *desc, IdempotencyKey: *key})
		return emit(stdout, stderr, v, err)
	case "get":
		// `colab artifact get <id> [--out <path>]` — the id is positional.
		rest := args[1:]
		id := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			id, rest = rest[0], rest[1:]
		}
		fs, _ := newFlagSet("artifact get", stderr)
		out := fs.String("out", "", "write the artifact body here (a file path, or a directory)")
		idFlag := fs.String("artifact", "", "alternative to the positional <id>")
		if err := fs.Parse(rest); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "artifact get: unexpected argument %q", fs.Arg(0))
		}
		if id == "" {
			id = *idFlag
		}
		v, err := colab.ArtifactGet(ctx, c, colab.ArtifactGetArgs{Artifact: id, Out: *out})
		return emit(stdout, stderr, v, err)
	}
	return usage(stderr, "colab artifact: unknown subcommand %q (submit · get)", args[0])
}

func runReview(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "approve" && args[0] != "reject") {
		return usage(stderr, "usage: colab review approve --artifact <id> [--note <t>] | colab review reject --artifact <id> --reason <text>")
	}
	verdict := args[0]
	fs, _ := newFlagSet("review "+verdict, stderr)
	artifact := fs.String("artifact", "", "artifact id (required — openapi reviewArtifact is POST /artifacts/{id}/review)")
	note := fs.String("note", "", "approve: comments recorded with the review")
	reason := fs.String("reason", "", "reject: why (required) — posted as a reply on the artifact thread")
	key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
	if err := fs.Parse(args[1:]); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "review %s: unexpected argument %q", verdict, fs.Arg(0))
	}
	a := colab.ReviewArgs{Artifact: *artifact, Note: *note, Reason: *reason, IdempotencyKey: *key}
	ctx := context.Background()
	c := client.New(client.FromEnv(getenv))
	if verdict == "approve" {
		v, err := colab.ReviewApprove(ctx, c, a)
		return emit(stdout, stderr, v, err)
	}
	v, err := colab.ReviewReject(ctx, c, a)
	return emit(stdout, stderr, v, err)
}
