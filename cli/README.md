# colab CLI / MCP (stream C)

`colab` is the only path an agent has back to the platform (contracts/colab-cli.md).
It works for any runtime with a shell; `colab mcp serve` exposes the same
commands as MCP tools with the same JSON arguments and results.

P1 surface: `session get` · `session messages` · `message post` · `mcp serve` · `version`.
P2 surface (`contracts/colab-cli.md` v0.4 §2.2·2.3): `status set` · `lane delegate` ·
`decision record` · `artifact submit` · `artifact get` · `review approve|reject`.

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

Anything missing from the environment is resolved via `GET /cli/context`, which is
fetched **on first need and cached for the life of the process** — at most one round
trip, not one per command (contract §1, backlog C-1). An unconditional preflight would
double every command's request count for nothing: a revoked token surfaces as `401` on
each command's own request anyway. A command that needs nothing from the context
(`artifact get`, or any command whose ids come from the environment) never fetches it.

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
| 2 | argument error (missing `--body`, unknown `--mention`, bad `--limit`, `status set blocked` without `--note`, `review` without `--artifact`) | — |
| 3 | refused: permission · state · policy | 403 · 404 · 409 · 422 |
| 4 | no token / token revoked | 401 (`code: token_revoked`) |
| 5 | server unreachable | network error · 5xx |

## Commands

```sh
colab session get [--session S]
colab session messages [--since <cursor|message_id>] [--limit N] [--thread <root_id>]
colab message post --body <text> [--reply-to <msg_id>] [--mention @A,@B] [--idempotency-key K]

colab status set working|blocked|done [--note <text>]
colab lane delegate --agent <name> --brief <text> [--depends-on <lane_id>] [--profile <name>]
colab decision record --summary <s> [--rationale <r>]
colab artifact submit --type <t> --file <p> [--name <n>] [--description <d>]
colab artifact get <id> [--out <path>]
colab review approve --artifact <id> [--note <t>]
colab review reject  --artifact <id> --reason <text>

colab mcp serve
colab version
```

`session messages --since <x>` is sent to the server as the `after=<x>` query
parameter (messages newer than that cursor / message id). `--limit` must be
1..200 when given; an explicit `--limit 0` is exit 2, omit it for the server
default (50).

`message post` sends `Idempotency-Key: UUIDv5(namespace, "task:<task_id>:<seq>")`
automatically (`contracts/colab-cli.md` §1 v0.2), together with
`X-Colab-Client-Seq: <seq>` — the same seq the key was derived from (v0.3,
openapi `ClientSeq`). The server stores it as `idempotency_key.client_seq` and
answers `last_seq = max(client_seq)`, so a hole in the seq (a post that failed
on the network) never leads to a key reuse. With an explicit `--idempotency-key`
the seq is unknown, so the header is omitted and the server falls back to its
UUIDv5 probe. The namespace is fixed:
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

## P2 commands

### `status set`

`--note` is required for `blocked`: it *is* the question. The server marks the lane
`blocked`, posts the question card on the lane thread and wakes the delegator
immediately (Director inbox `lane_blocked` when there is no delegator, E3-08). The
result is the server's `setTaskStatus` body — `task`, `lane`, `question_message_id`
and:

```json
{"status": "blocked", "turn_end_required": true, "question_message_id": "…"}
```

`turn_end_required: true` means **end your turn now**. The CLI reports the server's
value and never computes its own. The flag has exactly one name across CLI, server and
daemon (contract v0.4 §2.4): ACP's `stopReason: end_turn` states that a turn already
ended, this field instructs one to end, and one name for both is how P1's
`kind` ↔ `runtime_kind` defect happened.

### `lane delegate`

Always a new lane (resolution rule 2); `delegated_from_task_id` is the calling task,
which is the rejoin group key (FR-6.5). `--agent` is a participant *name*; the CLI
resolves it against the `/cli/context` roster and sends `agent_id`. A target that is
not a session participant is refused **by the CLI**, before any request, with exit 3:

```json
{"error": {"exit": 3, "code": "not_participant",
  "detail": "… participants: Researcher, Reviewer, Lead. ask the Director to add them as a participant with `colab hitl ask` …"}}
```

