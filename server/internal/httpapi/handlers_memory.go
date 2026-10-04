package httpapi

// handlers_memory.go is openapi v0.3.12's `memory` tag (PRD FR-4.6, 맥락
// 2단계): the mission state ledger. The rules live in internal/memory; this
// file resolves who is calling and for which mission, and translates.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/memory"
	"github.com/ingki3/agent-collabortion/server/internal/roles"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// memoryVerb is the feed row a ledger write leaves on the attempt (colab-cli
// §4): task_event.schema.json has no ledger verb and is closed, so a write
// rides on `update` with the command in the payload, like `work propose`
// rides on `hitl`.
const memoryVerb = "update"

// outsideMission is a task token writing to a mission that is not its turn's.
func outsideMission() *Problem {
	return apperr.Forbidden("outside_task_scope", "이 턴의 미션 원장에만 쓸 수 있습니다")
}

// memoryWriter resolves the calling task for a write: noteMemory ·
// supersedeMemory · retireMemory are TaskToken-only. It answers the task's
// mission (nil outside one) and the author.
func (s *Server) memoryWriter(r *http.Request, cmd gen.ColabCommand) (*uuid.UUID, memory.Author, *Problem) {
	pr := principalOf(r)
	if pr.Task == nil {
		if pr.User == nil {
			return nil, memory.Author{}, apperr.Unauthorized("unauthorized", "로그인이 필요합니다")
		}
		return nil, memory.Author{}, apperr.Forbidden("task_token_required", "원장은 에이전트의 턴에서 씁니다 — 사람이 쓰는 입력 화면은 아직 없습니다")
	}
	if p := s.commandAllowed(r, cmd); p != nil {
		return nil, memory.Author{}, p
	}
	role, err := s.agentRole(r)
	if err != nil {
		return nil, memory.Author{}, apperr.Internal(err)
	}
	var work *uuid.UUID
	if err := s.DB.QueryRow(r.Context(), `SELECT work_id FROM task WHERE id = $1`, pr.Task.TaskID).Scan(&work); err != nil {
		return nil, memory.Author{}, apperr.Internal(err)
	}
	agent, task := pr.Task.AgentID, pr.Task.TaskID
	return work, memory.Author{AgentID: &agent, TaskID: &task, Role: role}, nil
}

// memoryFeed writes the feed row after a successful write. Its own
// transaction — the write is the answer whether or not the note lands.
func (s *Server) memoryFeed(r *http.Request, cmd gen.ColabCommand, it *memory.Item) {
	pr := principalOf(r)
	if pr.Task == nil {
		return
	}
	if err := s.writeServerEvent(r.Context(), pr.Task.TaskID, pr.Task.Attempt, "status", memoryVerb, it.ID.String(), "ok",
		map[string]any{"command": roles.CLIName(cmd), "args": map[string]any{"kind": it.Kind}, "result_ref": it.ID.String()}, s.Clock.Now()); err != nil {
		s.Log.Warn("memory feed row", "err", err, "task", pr.Task.TaskID)
	}
}

func (s *Server) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.DB, fn)
}

// ListMemory is `colab memory get` and the mission panel's 「원장」 tab.
func (s *Server) ListMemory(w http.ResponseWriter, r *http.Request, workId gen.WorkId, params gen.ListMemoryParams) {
	if pr := principalOf(r); pr.Task != nil {
		wk, err := sessions.LoadWorkRow(r.Context(), s.DB, workId)
		if err != nil {
			writeErr(w, err)
			return
		}
		if pr.Task.SessionID != wk.RoomId {
			writeProblem(w, apperr.Forbidden("outside_task_scope", "다른 방에는 접근할 수 없습니다"))
			return
		}
		if p := s.commandAllowed(r, gen.ColabCommandMemoryGet); p != nil {
			writeProblem(w, p)
			return
		}
	} else if _, _, _, p := s.workGate(r, workId, rooms.ActView); p != nil {
		writeProblem(w, p)
		return
	}
	kind, status := "", string(gen.ListMemoryParamsStatusActive)
	if params.Kind != nil {
		if !params.Kind.Valid() {
			writeProblem(w, apperr.Validation(apperr.Field("kind", "invalid", "kind 는 fact · assignment · open_question · lesson · plan · progress 중 하나입니다")))
			return
		}
		kind = string(*params.Kind)
	}
	if params.Status != nil {
		switch *params.Status {
		case gen.ListMemoryParamsStatusActive, gen.ListMemoryParamsStatusSuperseded, gen.ListMemoryParamsStatusRetired, gen.ListMemoryParamsStatusAll:
			status = string(*params.Status)
		default:
			writeProblem(w, apperr.Validation(apperr.Field("status", "invalid", "status 는 active · superseded · retired · all 중 하나입니다")))
			return
		}
	}
	items, err := memory.List(r.Context(), s.DB, workId, kind, status)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]gen.MemoryItem, 0, len(items))
	for _, it := range items {
		out = append(out, memory.ToAPI(it))
	}
	writeJSON(w, http.StatusOK, out)
}

