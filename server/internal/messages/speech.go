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
	// TriggerSpeech · TriggerAddressees are the trigger message's own stored
	// speech and addressees. A turn woken by a report **addressed to this
	// author** answers it with a request, never a report — 보고에 대한 보고는
	// 없다 (PRD FR-3.1.3 표 8행 보완, T-AGENTFIX B6). An agent woken only by a
	// body mention chip inside someone's report is not its addressee; what it
	// returns is still its own report (review #343 블로커 1).
	TriggerSpeech     string
	TriggerAddressees []Addressee

	// Upstream is PRD FR-3.1.3 「윗선 지시」 (v0.19.8): for a turn woken by a
	// report addressed to this author, the instruction that report ultimately
	// answers — the trigger of the task that wrote the trigger report's
	// responds_to, walked up through further reports (최대 5단, 시스템·자기
	// 자신에서 멈춘다). Store walks it with rows at write time; nil when not
	// found. Its author is the 「윗선 요청자」.
	UpstreamMessageID  *uuid.UUID
	UpstreamAuthorType string
	UpstreamAuthorID   *uuid.UUID
	UpstreamAuthorName string

	// PRD FR-3.8 (v0.19.15, openapi v0.3.10) premises — set only by the
	// caller that knows (router.postRow, cards.StoreResult), never read back
	// from the body. A row written without them classifies as before, which
	// is what keeps the old backfill's parity (TestConvoSpeech_Backfill…).
	//
	// MentionAsks: the agent mentions in this agent message make QUESTION
	// tasks (no card hands work over) — the speech is `question`, not
	// `request`.
	MentionAsks bool
	// AnswersAsker: this message is written in a question task's turn, and
	// this is who asked — what it says to them is `answer`.
	AnswersAsker *Addressee
	// CardReportTo · CardRespondsTo: a result card bubble — `report` to the
	// card's delegator, answering the version's delegation bubble.
	CardReportTo   *Addressee
	CardRespondsTo *uuid.UUID
}

// UpstreamMaxSteps is PRD FR-3.1.3 「윗선 지시」's bound: at most this many
// trigger messages are read on the way up.
const UpstreamMaxSteps = 5

