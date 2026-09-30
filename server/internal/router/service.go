package router

import (
	"github.com/ingki3/agent-collabortion/server/internal/cards"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/lanes"
	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/quiet"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

var (
	// ErrSessionNotFound wraps the 404 Problem so every handler that answers
	// with apperr.As says "방을 찾을 수 없습니다" rather than 500. The row is
	// read under FOR UPDATE, so this is also what a message queued behind a
	// deleteSession gets once the delete commits (S-82 — the reverse race,
	// TestS82DeleteRacesPostMessage): before, it surfaced as `internal` with
	// the router's own sentence in `cause`.
	ErrSessionNotFound = fmt.Errorf("router: session not found: %w", apperr.NotFound("session"))
	ErrParentNotFound  = errors.New("router: parent message not found")
)

// ServerSeqBase re-exports tasks.ServerSeqBase for callers that hold the
// router (e.g. the command-expiry sweep).
const ServerSeqBase = tasks.ServerSeqBase

// Notifier wakes long-polling claims (queue.Notifier).
type Notifier interface{ Notify() }

// Author is who posts: a user (originator) or an agent task (TaskToken).
type Author struct {
	Type    string // user | agent | system
	UserID  *uuid.UUID
	AgentID *uuid.UUID
	TaskID  *uuid.UUID
	Attempt int
}

type Service struct {
	DB       *pgxpool.Pool
	Clock    clock.Clock
	Hub      *realtime.Hub
	Notifier Notifier
	// Tasks carries out the consequences of a pause (FR-2.3 drain vs cancel).
	Tasks *tasks.Service

	// QuietPublish emits `work.completion_progress` for a mission whose
	// approval-quiet state or held-trigger count just changed (T-QUIET). The
	// progress read model lives in internal/sessions, which imports this
	// package — wired in httpapi.NewServer; nil in unit tests.
	QuietPublish func(ctx context.Context, q db.DBTX, wsID, roomID, workID uuid.UUID)

	// focusFlush holds the lanes with a 「지금」 flush timer pending
	// (scheduleFocusFlush) — lane id → true.
	focusFlush sync.Map
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub, n Notifier) *Service {
	return &Service{DB: pool, Clock: c, Hub: h, Notifier: n}
}

// WithTasks wires the task service in. It is set after construction because
// the two services are built in either order by the server wiring.
func (s *Service) WithTasks(t *tasks.Service) *Service { s.Tasks = t; return s }

// RulePlatform is the "rule" recorded for a trigger the PLATFORM raised rather
// than the routing table (FR-3.3 numbers 1–8 are the table's own). It exists so
// `session_hop.rule` still says where a hop came from.
const RulePlatform = 9

// PlatformTrigger is a wake-up the server owes somebody because of an event of
// its own, not because of what a message says.
//
// S-59: `review reject` posts the reason into the submitting lane's thread and
// openapi v0.7.3 says the server then "그 lane 을 명시적으로 재진입시킨다"
// (E16-B 5단계). That reply is an AGENT message with no mention, so routing
// rule 4 ("에이전트의 메시지는 멘션이 없으면 아무것도 트리거하지 않는다") stops
// it — measured in T-I4: the rejected agent only ever woke up because a person
// relayed the rejection by hand. The trigger is added AFTER Decide so rule 4
// keeps its meaning for every ordinary message; the platform simply is not a
// message.
//
// LaneID is the lane to re-enter, decided by the caller (the submitting task's
// lane). It is fed to lane resolution rule 1, so the result is the same lane
// with `reentry_count`+1 — exactly what a thread reply would have produced.
type PlatformTrigger struct {
	AgentID uuid.UUID
	LaneID  uuid.UUID
}

// Post persists the message, applies the rules and creates or merges tasks.
// One transaction per session (row lock) so two concurrent posts cannot create
// two queued tasks on the same lane.
func (s *Service) Post(ctx context.Context, sessionID uuid.UUID, author Author, in gen.MessageCreate) (*gen.MessagePostResult, error) {
	return s.PostWithTrigger(ctx, sessionID, author, in, nil)
}

// PostWithTrigger is Post plus a platform trigger (S-59).
func (s *Service) PostWithTrigger(ctx context.Context, sessionID uuid.UUID, author Author, in gen.MessageCreate, platform *PlatformTrigger) (*gen.MessagePostResult, error) {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The ROOM row is the lock (V19_R1B_HANDOFF (b) router/service.go:116):
	// every post to a room serialises here, and locking "the room's mission"
	// alongside it would lock every mission of the room once there are several.
	// The same read fetches the old-path mark the legacy attribution rule
	// needs (#292 NN3: no extra query per message).
	var wsID uuid.UUID
	var legacy *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id, legacy_work_id FROM room WHERE id = $1 FOR UPDATE`, sessionID).Scan(&wsID, &legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	result, _, err := s.postRow(ctx, tx, sessionID, wsID, legacy, author, in, platform, nil, now)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		sid := sessionID
		_ = s.Hub.Publish(ctx, tx, wsID, &sid, "message.created", result.Message)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if len(result.Triggers) > 0 && s.Notifier != nil {
		s.Notifier.Notify()
	}
	return result, nil
}

// partRow is what makes a row one part of a group post (PRD FR-3.1.4,
// openapi v0.3.6 D26): its place in the group and its `to` — the mentions
// routing and speech read instead of the body's.
type partRow struct {
	GroupID uuid.UUID
	Index   int
	Size    int
	To      []gen.Mention
}

// postRow is one message row and everything FR-3.3 does with it — routing,
// lane resolution, task creation/coalescing, speech, mission, people's inbox
// items — inside the caller's transaction, after the caller locked the room.
// Post runs it once; PostGroup once per part, so a part gets exactly the
// rules a message gets (D26: "지금 코드 그대로 행마다"). It does not publish
// message.created: the caller does, in order, just before it commits.
func (s *Service) postRow(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, legacy *uuid.UUID, author Author, in gen.MessageCreate, platform *PlatformTrigger, part *partRow, now time.Time) (*gen.MessagePostResult, uuid.UUID, error) {
	assignee, err := routingAssignee(ctx, tx, sessionID, in, legacy)
	if err != nil {
		return nil, uuid.Nil, err
	}

	participants, profiles, err := loadParticipants(ctx, tx, sessionID)
	if err != nil {
		return nil, uuid.Nil, err
	}

	th, err := threadPremise(ctx, tx, sessionID, in.ParentId)
	if err != nil {
		return nil, uuid.Nil, err
	}
	parent := th.Parent

	// Rule 8 premise: the author's own lane and the join group it belongs to.
	authorDelegator, joinFired, err := delegatorPremise(ctx, tx, author.TaskID)
	if err != nil {
		return nil, uuid.Nil, err
	}

	var suppress []uuid.UUID
	if in.SuppressAgentIds != nil {
		for _, id := range *in.SuppressAgentIds {
			suppress = append(suppress, uuid.UUID(id))
		}
	}
	dec := Decide(Input{
		Content: in.Content, AuthorType: author.Type, Participants: participants,
		AuthorAgentID: author.AgentID, AssigneeAgentID: assignee, Suppress: suppress,
		ReplyToAgentID: th.ReplyTo, ThreadOwnerAgentID: th.ThreadOwner,
		AuthorLaneDelegatorID: authorDelegator, JoinGroupFired: joinFired,
		Mentions: partMentions(part),
	})

	if platform != nil && platform.AgentID != uuid.Nil {
		already := false
		for _, tr := range dec.Triggers {
			if tr.AgentID == platform.AgentID {
				already = true
			}
		}
		// A mention that already woke the same agent is the same wake-up; two
		// triggers would coalesce onto one task anyway, and the hop would be
		// double-counted against FR-3.5's limits.
		if !already {
			dec.Triggers = append(dec.Triggers, Trigger{AgentID: platform.AgentID, Rule: RulePlatform})
		}
	}

	// FR-3.1.1: the mission this message (and the lanes/tasks it makes)
	// belongs to — decided BEFORE the insert, from the same premises the
	// preview reads.
	attr, err := attribute(ctx, tx, sessionID, in, author, th, dec, legacy)
	if err != nil {
		return nil, uuid.Nil, err
	}

	// openapi v0.3.7 (FR-3.7): the ids are checked before anything is
	// written — a bad list refuses the whole post.
	attachIDs, err := NormalizeAttachments(in.AttachmentIds)
	if err != nil {
		return nil, uuid.Nil, err
	}

	var authorID *uuid.UUID
	switch author.Type {
	case "user":
		authorID = author.UserID
	case "agent":
		authorID = author.AgentID
	}
	var groupID *uuid.UUID
	var groupIndex, groupSize *int
	if part != nil {
		groupID, groupIndex, groupSize = &part.GroupID, &part.Index, &part.Size
	}
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, parent_id, content, mentions, source_task_id, kind, state, created_at, work_id, detail,
		                     group_id, group_index, group_size)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'text', 'posted', $8, $9, $10, $11, $12, $13) RETURNING id`,
		sessionID, author.Type, authorID, parent, in.Content, dec.Mentions, author.TaskID, now, attr.WorkID, in.Detail,
		groupID, groupIndex, groupSize).Scan(&msgID); err != nil {
		return nil, uuid.Nil, fmt.Errorf("router: insert message: %w", err)
	}
	if err := attach(ctx, tx, sessionID, msgID, attachIDs); err != nil {
		return nil, uuid.Nil, err
	}
	// PRD FR-3.8 2: who this agent message's mentions ask, and whether it
	// answers a question — decided once, read by the speech and by routing.
	qp, err := questionPremise(ctx, tx, author, th, platform)
	if err != nil {
		return nil, uuid.Nil, err
	}
	{
		var ids []uuid.UUID
		for _, m := range dec.Mentions {
			if m.Kind == gen.MentionKindAgent {
				if id, err := uuid.Parse(m.Id); err == nil {
					ids = append(ids, id)
				}
			}
		}
		qp.refineAsks(ids)
	}
	// openapi v0.3.2 (D24, FR-3.1.3): the speech is decided here, in the same
	// transaction as the insert, so no reader ever sees a message without one.
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{MentionAsks: qp.asks, AnswersAsker: qp.askerAddr()}); err != nil {
		return nil, uuid.Nil, err
	}
	// FR-3.2 「사람 (Director가 아니어도 알림)」: a person the body mentions gets
	// a `mention` inbox item (SCREEN §4.14 「나를 멘션한 메시지」). Routing
	// never wakes anyone for it (rule 3) — this is the only thing a person's
	// mention does besides addressing them (T-HUMANMENTION).
	if !isNote(in.Content) {
		if err := notifyMentionedPeople(ctx, tx, wsID, sessionID, msgID, attr.WorkID, author, dec.Mentions, now); err != nil {
			return nil, uuid.Nil, err
		}
	}

	// T-QUIET (FR-2A.2.3): the message's effect on its mission's approval
	// wait — a person re-opens it, an agent's report to a person closes the
	// round — decided after the speech is stored (the report test reads it).
	qg, err := gateMessage(ctx, tx, attr.WorkID, author, in.Content, msgID, now)
	if err != nil {
		return nil, uuid.Nil, err
	}
	for _, l := range qg.released {
		s.publishLane(ctx, tx, l)
	}
	if qg.changed {
		s.publishQuiet(ctx, tx, wsID, sessionID, *attr.WorkID)
	}
	var held []heldAgent

	result := &gen.MessagePostResult{}
	result.Triggers = make([]postTrigger, 0)
	result.Warnings = make([]routeWarning, 0)
	for _, w := range dec.Warnings {
		result.Warnings = append(result.Warnings, warningOf(w.AgentID, w.Code, w.Message))
	}

	originator := author.UserID
	if author.Type == "agent" && author.TaskID != nil {
		o, ok, err := taskOriginator(ctx, tx, *author.TaskID)
		if err != nil {
			return nil, uuid.Nil, err
		}
		if ok {
			originator = o
		}
	}
	primaryTasks := map[uuid.UUID]uuid.UUID{}
	// The "새 lane으로 보내기" toggle is per message and never sticks: it lives
	// in this request body, so the next message starts from rule 3 again
	// (E2-14). A persisted toggle would silently kill rule 3 for the session.
	newLane := in.NewLane != nil && *in.NewLane && author.Type == "user"

	// S-78: every trigger of this message shares one cause — the hop that
	// woke the turn writing it — so a message that mentions three agents is
	// three hops at the same depth, not a chain of three.
	var cause int64
	var ret reportReturn
	if author.Type == "agent" && author.TaskID != nil {
		if cause, _, err = causeOfTask(ctx, tx, *author.TaskID); err != nil {
			return nil, uuid.Nil, err
		}
		if ret, err = returningReport(ctx, tx, msgID); err != nil {
			return nil, uuid.Nil, err
		}
	}
	rc := routeCtx{
		sessionID: sessionID, wsID: wsID, author: author, th: th, platform: platform,
		work: attr.WorkID, profiles: profiles, participants: participants,
		msgID: msgID, originator: originator, newLane: newLane, now: now, question: qp,
	}
	for _, tr := range dec.Triggers {
		// FR-3.5: every trigger is judged against the room's trigger history
		// (judgeHop — the same verdict Delegate and wake use). A trigger that
		// trips a limit is not created and the ROOM pauses (E4-01); the hop is
		// recorded anyway so the next decision reads a complete history.
		hopCause := cause
		if ret.ok && tr.AgentID == ret.requester {
			// PRD FR-3.5 v0.19.12: a report coming back to the agent that
			// wrote the request closes that round — the hop takes the
			// request-writing task's OWN cause, exactly as a join/re-entry
			// wake does, so the requester wakes at the depth it asked from.
			hopCause = ret.cause
		}
		next := Hop{ToAgent: tr.AgentID, At: now, CauseID: hopCause}
		if author.Type == "agent" && author.AgentID != nil {
			next.FromAgent = *author.AgentID
		}
		v, err := s.judgeHop(ctx, tx, sessionID, wsID, next, msgID, tr.Rule, now)
		if err != nil {
			return nil, uuid.Nil, err
		}
		if !v.Allowed {
			if err := s.pauseForLoop(ctx, tx, sessionID, wsID, v, now); err != nil {
				return nil, uuid.Nil, err
			}
			result.Warnings = append(result.Warnings, warningOf(&tr.AgentID, "loop_limit", v.PausedText()))
			continue
		}
		routed, err := s.routeTrigger(ctx, tx, rc, tr)
		if err != nil {
			return nil, uuid.Nil, err
		}
		held = append(held, routed.held...)
		result.Triggers = append(result.Triggers, routed.entry)
		primaryTasks[tr.AgentID] = uuid.UUID(routed.entry.TaskId)
	}

	// harness v0.9.15: the post's result tells the writing turn whom it did
	// not wake (Lead 판정 2026-09-28 — warnings[] code approval_pending).
	for _, h := range held {
		id := h.id
		result.Warnings = append(result.Warnings, warningOf(&id, quiet.WarningCode, quiet.Notice(h.name)))
	}
	if len(held) > 0 && attr.WorkID != nil {
		s.publishQuiet(ctx, tx, wsID, sessionID, *attr.WorkID)
	}

	// Rule 7: rule 5 woke somebody other than the assignee, so the assignee
	// gets a deferred task five minutes out (E1-12).
	if fb := PlanFallback(dec, assignee, now); fb != nil {
		var primary uuid.UUID
		for _, tr := range dec.Triggers {
			if tr.Rule == 5 {
				primary = primaryTasks[tr.AgentID]
			}
		}
		if primary != uuid.Nil {
			laneID, taskID, ok, err := s.scheduleFallback(ctx, tx, sessionID, *fb, primary, profiles[fb.AgentID], msgID, originator, attr.WorkID, now)
			if err != nil {
				return nil, uuid.Nil, err
			}
			if ok {
				result.Triggers = append(result.Triggers, postTrigger{AgentId: fb.AgentID, LaneId: laneID, TaskId: taskID,
					DeferredUntil: nullable.NewNullableWithValue(fb.DueAt)})
			}
		}
	}

	// E1-13: the primary agent answered inside the window, so the deferred
	// assignee task must never run.
	if author.Type == "agent" && author.TaskID != nil {
		if err := s.cancelFallbacksFor(ctx, tx, *author.TaskID, now); err != nil {
			return nil, uuid.Nil, err
		}
	}

	// colab-cli.md §4: the server records CLI calls as status task_events.
	// attempt is CHECK (attempt >= 1); a caller that leaves it zero would take
	// a 23514 instead of posting, so fall back to the task's own attempt.
	if author.Attempt < 1 {
		author.Attempt = 1
		if author.TaskID != nil {
			_ = tx.QueryRow(ctx, `SELECT attempt FROM task WHERE id = $1`, *author.TaskID).Scan(&author.Attempt)
		}
	}
	if author.Type == "agent" && author.TaskID != nil {
		if err := tasks.InsertServerEvent(ctx, tx, *author.TaskID, author.Attempt, "status", "post_message",
			msgID.String(), "ok",
			map[string]any{"command": "message post", "result_ref": msgID.String()}, now); err != nil {
			return nil, uuid.Nil, err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE room SET updated_at = $2 WHERE id = $1`, sessionID, now); err != nil {
		return nil, uuid.Nil, err
	}
	msg, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	result.Message = messages.ToAPI(msg)
	return result, msgID, nil
}

// routeCtx is what every trigger of one post shares.
type routeCtx struct {
	sessionID, wsID uuid.UUID
	author          Author
	th              thread
	platform        *PlatformTrigger
	work            *uuid.UUID // the message's mission (attribute)
	profiles        map[uuid.UUID]uuid.UUID
	participants    []Participant
	msgID           uuid.UUID
	originator      *uuid.UUID
	newLane         bool
	now             time.Time
	question        questionCtx
}

// routed is one trigger's outcome.
type routed struct {
	entry postTrigger
	held  []heldAgent
}

// routeTrigger carries out one allowed trigger of a post (T-RF1: it was the
// body of postRow's loop): resolve the lane (FR-3.3 lane rules 1–4), merge
// into the lane's queued task or make one (FR-3.4), hold it when the mission
// waits for approval (T-QUIET), and publish task.updated. The loop limit was
// already judged by the caller.
func (s *Service) routeTrigger(ctx context.Context, tx pgx.Tx, rc routeCtx, tr Trigger) (routed, error) {
	var out routed
	rootLane, topLevel := rc.th.laneFor(tr)
	// PRD FR-3.8 2: an agent's mention of another agent is a question — it
	// lands on the lane the rules pick but does not re-enter or move it.
	question := rc.question.makesQuestion(tr)
	opts := laneOpts{
		threadRootLane: rootLane,
		topLevelMent:   topLevel,
		forceNewLane:   rc.newLane,
		work:           rc.work,
		question:       question,
	}
	if tr.Rule == RulePlatform && rc.platform != nil && rc.platform.LaneID != uuid.Nil {
		// 해소 규칙 1 with the lane named outright: the caller knows which
		// lane the event belongs to (the one that submitted the artifact),
		// and it must not depend on the reply's thread happening to root
		// there. Same lane, `reentry_count`+1 — never a new lane.
		opts.threadRootLane = rc.platform.LaneID
		opts.forceNewLane = false
		opts.pinned = true
	}
	laneID, _, err := s.resolveLaneFor(ctx, tx, rc.sessionID, tr, rc.profiles[tr.AgentID], opts, rc.now)
	if err != nil {
		return out, err
	}
	laneWork, err := bindLaneWork(ctx, tx, laneID, rc.work)
	if err != nil {
		return out, err
	}
	// FR-3.4: a queued task on the lane absorbs this message. PlanArrival
	// owns the decision (never cancel a running turn, merge per LANE, keep
	// arrival order); this only reads the lane's queue and writes the answer.
	queued, coalesced, err := lockQueuedTask(ctx, tx, laneID)
	if err != nil {
		return out, err
	}
	var laneStatus string
	if err := tx.QueryRow(ctx, `SELECT status::text FROM lane WHERE id = $1`, laneID).Scan(&laneStatus); err != nil {
		return out, err
	}
	arrival := PlanArrival(laneID, laneStatus, queued.Coalesced, []uuid.UUID{rc.msgID})
	if arrival.CancelledRunningTurn {
		// Unreachable by construction — the invariant is that no message
		// cancels a turn — but an explicit refusal beats a silent one if
		// PlanArrival ever changes.
		return out, fmt.Errorf("router: FR-3.4 invariant: a message may not cancel a running turn")
	}
	var taskID uuid.UUID
	if coalesced {
		taskID = queued.ID
		// The absorbing task takes the lane's mission when it had none
		// (T-R4b): a queued task born mission-less on a lane this post just
		// bound would otherwise run the mission's turn as "미션 없음" —
		// brief [4], budget, COLAB_WORK_ID and the reply all read task.work_id.
		// A task that already has a mission keeps it.
		if _, err := tx.Exec(ctx, `UPDATE task SET coalesced_message_ids = $2, work_id = COALESCE(work_id, $4), updated_at = $3 WHERE id = $1`,
			taskID, arrival.CoalescedMessageIDs, rc.now, laneWork); err != nil {
			return out, err
		}
		if err := promoteQueued(ctx, tx, taskID, laneID, question); err != nil {
			return out, err
		}
	} else {
		kind := ""
		if question {
			kind = cards.KindQuestion
		}
		if taskID, err = insertQueuedTask(ctx, tx, newQueuedTask{
			LaneID: laneID, SessionID: rc.sessionID, AgentID: tr.AgentID, ProfileID: rc.profiles[tr.AgentID],
			TriggerMessageID: rc.msgID, Originator: rc.originator, Coalesced: arrival.CoalescedMessageIDs,
			Work: laneWork, Now: rc.now, Kind: kind,
		}); err != nil {
			return out, fmt.Errorf("router: %w", err)
		}
	}
	// T-QUIET: an agent's trigger in a mission waiting for approval is
	// made and held — queued_reason approval_pending, which the claim
	// passes by. A platform trigger (a reviewer's rejection re-entering
	// the submitter's lane) is the system's, not an agent's word.
	if rc.author.Type == "agent" && tr.Rule != RulePlatform {
		hold, closed, err := holdsFor(ctx, tx, laneWork)
		if err != nil {
			return out, err
		}
		if closed && !coalesced {
			if err := quiet.CancelClosed(ctx, tx, taskID, rc.now); err != nil {
				return out, err
			}
			out.held = append(out.held, heldAgent{tr.AgentID, participantName(rc.participants, tr.AgentID)})
			s.publishLane(ctx, tx, laneID)
		}
		if hold {
			// A new task is held. One this message merged into is held
			// only if it already was: a queued turn a person's message
			// made runs, and this message rides along in it.
			var held bool
			if coalesced {
				held, err = quiet.IsHeld(ctx, tx, taskID)
			} else {
				held, err = quiet.Hold(ctx, tx, taskID)
			}
			if err != nil {
				return out, err
			}
			if held {
				out.held = append(out.held, heldAgent{tr.AgentID, participantName(rc.participants, tr.AgentID)})
				s.publishLane(ctx, tx, laneID)
			}
		}
	}
	out.entry = postTrigger{AgentId: tr.AgentID, Coalesced: coalesced, LaneId: laneID, TaskId: taskID}
	if t, err := tasks.Get(ctx, tx, taskID); err == nil && s.Hub != nil {
		sid := rc.sessionID
		_ = s.Hub.Publish(ctx, tx, rc.wsID, &sid, "task.updated", tasks.ToAPI(t, nil, nil))
	}
	return out, nil
}

// laneOpts carries the premises the four lane rules read that the trigger
// itself does not know.
type laneOpts struct {
	threadRootLane uuid.UUID
	topLevelMent   bool
	forceNewLane   bool
	// work is the mission the trigger runs for (FR-3.1.1; nil = none). Only
	// lanes of that mission — or bound to none yet — are candidates (T-R1b2).
	work *uuid.UUID
	// pinned: the caller named the lane outright (a platform trigger — the
	// lane that submitted a rejected artifact). It is re-entered whatever
	// mission the triggering message was filed under; the task then runs for
	// the lane's own mission (bindLaneWork).
	pinned bool
	// question: the trigger makes a question task (PRD FR-3.8 2). A reused
	// lane is NOT re-entered — no `queued`, no reentry_count — so a done lane
	// stays done and a running one keeps running.
	question bool
}

// resolveLaneFor applies PRD FR-3.3's lane resolution (rules 1–4) to one
// trigger. The decision itself is lanestate.Resolve — this only loads the
// candidates and writes the result.
func (s *Service) resolveLaneFor(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, tr Trigger, profileID uuid.UUID, o laneOpts, now time.Time) (uuid.UUID, bool, error) {
	// PRD v0.19: a lane belongs to at most one mission ("그 lane 이 매인 일"),
	// so the lanes a trigger may land on are its own mission's and the unbound
	// ones. With several missions per room (T-R1b2) the agent's newest lane is
	// easily another mission's — a closed one, even — and reusing it ran the
	// message for THAT mission (bindLaneWork keeps a bound lane's mission):
	// a "미션 없음" chat line queued under a completed mission never ran. In a
	// one-mission room every lane is that mission's and nothing changes.
	existing, err := laneCandidates(ctx, tx, sessionID, tr.AgentID, &o)
	if err != nil {
		return uuid.Nil, false, err
	}

	d := lanestate.Resolve(lanestate.Request{
		AgentID: tr.AgentID, Existing: existing,
		ThreadRootLaneID: o.threadRootLane,
		TopLevelMention:  o.topLevelMent, ForceNewLane: o.forceNewLane,
	})
	if !d.Created {
		// Lock the row we are about to reuse so two concurrent posts cannot
		// both re-enter it.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM lane WHERE id = $1 FOR UPDATE`, d.LaneID); err != nil {
			return uuid.Nil, false, err
		}
		if d.Reentry && !o.question {
			// FR-6.2 allows done/blocked → running. The lane becomes `running`
			// when the task is dispatched (tasks.MarkDispatched); until then it
			// is honestly `queued`, because no turn is in flight.
			if _, err := tx.Exec(ctx, `
				UPDATE lane SET status = 'queued', reentry_count = reentry_count + 1, finished_at = NULL, updated_at = $2
				WHERE id = $1`, d.LaneID, now); err != nil {
				return uuid.Nil, false, err
			}
			// A done/blocked card going back to queued is a status change like
			// any other: S7 must not keep showing it finished.
			s.publishLane(ctx, tx, d.LaneID)
		}
		return d.LaneID, false, nil
	}
	var id uuid.UUID
	var deleg *uuid.UUID
	if d.DelegatedFromTaskID != uuid.Nil {
		deleg = &d.DelegatedFromTaskID
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, delegated_from_task_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'queued', $5, $5) RETURNING id`,
		sessionID, tr.AgentID, profileID, deleg, now).Scan(&id); err != nil {
		return uuid.Nil, false, fmt.Errorf("router: insert lane: %w", err)
	}
	// Rules 2·4 make a lane: the row's first status is a status change too, and
	// without a frame the queued card only appears on the next full REST read.
	s.publishLane(ctx, tx, id)
	return id, true, nil
}

// publishLane emits `lane.updated` for the S7 board (lanes.Publish). Called
// from every router path that writes lane.status — a frame the board misses is
// a card frozen at its last-loaded state.
func (s *Service) publishLane(ctx context.Context, q db.DBTX, laneID uuid.UUID) {
	_ = lanes.Publish(ctx, s.Hub, q, laneID)
}

// loopLimits reads workspace_settings.loop_limits, falling back to the FR-3.5
// defaults for keys the row does not carry (E4-09 overrides one of them).
func (s *Service) loopLimits(ctx context.Context, tx pgx.Tx, wsID uuid.UUID) (Limits, error) {
	lim := DefaultLimits()
	var raw map[string]int
	err := tx.QueryRow(ctx, `SELECT loop_limits FROM workspace_settings WHERE workspace_id = $1`, wsID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return lim, nil
	}
	if err != nil {
		return lim, err
	}
	if v, ok := raw["max_chain_depth"]; ok {
		lim.MaxChainDepth = v
	}
	if v, ok := raw["max_hops_per_hour"]; ok {
		lim.MaxHopsPerHour = v
	}
	if v, ok := raw["max_pair_roundtrips"]; ok {
		lim.MaxPairRoundtrips = v
	}
	return lim, nil
}

// loadHops reads the trigger history the limits reason over: every hop from
// the room's LAST HUMAN hop on, plus the rolling hour.
//
// The rolling hour bounds hops_per_hour; chain depth and pair roundtrips walk
// backwards to the last human hop. The window used to be "the last 200 rows",
// which was safe for a session and is not for a room (NN3, V19_impl §4 위험
// 3): a room lives for weeks, and 200 agent hops in a row put the last person
// outside the window — chainDepth then saw an agent-only history, answered 0
// (its E4-07 reading of "no person, no chain") and max_chain_depth switched
// itself off exactly where a runaway chain is longest. Reading from the last
// human hop keeps that person in view however long the run since; a room with
// no human hop at all is read whole (it cannot be long without a person — a
// loop there trips hops_per_hour first). session_hop_human (migration r1b1_room_gate) finds the
// anchor.
//
// HopReadCap is the safety net under that window (#292 review NN2): the
// newest HopReadCap rows at most. Every limit fires long before it in any
// workspace a person configured (max_chain_depth 8, max_hops_per_hour 60 by
// default) — it bounds memory for a workspace that raised the limits to
// "effectively off", where the window would otherwise grow with the room.
func (s *Service) loadHops(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, now time.Time) ([]Hop, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, from_agent_id, to_agent_id, created_at, cause FROM (
		  SELECT id, from_agent_id, to_agent_id, created_at, COALESCE(cause_hop_id, 0) AS cause
		  FROM session_hop
		  WHERE session_id = $1
		    AND (id >= COALESCE((SELECT max(id) FROM session_hop
		                          WHERE session_id = $1 AND from_agent_id IS NULL), 0)
		         OR created_at > $2)
		  ORDER BY id DESC LIMIT $3) w
		ORDER BY id`, sessionID, now.Add(-HopWindow), HopReadCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hop
	for rows.Next() {
		var h Hop
		var from *uuid.UUID
		if err := rows.Scan(&h.ID, &from, &h.ToAgent, &h.At, &h.CauseID); err != nil {
			return nil, err
		}
		if from != nil {
			h.FromAgent = *from
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HopReadCap bounds loadHops (see there). A variable so a test can shrink it.
var HopReadCap = 5000

// causeOfTask is the causal link chain depth follows (S-78, loop.go
// chainDepth): the hop that created `task` — the row recorded for its trigger
// message toward its agent — as (id, that hop's own cause). A trigger written
// from `task` has CauseID = id; a notice that wakes the task's agent because
// work it asked for ended (join, blocked question, re-entry report) has
// CauseID = cause, so the requester returns at its OWN depth rather than one
// below the child that woke it.
//
// (0, 0) when the task has no hop: created before a loop resume erased the
// session's hops, or by a path that records none. chainDepth then reads it as
// "no cause".
//
// A queued task absorbs later messages (FR-3.4 coalescing), and the turn
// answers all of them: the latest hop among the trigger and the coalesced
// messages is the cause — the assignee's initial task, born from the
// session-start message, is usually run for the Director's first mention.
func causeOfTask(ctx context.Context, q pgx.Tx, taskID uuid.UUID) (id, cause int64, err error) {
	err = q.QueryRow(ctx, `
		SELECT h.id, COALESCE(h.cause_hop_id, 0) FROM task t
		JOIN session_hop h ON h.session_id = t.session_id AND h.to_agent_id = t.agent_id
		  AND (h.message_id = t.trigger_message_id OR h.message_id = ANY(t.coalesced_message_ids))
		WHERE t.id = $1 ORDER BY h.id DESC LIMIT 1`, taskID).Scan(&id, &cause)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	return id, cause, err
}

// reportReturn is PRD FR-3.5 v0.19.12's premise for one agent message: it is
// a report (speech = report, decided at write time by messages.Store) whose
// responds_to — the original request — was written by the agent `requester`
// from a task whose hop had cause `cause`. A trigger of this message toward
// `requester` is the report coming home and takes `cause` as its own.
type reportReturn struct {
	ok        bool
	requester uuid.UUID
	cause     int64
}

// returningReport reads reportReturn for a stored message. Only the stored
// speech and responds_to are consulted (#343/#370: the server decides them
// the moment the row is written; a group part is one row, #374), so the rule
// never second-guesses the body. A report whose responds_to is a person's,
// a system message, or a request written outside a task is not a return.
func returningReport(ctx context.Context, q pgx.Tx, msgID uuid.UUID) (reportReturn, error) {
	var requester, reqTask *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT r.author_id, r.source_task_id
		FROM message m JOIN message r ON r.id = m.responds_to_message_id
		WHERE m.id = $1 AND m.speech = 'report' AND r.author_type = 'agent'`, msgID).Scan(&requester, &reqTask)
	if errors.Is(err, pgx.ErrNoRows) {
		// PRD FR-3.8 2: a question turn's answer to the agent that asked is
		// the same return as a report (FR-3.5 v0.19.12) — the asker wakes at
		// the depth it asked from, so question↔answer does not deepen the
		// chain. The question is the question task's trigger message.
		err = q.QueryRow(ctx, `
			SELECT r.author_id, r.source_task_id
			FROM message m JOIN task t ON t.id = m.source_task_id AND t.kind = 'question'
			JOIN message r ON r.id = t.trigger_message_id
			WHERE m.id = $1 AND m.speech = 'answer' AND r.author_type = 'agent'`, msgID).Scan(&requester, &reqTask)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return reportReturn{}, nil
	}
	if err != nil {
		return reportReturn{}, err
	}
	if requester == nil || reqTask == nil {
		return reportReturn{}, nil
	}
	// The request-writing turn's depth is the depth of the hop that woke it,
	// so the report hop takes THAT hop's cause (as wake does for a join).
	_, cause, err := causeOfTask(ctx, q, *reqTask)
	if err != nil {
		return reportReturn{}, err
	}
	return reportReturn{ok: true, requester: *requester, cause: cause}, nil
}

// judgeHop is FR-3.5's VERDICT for one trigger: a delegation (delegate.go), a
// wake-up the server owes a delegator or an author (status.go wake — join,
// blocked question, re-entry report), and each trigger of a post (postRow).
// Post used to run a copy of this inline against a history it loaded once and
// appended to in memory; T-RF1 made it call this per trigger. The history is
// re-read each time and so includes the sibling hops just recorded — the same
// rows the in-memory append stood for (a human hop is never limited, so the
// window re-anchoring on a person's own sibling hop changes no verdict).
//
// One real difference, and why it changes no verdict (#393 review NN1): the
// in-memory hop had ID 0, the re-read one its real id, and CheckLoopLimits
// keys chain depth by hop ID (`depths[h.ID]`, loop.go). A sibling could
// therefore only matter as another trigger's CAUSE — and within one post it
// never is: every trigger's cause is `cause`/`ret.cause`, read once before
// the loop from rows that existed before this post, so the siblings of one
// post share their cause and none is another's.
//
// S-76: neither path used to be gated. `Delegate` recorded its hop and
// `wake` recorded nothing, so a delegator that re-delegated on every join
// notice ran a delegate ↔ join cycle that CheckLoopLimits never saw — 529
// tasks in 70 seconds with the session still `active` (T-I5, 77_ S1x). A
// delegation is an agent→agent hop and so is the notice that wakes the
// delegator when the child ends; the limiter has to see both, or the one
// loop that needs no mention at all is the one it cannot stop.
//
// The hop is recorded either way (allowed=false when it tripped) so the next
// decision reads a complete history. What this function does NOT do is stop
// the session: that is pauseForLoop, and the caller invokes it (S-79, PR #213
// 리뷰 NN2) — the verdict and its consequence used to be one function, so a
// caller that returned 409 and one that went quiet were both standing on a
// session already paused without the code saying so. Every site now reads
// judge → (tripped) pause → its own answer, and a third caller cannot forget
// the pause because nothing pauses for it.
func (s *Service) judgeHop(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID,
	next Hop, msgID uuid.UUID, rule int, now time.Time) (LoopVerdict, error) {
	limits, err := s.loopLimits(ctx, tx, wsID)
	if err != nil {
		return LoopVerdict{}, err
	}
	history, err := s.loadHops(ctx, tx, sessionID, now)
	if err != nil {
		return LoopVerdict{}, err
	}
	v := CheckLoopLimits(history, next, limits, now)
	if err := s.recordHop(ctx, tx, sessionID, next, msgID, rule, v.Allowed); err != nil {
		return LoopVerdict{}, err
	}
	return v, nil
}

// RecordHumanHop writes a person's hop toward `agent` for a trigger that did
// not come through Post — the two other ways a person starts or re-roots an
// agent's chain (S-78, FR-3.5 "사람이 개입하면 0"):
//
//   - session start: the Director created the session and its goal, and the
//     assignee's initial task (sessions.Create, E16-A step 1) is that person's
//     message to the assignee. Without the row a session the Director never
//     writes into has no human hop at all, and chainDepth measures nothing in
//     it — the chain the goal started would be rooted nowhere;
//   - a HITL answer: the task resumes on a person's word (handlers_hitl), so
//     the hops its next turn makes are one below the person, whatever depth
//     the question was asked at. `msgID` is the task's trigger message, which
//     is how causeOfTask finds the row for the resumed task.
//
// A human hop also ends a pair run and is not counted toward hops_per_hour,
// exactly as a human message is.
func (s *Service) RecordHumanHop(ctx context.Context, tx pgx.Tx, sessionID, agent, msgID uuid.UUID, now time.Time) error {
	return s.recordHop(ctx, tx, sessionID, Hop{ToAgent: agent, At: now}, msgID, RulePlatform, true)
}

func (s *Service) recordHop(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, h Hop, msgID uuid.UUID, rule int, allowed bool) error {
	var from *uuid.UUID
	if !h.Human() {
		f := h.FromAgent
		from = &f
	}
	var cause *int64
	if h.CauseID != 0 {
		c := h.CauseID
		cause = &c
	}
	// A resume (S-80) records a human hop with no message — message_id is
	// nullable and a zero uuid would break the FK.
	var msg *uuid.UUID
	if msgID != uuid.Nil {
		m := msgID
		msg = &m
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, message_id, rule, allowed, created_at, cause_hop_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, sessionID, from, h.ToAgent, msg, rule, allowed, h.At, cause)
	return err
}

// pauseForLoop is FR-3.5's consequence, at the ROOM (PRD v0.19 §3.1: routing
// and the loop limits are the room's): the room's gate goes up with
// `blocked_reason: loop`, the reason detail NAMES the limit, and the room
// owner gets a system-issued HITL (approver_spec room_owner — absent owner
// delegation, FR-2A.3). The room's active missions are parked with the same
// reason and marked as the room's (roomgate package comment), so each reads
// `paused(loop)`.
func (s *Service) pauseForLoop(ctx context.Context, tx pgx.Tx, sessionID, wsID uuid.UUID, v LoopVerdict, now time.Time) error {
	room, err := roomgate.Lock(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	if room.BlockedReason != nil {
		return nil // already stopped; one block per room, not one per trigger
	}
	detail := tasks.WithLoop(tasks.PausedDetail("loop", now), v.Detail, v.LimitCount(), v.Agents)
	agents := make([]openapi_types.UUID, 0, len(v.Agents))
	for _, a := range v.Agents {
		agents = append(agents, openapi_types.UUID(a))
	}
	if _, err := roomgate.Block(ctx, tx, sessionID, roomgate.ReasonLoop, gen.BlockedDetail{LoopAgents: &agents}, &detail, now); err != nil {
		return err
	}
	question := v.QuestionText()
	due := now.Add(24 * time.Hour)
	var hitlID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, proposed_default, approver_spec, purpose, due_at, created_at)
		VALUES ($1, NULL, 'system', 'approval', $2, NULL, 'room_owner', 'loop', $3, $4) RETURNING id`,
		sessionID, question, due, now).Scan(&hitlID); err != nil {
		return fmt.Errorf("router: loop hitl: %w", err)
	}
	// S-45: the timeline card. A loop pause is the one a reader is most likely
	// to meet in the timeline itself — the session stops mid-conversation — and
	// it posted no card at all, so the feed simply went quiet (SCREEN §4.5).
	// T-APPROVAL: AttachHitlCard links the card and publishes `hitl.created`.
	if _, err := messages.AttachHitlCard(ctx, s.Hub, tx, wsID, sessionID, hitlID, messages.HitlCard{
		Type: "approval", Question: question,
	}, now); err != nil {
		return fmt.Errorf("router: loop hitl card: %w", err)
	}
	// FR-8 v0.19: the whole room stopped — `room_paused` (action_required)
	// for the room owner's chain, whose answer lifts the gate.
	if err := roomgate.FileInbox(ctx, tx, roomgate.Item{
		Type: inbox.TypeRoomPaused, WorkspaceID: wsID, RoomID: sessionID, HitlID: hitlID,
		Created: now, Due: due,
	}); err != nil {
		return err
	}
	// FR-2.3: what happens to a turn already running depends on WHY we paused.
	// A loop pause is not a budget breach — the work in flight is legitimate —
	// so it drains. PlanDispatch owns that distinction.
	if s.Tasks != nil {
		if err := s.Tasks.PauseSessionTasks(ctx, tx, sessionID, "loop", loopPausedDetail(v, now), now); err != nil {
			return err
		}
	}
	roomgate.PublishUpdated(ctx, s.Hub, tx, sessionID)
	return nil
}

// scheduleFallback inserts rule 7's deferred assignee task. It is `deferred`
// with not_before = +5m, so the queue cannot hand it out early and the sweep
// promotes it when the window closes.
func (s *Service) scheduleFallback(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, fb Fallback, primary, profileID, msgID uuid.UUID, originator, work *uuid.UUID, now time.Time) (uuid.UUID, uuid.UUID, bool, error) {
	// One pending fallback per lane is enough; a second reply inside the same
	// window must not stack two assignee wake-ups.
	var dup int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM task WHERE fallback_for_task_id = $1 AND status = 'deferred'`, primary).Scan(&dup); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	if dup > 0 {
		return uuid.Nil, uuid.Nil, false, nil
	}
	laneID, _, err := s.resolveLaneFor(ctx, tx, sessionID, Trigger{AgentID: fb.AgentID, Rule: 7}, profileID,
		laneOpts{topLevelMent: true, work: work}, now)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	laneWork, err := bindLaneWork(ctx, tx, laneID, work)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id,
		                  status, not_before, fallback_for_task_id, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, 'deferred', $7, $8, $9, $9, $10) RETURNING id`,
		laneID, sessionID, fb.AgentID, profileID, msgID, originator, fb.DueAt, primary, now, laneWork).Scan(&taskID); err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("router: fallback task: %w", err)
	}
	return laneID, taskID, true, nil
}

