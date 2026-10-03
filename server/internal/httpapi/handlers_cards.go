package httpapi

// handlers_cards.go is openapi v0.3.10's `cards` tag (PRD FR-3.8): the
// board, one card, the result card and the two judgements. The rules live in
// internal/cards (pure) and router/cards.go (writes); this file translates.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// draftOf is TaskCardInput as the checks see it.
func draftOf(in gen.TaskCardInput) cards.Draft {
	d := cards.Draft{AssigneeID: uuid.UUID(in.AgentId), Goal: in.Goal, Boundaries: in.Boundaries}
	for _, c := range in.Criteria {
		d.Criteria = append(d.Criteria, cards.Criterion{Text: c.Text, Method: string(c.Method)})
	}
	if in.Refs != nil {
		for _, r := range *in.Refs {
			d.Refs = append(d.Refs, cards.Ref{Kind: string(r.Kind), ID: uuid.UUID(r.Id)})
		}
	}
	if in.OutputFormat.IsSpecified() && !in.OutputFormat.IsNull() {
		v := in.OutputFormat.MustGet()
		d.OutputFormat = &v
	}
	if in.BudgetUsd.IsSpecified() && !in.BudgetUsd.IsNull() {
		v := float64(in.BudgetUsd.MustGet())
		d.BudgetUSD = &v
	}
	return d
}

func resultOf(in gen.CardResultInput) cards.ResultIn {
	out := cards.ResultIn{Summary: in.Summary, Confirmed: in.Confirmed, Assumed: in.Assumed}
	if in.Deviations.IsSpecified() && !in.Deviations.IsNull() {
		v := in.Deviations.MustGet()
		out.Deviations = &v
	}
	if in.OpenIssues.IsSpecified() && !in.OpenIssues.IsNull() {
		v := in.OpenIssues.MustGet()
		out.OpenIssues = &v
	}
	for _, v := range in.Verdicts {
		vi := cards.VerdictIn{Criterion: v.Criterion, Verdict: string(v.Verdict)}
		if v.Evidence != nil {
			for _, e := range *v.Evidence {
				vi.Evidence = append(vi.Evidence, cards.Evidence{Kind: string(e.Kind), Ref: e.Ref})
			}
		}
		if v.Note.IsSpecified() && !v.Note.IsNull() {
			n := v.Note.MustGet()
			vi.Note = &n
		}
		out.Verdicts = append(out.Verdicts, vi)
	}
	return out
}

// cardAccess loads a card the caller may read: a task token of the same
// room, or a person who may view the room.
func (s *Server) cardAccess(r *http.Request, cardID uuid.UUID) (*cards.Row, *Problem) {
	c, err := cards.Get(r.Context(), s.DB, cardID)
	if errors.Is(err, cards.ErrNotFound) {
		return nil, apperr.NotFound("card")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if pr := principalOf(r); pr.Task != nil && pr.Task.SessionID != c.RoomID {
		return nil, apperr.NotFound("card")
	}
	if _, p := s.sessionAccess(r, c.RoomID); p != nil {
		if p.Status == http.StatusNotFound {
			return nil, apperr.NotFound("card")
		}
		return nil, p
	}
	return c, nil
}

func judgeOf(pr *Principal) cards.Judge {
	if pr.Task != nil {
		id := pr.Task.AgentID
		return cards.Judge{Agent: &id}
	}
	if pr.User != nil {
		id := uuid.UUID(pr.User.Id)
		return cards.Judge{Person: &id}
	}
	return cards.Judge{}
}

// ListRoomCards is `colab card list` · the mission panel's 분담표.
func (s *Server) ListRoomCards(w http.ResponseWriter, r *http.Request, roomId gen.RoomId, params gen.ListRoomCardsParams) {
	if _, p := s.sessionAccess(r, roomId); p != nil {
		writeProblem(w, p)
		return
	}
	if p := s.commandAllowed(r, gen.ColabCommandCardList); p != nil {
		writeProblem(w, p)
		return
	}
	var work *uuid.UUID
	all := true
	if params.WorkId != nil {
		all = false
		v := strings.TrimSpace(*params.WorkId)
		if v != "none" && v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				writeProblem(w, apperr.Validation(apperr.Field("work_id", "invalid", "work_id 는 미션 id 또는 none 입니다")))
				return
			}
			work = &id
		}
	}
	board, err := cards.Board(r.Context(), s.DB, roomId, work, all)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

// GetCard is `colab card get` — with the past versions.
func (s *Server) GetCard(w http.ResponseWriter, r *http.Request, cardId gen.CardId) {
	c, p := s.cardAccess(r, cardId)
	if p != nil {
		writeProblem(w, p)
		return
	}
	if p := s.commandAllowed(r, gen.ColabCommandCardGet); p != nil {
		writeProblem(w, p)
		return
	}
	api, err := cards.ToAPI(r.Context(), s.DB, c, judgeOf(principalOf(r)), true)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api)
}