// NoteMemory is `colab memory note`. A lesson that merged into an existing
// one answers 200 with that lesson (support_count raised); a new row is 201.
func (s *Server) NoteMemory(w http.ResponseWriter, r *http.Request, workId gen.WorkId, params gen.NoteMemoryParams) {
	taskWork, author, p := s.memoryWriter(r, gen.ColabCommandMemoryNote)
	if p != nil {
		writeProblem(w, p)
		return
	}
	wk, err := sessions.LoadWorkRow(r.Context(), s.DB, workId)
	if err != nil {
		writeErr(w, err)
		return
	}
	if principalOf(r).Task.SessionID != wk.RoomId || taskWork == nil || *taskWork != workId {
		writeProblem(w, outsideMission())
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.MemoryItemInput
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	note := memory.NoteIn{WorkID: workId, RoomID: wk.RoomId, Kind: string(in.Kind), Content: in.Content, Author: author}
	if in.Certainty.IsSpecified() && !in.Certainty.IsNull() {
		v := string(in.Certainty.MustGet())
		note.Certainty = &v
	}
	if in.Outcome.IsSpecified() && !in.Outcome.IsNull() {
		v := string(in.Outcome.MustGet())
		note.Outcome = &v
	}
	if in.SourceMessageIds != nil {
		note.Sources = append(note.Sources, *in.SourceMessageIds...)
	}
	call := func() (int, any, *Problem) {
		var it *memory.Item
		var created bool
		err := s.inTx(r.Context(), func(tx pgx.Tx) error {
			var err error
			it, created, err = memory.Note(r.Context(), tx, note, s.Clock.Now())
			return err
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		s.memoryFeed(r, gen.ColabCommandMemoryNote, it)
		status := http.StatusCreated
		if !created {
			status = http.StatusOK
		}
		return status, memory.ToAPI(it), nil
	}
	key := ""
	if params.IdempotencyKey != nil {
		key = params.IdempotencyKey.String()
	}
	s.idempotent(r.Context(), w, taskScope(principalOf(r).Task.TaskID), key, requestHash(r, body), call)
}

// memoryTarget loads the item a supersede/retire names, for a task of its
// mission.
func (s *Server) memoryTarget(r *http.Request, id uuid.UUID, taskWork *uuid.UUID) (*memory.Item, uuid.UUID, *Problem) {
	it, err := memory.Get(r.Context(), s.DB, id)
	if errors.Is(err, memory.ErrNotFound) {
		return nil, uuid.Nil, apperr.NotFound("memory")
	}
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	wk, err := sessions.LoadWorkRow(r.Context(), s.DB, it.WorkID)
	if err != nil {
		return nil, uuid.Nil, apperr.As(err)
	}
	if principalOf(r).Task.SessionID != wk.RoomId {
		// Another room's item does not exist for this token.
		return nil, uuid.Nil, apperr.NotFound("memory")
	}
	if taskWork == nil || *taskWork != it.WorkID {
		return nil, uuid.Nil, outsideMission()
	}
	return it, wk.RoomId, nil
}

// SupersedeMemory is `colab memory supersede`.
func (s *Server) SupersedeMemory(w http.ResponseWriter, r *http.Request, memoryId gen.MemoryId, params gen.SupersedeMemoryParams) {
	taskWork, author, p := s.memoryWriter(r, gen.ColabCommandMemorySupersede)
	if p != nil {
		writeProblem(w, p)
		return
	}
	body, p := readBody(w, r)
	if p != nil {
		writeProblem(w, p)
		return
	}
	// kind · work_id come from the target (422 kind_immutable when sent).
	var keys map[string]json.RawMessage
	if json.Unmarshal(body, &keys) == nil {
		for _, k := range []string{"kind", "work_id"} {
			if _, ok := keys[k]; ok {
				pr := apperr.Validation(apperr.Field(k, "kind_immutable", memory.KindImmutableSentence))
				pr.Code, pr.Detail = "kind_immutable", memory.KindImmutableSentence
				writeProblem(w, pr)
				return
			}
		}
	}
	var in gen.SupersedeMemoryJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	_, roomID, p := s.memoryTarget(r, memoryId, taskWork)
	if p != nil {
		writeProblem(w, p)
		return
	}
	sup := memory.SupersedeIn{Content: in.Content, Author: author}
	if in.Certainty.IsSpecified() && !in.Certainty.IsNull() {
		v := string(in.Certainty.MustGet())
		sup.Certainty = &v
	}
	if in.SourceMessageIds != nil {
		sup.Sources = append(sup.Sources, *in.SourceMessageIds...)
	}
	call := func() (int, any, *Problem) {
		var item, old *memory.Item
		err := s.inTx(r.Context(), func(tx pgx.Tx) error {
			var err error
			item, old, err = memory.Supersede(r.Context(), tx, memoryId, sup, roomID, s.Clock.Now())
			return err
		})
		if err != nil {
			return 0, nil, apperr.As(err)
		}
		s.memoryFeed(r, gen.ColabCommandMemorySupersede, item)
		return http.StatusCreated, map[string]any{"item": memory.ToAPI(item), "superseded": memory.ToAPI(old)}, nil
	}
	key := ""
	if params.IdempotencyKey != nil {
		key = params.IdempotencyKey.String()
	}
	s.idempotent(r.Context(), w, taskScope(principalOf(r).Task.TaskID), key, requestHash(r, body), call)
}

// RetireMemory is `colab memory retire`.
func (s *Server) RetireMemory(w http.ResponseWriter, r *http.Request, memoryId gen.MemoryId) {
	taskWork, author, p := s.memoryWriter(r, gen.ColabCommandMemoryRetire)
	if p != nil {
		writeProblem(w, p)
		return
	}
	var in gen.RetireMemoryJSONBody
	if p := decodeJSON(w, r, &in); p != nil {
		writeProblem(w, p)
		return
	}
	if _, _, p := s.memoryTarget(r, memoryId, taskWork); p != nil {
		writeProblem(w, p)
		return
	}
	var it *memory.Item
	err := s.inTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		it, err = memory.Retire(r.Context(), tx, memoryId, in.Reason, author, s.Clock.Now())
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.memoryFeed(r, gen.ColabCommandMemoryRetire, it)
	writeJSON(w, http.StatusOK, memory.ToAPI(it))
}