// cancelFallbacksFor is E1-13: the primary agent spoke, so the deferred
// assignee task it was covering for is cancelled rather than run.
func (s *Service) cancelFallbacksFor(ctx context.Context, tx pgx.Tx, primary uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE task SET status = 'cancelled', finished_at = $2, updated_at = $2
		WHERE fallback_for_task_id = $1 AND status = 'deferred'`, primary, now)
	return err
}

// SystemPost inserts a system message without routing (session start etc.).
//
// It publishes, because a system message IS a timeline message: the session
// start notice, the join bundle and the re-entry notice all reached S7 only on
// reload before (G4 2판 W10). Routing is what SystemPost skips — not the frame.
func (s *Service) SystemPost(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, content string) (uuid.UUID, error) {
	return s.SystemPostWork(ctx, tx, sessionID, nil, content)
}

// SystemPostWork is SystemPost for a line that speaks about one mission
// (FR-3.1.1): the row carries its work_id from the insert on, so the
// `message.created` frame already names the mission.
func (s *Service) SystemPostWork(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, workID *uuid.UUID, content string) (uuid.UUID, error) {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, kind, created_at, work_id) VALUES ($1, 'system', NULL, $2, 'system', $3, $4) RETURNING id`,
		sessionID, strings.TrimSpace(content), s.Clock.Now(), workID).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if err := messages.Store(ctx, tx, id, messages.StoreOpts{}); err != nil {
		return uuid.Nil, err
	}
	s.publishMessage(ctx, tx, sessionID, id)
	return id, nil
}

