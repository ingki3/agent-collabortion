// Package inbox is FR-8 / SCREEN §4.6: what lands in a person's inbox, how
// urgent it is, and what they can do about it without opening the session.
//
// The severity table is a function rather than a literal at each insert site
// because the badge is derived from it (`getInboxSummary` counts
// action_required only) and a single miscategorised insert makes the badge
// either useless or alarming.
package inbox

// The seven item types (inbox_item_type, FR-8).
const (
	TypeHitlRequest      = "hitl_request"
	TypeLaneBlocked      = "lane_blocked"
	TypeSessionCompleted = "session_completed"
	TypeSessionPaused    = "session_paused"
	TypeRunFailed        = "run_failed"
	TypeRuntimeOffline   = "runtime_offline"
	TypeMention          = "mention"
	// TypeWorkdirGCBlocked is FR-6.4's "삭제하지 않고 Director 에게 알린다"
	// (E13-12·13). Added in P4 by Lead decision (T-S9 ask 1); the contract's
	// InboxItemType grows the same value.
	TypeWorkdirGCBlocked = "workdir_gc_blocked"

	// v0.2.0 (PRD v0.19, openapi InboxItemType). room_invited · workdir_quota
	// are written by the room API (T-R1b3); the others are the mission and
	// first-run streams' to write — their severity and actions are fixed here
	// so every writer lands on the same badge.
	TypeRoomInvited      = "room_invited"
	TypeRoomPaused       = "room_paused"
	TypeWorkdirQuota     = "workdir_quota"
	TypeWorkProposed     = "work_proposed"
	TypeWorkPaused       = "work_paused"
	TypeWorkCompleted    = "work_completed"
	TypeIsolationConfirm = "isolation_confirm"
)

// Recipient bases (InboxItem.recipient_basis) — the card's 「Director 로서」·
// 「방장으로서」: why this item is in THIS person's inbox.
const (
	BasisDirector       = "director"
	BasisDeputy         = "deputy"
	BasisRoomOwner      = "room_owner"
	BasisRoomDeputy     = "room_deputy"
	BasisWorkspaceOwner = "workspace_owner"
)

// The three severities (inbox_severity, SCREEN §4.6).
const (
	ActionRequired = "action_required"
	Attention      = "attention"
	Info           = "info"
)

// Severity is SCREEN §4.6's table. The split is "does this WAIT for me?":
//
//	action_required  nothing moves until a person acts — an open HITL request,
//	                 a lane blocked on a question, a session paused for budget
//	                 or a loop.
//	attention        something went wrong and a person should look, but the
//	                 platform is not holding a turn for them.
//	info             it happened; read it when you read the inbox.
//
// `info` items are deliberately outside the nav badge: counting them makes the
// badge a permanent number nobody reads.
func Severity(itemType string) string {
	switch itemType {
	case TypeHitlRequest, TypeLaneBlocked, TypeSessionPaused,
		TypeRoomPaused, TypeWorkPaused, TypeIsolationConfirm:
		return ActionRequired
	case TypeRunFailed, TypeRuntimeOffline, TypeWorkdirGCBlocked, TypeWorkdirQuota, TypeWorkProposed:
		return Attention
	case TypeSessionCompleted, TypeMention, TypeRoomInvited, TypeWorkCompleted:
		return Info
	}
	return Info
}

// Actions is the inline action list (openapi InboxItem.actions). It is
// permission-aware: an action the caller cannot take is not offered, because a
// button that 403s is worse than no button (FR-5.3 last bullet).
func Actions(itemType, hitlType string, canRespond bool) []string {
	switch itemType {
	case TypeHitlRequest:
		if !canRespond {
			return []string{"open_session"}
		}
		switch hitlType {
		case "approval":
			return []string{"approve", "reject", "open_session"}
		default:
			return []string{"answer", "open_session"}
		}
	case TypeLaneBlocked, TypeMention:
		return []string{"reply", "open_session"}
	case TypeSessionPaused:
		if canRespond {
			return []string{"approve_continue", "open_session"}
		}
		return []string{"open_session"}
	case TypeRunFailed:
		if canRespond {
			return []string{"restart", "open_session"}
		}
		return []string{"open_session"}
	case TypeWorkdirGCBlocked:
		// The two ways out FR-6.4 names. "정리" is the manual delete S13
		// offers once the person has merged or discarded.
		return []string{"open_workdirs", "delete_workdir"}
	case TypeRuntimeOffline:
		return []string{"open_runtimes"}
	case TypeSessionCompleted:
		return []string{"open_session"}
	case TypeWorkdirQuota:
		// FR-6.4: the way out is clearing folders on S13.
		return []string{"open_workdirs"}
	case TypeRoomInvited, TypeRoomPaused, TypeWorkProposed, TypeIsolationConfirm:
		// A room-level card (openapi 0.2.1): the room, not a mission, is what
		// opens. A proposal is not a mission yet; the isolation question is
		// the room's first run.
		return []string{"open_room"}
	case TypeWorkPaused, TypeWorkCompleted:
		return []string{"open_work"}
	}
	return []string{"open_session"}
}

// SortRank is the group order of SCREEN §4.6: overdue first, then by severity.
// Within a group the caller orders by due_at, soonest first — an inbox sorted
// by arrival buries the thing that expires in ten minutes under five notices.
func SortRank(severity string, overdue bool) int {
	if overdue {
		return 0
	}
	switch severity {
	case ActionRequired:
		return 1
	case Attention:
		return 2
	}
	return 3
}
