// Package lanefocus is PRD FR-3.1.5 「지금 무엇을 풀고 있는지」 on the server
// side (openapi v0.3.8 Lane.focus · LaneFocus): the one sentence a running
// lane shows a person as 「지금 …」.
//
// There are exactly two writers and one eraser:
//
//   - the agent, through `colab status set working --note` (Declare,
//     source = agent) — router.SetAgentStatus;
//   - the server at the start of a turn, before the agent has said anything
//     (Derive, source = derived) — tasks.MarkDispatched. The sentence is
//     built from the trigger row alone, never guessed (FR-3.1.5 item 3);
//   - the end of the turn (turn_end · error · cancel · finish) empties it
//     (ClearIfIdle) — tasks.publish, the one place every task transition
//     already passes. After the turn the posted message speaks.
//
// It imports nothing that imports tasks, so tasks can call it.
package lanefocus

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Sources (openapi LaneFocusSource).
const (
	SourceAgent   = "agent"
	SourceDerived = "derived"
)

// MaxRunes is openapi LaneFocus.text maxLength — 「120자에서 자른다」.
const MaxRunes = 120

// TriggerRunes is FR-3.1.5 item 3's 「〈트리거 첫 40자〉」.
const TriggerRunes = 40

// CoalesceWindow is openapi setTaskStatus 「60초 안에 연속으로 오면 마지막 것만
// lane.updated 로 흘린다」.
const CoalesceWindow = 60 * time.Second

// The derived sentences (FR-3.1.5 item 3) — the server's wording table for
// this feature, locked by lanefocus_test.go and by internal/wording (the names
// end in Sentence, so the lock reads them as sentences for a person).
//
//	FocusRequestSentence   「〈작성자〉 의 「〈첫 40자〉」 요청을 처리하고 있습니다」
//	FocusDelegateSentence  「〈위임자〉가 맡긴 「〈첫 40자〉」를 하고 있습니다」
//	FocusNoticeSentence    a system line woke the turn (join bundle, re-entry
//	                       notice, a question's answer): it has no author, so
//	                       the notice itself is quoted.
//
// 〈작성자〉 is 「@이름」 for an agent and the display name for a person — the
// timeline's own convention (SCREEN §4.6 머리 「→ Simplist · → @Designer」). The
// space before 「의」 is the Pencil's (S7-C LgIO6 「@Lead 의 「…」」); the subject
// particle after the delegator is chosen by its last syllable (Subject).
const (
	FocusRequestSentence  = "%s 의 「%s」 요청을 처리하고 있습니다"
	FocusDelegateSentence = "%s 맡긴 「%s」를 하고 있습니다"
	FocusNoticeSentence   = "알림 「%s」에 따라 작업하고 있습니다"
)

// Subject appends the subject particle: 「이」 after a Hangul syllable with a
// final consonant, 「가」 otherwise (a Latin name reads 「@Lead 가」).
func Subject(word string) string {
	r := []rune(word)
	if len(r) > 0 {
		if last := r[len(r)-1]; last >= 0xAC00 && last <= 0xD7A3 && (last-0xAC00)%28 != 0 {
			return word + "이"
		}
	}
	return word + " 가"
}

