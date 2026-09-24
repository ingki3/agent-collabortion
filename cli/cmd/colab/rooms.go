package main

import (
	"context"
	"flag"
	"io"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// colab-cli.md v0.8 §2.4a — room get · room messages (the session commands
// under the room name) · room list · room read · work propose.

func runRoom(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr, "usage: colab room get | room messages | room list | room read --room <id>")
	}
	c := client.New(client.FromEnv(getenv))
	ctx := context.Background()
	switch args[0] {
	case "get":
		fs, _ := newFlagSet("room get", stderr)
		room := fs.String("room", "", "room id (default COLAB_SESSION_ID / token scope)")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "room get: unexpected argument %q", fs.Arg(0))
		}
		v, err := colab.RoomGet(ctx, c, colab.RoomGetArgs{Room: *room})
		return emit(stdout, stderr, v, err)
	case "messages":
		fs, _ := newFlagSet("room messages", stderr)
		room := fs.String("room", "", "room id (default COLAB_SESSION_ID / token scope)")
		since := fs.String("since", "", "only messages newer than this cursor / message id (sent as after=)")
		limit := fs.Int("limit", 0, "max messages, 1..200 (omit for the server default 50)")
		thread := fs.String("thread", "", "thread root message id (root + replies)")
		work := fs.String("work", "", "only this mission's messages")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "room messages: unexpected argument %q", fs.Arg(0))
		}
		a := colab.RoomMessagesArgs{Room: *room, Since: *since, Thread: *thread, Work: *work}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" {
				a.Limit = limit
			}
		})
		v, err := colab.RoomMessages(ctx, c, a)
		return emit(stdout, stderr, v, err)
	case "list":
		fs, _ := newFlagSet("room list", stderr)
		query := fs.String("query", "", "only rooms matching this text")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "room list: unexpected argument %q", fs.Arg(0))
		}
		v, err := colab.RoomList(ctx, c, colab.RoomListArgs{Query: *query})
		return emit(stdout, stderr, v, err)
	case "read":
		fs, _ := newFlagSet("room read", stderr)
		room := fs.String("room", "", "the room to read (an id from colab room list)")
		tail := fs.Int("tail", 0, "recent messages, 1..100 (omit for the server default 30)")
		query := fs.String("query", "", "only messages matching this text")
		if err := fs.Parse(args[1:]); err != nil {
			return client.ExitUsage
		}
		if fs.NArg() > 0 {
			return usage(stderr, "room read: unexpected argument %q", fs.Arg(0))
		}
		a := colab.RoomReadArgs{Room: *room, Query: *query}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "tail" { // explicit --tail (even 0) is validated, not ignored
				a.Tail = tail
			}
		})
		v, err := colab.RoomRead(ctx, c, a)
		return emit(stdout, stderr, v, err)
	}
	return usage(stderr, "colab room: unknown subcommand %q", args[0])
}

func runWork(args []string, getenv client.Getenv, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "propose" {
		return usage(stderr, "usage: colab work propose --goal <text> --why <text>")
	}
	fs, _ := newFlagSet("work propose", stderr)
	goal := fs.String("goal", "", "the mission's goal")
	why := fs.String("why", "", "why it should be a mission")
	key := fs.String("idempotency-key", "", "reuse a previous key to retry the same proposal")
	if err := fs.Parse(args[1:]); err != nil {
		return client.ExitUsage
	}
	if fs.NArg() > 0 {
		return usage(stderr, "work propose: unexpected argument %q", fs.Arg(0))
	}
	c := client.New(client.FromEnv(getenv))
	v, err := colab.WorkPropose(context.Background(), c, colab.WorkProposeArgs{Goal: *goal, Why: *why, IdempotencyKey: *key})
	return emit(stdout, stderr, v, err)
}
