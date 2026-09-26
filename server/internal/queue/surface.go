package queue

import (
	"fmt"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Surface is every sentence the server writes that names a colab command,
// in the words of one tool surface (harness §10 v0.9.6). A `mcp` agent
// (claude_code) reaches the platform through the colab MCP tools and its shell
// has no `colab` on PATH — a brief that says `colab message post` makes it run
// that in the shell, fail, and only then fall back to the tool, and the failed
// shell call is on the person's 「작업 과정」 as an error (삼성전자 미션,
// 2026-09-25 12:32). A `cli_wrapper` agent (hermes) has no MCP and reads the
// shell command, which the daemon rewrites to the wrapper's path.
//
// The surface is picked from the bundle's runtime_kind — the same table the
// daemon prepares by (acp.DefaultToolSurface) — so two turns of the same agent
// on the same profile get byte-identical [1]~[5] (E12-11).
type Surface struct {
	// Kind is the harness §9/§10 tool_surface value: "mcp" | "cli_wrapper".
	Kind string

	// Brief [2] header and lines.
	Header      string
	PostLine    string
	ReadLine    string
	DetailRule  string
	Deliverable string
	// Brief [3] (lead only).
	HitlAskLine string
	// Brief [6]: the artifact lines.
	ArtifactsHeader string
	ReuseArtifacts  string // fmt: artifact count
	// The turn prompt: FR-4.1's truncation line, the closing instruction and
	// the thread-reply line (v0.9.3).
	RoomMessages string // how to read the room's messages, as a phrase
	Respond      string
	ThreadReply  string
	// threadRead names how to read one thread in full (the <history>
	// demotion line, v0.9.4·v0.9.5), given the message id.
	threadRead string // fmt: message id
	// harness v0.9.7: brief [2]'s fixed folder line (always the same bytes,
	// mission or not — E12-11) and the `<folders>` block's last sentence.
	FoldersRule string
	FoldersLast string
}

// Tool surface values (harness §9 runtime.capabilities[].tool_surface).
const (
	SurfaceMCP        = "mcp"
	SurfaceCLIWrapper = "cli_wrapper"
)

// DetailRule and DeliverableRule are brief [2]'s 「대화와 작업 내용」 lines
// for the shell surface (harness §10 v0.9.4, PRD FR-3.1.2); DetailRuleMCP and
// DeliverableRuleMCP are the same rule in tool words (v0.9.6). They are
// constants — [2] must stay byte-identical between two turns of the same
// agent (E12-11). The deliverable line stands alone so the daemon's role filter
// (harness v0.8.10) drops only it for a role without `artifact submit`.
const (
	DetailRule         = "`--body` is the conversation: who it is for, what, the conclusion and the next step, about five lines. Research results, full drafts and tables go in `--detail` (or `--detail-file <path>`) — the screen folds them under the conversation, and the agent you hand the work to reads them in full."
	DeliverableRule    = "A final deliverable is not a message: submit it with `colab artifact submit`, and say in `--body` that it is there instead of pasting it again."
	DetailRuleMCP      = "`body` is the conversation: who it is for, what, the conclusion and the next step, about five lines. Research results, full drafts and tables go in `detail` (or `detail_file` with a file path, relative to your working folder; not both) — the screen folds them under the conversation, and the agent you hand the work to reads them in full."
	DeliverableRuleMCP = "A final deliverable is not a message: submit it with the `colab_artifact_submit` tool, and say in `body` that it is there instead of pasting it again."
)

// ThreadReplyInstruction is harness §10 v0.9.3's closing line for a turn that
// started in a thread: 「스레드로 들어온 메시지에는 그 스레드에 답한다 —
// `colab message post` 는 기본으로 그 스레드에 답글을 단다. 메인 타임라인에
// 올려야 할 때만 `--top-level`」. Only a threaded turn gets it — a top-level
// turn has no COLAB_THREAD_ID and nothing to choose between.
// ThreadReplyInstructionMCP is the same line for the tool (v0.9.6).
// FoldersRule / FoldersRuleMCP are harness v0.9.7's brief [2] line, and
// FoldersLast / FoldersLastMCP the `<folders>` block's last sentence — word
// for word the contract's, per tool surface (v0.9.6 rule: a `mcp` agent is
// never told a shell `colab …`).
const (
	FoldersRule    = "- Your folders are listed in the turn prompt's `<folders>`: your working folder, this mission's shared folder and your mission peers' folders. Write only in your own folder and the shared folder; read your peers' folders but do not edit them. Anything from another mission or another room comes as an artifact or through `colab room read`."
	FoldersRuleMCP = "- Your folders are listed in the turn prompt's `<folders>`: your working folder, this mission's shared folder and your mission peers' folders. Write only in your own folder and the shared folder; read your peers' folders but do not edit them. Anything from another mission or another room comes as an artifact or through the `colab_room_read` tool."
	FoldersLast    = "Folders of other missions and other rooms are not listed here: ask for an artifact, or use `colab room read`."
	FoldersLastMCP = "Folders of other missions and other rooms are not listed here: ask for an artifact, or use the `colab_room_read` tool."
)

const (
	ThreadReplyInstruction    = "A trigger message with a `thread` attribute was posted in that thread: answer in the thread. `colab message post` replies to that thread by default; add `--top-level` only when the reply belongs on the main timeline."
	ThreadReplyInstructionMCP = "A trigger message with a `thread` attribute was posted in that thread: answer in the thread. `colab_message_post` replies to that thread by default; set `top_level` only when the reply belongs on the main timeline (or `reply_to` to answer one message)."
)

var shellSurface = Surface{
	Kind:            SurfaceCLIWrapper,
	Header:          "[2] Workspace rules and colab CLI\n",
	PostLine:        "- Post every reply to the session with `colab message post --body \"<text>\"`. Text you print to stdout is NOT delivered.\n",
	ReadLine:        "- Read more history with `colab room messages`, room details with `colab room get`.\n",
	DetailRule:      DetailRule,
	Deliverable:     DeliverableRule,
	HitlAskLine:     "- When a decision needs a person, ask with `colab hitl ask` rather than guessing; the answer comes back in the next turn's `<resumed>`.\n",
	ArtifactsHeader: "Artifacts submitted in this session (read one with `colab artifact get <id>` — the id, not the name, is at the end of each line):\n",
	ReuseArtifacts:  "이전 세션의 아티팩트 %d개 — `colab artifact get <id>` 로 읽어라(이름이 아니라 id).\n",
	RoomMessages:    "`colab room messages`",
	Respond:         "Respond to the trigger. Post your reply with `colab message post`; mention the person or agent you are answering when a reply is expected.\n",
	ThreadReply:     ThreadReplyInstruction,
	threadRead:      "`colab room messages --thread %s`",
	FoldersRule:     FoldersRule,
	FoldersLast:     FoldersLast,
}

var mcpSurface = Surface{
	Kind:            SurfaceMCP,
	Header:          "[2] Workspace rules and colab tools\n",
	PostLine:        "- Post every reply to the session with the `colab_message_post` tool (`body`; `mention` names the agents you hand work to). Text you print to stdout is NOT delivered. The colab tools are MCP tools, not shell commands.\n",
	ReadLine:        "- Read more history with the `colab_room_messages` tool, room details with `colab_room_get`.\n",
	DetailRule:      DetailRuleMCP,
	Deliverable:     DeliverableRuleMCP,
	HitlAskLine:     "- When a decision needs a person, ask with the `colab_hitl_ask` tool rather than guessing; the answer comes back in the next turn's `<resumed>`.\n",
	ArtifactsHeader: "Artifacts submitted in this session (read one with the `colab_artifact_get` tool — its `artifact` is the id at the end of each line, not the name):\n",
	ReuseArtifacts:  "이전 세션의 아티팩트 %d개 — `colab_artifact_get` 툴로 읽어라(`artifact` 는 이름이 아니라 id).\n",
	RoomMessages:    "the `colab_room_messages` tool",
	Respond:         "Respond to the trigger. Post your reply with the `colab_message_post` tool; mention the person or agent you are answering when a reply is expected.\n",
	ThreadReply:     ThreadReplyInstructionMCP,
	threadRead:      "`colab_room_messages` 툴의 `thread: \"%s\"`",
	FoldersRule:     FoldersRuleMCP,
	FoldersLast:     FoldersLastMCP,
}

// SurfaceFor is the text set for a profile's runtime_kind: hermes reads the
// shell (cli_wrapper), every other runtime the MCP tools — the daemon's
// acp.DefaultToolSurface table.
func SurfaceFor(runtimeKind string) Surface {
	if contracts.RuntimeKind(runtimeKind) == contracts.RuntimeHermes {
		return shellSurface
	}
	return mcpSurface
}

// ThreadRead is how to read the thread of message id in full.
func (s Surface) ThreadRead(id string) string { return fmt.Sprintf(s.threadRead, id) }

// Section2 is brief [2] in this surface's words.
func (s Surface) Section2() string {
	return s.Header +
		"- Mention syntax: [@Name](mention://agent/<id>). Only mention session participants listed in [5].\n" +
		s.PostLine +
		s.ReadLine +
		"- " + s.DetailRule + "\n" +
		"- " + s.Deliverable + "\n" +
		s.FoldersRule + "\n" +
		"- Mentioning an agent creates work for it; do not mention agents just to acknowledge.\n" +
		"- Your COLAB_TASK_TOKEN is valid for this attempt only; if a call returns token_revoked, stop immediately.\n\n"
}