// publishMessage sends `message.created` for a row this service inserted
// outside Post/DelegateLane. The workspace is read here rather than threaded
// through every caller: these are all one-per-turn events, not a hot path, and
// a missing frame is the bug this exists to prevent.
//
// A publish failure is not the caller's failure — the message is committed
// either way and the client re-reads via REST (realtime D1) — so it is logged
// by the hub's persist error, not returned.
func (s *Service) publishMessage(ctx context.Context, tx pgx.Tx, sessionID, msgID uuid.UUID) {
	if s.Hub == nil {
		return
	}
	var wsID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, sessionID).Scan(&wsID); err != nil {
		return
	}
	_ = messages.Publish(ctx, s.Hub, tx, wsID, sessionID, msgID)
}

// ---------------------------------------------------------------------------
// Premises. Post and Preview must read exactly the same facts, or the preview
// (FR-3.6) promises triggers the post does not create — which is the bug the
// web's local calculation had. They share these three loaders.
// ---------------------------------------------------------------------------

func loadParticipants(ctx context.Context, q db.DBTX, sessionID uuid.UUID) ([]Participant, map[uuid.UUID]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		SELECT sp.agent_id, a.name, a.respond_to = 'nobody', sp.profile_id
		FROM room_participant sp JOIN agent a ON a.id = sp.agent_id
		WHERE sp.room_id = $1 AND sp.left_at IS NULL ORDER BY sp.joined_at`, sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var participants []Participant
	profiles := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var p Participant
		var profile uuid.UUID
		if err := rows.Scan(&p.AgentID, &p.Name, &p.Disabled, &profile); err != nil {
			return nil, nil, err
		}
		participants = append(participants, p)
		profiles[p.AgentID] = profile
	}
	return participants, profiles, rows.Err()
}

// thread is the FR-3.3 rule 5 / lane rule 1 position of a message.
type thread struct {
	// Parent is the normalised thread ROOT: a reply to a reply hangs off the
	// root, not off the message the human clicked.
	Parent *uuid.UUID
	// ReplyTo owns the message actually replied to; ThreadOwner owns the root.
	ReplyTo     *uuid.UUID
	ThreadOwner *uuid.UUID
	// RootLane is the lane whose task produced the root (lane rule 1), and
	// RootLaneAgent the agent that lane belongs to.
	RootLane      uuid.UUID
	RootLaneAgent uuid.UUID
	// RootKind is the root's message_kind — a `blocked_q` root makes the
	// delegator's reply the answer that re-enters the child (FR-6.2.1), never
	// a question (Lead 판정 Q1).
	RootKind string
}

// laneFor is lane rule 1's premise for one trigger: the root's lane, and
// whether the trigger resolves as a top-level mention (rule 3).
//
// Rule 1 is scenario B — QA, in a review thread, asks Frontend, and the fix
// lands in the lane Frontend's root came out of. It holds only when the
// trigger is FOR the root lane's own agent (PRD FR-3.3 v0.19.1, Lead 판정
// T-THREAD). A trigger for any other agent used to land on that lane too: one
// turn per lane made it wait on the other agent, and a queued task already on
// the lane swallowed it whole (coalescing is per lane), so the mentioned
// agent never woke. Agents answering in threads (harness v0.9.3) made that
// the common path — Lead replying under its own summary and handing work on
// by mention. Such a trigger skips rule 1 and resolves as the same mention at
// the top level would (rule 3, then 4).
//
// A thread whose root came out of no lane is unchanged: no rule 1, and a
// thread reply is not a top-level mention (rule 4).
func (th thread) laneFor(tr Trigger) (rootLane uuid.UUID, topLevel bool) {
	switch {
	case th.Parent == nil:
		return uuid.Nil, tr.Rule == 2
	case th.RootLane == uuid.Nil:
		return uuid.Nil, false
	case th.RootLaneAgent == tr.AgentID:
		return th.RootLane, false
	}
	// Rule 5 — a reply that wakes the author of the message replied to — is
	// the thread's own form of a mention, and off the root's lane it goes
	// where a mention of that agent would.
	return uuid.Nil, tr.Rule == 2 || tr.Rule == 5
}

func threadPremise(ctx context.Context, q db.DBTX, sessionID uuid.UUID, parentID nullable.Nullable[openapi_types.UUID]) (thread, error) {
	var th thread
	if !parentID.IsSpecified() || parentID.IsNull() {
		return th, nil
	}
	pid := uuid.UUID(parentID.MustGet())
	var root *uuid.UUID
	var psess uuid.UUID
	var pType string
	var pAuthor *uuid.UUID
	err := q.QueryRow(ctx, `SELECT parent_id, session_id, author_type::text, author_id FROM message WHERE id = $1`, pid).
		Scan(&root, &psess, &pType, &pAuthor)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && psess != sessionID) {
		return th, ErrParentNotFound
	}
	if err != nil {
		return th, err
	}
	if pType == "agent" {
		th.ReplyTo = pAuthor
	}
	if root != nil {
		th.Parent = root
	} else {
		th.Parent = &pid
	}
	var rType string
	var rAuthor, rTask *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT author_type::text, author_id, source_task_id, kind::text FROM message WHERE id = $1`, *th.Parent).
		Scan(&rType, &rAuthor, &rTask, &th.RootKind); err != nil {
		return th, err
	}
	if rType == "agent" {
		th.ThreadOwner = rAuthor
	}
	// Lane rule 1: the thread root came out of a task, so the reply goes to
	// that task's lane and keeps the same workdir (scenario B).
	if rTask != nil {
		var lid, aid uuid.UUID
		err := q.QueryRow(ctx, `SELECT t.lane_id, l.agent_id FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, *rTask).Scan(&lid, &aid)
		if err == nil {
			th.RootLane, th.RootLaneAgent = lid, aid
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return th, err
		}
	}
	return th, nil
}

// delegatorPremise answers rule 8: who delegated the author's lane, and has
// that lane's join group already fired? The suppression lasts only until it
// has (E1-17) — otherwise a re-entered child could never speak to its
// delegator again.
func delegatorPremise(ctx context.Context, q db.DBTX, taskID *uuid.UUID) (*uuid.UUID, bool, error) {
	if taskID == nil {
		return nil, false, nil
	}
	var deleg *uuid.UUID
	var firedAt *time.Time
	err := q.QueryRow(ctx, `
		SELECT d.agent_id, d.join_fired_at
		FROM task t JOIN lane l ON l.id = t.lane_id
		JOIN task d ON d.id = l.delegated_from_task_id
		WHERE t.id = $1`, *taskID).Scan(&deleg, &firedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return deleg, firedAt != nil, nil
}

// loopPausedDetail is the jsonb the S5 banner reads for a loop pause (openapi
// PausedDetail.loop): which of the three limits tripped, the count that tripped
// it and the agents involved. "loop" on its own does not tell the Director
// which setting to raise (FR-3.5).
func loopPausedDetail(v LoopVerdict, now time.Time) []byte {
	count := 0
	switch v.Detail {
	case DetailChainDepth:
		count = v.ChainDepth
	case DetailHopsPerHour:
		count = v.HopsThisWindow
	case DetailPairRoundtrips:
		count = v.PairRoundtrips
	}
	d := tasks.WithLoop(tasks.PausedDetail("loop", now), v.Detail, count, v.Agents)
	raw, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return raw
}
