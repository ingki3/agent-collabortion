// Speech is PRD FR-3.1.3 / openapi v0.3.2 D24: the KIND of speech a message is
// (지시 · 위임 · 보고 · 질문 · 답 …) and WHO it is addressed to.
//
// The server decides it because the server is the only place that knows at
// write time: `router.Delegate` knows it is writing a delegation, and an agent
// post knows its own task's trigger message. A screen that reconstructs this
// afterwards has to guess (match the body against the lane brief, walk
// listLaneTasks per old turn), and every client would guess differently.
//
// Classify is pure and is the single rule table. Store runs it against one
// stored row, reading only the premises the table names, and writes the four
// columns. Every INSERT INTO message path calls Store, so a message never
// exists without its speech.
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// Addressee is one entry of openapi Message.addressees.
//
// ID is rendered even when null so the Go and the SQL copies of the rule table
// produce byte-identical jsonb (review #335 NN2) — `@all` has no id.
type Addressee struct {
	Kind string     `json:"kind"` // agent · user · all
	ID   *uuid.UUID `json:"id"`
	Name string     `json:"name"`
}

// SpeechInput is the FR-3.1.3 table's premises — nothing else is consulted.
// A premise that is absent must be the zero value, never a guess.
type SpeechInput struct {
	Kind       string // message_kind
	AuthorType string
	AuthorID   *uuid.UUID
	Mentions   []gen.Mention
	Content    string

	// Parent (thread root) — only its kind and author matter.
	ParentKind       string
	ParentAuthorType string
	ParentAuthorID   *uuid.UUID
	ParentAuthorName string

	// WaitingFor is PRD FR-3.1.3 표 3행 후반, for a question card with no
	// mention: who that lane waits on (`Lane.waiting_for`) — its delegator, or
	// the human who started the chain when nobody delegated it.
	WaitingForKind string // agent · user
	WaitingForID   *uuid.UUID
	WaitingForName string

	// Delegation: set by router.Delegate, which knows it is writing one.
	DelegatedLaneID    *uuid.UUID
	DelegateTargetID   *uuid.UUID
	DelegateTargetName string

	// Report: the message that woke the turn this message was posted from
	// (task.trigger_message_id) and its author — the requester.
	TriggerMessageID  *uuid.UUID
	TriggerAuthorType string
	TriggerAuthorID   *uuid.UUID
	TriggerAuthorName string
}

// SpeechOut is what gets stored (and returned by every read).
type SpeechOut struct {
	Speech        string
	Addressees    []Addressee
	RespondsTo    *uuid.UUID
	DelegatedLane *uuid.UUID
}

// IsNote is routing rule 1's premise and openapi Message.is_note: it is the
// `/note` prefix and nothing else.
func IsNote(content string) bool { return strings.HasPrefix(content, "/note") }

func same(a, b Addressee) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.ID == nil || b.ID == nil {
		return a.ID == b.ID && a.Name == b.Name
	}
	return *a.ID == *b.ID
}

func appendUniq(list []Addressee, a Addressee) []Addressee {
	for _, x := range list {
		if same(x, a) {
			return list
		}
	}
	return append(list, a)
}

// mentionAddressees is the content's mentions minus the author (nobody
// addresses themselves). `detail`'s mentions never get here: the column is not
// a premise, for the same reason routing ignores it (D23).
func mentionAddressees(in SpeechInput) []Addressee {
	out := []Addressee{}
	for _, m := range in.Mentions {
		if m.Kind == gen.MentionKindAll {
			out = appendUniq(out, Addressee{Kind: "all", Name: "all"})
			continue
		}
		id, err := uuid.Parse(m.Id)
		if err != nil {
			continue
		}
		if in.AuthorID != nil && id == *in.AuthorID {
			continue
		}
		name := ""
		if m.DisplayName != nil {
			name = *m.DisplayName
		}
		out = appendUniq(out, Addressee{Kind: string(m.Kind), ID: &id, Name: name})
	}
	return out
}

func authorAsAddressee(authorType string, id *uuid.UUID, name string) *Addressee {
	if authorType == "system" || id == nil {
		return nil
	}
	kind := "user"
	if authorType == "agent" {
		kind = "agent"
	}
	return &Addressee{Kind: kind, ID: id, Name: name}
}

func contains(list []Addressee, a Addressee) bool {
	for _, x := range list {
		if same(x, a) {
			return true
		}
	}
	return false
}