// Truncate cuts s to MaxRunes runes, ending in 「…」 when it cut.
func Truncate(s string) string {
	return cut(s, MaxRunes)
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

var mentionLinkRe = regexp.MustCompile(`\[@([^\]]*)\]\(mention://(?:agent|user|all)/[^)\s]+\)`)
var leadingMentionsRe = regexp.MustCompile(`^(?:\s*\[@[^\]]*\]\(mention://(?:agent|user|all)/[^)\s]+\)[\s,.:·]*)+`)
var spaceRe = regexp.MustCompile(`\s+`)

// Plain is a trigger body as a person reads it on one line: mention links
// become 「@이름」, markdown emphasis marks go, whitespace (newlines too)
// collapses to one space.
func Plain(content string) string {
	s := mentionLinkRe.ReplaceAllString(content, "@$1")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// Quote is the trigger body as the derived sentence quotes it: the leading
// mentions go (they name who the request is FOR — 「@R 커브를 봐 주세요」 is
// quoted as 「커브를 봐 주세요」), the rest is Plain, cut at TriggerRunes.
func Quote(content string) string {
	return cut(Plain(leadingMentionsRe.ReplaceAllString(content, "")), TriggerRunes)
}

// Trigger is what Derive needs to know about the message that woke a turn.
type Trigger struct {
	// AuthorType is message.author_type: agent · user · system.
	AuthorType string
	// AuthorName is the agent's name or the person's display name ("" for
	// system).
	AuthorName string
	// Content is the message body.
	Content string
	// Delegated is true when the turn is a delegation (task
	// delegated_from_task_id is set): the trigger is the delegator's
	// `lane delegate` message — the server's mention link then the brief,
	// so Quote (which drops leading mentions) quotes the brief.
	Delegated bool
}

// Sentence is FR-3.1.5 item 3's derived sentence for t, or "" when there is
// nothing to quote (an empty trigger).
func Sentence(t Trigger) string {
	quote := Quote(t.Content)
	if quote == "" {
		return ""
	}
	who := strings.TrimSpace(t.AuthorName)
	if t.AuthorType == "agent" && who != "" {
		who = "@" + who
	}
	switch {
	case t.AuthorType == "system" || who == "":
		return Truncate(fmt.Sprintf(FocusNoticeSentence, quote))
	case t.Delegated:
		return Truncate(fmt.Sprintf(FocusDelegateSentence, Subject(who), quote))
	default:
		return Truncate(fmt.Sprintf(FocusRequestSentence, who, quote))
	}
}

// Derive writes the derived sentence for the turn that task taskID is
// starting on laneID. A task with no trigger row, or a trigger with nothing
// to quote, leaves the lane's focus empty — no guess.
func Derive(ctx context.Context, q db.DBTX, laneID, taskID uuid.UUID, now time.Time) error {
	var (
		authorType, content string
		authorName          *string
		delegatedFrom       *uuid.UUID
	)
	err := q.QueryRow(ctx, `
		SELECT m.author_type::text, m.content,
		       COALESCE(a.name, u.display_name),
		       t.delegated_from_task_id
		FROM task t
		JOIN message m ON m.id = t.trigger_message_id
		LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
		LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
		WHERE t.id = $1`, taskID).Scan(&authorType, &content, &authorName, &delegatedFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return Clear(ctx, q, laneID)
	}
	if err != nil {
		return fmt.Errorf("lanefocus: trigger: %w", err)
	}
	tr := Trigger{AuthorType: authorType, Content: content, Delegated: delegatedFrom != nil}
	if authorName != nil {
		tr.AuthorName = *authorName
	}
	text := Sentence(tr)
	if text == "" {
		return Clear(ctx, q, laneID)
	}
	// focus_published_at resets: the window is about consecutive AGENT
	// declarations, and the turn's first one must replace this sentence on
	// the screen at once rather than wait out the previous turn's minute.
	_, err = q.Exec(ctx, `
		UPDATE lane SET focus_text = $2, focus_at = $3, focus_source = 'derived', focus_published_at = NULL WHERE id = $1`, laneID, text, now)
	return err
}

// Declared is Declare's answer: whether the new sentence should go out as
// `lane.updated` now, and — when it should not — when the coalescing window
// closes (the caller flushes then).
type Declared struct {
	PublishNow bool
	FlushAt    time.Time
}

// Declare stores the agent's sentence (source = agent, cut at MaxRunes) and
// decides the 60-second coalescing: the first declaration after a quiet
// minute goes out at once; the ones after it inside the window are stored
// and only the last one goes out when the window closes.
func Declare(ctx context.Context, q db.DBTX, laneID uuid.UUID, note string, now time.Time) (Declared, error) {
	text := Truncate(strings.TrimSpace(note))
	var published *time.Time
	if err := q.QueryRow(ctx, `SELECT focus_published_at FROM lane WHERE id = $1 FOR UPDATE`, laneID).Scan(&published); err != nil {
		return Declared{}, fmt.Errorf("lanefocus: declare: %w", err)
	}
	out := Declared{PublishNow: published == nil || !now.Before(published.Add(CoalesceWindow))}
	if out.PublishNow {
		_, err := q.Exec(ctx, `
			UPDATE lane SET focus_text = $2, focus_at = $3, focus_source = 'agent', focus_published_at = $3 WHERE id = $1`, laneID, text, now)
		return out, err
	}
	out.FlushAt = published.Add(CoalesceWindow)
	_, err := q.Exec(ctx, `
		UPDATE lane SET focus_text = $2, focus_at = $3, focus_source = 'agent' WHERE id = $1`, laneID, text, now)
	return out, err
}

// TakePending reports whether the lane holds an agent sentence that has not
// gone out yet and whose window has closed, and if so marks it published at
// now. The caller publishes the lane when it returns true.
func TakePending(ctx context.Context, q db.DBTX, laneID uuid.UUID, now time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE lane SET focus_published_at = $2
		WHERE id = $1 AND focus_source = 'agent' AND focus_at IS NOT NULL
		  AND (focus_published_at IS NULL OR (focus_at > focus_published_at AND focus_published_at + $3::interval <= $2))`,
		laneID, now, fmt.Sprintf("%d seconds", int(CoalesceWindow/time.Second)))
	if err != nil {
		return false, fmt.Errorf("lanefocus: pending: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkPublished records that the lane's current sentence went out at now
// through a frame published for another reason (the lane's status changed in
// the same call), so the window starts there.
func MarkPublished(ctx context.Context, q db.DBTX, laneID uuid.UUID, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE lane SET focus_published_at = $2 WHERE id = $1 AND focus_source = 'agent'`, laneID, now)
	return err
}

// Clear empties the lane's focus unconditionally.
func Clear(ctx context.Context, q db.DBTX, laneID uuid.UUID) error {
	_, err := q.Exec(ctx, `
		UPDATE lane SET focus_text = NULL, focus_at = NULL, focus_source = NULL
		WHERE id = $1 AND focus_text IS NOT NULL`, laneID)
	return err
}

// ClearIfIdle empties the focus when no turn is running on the lane any
// more. It is called on every task transition: a SECOND task queued on a lane
// whose turn is still running must not erase that turn's sentence.
func ClearIfIdle(ctx context.Context, q db.DBTX, laneID uuid.UUID) error {
	_, err := q.Exec(ctx, `
		UPDATE lane SET focus_text = NULL, focus_at = NULL, focus_source = NULL
		WHERE id = $1 AND focus_text IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM task WHERE lane_id = $1 AND status IN ('dispatched', 'preparing', 'running'))`, laneID)
	return err
}
