package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// Part-message limits (openapi v0.3.6 MessageGroupCreate.parts minItems ·
// maxItems, PRD FR-3.1.4 1번). The migration's CHECK is the floor under them.
const (
	MinParts = 2
	MaxParts = 6
)

// partMentions is Decide's mention override for a part (nil for an ordinary
// message, which keeps parsing its body).
func partMentions(p *partRow) []gen.Mention {
	if p == nil {
		return nil
	}
	return p.To
}

// PostGroup is postMessageGroup (PRD FR-3.1.4, openapi v0.3.6 D26): an
// agent's one post that says different things to different recipients. Each
// part is one message row — same group_id, group_index = its place — and each
// row goes through postRow, the path postMessage takes, so routing (FR-3.3
// rules 1~8), lane resolution, speech (FR-3.1.3), the mission and people's
// inbox items are decided per part by the rules that already exist. The one
// difference is the part's mentions: its `to`, never its body.
//
// All parts are one transaction: a part that fails validation or insertion
// leaves no row at all. `message.created` goes out per row in group_index
// order, just before the commit.
func (s *Service) PostGroup(ctx context.Context, sessionID uuid.UUID, author Author, in gen.MessageGroupCreate) (*gen.MessageGroupPostResult, error) {
	if author.Type != "agent" || author.AgentID == nil {
		return nil, apperr.Forbidden("agent_only", "부분 메시지는 에이전트만 보낼 수 있습니다")
	}
	if p := ValidateParts(in.Parts); p != nil {
		return nil, p
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var legacy *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id, legacy_work_id FROM room WHERE id = $1 FOR UPDATE`, sessionID).Scan(&wsID, &legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	participants, _, err := loadParticipants(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	humans, err := roomPeople(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	tos, p := ResolvePartTargets(in.Parts, participants, humans)
	if p != nil {
		return nil, p
	}

	groupID := uuid.New()
	out := &gen.MessageGroupPostResult{GroupId: groupID, Parts: make([]gen.MessagePostResult, 0, len(in.Parts))}
	for i, part := range in.Parts {
		// v0.3.7 (FR-3.7): a part's files are the part's own — they ride the
		// same MessageCreate the single post uses, so NormalizeAttachments
		// gives the group the identical room·10·dedup·422 rules with no second
		// copy of them here. A group of 6 may name 10 files each; the limit is
		// per part because each recipient sees only its own part.
		msg := gen.MessageCreate{Content: part.Content, Detail: part.Detail, ParentId: in.ParentId, AttachmentIds: part.AttachmentIds}
		if in.WorkId.IsSpecified() {
			msg.WorkId = in.WorkId
		}
		res, _, err := s.postRow(ctx, tx, sessionID, wsID, legacy, author, msg, nil,
			&partRow{GroupID: groupID, Index: i, Size: len(in.Parts), To: tos[i]}, now)
		if err != nil {
			return nil, err
		}
		out.Parts = append(out.Parts, *res)
	}
	if s.Hub != nil {
		sid := sessionID
		for _, r := range out.Parts {
			_ = s.Hub.Publish(ctx, tx, wsID, &sid, "message.created", r.Message)
		}
	}
	if err := commitGroupTx(ctx, tx); err != nil {
		return nil, err
	}
	for _, r := range out.Parts {
		if len(r.Triggers) > 0 && s.Notifier != nil {
			s.Notifier.Notify()
			break
		}
	}
	return out, nil
}

// commitGroupTx commits a part group. A commit that fails is the one way this
// path could answer 201 with no row at all (review #374a NN2), so its error is
// returned — the handler turns it into a 5xx and no part is claimed to exist.
// `commitGroupFail` is the test seam: the atomicity test sets it to make a
// healthy commit fail, the way the injection did by hand.
var commitGroupFail func() error

func commitGroupTx(ctx context.Context, tx pgx.Tx) error {
	if commitGroupFail != nil {
		if err := commitGroupFail(); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetCommitGroupFailForTest makes the next part-group commits fail, and
// returns the function that puts it back. Tests only.
func SetCommitGroupFailForTest(fn func() error) func() {
	prev := commitGroupFail
	commitGroupFail = fn
	return func() { commitGroupFail = prev }
}

// ValidateParts is the part rules that need no database (422, no row made):
// the count (`parts_count`), an empty body, a `/note` body (`parts_note` — a
// memo addresses nobody and cannot be a part) and an empty `to`.
func ValidateParts(parts []gen.MessagePartCreate) *apperr.Problem {
	if len(parts) < MinParts || len(parts) > MaxParts {
		return apperr.Validation(apperr.Field("parts", "parts_count",
			fmt.Sprintf("부분은 %d~%d개여야 합니다(지금 %d개) — 받는 쪽이 한 묶음이면 본문 하나로 보내세요", MinParts, MaxParts, len(parts))))
	}
	for i, p := range parts {
		field := fmt.Sprintf("parts[%d]", i)
		if isNote(p.Content) {
			return apperr.Validation(apperr.Field(field+".content", "parts_note", "메모(/note)는 부분이 될 수 없습니다 — 메모는 따로 보내세요"))
		}
		if trimmed := p.Content; len(trimmed) == 0 || isBlank(trimmed) {
			return apperr.Validation(apperr.Field(field+".content", "required", "부분마다 내용을 입력해 주세요"))
		}
		if len(p.To) == 0 {
			return apperr.Validation(apperr.Field(field+".to", "unknown_mention", "부분마다 받는 쪽(to)이 하나 이상 있어야 합니다"))
		}
	}
	return nil
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// ResolvePartTargets turns each part's `to` (mention links) into the mentions
// that part is routed and classified by. An element must be exactly one
// mention link to an agent participant, a person in the room, or `@all`
// (`unknown_mention`); one agent may be in only one part of the group
// (`parts_duplicate_agent` — one post never gives one agent two triggers).
func ResolvePartTargets(parts []gen.MessagePartCreate, participants []Participant, humans []uuid.UUID) ([][]gen.Mention, *apperr.Problem) {
	agents := map[uuid.UUID]Participant{}
	for _, p := range participants {
		agents[p.AgentID] = p
	}
	people := map[uuid.UUID]bool{}
	for _, h := range humans {
		people[h] = true
	}
	owner := map[uuid.UUID]int{}
	out := make([][]gen.Mention, len(parts))
	for i, part := range parts {
		field := fmt.Sprintf("parts[%d].to", i)
		seen := map[string]bool{}
		list := []gen.Mention{}
		for _, raw := range part.To {
			ms := ParseMentions(raw)
			if len(ms) != 1 || !isWholeLink(raw) {
				return nil, apperr.Validation(apperr.Field(field, "unknown_mention",
					fmt.Sprintf("받는 쪽 %q 은(는) 멘션 링크 하나여야 합니다([@이름](mention://agent|user/<id>) 또는 [@all](mention://all/all))", raw)))
			}
			m := ms[0]
			switch m.Kind {
			case gen.MentionKindAll:
			case gen.MentionKindAgent:
				id, err := uuid.Parse(m.Id)
				if _, ok := agents[id]; err != nil || !ok {
					return nil, apperr.Validation(apperr.Field(field, "unknown_mention", fmt.Sprintf("%s 은(는) 이 방 참여 에이전트가 아닙니다", raw)))
				}
				if j, dup := owner[id]; dup && j != i {
					return nil, apperr.Validation(apperr.Field(field, "parts_duplicate_agent",
						fmt.Sprintf("한 에이전트는 한 부분에만 올 수 있습니다 — %s 이(가) 부분 %d 과 %d 에 있습니다", raw, j+1, i+1)))
				}
				owner[id] = i
			case gen.MentionKindUser:
				id, err := uuid.Parse(m.Id)
				if err != nil || !people[id] {
					return nil, apperr.Validation(apperr.Field(field, "unknown_mention", fmt.Sprintf("%s 은(는) 이 방 참여자가 아닙니다", raw)))
				}
			default:
				return nil, apperr.Validation(apperr.Field(field, "unknown_mention", fmt.Sprintf("받는 쪽 %q 을(를) 알 수 없습니다", raw)))
			}
			key := string(m.Kind) + "/" + m.Id
			if seen[key] {
				continue
			}
			seen[key] = true
			list = append(list, m)
		}
		out[i] = list
	}
	return out, nil
}

// isWholeLink: the element is the link and nothing else (surrounding spaces
// tolerated) — "@Designer 에게" is not a recipient.
func isWholeLink(raw string) bool {
	loc := mentionRe.FindStringIndex(raw)
	if loc == nil {
		return false
	}
	return isBlank(raw[:loc[0]]) && isBlank(raw[loc[1]:])
}

// roomPeople is the room's live people — the same set sessions.HumanRoster
// hands the CLI as `humans` (router cannot import sessions: cycle).
func roomPeople(ctx context.Context, tx pgx.Tx, roomID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM room_participant WHERE room_id = $1 AND left_at IS NULL AND user_id IS NOT NULL`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
