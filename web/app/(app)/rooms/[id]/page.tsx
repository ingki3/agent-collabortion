"use client";
/**
 * S7 방 화면(`/rooms/:id`) — **지금은 옛 S7 세션 화면을 그대로 그리는 얇은 페이지**다(T-R2-W1). S7 재작성(칩 줄·우열 분할·room+works 로딩)은 T-R2-W2.
 *
 * 방 id 는 옛 세션 id 와 같다(§7 이관 규칙) — 옛 세션에서 온 방은 `/sessions/{id}` 로 그 화면이 그대로 뜬다. **`createRoom` 으로 새로 만든 방은
 * 옛 세션이 없다**(서버: `legacy_work_id` 가 없어 `getSession` 404). 그 방은 `getRoom` 으로 이름·설명만 보이는 임시 화면을 그린다 —
 * S18 「만들기」 → 여기로 오는 흐름이 404 에서 끝나지 않게. W2 가 이 분기를 지운다.
 */
import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import SessionPage from "../../sessions/[id]/page";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { ROOM_PENDING } from "@/lib/wording";
import type { Room } from "@/lib/api/types";

type View = { kind: "probing" } | { kind: "session" } | { kind: "room"; room: Room } | { kind: "error"; message: string };

export default function RoomPage() {
  const { id } = useParams<{ id: string }>();
  const [view, setView] = useState<View>({ kind: "probing" });
  useEffect(() => {
    let live = true;
    setView({ kind: "probing" });
    api.get("/sessions/{sessionId}", { path: { sessionId: id } }).then(
      () => live && setView({ kind: "session" }),
      async (e) => {
        // 옛 세션이 없는 방만 임시 화면으로 — 그 밖의 오류(403 등)는 옛 S7 이 제 문장으로 말한다.
        if (!(isApiError(e) && e.status === 404)) return live && setView({ kind: "session" });
        try {
          const room = await api.get("/rooms/{roomId}", { path: { roomId: id } });
          if (live) setView({ kind: "room", room });
        } catch (e2) {
          if (live) setView({ kind: "error", message: errorMessage(e2) });
        }
      },
    );
    return () => {
      live = false;
    };
  }, [id]);

  if (view.kind === "probing") return null;
  if (view.kind === "session") return <SessionPage />;
  if (view.kind === "error") {
    return (
      <div data-testid="room-error">
        <p className="problem">{view.message}</p>
        <Link href="/rooms" className="btn">{ROOM_PENDING.back}</Link>
      </div>
    );
  }
  return (
    <div className="stack" data-testid="room-pending" data-room-id={view.room.id}>
      <Link href="/rooms" className="small muted-3">← {ROOM_PENDING.back}</Link>
      <h1 style={{ margin: 0, fontSize: "var(--fs-title)" }} data-testid="room-title">{view.room.name}</h1>
      {view.room.description && <p className="muted" style={{ margin: 0 }}>{view.room.description}</p>}
      <div className="empty">
        <div className="empty__body">{ROOM_PENDING.body}</div>
      </div>
    </div>
  );
}