Agents cannot add participants (FR-1.5), so the alternative route is named in the
error rather than left to be guessed (E15-02). `--depends-on` is repeatable and
accepts comma-separated ids; v1 stores them and DAG execution is v1.1.

### `decision record`

Exactly two fields, `--summary` and `--rationale` (`--title`/`--body` are aliases) —
that is the whole `Decision` schema. v0.3's `--options`/`--chosen` were removed in
v0.4 rather than folded into the rationale text: a structured-looking string nobody
can query is worse than not accepting the input.

### `artifact submit` · `artifact get`

`submit` is `multipart/form-data {name, type, file, description}`, matching openapi
`submitArtifact` exactly. `--file` is the file (`--path` is an alias); `--name`
defaults to its base name and re-submitting the same name is version+1 (FR-4.3).
`--type` is an open set (`file` · `diff` · `branch` · `doc` · `report` …). The 50 MB
ceiling is checked locally before the upload. There is **no `--url`**: openapi has no
url part, and an absent flag tells the truth about an absent feature.

`get` returns metadata; with `--out` it also downloads the body (`--out` may be a
directory, in which case the `Content-Disposition` filename is used). This is the only
cross-lane read (FR-6.1) — worktree paths are never exposed.

### `review approve` · `review reject`

`POST /artifacts/{id}/review`, so `--artifact` is required (exit 2 without it).
`--note` (approve) and `--reason` (reject, required) both become the request's
`comments`. An agent the `agent_approval` completion condition did not designate gets
the server's `403 not_designated_reviewer`, which the CLI reports as exit 3 with
`code: not_reviewer` (E6-06); nothing is stored, and the server's own problem stays in
`error.problem`.

### Idempotency

The four P2 write operations take an **optional** `Idempotency-Key`, so the CLI sends
one only when `--idempotency-key` is given. They deliberately do not consume the
`message post` seq counter: the server computes `last_seq = max(client_seq)` from the
`X-Colab-Client-Seq` header, so a second consumer of that space would make
`message post` re-use a key.

## MCP

`colab mcp serve` speaks MCP over stdio (protocol `2025-06-18`): `initialize`,
`ping`, `tools/list`, `tools/call`. One tool per command, named for the command path
with underscores (contract §3): `colab_session_get`, `colab_session_messages`,
`colab_message_post`, `colab_status_set`, `colab_lane_delegate`,
`colab_decision_record`, `colab_artifact_submit`, `colab_artifact_get`,
`colab_review_approve`, `colab_review_reject`. Arguments are the command's flags as
JSON (`body`, `reply_to`, `mention: ["@A"]`, `status`, `note`, `agent`, `brief`,
`summary`, `type`, `file`, `artifact`, `reason`, …). Results come back as
`structuredContent` plus the same JSON as text. `TestP2CommandAndMCPToolAgree`
(`cmd/colab/p2_test.go`) runs every P2 command both ways against the same fake server
and requires the two documents to be equal.
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
> - `colab status set blocked --note "<question>"` — ask **your delegator** something and
>   stop. The reply's `turn_end_required: true` means end your turn now; the answer
>   arrives as a reply that restarts you. `colab status set done` when this turn's work
>   is finished.
> - `colab lane delegate --agent <Name> --brief "<what to do>"` — hand work to another
>   **existing participant**. You cannot create participants; if the name is refused
>   with `not_participant`, ask the Director via `colab hitl ask`.
> - `colab decision record --summary "<what>" --rationale "<why>"` — record a decision so
>   later turns and the Director can see it.
> - `colab artifact submit --type <t> --file <path>` — hand over a result. This is what
>   the session's completion conditions read. `colab artifact get <id> [--out <path>]` is
>   the only way to read another lane's work.
> - `colab review approve|reject --artifact <id>` — `reject` needs `--reason`, which is
>   posted back on the artifact's thread.
> - Exit codes: 0 ok · 2 bad arguments · 3 refused (read `detail`) · 4 your token is
>   gone — **stop immediately** · 5 server unreachable — retry the same command with
>   the printed `idempotency_key`.
> - Messages you already posted are listed in this prompt; do not post them again.
