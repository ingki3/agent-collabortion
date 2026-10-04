package main

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// runMemory is `colab memory note|supersede|retire|get` (colab-cli v0.9.12
// §2.3, the mission ledger — PRD FR-4.6). Each subcommand has its own flag
// set, so a flag another subcommand takes (supersede --kind, note --reason)
// is an unknown flag: exit 2.
func runMemory(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	const u = "usage: colab memory note --kind <fact|assignment|open_question|lesson|plan|progress> --content <text> [--certainty <c>] [--outcome <o>] [--source <msg_id>,...]\n" +
		"       colab memory supersede <id> --content <text> [--source <msg_id>,...]\n" +
		"       colab memory retire <id> --reason <text>\n" +
		"       colab memory get [--kind <k>] [--work <id>] [--status active|superseded|retired|all]"
	if len(args) == 0 {
		return usage(stderr, u)
	}
	sub, rest := args[0], args[1:]
	// A leading positional id (supersede <id> --content …).
	pos := ""
	if (sub == "supersede" || sub == "retire") && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos, rest = rest[0], rest[1:]
	}
	fs, _ := newFlagSet("memory "+sub, stderr)
	var (
		kind, content, certainty, outcome, reason, work, status, key *string
		sources                                                      repeatable
	)
	switch sub {
	case "note":
		kind = fs.String("kind", "", "fact | assignment | open_question | lesson | plan | progress (plan·progress: lead only)")
		content = fs.String("content", "", "the item, one sentence (1..300 chars)")
		certainty = fs.String("certainty", "", "kind fact only: given | to_verify | derived | guess")
		outcome = fs.String("outcome", "", "kind lesson only: dead_end | corrected | useful")
		fs.Var(&sources, "source", "message id(s) this item comes from; repeatable / comma-separated (at most 10)")
		key = fs.String("idempotency-key", "", "optional Idempotency-Key (uuid)")
	case "supersede":
		content = fs.String("content", "", "the replacing item (1..300 chars); kind and certainty come from the target")
		fs.Var(&sources, "source", "message id(s) this item comes from; repeatable / comma-separated (at most 10)")
		key = fs.String("idempotency-key", "", "optional Idempotency-Key (uuid)")
	case "retire":
		reason = fs.String("reason", "", "why the item no longer holds (1..300 chars)")
	case "get":
		kind = fs.String("kind", "", "only this kind")
		work = fs.String("work", "", "mission id (default COLAB_WORK_ID)")
		status = fs.String("status", "active", "active | superseded | retired | all")
	default:
		return usage(stderr, "colab memory: unknown subcommand %q (note · supersede · retire · get)", sub)
	}
	if err := fs.Parse(rest); err != nil {
		return client.ExitUsage
	}
	if (sub == "supersede" || sub == "retire") && pos == "" && fs.NArg() > 0 {
		pos = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return usage(stderr, "memory %s: unexpected argument %q", sub, fs.Arg(0))
	}
	// --certainty / --outcome given empty is still given: colab.MemoryNote
	// only sees the value, so an explicit empty one is refused here.
	var bad string
	fs.Visit(func(f *flag.Flag) {
		if (f.Name == "certainty" || f.Name == "outcome") && f.Value.String() == "" {
			bad = f.Name
		}
	})
	if bad != "" {
		return emit(stdout, stderr, nil, client.Usage("--%s is empty", bad))
	}
	ctx := context.Background()
	c := client.New(client.FromEnv(getenv))
	var v any
	var err error
	switch sub {
	case "note":
		v, err = colab.MemoryNote(ctx, c, colab.MemoryNoteArgs{Kind: *kind, Content: *content, Certainty: *certainty,
			Outcome: *outcome, SourceMessageIDs: sources, IdempotencyKey: *key})
	case "supersede":
		v, err = colab.MemorySupersede(ctx, c, colab.MemorySupersedeArgs{Memory: pos, Content: *content,
			SourceMessageIDs: sources, IdempotencyKey: *key})
	case "retire":
		v, err = colab.MemoryRetire(ctx, c, colab.MemoryRetireArgs{Memory: pos, Reason: *reason})
	case "get":
		v, err = colab.MemoryGet(ctx, c, colab.MemoryGetArgs{Kind: *kind, Work: *work, Status: *status})
	}
	return emit(stdout, stderr, v, err)
}