// Classify is the FR-3.1.3 table, in its order. A row whose premise is missing
// falls through to the next — it never guesses from the body text.
func Classify(in SpeechInput) SpeechOut {
	empty := []Addressee{}
	switch in.Kind {
	case "system":
		return SpeechOut{Speech: string(gen.MessageSpeechSystem), Addressees: empty}
	case "hitl":
		// The HITL card names its own recipient (SCREEN §4.6).
		return SpeechOut{Speech: string(gen.MessageSpeechHitl), Addressees: empty}
	}
	mentioned := mentionAddressees(in)
	switch in.Kind {
	case "blocked_q":
		// PRD FR-3.1.3 표 3행: 멘션(위임자)이 먼저고, 없으면 그 lane 이 기다리는
		// 상대(`Lane.waiting_for` — 위임자가 없는 lane 은 Director)다. 위임 없이
		// 사람이 직접 불러 만든 lane 의 질문 카드에는 멘션이 없다(review #335 NN3).
		if len(mentioned) == 0 && in.WaitingForName != "" {
			return SpeechOut{Speech: string(gen.MessageSpeechQuestion),
				Addressees: []Addressee{{Kind: in.WaitingForKind, ID: in.WaitingForID, Name: in.WaitingForName}}}
		}
		return SpeechOut{Speech: string(gen.MessageSpeechQuestion), Addressees: mentioned}
	case "summary":
		// A mission summary is for the room.
		return SpeechOut{Speech: string(gen.MessageSpeechSummary), Addressees: empty}
	}
	parentAuthor := authorAsAddressee(in.ParentAuthorType, in.ParentAuthorID, in.ParentAuthorName)
	if in.ParentKind == "blocked_q" {
		to := []Addressee{}
		if parentAuthor != nil && (in.AuthorID == nil || *parentAuthor.ID != *in.AuthorID) {
			to = appendUniq(to, *parentAuthor)
		}
		for _, a := range mentioned {
			to = appendUniq(to, a)
		}
		return SpeechOut{Speech: string(gen.MessageSpeechAnswer), Addressees: to}
	}
	if IsNote(in.Content) {
		// 「기록만」 — a note addresses nobody, by rule 1.
		return SpeechOut{Speech: string(gen.MessageSpeechNote), Addressees: empty}
	}
	// A thread reply with no mention is addressed to whoever it replies to.
	base := mentioned
	if len(base) == 0 && parentAuthor != nil && (in.AuthorID == nil || *parentAuthor.ID != *in.AuthorID) {
		base = []Addressee{*parentAuthor}
	}
	if in.AuthorType == "agent" {
		if in.DelegatedLaneID != nil && in.DelegateTargetID != nil {
			to := Addressee{Kind: "agent", ID: in.DelegateTargetID, Name: in.DelegateTargetName}
			for _, a := range mentioned {
				if same(a, to) && a.Name != "" {
					to.Name = a.Name
				}
			}
			lane := *in.DelegatedLaneID
			return SpeechOut{Speech: string(gen.MessageSpeechDelegate), Addressees: []Addressee{to}, DelegatedLane: &lane}
		}
		requester := authorAsAddressee(in.TriggerAuthorType, in.TriggerAuthorID, in.TriggerAuthorName)
		if in.TriggerMessageID != nil && requester != nil &&
			(in.AuthorID == nil || *requester.ID != *in.AuthorID) &&
			(len(base) == 0 || contains(base, *requester)) {
			// PRD FR-3.1.3 표 7행 받는 쪽 = **요청자** 한 명이다(review #335 R3).
			// 같은 메시지가 다른 에이전트도 부르면 그쪽은 라우팅이 깨우고(FR-3.3)
			// 화면에는 본문 멘션 칩으로 남는다 — 보고받은 쪽으로 보이지 않는다.
			to := []Addressee{*requester}
			trig := *in.TriggerMessageID
			return SpeechOut{Speech: string(gen.MessageSpeechReport), Addressees: to, RespondsTo: &trig}
		}
	}
	agentMentioned := false
	for _, a := range mentioned {
		if a.Kind == "agent" {
			agentMentioned = true
		}
	}
	if agentMentioned {
		if in.AuthorType == "user" {
			return SpeechOut{Speech: string(gen.MessageSpeechInstruct), Addressees: mentioned}
		}
		return SpeechOut{Speech: string(gen.MessageSpeechRequest), Addressees: mentioned}
	}
	return SpeechOut{Speech: string(gen.MessageSpeechChat), Addressees: base}
}

// StoreOpts carries what only the caller knows: that this insert IS a
// delegation (router.Delegate) and which lane it created.
type StoreOpts struct {
	DelegatedLaneID    *uuid.UUID
	DelegateTargetID   *uuid.UUID
	DelegateTargetName string
}

