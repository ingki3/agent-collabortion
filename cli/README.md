# colab CLI / MCP (stream C)

`colab` is the only path an agent has back to the platform (contracts/colab-cli.md).
It works for any runtime with a shell; `colab mcp serve` exposes the same
commands as MCP tools with the same JSON arguments and results.

P1 surface: `session get` · `session messages` · `message post` · `mcp serve` · `version`.

## Build · test

```sh
cd cli && go vet ./... && go test -race ./...
go build -o ../bin/colab ./cmd/colab
```

Only the standard library is used; the MCP server is a minimal newline-delimited
JSON-RPC 2.0 loop (`internal/mcp`) so the CLI stays one static binary.

## Environment (set by the daemon — `contracts/colab-cli.md` §1, `harness.md` §2.1)

This table is the contract set, nothing more. The daemon sets exactly these.

| var | meaning |
|---|---|
| `COLAB_TASK_TOKEN` | attempt-scoped token, sent as `Authorization: Bearer`. Missing → exit 4 `no_token` (test chat, E15-04). Revoked → `401 token_revoked` → exit 4 (E11-04). |
| `COLAB_SERVER_URL` | server **origin** (e.g. `https://colab.example`); the CLI appends `/api/v1` (openapi `servers[0].url`). |
| `COLAB_TASK_ID` | this task (Idempotency-Key input, default for path parameters). |
| `COLAB_TASK_ATTEMPT` | this attempt. Marks the attempt boundary for the seq state (below); not part of the key. |
| `COLAB_LANE_ID` `COLAB_SESSION_ID` `COLAB_AGENT_NAME` | defaults when a command omits the argument. |
| `COLAB_API_PREFIX` | optional override of the `/api/v1` prefix (contract §1). |

Anything missing from the environment is resolved once via `GET /cli/context`.

## CLI-internal state (not part of the environment contract)

The daemon never sets these; they exist for tests and manual retries.

| var | meaning |
|---|---|
| `COLAB_STATE_DIR` | where the seq state lives (default `$XDG_STATE_HOME/colab` or `~/.local/state/colab`). One file per task, `seq-<task_id>`, holding `<attempt> <seq>`. |
| `COLAB_CLIENT_SEQ` | forces the seq of the next `message post` (re-sends that seq's key; same effect as `--idempotency-key`). |

## Output · exit codes

All commands print JSON on stdout (`--json` is accepted and is the default).
Errors print `{"error":{"code","exit","status","title","detail","problem"}}` on
stdout plus one line on stderr.

| exit | meaning | HTTP |
|---|---|---|
| 0 | ok | 2xx |
| 2 | argument error (missing `--body`, unknown `--mention`, bad `--limit`) | — |
| 3 | refused: permission · state · policy | 403 · 404 · 409 · 422 |
| 4 | no token / token revoked | 401 (`code: token_revoked`) |
| 5 | server unreachable | network error · 5xx |

## Commands

```sh
colab session get [--session S]
colab session messages [--since <cursor|message_id>] [--limit N] [--thread <root_id>]
colab message post --body <text> [--reply-to <msg_id>] [--mention @A,@B] [--idempotency-key K]
colab mcp serve
colab version
```

`session messages --since <x>` is sent to the server as the `after=<x>` query
parameter (messages newer than that cursor / message id). `--limit` must be
1..200 when given; an explicit `--limit 0` is exit 2, omit it for the server
default (50).

`message post` sends `Idempotency-Key: UUIDv5(namespace, "task:<task_id>:<seq>")`
automatically (`contracts/colab-cli.md` §1 v0.2). The namespace is fixed:
`UUIDv5(NameSpace_DNS, "colab")` = `454e4096-cb98-57f5-b314-6c5499b55cc8`. The
**attempt is not part of the key** — `seq` is task-scoped and continues across
attempts: on an attempt boundary (first post of an attempt, or the state file was
written by another attempt / another host) the CLI reads `last_seq` from
`GET /cli/context` and continues at `last_seq + 1`; within the attempt the seq state
file is authoritative and no round trip is needed. So attempt 2 never re-uses an
attempt-1 key, and a network re-send of the same seq (`--idempotency-key`,
`COLAB_CLIENT_SEQ`) returns the first response with `"replayed": true` and stores
nothing (E8-04). Re-posting the same *content* under a new seq is a new message —
skipping already-posted messages is the resume prompt's `posted_message_ids` job.
`--mention` resolves each name to the participant's
`mention_link` from `/cli/context` and prepends it to the body; a name that is not a
session participant is exit 2 `unknown_mention` with the roster in `detail` (FR-1.5).

`message post` result:

```json
{
  "message_id": "…",
  "triggered": ["Reviewer"],
  "suppressed": ["Lead"],
  "triggers": [{"agent_id":"…","task_id":"…","lane_id":"…","coalesced":false}],
  "warnings": [{"code":"suppressed_delegator","message":"…","agent_id":"…"}],
  "idempotency_key": "47beee27-4c46-5269-ac72-040d886f1259",
  "replayed": false
}
```

`triggered` is the agent names of `triggers[]`; `suppressed` is the agent names of
`warnings[]` whose `code` is exactly `suppressed_delegator` (rule 8). Other warning
codes (`not_participant`, `loop_limit_near`, `agent_disabled`) stay in `warnings[]`
only.

`session messages` result adds `included` / `total` / `truncated` (E8-12) around the
server's `items[]` and cursors.

## MCP

`colab mcp serve` speaks MCP over stdio (protocol `2025-06-18`): `initialize`,
`ping`, `tools/list`, `tools/call`. Tools: `colab_session_get`,
`colab_session_messages`, `colab_message_post` — arguments are the command's flags
as JSON (`body`, `reply_to`, `mention: ["@A"]`, `since`, `limit`, `thread`,
`session`). Results come back as `structuredContent` plus the same JSON as text.
Command failures are tool results with `isError: true` and the CLI error object,
not JSON-RPC errors, so the model can read `code`/`detail`.

The daemon registers it as the only MCP server (`harness.md` §3):

```json
{"mcpServers": {"colab": {"command": "colab", "args": ["mcp", "serve"]}}}
```

## Brief text for agents (draft for brief section [2])

> You talk to the platform only through the `colab` CLI (or the `colab_*` MCP tools —
> same arguments, same results). Everything prints JSON.
>
> - `colab session get` — the goal, acceptance criteria, completion progress and the
>   participant roster (name · role · status). Read it before you start.
> - `colab session messages [--since <id>] [--limit N] [--thread <root_id>]` — read more
>   of the thread when the history in this prompt says `truncated: true`
>   (`--since` = messages newer than that id).
> - `colab message post --body "<markdown>" [--reply-to <msg_id>] [--mention @Name,@Name]` —
>   post to the session. **Your message triggers another agent only if you `--mention`
>   them.** Mentioning your delegator is suppressed until you rejoin — use
>   `colab status set blocked` for questions to them. The result tells you who was
>   `triggered` and who was `suppressed`.
> - Exit codes: 0 ok · 2 bad arguments · 3 refused (read `detail`) · 4 your token is
>   gone — **stop immediately** · 5 server unreachable — retry the same command with
>   the printed `idempotency_key`.
> - Messages you already posted are listed in this prompt; do not post them again.
