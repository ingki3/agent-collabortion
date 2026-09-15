package events

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Masking is PRD §9's workspace switch (`workspace_settings.task_event_masking`,
// openapi WorkspaceSettings) applied to the activity feed.
//
// WHAT §9 ACTUALLY PROMISES. "소스 트리 전체는 사용자 머신에 남는다. 다만
// 에이전트가 읽고 쓴 내용 — 파일 편집 diff, 셸 명령과 출력 — 은 활동 로그로
// 서버에 저장된다." The switch is how a workspace declines that second half:
// the SHAPE of the feed survives (what tool ran, on which file, how many lines,
// what exit code) and the CONTENT does not.
//
// WHAT IS DELIBERATELY KEPT. Paths, line counts, exit codes and durations stay.
// They are metadata, not content, and they are the whole of what makes the feed
// readable — a masked feed that says only "a tool ran" is one nobody opens, and
// a workspace that turns masking on would then have no activity log at all
// rather than a discreet one.
//
// The masking happens at INGEST, before the row is written. Masking on read
// would leave the raw diff on the server's disk, which is the exact thing the
// setting exists to prevent.
func Masking(ctx context.Context, q db.DBTX, wsID uuid.UUID) (bool, error) {
	var on bool
	err := q.QueryRow(ctx,
		`SELECT COALESCE((SELECT task_event_masking FROM workspace_settings WHERE workspace_id = $1), false)`,
		wsID).Scan(&on)
	if err != nil {
		return false, fmt.Errorf("events: masking setting: %w", err)
	}
	return on, nil
}

// Mask rewrites one event's payload in place and reports whether anything was
// replaced. An event with nothing to hide is not marked `masked` — a flag on
// every row would make the one that IS redacted invisible.
func Mask(e *contracts.TaskEvent) bool {
	if e.Payload == nil {
		return false
	}
	masked := false
	replace := func(key string) {
		v, ok := e.Payload[key].(string)
		if !ok || v == "" {
			return
		}
		e.Payload[key] = fmt.Sprintf("[마스킹됨 · %d자]", len([]rune(v)))
		masked = true
	}
	switch e.Class {
	case "message":
		// The agent's own words. `chars` survives so the feed can still show
		// that it said something and how much.
		if txt, ok := e.Payload["text"].(string); ok && txt != "" {
			if _, has := e.Payload["chars"]; !has {
				e.Payload["chars"] = len([]rune(txt))
			}
			replace("text")
		}
	case "tool":
		// `summary` is the tool's OUTPUT — the shell's stdout, the diff. The
		// shell command itself is input: its first token stays (that is what
		// `object_ref` already carries per the schema, and it is what makes the
		// row say "ran git" rather than "ran something"), the arguments go.
		replace("summary")
		if headOnly(e, "command") {
			masked = true
		}
		// S-77: `title` is whatever the adapter chose to call the tool call,
		// and for a shell tool that is the COMMAND LINE — Claude Code's ACP
		// adapter puts the whole of it there (`"title": "ls -la | grep manual"`
		// in the G6 database), so masking `command` alone left the arguments
		// in the row one key over. The schema is closed (S-52): no new key,
		// the value is cut to its first word like `command`.
		// A permission request (verb=permission, the schema's second `tool`
		// payload) names the tool call it is asking about with the same
		// command line — same key, same treatment.
		if headOnly(e, "title") {
			masked = true
		}
		if masked && e.Verb != "permission" {
			// The `tool` payload has its own `masked` flag in the schema, so
			// the row says so in both places rather than only in the column.
			// (The `permission` payload has no such key — closed schema.)
			e.Payload["masked"] = true
		}
	}
	return masked
}

// headOnly keeps the first word of a free-text key and replaces the rest with
// the summary, reporting whether anything was cut. A single-word value has no
// arguments to hide and is left as it is.
func headOnly(e *contracts.TaskEvent, key string) bool {
	v, ok := e.Payload[key].(string)
	if !ok || v == "" {
		return false
	}
	first, rest, found := strings.Cut(strings.TrimSpace(v), " ")
	if !found {
		return false
	}
	e.Payload[key] = first + " [마스킹됨 · " + fmt.Sprint(len([]rune(rest))) + "자]"
	return true
}
