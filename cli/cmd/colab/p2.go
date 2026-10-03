package main

import (
	"context"
	"io"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// P2 commands — contracts/colab-cli.md §2.2·2.3: card (v0.9.10) ·
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

// runLane is the retired `colab lane delegate` (colab-cli v0.9.10): nothing
// goes to the server — exit 3 card_required with the card sentence.
func runLane(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	return emit(stdout, stderr, nil, colab.LaneDelegateRetired())
}

// runCard is `colab card delegate|report|accept|revise|get|list` (v0.9.10).
func runCard(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	const u = "usage: colab card delegate --file <card.json> [--depends-on <lane_id>] [--profile <name>] | report --file <result.json> | accept <C-n|id> --comment <text> | revise <C-n|id> --reason <text> [--file <patch.json>] | get <C-n|id> | list"
	if len(args) == 0 {
		return usage(stderr, u)
	}
	sub, rest := args[0], args[1:]
	// A leading positional card (accept C-3 --…).
	pos := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos, rest = rest[0], rest[1:]
	}
	fs, _ := newFlagSet("card "+sub, stderr)
	file := fs.String("file", "", "JSON file")
	reason := fs.String("reason", "", "revise: what is missing")
	comment := fs.String("comment", "", "accept: what you checked and why it is done (required)")
	profile := fs.String("profile", "", "delegate: profile name")
	session := fs.String("session", "", "room id override")
	key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid)")
	var dependsOn repeatable
	fs.Var(&dependsOn, "depends-on", "delegate: lane id this lane waits for; repeatable / comma-separated")
	if err := fs.Parse(rest); err != nil {
		return client.ExitUsage
	}
	if pos == "" && fs.NArg() > 0 {
		pos = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return usage(stderr, "card %s: unexpected argument %q", sub, fs.Arg(0))
	}
	ctx := context.Background()
	c := client.New(client.FromEnv(getenv))
	var v any
	var err error
	switch sub {
	case "delegate":
		v, err = colab.CardDelegate(ctx, c, colab.CardDelegateArgs{File: *file, DependsOn: dependsOn, Profile: *profile, Session: *session, IdempotencyKey: *key})
	case "report":
		var r *colab.CardReportResult
		r, err = colab.CardReport(ctx, c, colab.CardReportArgs{File: *file, IdempotencyKey: *key})
		if err == nil && r.Notice != "" {
			// harness v0.9.16: the downgrade notice is one stdout line.
			_, _ = io.WriteString(stdout, r.Notice+"\n")
		}
		v = r
	case "accept":
		v, err = colab.CardAccept(ctx, c, colab.CardJudgeArgs{Card: pos, Comment: *comment, IdempotencyKey: *key})
	case "revise":
		v, err = colab.CardRevise(ctx, c, colab.CardJudgeArgs{Card: pos, Reason: *reason, File: *file, IdempotencyKey: *key})
	case "get":
		v, err = colab.CardGet(ctx, c, colab.CardGetArgs{Card: pos})
	case "list":
		v, err = colab.CardList(ctx, c, colab.CardListArgs{Session: *session})
	default:
		return usage(stderr, u)
	}
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
	note := fs.String("note", "", "working: what you are doing now, one sentence for the people (shown as 「지금 …」, 120 chars); blocked: REQUIRED — the question the delegator answers")
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
	session := fs.String("session", "", "room id override (default COLAB_ROOM_ID / token scope)")
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
			"       colab artifact submit --type diff [--base <rev>] [--name <n>] [--description <d>]\n"+
			"       colab artifact get <id> [--out <path>]")
	}
	c := client.New(client.FromEnv(getenv))
	ctx := context.Background()
	switch args[0] {
	case "submit":
		fs, _ := newFlagSet("artifact submit", stderr)
		session := fs.String("session", "", "room id override (default COLAB_ROOM_ID / token scope)")
		name := fs.String("name", "", "artifact name; re-submitting the same name is version+1 "+
			"(default: the file's base name; for a generated --type diff, the branch's last segment)")
		typ := fs.String("type", "", "artifact type — open set: file · diff · branch · doc · report …; "+
			"a generated diff is a patch for \"git apply\", not a \"git am\" mailbox")
		file := fs.String("file", "", "file to upload, max 50 MB (openapi's multipart part name); "+
			"optional for --type diff — omitted, the CLI diffs this workdir")
		path := fs.String("path", "", "alias of --file")
		desc := fs.String("description", "", "optional description (a --type diff submission puts "+
			"`diff <branch>@<commit> vs <base>` on the first line and this underneath)")
		base := fs.String("base", "", "--type diff only: what to diff against "+
			"(default: the repository's default branch — origin/HEAD, else main/master)")
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
			Description: *desc, Base: *base, IdempotencyKey: *key})
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