// Store classifies one stored message and writes speech · addressees ·
// responds_to_message_id · delegated_lane_id. It reads the premises the table
// names — the row itself, its parent, and the trigger message of the task that
// posted it — and nothing else.
//
// It runs in the caller's transaction, right after the INSERT, so no message
// is ever visible without its speech.
func Store(ctx context.Context, q db.DBTX, msgID uuid.UUID, opts StoreOpts) error {
	var in SpeechInput
	var mentions []byte
	var parentID, sourceTask *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT m.kind::text, m.author_type::text, m.author_id, m.mentions, m.content, m.parent_id, m.source_task_id
		FROM message m WHERE m.id = $1`, msgID).
		Scan(&in.Kind, &in.AuthorType, &in.AuthorID, &mentions, &in.Content, &parentID, &sourceTask)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("messages: speech premises: %w", err)
	}
	if len(mentions) > 0 {
		_ = json.Unmarshal(mentions, &in.Mentions)
	}
	if parentID != nil {
		// The thread ROOT decides 「답」 — a reply to a reply is still an answer
		// to the question card (FR-6.2.1: the card IS the root).
		if err := q.QueryRow(ctx, `
			SELECT p.kind::text, p.author_type::text, p.author_id, COALESCE(u.display_name, a.name, '')
			FROM message p
			LEFT JOIN app_user u ON p.author_type = 'user' AND u.id = p.author_id
			LEFT JOIN agent a ON p.author_type = 'agent' AND a.id = p.author_id
			WHERE p.id = $1`, *parentID).
			Scan(&in.ParentKind, &in.ParentAuthorType, &in.ParentAuthorID, &in.ParentAuthorName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("messages: speech parent: %w", err)
		}
	}
	in.DelegatedLaneID, in.DelegateTargetID, in.DelegateTargetName = opts.DelegatedLaneID, opts.DelegateTargetID, opts.DelegateTargetName
	if in.Kind == "blocked_q" && len(in.Mentions) == 0 && sourceTask != nil {
		// 표 3행 후반: 멘션이 없으면 그 lane 이 기다리는 상대. `Lane.waiting_for` 는
		// 「blocked 면 위임자 이름 또는 Director」(openapi Lane) — 위임자가 있으면
		// 그 에이전트, 없으면 이 사슬을 시작한 사람이다(review #335 NN3).
		var agentID, userID *uuid.UUID
		var agentName, userName string
		if err := q.QueryRow(ctx, `
			SELECT d.agent_id, COALESCE(da.name, ''), t.originator_user_id, COALESCE(ou.display_name, '')
			FROM task t
			JOIN lane l ON l.id = t.lane_id
			LEFT JOIN task d ON d.id = l.delegated_from_task_id
			LEFT JOIN agent da ON da.id = d.agent_id
			LEFT JOIN app_user ou ON ou.id = t.originator_user_id
			WHERE t.id = $1`, *sourceTask).
			Scan(&agentID, &agentName, &userID, &userName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("messages: speech waiting_for: %w", err)
		}
		switch {
		case agentID != nil:
			in.WaitingForKind, in.WaitingForID, in.WaitingForName = "agent", agentID, agentName
		case userID != nil:
			in.WaitingForKind, in.WaitingForID, in.WaitingForName = "user", userID, userName
		}
	}
	if sourceTask != nil && opts.DelegatedLaneID == nil {
		if err := q.QueryRow(ctx, `
			SELECT t.trigger_message_id, tm.author_type::text, tm.author_id, COALESCE(u.display_name, a.name, '')
			FROM task t
			JOIN message tm ON tm.id = t.trigger_message_id
			LEFT JOIN app_user u ON tm.author_type = 'user' AND u.id = tm.author_id
			LEFT JOIN agent a ON tm.author_type = 'agent' AND a.id = tm.author_id
			WHERE t.id = $1`, *sourceTask).
			Scan(&in.TriggerMessageID, &in.TriggerAuthorType, &in.TriggerAuthorID, &in.TriggerAuthorName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("messages: speech trigger: %w", err)
		}
	}
	out := Classify(in)
	addr, err := json.Marshal(out.Addressees)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `
		UPDATE message SET speech = $2, addressees = $3, responds_to_message_id = $4, delegated_lane_id = $5
		WHERE id = $1`, msgID, out.Speech, addr, out.RespondsTo, out.DelegatedLane); err != nil {
		return fmt.Errorf("messages: speech store: %w", err)
	}
	return nil
}