// woke by a report addressed to this author — the premise of PRD FR-3.1.3
// 「보고를 받은 뒤의 말」(v0.19.8). A body mention chip inside someone's
// report does not make its reader an addressee (review #343 블로커 1).
func wokenByReportTo(in SpeechInput) bool {
	return in.TriggerMessageID != nil && in.AuthorID != nil &&
		in.TriggerSpeech == string(gen.MessageSpeechReport) &&
		contains(in.TriggerAddressees, Addressee{Kind: "agent", ID: in.AuthorID})
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
	if in.AuthorType == "agent" && in.CardReportTo != nil {
		// openapi v0.3.10: the result card bubble is a report to the card's
		// delegator, ↩ the delegation bubble of that version.
		return SpeechOut{Speech: string(gen.MessageSpeechReport), Addressees: []Addressee{*in.CardReportTo}, RespondsTo: in.CardRespondsTo}
	}
	if in.AuthorType == "agent" && in.AnswersAsker != nil && !in.MentionAsks &&
		(len(base) == 0 || contains(base, *in.AnswersAsker)) {
		// PRD FR-3.8 2 (Lead 판정 Q2): what a question turn says to the one who
		// asked is an answer — never a report, never another question.
		to := []Addressee{*in.AnswersAsker}
		for _, a := range mentioned {
			to = appendUniq(to, a)
		}
		// responds_to is a report's only (message_responds_to_shape); the
		// thread or the addressee already says what is answered.
		return SpeechOut{Speech: string(gen.MessageSpeechAnswer), Addressees: to}
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
		if wokenByReportTo(in) {
			// PRD FR-3.1.3 「보고를 받은 뒤의 말 — 누구에게 하는가」(v0.19.8,
			// Director 지적 2026-09-27): the turn was woken by a report made TO
			// this author. What it says next is split by its mentions.
			for _, a := range mentioned {
				if a.Kind == "agent" {
					// 에이전트를 멘션 → 요청(10번), 멘션된 쪽에게 (T-AGENTFIX B6).
					// v0.3.10: 카드 없이 에이전트에게 한 말은 질문이다.
					return SpeechOut{Speech: agentAsk(in), Addressees: mentioned}
				}
			}
			up := authorAsAddressee(in.UpstreamAuthorType, in.UpstreamAuthorID, in.UpstreamAuthorName)
			if in.UpstreamMessageID == nil {
				// 윗선 지시가 없으면 그 작성자도 없다 — 짝이 맞지 않는 입력은
				// 「못 찾음」으로 본다(review #370 NN1).
				up = nil
			}
			people := []Addressee{}
			for _, a := range mentioned {
				if a.Kind != "all" {
					// `@all` 은 사람이 아니라 방 전체다 — 보고의 받는 쪽에서 뺀다
					// (PRD 표 2행, #370 리뷰 B1 Lead 판정 2026-09-27).
					people = appendUniq(people, a)
				}
			}
			if len(people) > 0 {
				// 사람만 멘션(`@all` 이 같이 있어도) → 그 사람에게 보고. 윗선 지시가
				// 그 사람의 것일 때만 그 지시에 대한 보고다.
				var resp *uuid.UUID
				if up != nil && contains(people, *up) {
					r := *in.UpstreamMessageID
					resp = &r
				}
				return SpeechOut{Speech: string(gen.MessageSpeechReport), Addressees: people, RespondsTo: resp}
			}
			if up != nil && len(mentioned) == 0 {
				// 멘션 없음 → 윗선 요청자 한 명에게, 윗선 지시에 대한 보고.
				// 실측: Lead 가 Developer 의 보고를 받고 「Simplist 님, v9
				// 올렸습니다」— 요청(→ Developer)으로 보이던 말.
				r := *in.UpstreamMessageID
				return SpeechOut{Speech: string(gen.MessageSpeechReport), Addressees: []Addressee{*up}, RespondsTo: &r}
			}
			// `@all` 만(모두에게 한 말은 보고가 아니다) · 윗선을 못 찾음 → 대화(11번).
			return SpeechOut{Speech: string(gen.MessageSpeechChat), Addressees: base}
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
		return SpeechOut{Speech: agentAsk(in), Addressees: mentioned}
	}
	return SpeechOut{Speech: string(gen.MessageSpeechChat), Addressees: base}
}

// agentAsk is the speech of an agent mentioning another agent outside a
// delegation: `question` when that mention makes a question task (PRD FR-3.8
// 2 — the caller says so), else the old `request`.
func agentAsk(in SpeechInput) string {
	if in.MentionAsks {
		return string(gen.MessageSpeechQuestion)
	}
	return string(gen.MessageSpeechRequest)
}

// walkUpstream finds PRD FR-3.1.3 「윗선 지시」 with rows, at write time: from
// the trigger report, its responds_to (the message this author sent earlier —
// a delegation or request), then the trigger of the task that wrote it. If
// that is a report too, climb again — at most UpstreamMaxSteps triggers, and
// a system message or this author's own message stops the climb (not found).
//
// The SQL copy in *_message_speech_upstream.sql walks the same steps over its
// computed rows; the parity test holds them together.
func walkUpstream(ctx context.Context, q db.DBTX, in *SpeechInput) error {
	cur := *in.TriggerMessageID
	for step := 0; step < UpstreamMaxSteps; step++ {
		var up, upAuthor *uuid.UUID
		var upType, upName, upSpeech string
		err := q.QueryRow(ctx, `
			SELECT u.id, u.author_type::text, u.author_id, COALESCE(uu.display_name, ua.name, ''), COALESCE(u.speech, '')
			FROM message c
			JOIN message r ON r.id = c.responds_to_message_id AND r.author_type = 'agent'
			JOIN task t ON t.id = r.source_task_id
			JOIN message u ON u.id = t.trigger_message_id
			LEFT JOIN app_user uu ON u.author_type = 'user' AND uu.id = u.author_id
			LEFT JOIN agent ua ON u.author_type = 'agent' AND ua.id = u.author_id
			WHERE c.id = $1`, cur).
			Scan(&up, &upType, &upAuthor, &upName, &upSpeech)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("messages: speech upstream: %w", err)
		}
		if upType == "system" || upAuthor == nil || *upAuthor == *in.AuthorID {
			return nil
		}
		if upSpeech == string(gen.MessageSpeechReport) {
			cur = *up
			continue
		}
		in.UpstreamMessageID, in.UpstreamAuthorType, in.UpstreamAuthorID, in.UpstreamAuthorName = up, upType, upAuthor, upName
		return nil
	}
	return nil
}

// StoreOpts carries what only the caller knows: that this insert IS a
// delegation (router.Delegate) and which lane it created.
type StoreOpts struct {
	DelegatedLaneID    *uuid.UUID
	DelegateTargetID   *uuid.UUID
	DelegateTargetName string
	// PRD FR-3.8 — see SpeechInput.
	MentionAsks    bool
	AnswersAsker   *Addressee
	CardReportTo   *Addressee
	CardRespondsTo *uuid.UUID
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
	in.MentionAsks, in.AnswersAsker, in.CardReportTo, in.CardRespondsTo = opts.MentionAsks, opts.AnswersAsker, opts.CardReportTo, opts.CardRespondsTo
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
		var trigAddr []byte
		if err := q.QueryRow(ctx, `
			SELECT t.trigger_message_id, tm.author_type::text, tm.author_id, COALESCE(u.display_name, a.name, ''),
			       COALESCE(tm.speech, ''), tm.addressees
			FROM task t
			JOIN message tm ON tm.id = t.trigger_message_id
			LEFT JOIN app_user u ON tm.author_type = 'user' AND u.id = tm.author_id
			LEFT JOIN agent a ON tm.author_type = 'agent' AND a.id = tm.author_id
			WHERE t.id = $1`, *sourceTask).
			Scan(&in.TriggerMessageID, &in.TriggerAuthorType, &in.TriggerAuthorID, &in.TriggerAuthorName, &in.TriggerSpeech, &trigAddr); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("messages: speech trigger: %w", err)
		}
		if len(trigAddr) > 0 {
			_ = json.Unmarshal(trigAddr, &in.TriggerAddressees)
		}
		if wokenByReportTo(in) {
			if err := walkUpstream(ctx, q, &in); err != nil {
				return err
			}
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
