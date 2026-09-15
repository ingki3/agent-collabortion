package main

import (
	"context"
	"io"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// P3 commands — contracts/colab-cli.md v0.5.1 §2.4 HITL: ask ·
// approve-request · request-info. Exit codes are the §2 convention
// (0 · 2 · 3 · 4 · 5); the second open request on a task is the server's
// 409 → 3 (E7-04).
//
// The operation is session-scoped (POST /sessions/{S}/hitl-requests) and the
// task comes from the TaskToken, so the override flag is --session, not
// --task: there is nowhere to put a task id, and a flag the server cannot
// honour would be worse than none (v0.5.1, C-4).

const hitlUsage = `usage: colab hitl ask --question <text> --default <text> [--choices a,b,c] [--context <text>]
       colab hitl approve-request --summary <text> [--artifact <id>]
       colab hitl request-info --what <text> [--why <text>]`

func runHitl(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, hitlUsage)
	}
	ctx := context.Background()
	c := client.New(client.FromEnv(getenv))
	switch args[0] {
	case "ask":
		fs, _ := newFlagSet("hitl ask", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / /cli/context)")
		question := fs.String("question", "", "the question for the Director (required)")
		def := fs.String("default", "", "the answer you propose — REQUIRED (FR-5.1: question and choice both need one)")
		ctxt := fs.String("context", "", "background the human needs to answer")
		key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
		var choices repeatable
		fs.Var(&choices, "choices", "turns this into a choice request: 2+ options, repeatable / comma-separated; --default must be one of them")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "hitl ask: unexpected argument %q", fs.Arg(0))
		}
		v, err := colab.HitlAsk(ctx, c, colab.HitlAskArgs{
			Session: *session, Question: *question, Default: *def, Choices: choices,
			Context: *ctxt, IdempotencyKey: *key})
		return emit(stdout, stderr, v, err)
	case "approve-request":
		fs, _ := newFlagSet("hitl approve-request", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / /cli/context)")
		summary := fs.String("summary", "", "what you are asking approval for (required)")
		artifact := fs.String("artifact", "", "artifact id this approval is about")
		key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "hitl approve-request: unexpected argument %q", fs.Arg(0))
		}
		v, err := colab.HitlApproveRequest(ctx, c, colab.HitlApproveRequestArgs{
			Session: *session, Summary: *summary, Artifact: *artifact, IdempotencyKey: *key})
		return emit(stdout, stderr, v, err)
	case "request-info":
		fs, _ := newFlagSet("hitl request-info", stderr)
		session := fs.String("session", "", "session id (default COLAB_SESSION_ID / /cli/context)")
		what := fs.String("what", "", "the information you need (required)")
		why := fs.String("why", "", "why you need it")
		question := fs.String("question", "", "alias of --what")
		key := fs.String("idempotency-key", "", "optional Idempotency-Key (uuid) to make a retry replay")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "hitl request-info: unexpected argument %q", fs.Arg(0))
		}
		w := *what
		if w == "" {
			w = *question
		}
		v, err := colab.HitlRequestInfo(ctx, c, colab.HitlRequestInfoArgs{
			Session: *session, What: w, Why: *why, IdempotencyKey: *key})
		return emit(stdout, stderr, v, err)
	}
	return usage(stderr, "colab hitl: unknown subcommand %q (ask · approve-request · request-info)", args[0])
}
