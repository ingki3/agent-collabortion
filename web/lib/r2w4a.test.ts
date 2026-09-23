/**
 * T-R2-W4a 순수 규칙 — S8 v0.19(`lib/inbox-v19.ts`) · 안 읽음(`lib/unread.ts`) · S15(`lib/audit.ts`) · S9(`lib/screens-v19.ts`).
 */
import { describe, expect, it } from "vitest";
import {
  applyScope, delegationLine, filterOptions, ga, INBOX_V19, needsDelegationLine, quoteLine, roomNameOf, roomPausedLines, ROOM_PAUSED_CARD, shortcutsOf, workTitleOf,
  type InboxItem,
} from "./inbox-v19";
import { latestMessageId, sumUnread, unreadEffect } from "./unread";
import { actionLabel, AUDIT, dayRange, objectCell, payloadCell, roomCell } from "./audit";
import { agentRoomsView, AGENT_ROOMS } from "./screens-v19";
import type { StreamEvent } from "./api/types";

const it0 = (over: Partial<InboxItem> = {}): InboxItem => ({
  id: "i", workspace_id: "w", type: "hitl_request", severity: "action_required", session_id: "r1", room_id: "r1", ref_id: "h", read_at: null, created_at: "2026-09-24T00:00:00Z", actions: [],
  ...over,
});

describe("S8 맥락 한 줄", () => {
  it("방 이름 — 0.2.9 room.name → 목록 이름표 → 「(볼 수 없는 방)」(지어내지 않는다)", () => {
    expect(roomNameOf(it0({ room: { id: "r1", name: "결제팀" } }))).toBe("결제팀");
    expect(roomNameOf(it0(), new Map([["r1", "목록 이름"]]))).toBe("목록 이름");
    expect(roomNameOf(it0())).toBe(INBOX_V19.unknown_room);
  });
  it("미션 제목은 work_id 가 있을 때만(서버 session 자리가 미션) · 미션 밖 인용은 mention·lane_blocked 둘째 줄", () => {
    expect(workTitleOf(it0({ work_id: null, session: { id: "x", title: "옛 세션", status: "active" } }))).toBeNull();
    expect(workTitleOf(it0({ work_id: "w1", session: { id: "w1", title: "비교표", status: "active" } }))).toBe("비교표");
    expect(quoteLine(it0({ type: "mention", card: { body: "확인 부탁" } }))).toBe('"확인 부탁"');
    expect(quoteLine(it0({ type: "mention", work_id: "w1", card: { body: "확인 부탁" } }))).toBeNull();
    expect(quoteLine(it0({ type: "hitl_request", card: { body: "x" } }))).toBeNull();
  });
  it("두 바로가기 — 미션이 없으면 방 하나만", () => {
    expect(shortcutsOf(it0())).toEqual({ room: "/rooms/r1", work: null });
    expect(shortcutsOf(it0({ work_id: "w1" }))).toEqual({ room: "/rooms/r1", work: "/rooms/r1?work=w1" });
    expect(shortcutsOf(it0({ room_id: null, session_id: null }))).toEqual({ room: null, work: null });
  });
});

describe("S8 위임 줄(FR-2A.3)", () => {
  const now = Date.parse("2026-09-24T05:00:00Z");
  it("방 층 넷만 위임 시점을 적는다 — room_paused · isolation_confirm · 미션 밖 hitl_request · 미션 밖 lane_blocked", () => {
    expect(needsDelegationLine({ type: "room_paused", work_id: "w" })).toBe(true);
    expect(needsDelegationLine({ type: "isolation_confirm", work_id: null })).toBe(true);
    expect(needsDelegationLine({ type: "hitl_request", work_id: null })).toBe(true);
    expect(needsDelegationLine({ type: "hitl_request", work_id: "w" })).toBe(false);
    expect(needsDelegationLine({ type: "lane_blocked", work_id: null })).toBe(true);
    expect(needsDelegationLine({ type: "mention", work_id: null })).toBe(false);
  });
  it("아직 → 「HH:MM부터 답할 수 있습니다」(잠김) · 지나면 「위임됨 · 방장 〈민호〉가 …」 · 방장 본인은 줄 없음", () => {
    const later = "2026-09-24T06:30:00Z";
    const locked = delegationLine({ recipient_basis: "room_deputy" }, { canRespond: false, from: later, ownerName: "민호", now });
    expect(locked?.locked).toBe(true);
    expect(locked?.text).toMatch(/^\d\d:\d\d부터 답할 수 있습니다$/);
    expect(delegationLine({ recipient_basis: "room_deputy" }, { canRespond: true, ownerName: "민호", now })).toEqual({ text: "위임됨 · 방장 민호가 아직 답하지 않았습니다", locked: false });
    expect(delegationLine({ recipient_basis: "workspace_owner" }, { canRespond: true, now })?.text).toBe(INBOX_V19.delegated_owner_plain);
    expect(delegationLine({ recipient_basis: "deputy" }, { canRespond: true, now })?.text).toBe(INBOX_V19.delegated_director);
    expect(delegationLine({ recipient_basis: "room_owner" }, { canRespond: true, now })).toBeNull();
  });
  it("주격 조사 — 받침 이 · 없음 가 · 비한글 이(가)", () => {
    expect(ga("민호")).toBe("민호가");
    expect(ga("서연")).toBe("서연이");
    expect(ga("Lead")).toBe("Lead이(가)");
  });
});