// SubmitCardResult is `colab card report`.
func (s *Server) SubmitCardResult(w http.ResponseWriter, r *http.Request, cardId gen.CardId, params gen.SubmitCardResultParams) {
	pr := principalOf(r)
	if pr.Task == nil {
		writeProblem(w, apperr.Forbidden("not_card_task", cards.NotCardTaskSentence))
		return
	}
	if p := s.commandAllowed(r, gen.ColabCommandCardReport); p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.CardResultInput
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	call := func() (int, any, *Problem) {
		out, err := s.Router.SubmitResult(r.Context(), pr.Task.TaskID, pr.Task.Attempt, cardId, resultOf(in))
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusCreated, map[string]any{"card": out.Card, "message": out.Message, "downgraded": out.Downgraded, "notice": out.Notice}, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	s.idempotent(r.Context(), w, taskScope(pr.Task.TaskID), params.IdempotencyKey.String(), requestHash(r, body), call)
}

func (s *Server) judgement(r *http.Request) router.Judgement {
	pr := principalOf(r)
	if pr.Task != nil {
		id := pr.Task.TaskID
		return router.Judgement{TaskID: &id, Attempt: pr.Task.Attempt}
	}
	if pr.User != nil {
		id := uuid.UUID(pr.User.Id)
		return router.Judgement{UserID: &id}
	}
	return router.Judgement{}
}

// AcceptCard is `colab card accept` and the card bubble's 「수락」.
func (s *Server) AcceptCard(w http.ResponseWriter, r *http.Request, cardId gen.CardId) {
	if _, p := s.cardAccess(r, cardId); p != nil {
		writeProblem(w, p)
		return
	}
	if p := s.commandAllowed(r, gen.ColabCommandCardAccept); p != nil {
		writeProblem(w, p)
		return
	}
	// v0.3.11 (PRD FR-3.8 4): no empty acceptance — the comment says what
	// was checked, so 「누가 무엇을 보고 통과시켰나」 always stays.
	var in gen.AcceptCardJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	comment, p := judgementComment(in.Comment)
	if p != nil {
		writeProblem(w, p)
		return
	}
	out, err := s.Router.Accept(r.Context(), cardId, s.judgement(r), comment)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// judgementComment is acceptCard's comment check: trimmed, blank is 422
// judgement_comment_required (the code rides on the problem and its one
// field, like the other card 422s), over 600 runes is too_long.
func judgementComment(raw string) (string, *Problem) {
	c := strings.TrimSpace(raw)
	if c == "" {
		p := apperr.Validation(apperr.Field("comment", "judgement_comment_required", cards.JudgementCommentRequiredSentence))
		p.Code, p.Detail = "judgement_comment_required", cards.JudgementCommentRequiredSentence
		return "", p
	}
	if len([]rune(c)) > cards.JudgementCommentMax {
		return "", apperr.Validation(apperr.Field("comment", "too_long", cards.JudgementCommentTooLongSentence))
	}
	return c, nil
}

// ReviseCard is `colab card revise` and the card bubble's 「수정 요청」.
func (s *Server) ReviseCard(w http.ResponseWriter, r *http.Request, cardId gen.CardId, params gen.ReviseCardParams) {
	if _, p := s.cardAccess(r, cardId); p != nil {
		writeProblem(w, p)
		return
	}
	if p := s.commandAllowed(r, gen.ColabCommandCardRevise); p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.ReviseCardJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		writeProblem(w, apperr.Validation(apperr.Field("reason", "required", "무엇이 모자란지 사유를 적어 주세요 — 담당의 다음 턴에 그대로 갑니다")))
		return
	}
	if len([]rune(reason)) > 1000 {
		writeProblem(w, apperr.Validation(apperr.Field("reason", "too_long", "사유는 1000자까지입니다")))
		return
	}
	patch := patchOf(in.Card)
	call := func() (int, any, *Problem) {
		out, err := s.Router.Revise(r.Context(), cardId, s.judgement(r), reason, patch)
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		return http.StatusOK, map[string]any{"card": out.Card, "message": out.Message, "task": out.Task}, nil
	}
	if params.IdempotencyKey == nil {
		st, out, p := call()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, st, out)
		return
	}
	scope := "card:" + cardId.String()
	if pr := principalOf(r); pr.Task != nil {
		scope = taskScope(pr.Task.TaskID)
	} else if pr.User != nil {
		scope = "user:" + uuid.UUID(pr.User.Id).String()
	}
	s.idempotent(r.Context(), w, scope, params.IdempotencyKey.String(), requestHash(r, body), call)
}

func patchOf(in *gen.TaskCardPatch) router.Patch {
	var p router.Patch
	if in == nil {
		return p
	}
	p.Goal, p.Boundaries = in.Goal, in.Boundaries
	if in.Criteria != nil {
		cs := []cards.Criterion{}
		for _, c := range *in.Criteria {
			cs = append(cs, cards.Criterion{Text: c.Text, Method: string(c.Method)})
		}
		p.Criteria = &cs
	}
	if in.Refs != nil {
		rs := []cards.Ref{}
		for _, r := range *in.Refs {
			rs = append(rs, cards.Ref{Kind: string(r.Kind), ID: uuid.UUID(r.Id)})
		}
		p.Refs = &rs
	}
	if in.OutputFormat.IsSpecified() {
		var v *string
		if !in.OutputFormat.IsNull() {
			x := in.OutputFormat.MustGet()
			v = &x
		}
		p.OutputFormat = &v
	}
	if in.BudgetUsd.IsSpecified() {
		var v *float64
		if !in.BudgetUsd.IsNull() {
			x := float64(in.BudgetUsd.MustGet())
			v = &x
		}
		p.BudgetUSD = &v
	}
	return p
}
