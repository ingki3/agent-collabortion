"use client";
/**
 * 안 읽음(PRD §10 R2 M4 · SCREEN §4.3·§4.4·§6, T-R2-W4a) — **방 단위 하나**(§12.1-6 확정: `room_participant.last_read_message_id`, 사람 행만).
 *
 *   · 방 화면을 보고 있으면 `markRoomRead`(마지막으로 본 메시지) — 표식은 서버가 **앞으로만** 옮긴다(다른 탭이 지운 것을 되살리지 않는다).
 *   · 내비 「방」 옆 합계 — `listRooms` 한 번으로 방마다의 `unread_count` 를 더한다(방마다 묻지 않는다, §4.4 [구현 주의]).
 *   · `room.unread` SSE — 내가 다른 탭·기기에서 읽었을 때도 와서 두 곳의 배지가 갈리지 않는다(§6).
 *
 * 순수 함수(`latestMessageId`·`sumUnread`·`unreadEffect`)는 여기서 테스트하고, 훅 둘은 그것을 이어 붙일 뿐이다.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api/client";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { Message, RoomListItem, StreamEvent } from "@/lib/api/types";

/** 가장 최근 메시지 — 서버와 같은 순서(created_at, 같으면 id). 답글·아직 쓰는 중(`streaming`)인 것도 본 것이다. */
export function latestMessageId(ms: readonly Pick<Message, "id" | "created_at">[]): string | null {
  let best: Pick<Message, "id" | "created_at"> | null = null;
  for (const m of ms) {
    if (!best || m.created_at > best.created_at || (m.created_at === best.created_at && m.id > best.id)) best = m;
  }
  return best?.id ?? null;
}

/** 내비 합계 — 방마다의 수를 더한다. */
export const sumUnread = (counts: ReadonlyMap<string, number>): number => [...counts.values()].reduce((a, b) => a + b, 0);

/** SSE 한 프레임이 합계에 주는 효과 — 셀 수 있는 것(`room.unread`·삭제)은 그 자리에서, 새 메시지는 다시 부른다. */
export function unreadEffect(counts: ReadonlyMap<string, number>, ev: StreamEvent): { kind: "set"; counts: Map<string, number> } | { kind: "reload" } | { kind: "none" } {
  switch (ev.type) {
    case "room.unread": {
      const p = ev.payload as { room_id?: string; unread_count?: number };
      if (!p.room_id || typeof p.unread_count !== "number") return { kind: "none" };
      const next = new Map(counts);
      next.set(p.room_id, p.unread_count);
      return { kind: "set", counts: next };
    }
    case "room.deleted": {
      // 옛 이름 session.deleted 는 v0.3.0(R4, D22)에서 지워졌다.
      const p = ev.payload as { room_id?: string; session_id?: string };
      const id = p.room_id ?? p.session_id ?? ev.room_id;
      if (!id || !counts.has(id)) return { kind: "none" };
      const next = new Map(counts);
      next.delete(id);
      return { kind: "set", counts: next };
    }
    case "message.created":
    case "participant.joined":
    case "participant.left":
      return { kind: "reload" };
  }
  return { kind: "none" };
}

/** 새 메시지가 몰려올 때 목록 요청이 폭주하지 않게 모으는 간격. */
const RELOAD_MS = 1000;

/**
 * 내비 「방」 옆 안 읽음 합계. 못 읽으면 null(낡은 수를 남기지 않는다 — 받은 요청 뱃지와 같은 규칙).
 * **내가 참여한 방만** 센다 — 안 읽음 표식이 참여자(사람 행)에만 있다.
 */
export function useRoomsUnreadTotal(workspaceId: string | null | undefined): number | null {
  const [counts, setCounts] = useState<Map<string, number> | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const load = useCallback(async () => {
    if (!workspaceId) return;
    try {
      const page = await api.get("/workspaces/{workspaceId}/rooms", { path: { workspaceId }, query: { participating: true, limit: 200 } });
      setCounts(new Map((page.items as RoomListItem[]).map((r) => [r.id, r.unread_count])));
    } catch {
      setCounts(null);
    }
  }, [workspaceId]);
  useEffect(() => {
    void load();
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [load]);
  const onEvent = useCallback((ev: StreamEvent) => {
    setCounts((cur) => {
      if (!cur) return cur;
      const eff = unreadEffect(cur, ev);
      if (eff.kind === "set") return eff.counts;
      if (eff.kind === "reload" && !timer.current) {
        timer.current = setTimeout(() => {
          timer.current = null;
          void load();
        }, RELOAD_MS);
      }
      return cur;
    });
  }, [load]);
  useWorkspaceStream(workspaceId ?? undefined, onEvent, { onResync: () => void load() });
  return counts ? sumUnread(counts) : null;
}

/**
 * 방 화면을 **보고 있을 때만** 마지막 메시지까지 읽음으로 옮긴다(§4.3 · N9 `markRoomRead`).
 *   · 탭이 가려져 있으면 옮기지 않는다 — 안 본 것을 본 것으로 만들지 않는다. 다시 보이면 그때 옮긴다.
 *   · 같은 메시지로 두 번 부르지 않는다. 실패는 조용히 넘긴다(다음 메시지에서 다시 한다) — 배지가 하나 남는 것이 화면이 멈추는 것보다 낫다.
 *   · `enabled` 가 거짓이면(참여자가 아닌 공개 방·감사 열람 — 서버 403 not_participant) 부르지 않는다.
 */
export function useMarkRoomRead(roomId: string | null | undefined, messages: readonly Pick<Message, "id" | "created_at">[], enabled: boolean): void {
  const last = latestMessageId(messages);
  const sent = useRef<string | null>(null);
  const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState !== "hidden");
  useEffect(() => {
    if (typeof document === "undefined") return;
    const on = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  useEffect(() => {
    sent.current = null;
  }, [roomId]);
  useEffect(() => {
    if (!roomId || !last || !enabled || !visible || sent.current === last) return;
    sent.current = last;
    void Promise.resolve()
      .then(() => api.post("/rooms/{roomId}/read", { path: { roomId }, body: { last_read_message_id: last } }))
      .catch(() => {
        sent.current = null;
      });
  }, [roomId, last, enabled, visible]);
}