describe("S8 room_paused 두 문장 · 필터 둘째 줄", () => {
  it("수는 칸에서, 잔여 합계는 있을 때만 · 미션 0 이면 대화만", () => {
    expect(roomPausedLines({ works_stopped: 3, open_works_remaining_usd: 6.5 })).toEqual({
      stopped: ROOM_PAUSED_CARD.stopped(3), resume: `${ROOM_PAUSED_CARD.resume(3)} — 열린 미션 잔여 예산 합계 $6.50`,
    });
    expect(roomPausedLines({ works_stopped: 2 }).resume).toBe(ROOM_PAUSED_CARD.resume(2));
    expect(roomPausedLines(null)).toEqual({ stopped: ROOM_PAUSED_CARD.stopped_no_works, resume: ROOM_PAUSED_CARD.resume_no_works });
  });
  it("선택지는 받은 목록에서 · 방을 고르면 미션도 그 방 것만 · 「미션 없음」은 미션 밖 항목", () => {
    const items = [
      it0({ id: "a", room: { id: "r1", name: "결제팀" }, work_id: "w1", session: { id: "w1", title: "비교표", status: "active" } }),
      it0({ id: "b", room: { id: "r2", name: "인프라" }, room_id: "r2", session_id: "r2", work_id: "w2", session: { id: "w2", title: "배포", status: "active" } }),
      it0({ id: "c", room: { id: "r1", name: "결제팀" } }),
    ];
    const all = filterOptions(items);
    expect(all.rooms).toEqual([["r1", "결제팀"], ["r2", "인프라"]]);
    expect(filterOptions(items, undefined, "r1").works).toEqual([["w1", "비교표"]]);
    expect(applyScope(items, "r1", "").map((x) => x.id)).toEqual(["a", "c"]);
    expect(applyScope(items, "", "none").map((x) => x.id)).toEqual(["c"]);
    expect(applyScope(items, "", "w2").map((x) => x.id)).toEqual(["b"]);
  });
});

describe("안 읽음(M4)", () => {
  it("마지막 메시지 — created_at, 같으면 id", () => {
    expect(latestMessageId([{ id: "a", created_at: "2" }, { id: "c", created_at: "3" }, { id: "b", created_at: "3" }])).toBe("c");
    expect(latestMessageId([])).toBeNull();
  });
  it("room.unread 는 그 자리에서 · 삭제는 뺀다 · 새 메시지는 다시 부른다 · 합계", () => {
    const m = new Map([["r1", 2], ["r2", 5]]);
    const ev = (type: string, payload: unknown) => ({ type, payload } as unknown as StreamEvent);
    const set = unreadEffect(m, ev("room.unread", { room_id: "r1", unread_count: 0 }));
    expect(set.kind === "set" && sumUnread(set.counts)).toBe(5);
    const del = unreadEffect(m, ev("room.deleted", { room_id: "r2" }));
    expect(del.kind === "set" && [...del.counts.keys()]).toEqual(["r1"]);
    expect(unreadEffect(m, ev("message.created", {})).kind).toBe("reload");
    expect(unreadEffect(m, ev("lane.updated", {})).kind).toBe("none");
  });
});

describe("S15 활동 로그 칸", () => {
  it("행위는 사람 말, 모르는 값은 원시 값 그대로", () => {
    expect(actionLabel("room.read.denied")).toBe("다른 방 읽기 거부");
    expect(actionLabel("room.brand_new")).toBe("room.brand_new");
  });
  it("거부 행은 대상 방 이름을 숨기고 · 지워진 방은 내용에 남은 이름 + 「(지워진 방)」", () => {
    expect(objectCell({ action: "room.read.denied", object_ref: "room" })).toBe(AUDIT.hidden_target);
    expect(objectCell({ action: "room.read", object_ref: "room:abc" })).toBe("방");
    expect(roomCell({ action: "room.deleted", room: null, payload: { name: "회고" } })).toBe(`회고 ${AUDIT.deleted_room}`);
    expect(roomCell({ action: "x", room: null, payload: {} })).toBe(AUDIT.no_room);
  });
  it("내용 — uuid·객체는 빼고, 방향은 S23 의 말, 마스킹 표시는 따로", () => {
    const p = payloadCell({ direction: "in", other_room_id: "0b8c5a52-4a60-4d6c-9d7b-1a2b3c4d5e6f", summary: true, masked: true, args: { a: 1 } });
    expect(p).toEqual({ text: "방향 읽힘 · 요약 포함 예", masked: true });
  });
  it("기간 — 종료일은 그날 끝까지(다음 날 자정 미만)", () => {
    const r = dayRange("2026-09-01", "2026-09-01");
    expect(Date.parse(r.until!) - Date.parse(r.since!)).toBe(24 * 3600_000);
    expect(dayRange("", "")).toEqual({});
  });
});

describe("S9 참여 중인 방", () => {
  it("합계 = 볼 수 있는 방 + 볼 수 없는 방(개수를 속이지 않는다) · 이름은 볼 수 있는 방만", () => {
    expect(agentRoomsView({ rooms: [{ id: "a", name: "결제팀" }], room_count: 1, hidden_room_count: 2 })).toEqual({ rooms: [{ id: "a", name: "결제팀" }], hidden: 2, total: 3 });
    expect(AGENT_ROOMS.hidden(2)).toBe("+ 볼 수 없는 방 2");
    expect(agentRoomsView({})).toEqual({ rooms: [], hidden: 0, total: 0 });
  });
});
