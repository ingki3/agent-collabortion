"use client";
/**
 * S17 컴퓨터 바꾸기(SCREEN v0.19.2 §4.16) — `/runtimes/:id/rebind?room=`. 단위가 **세션에서 방으로** 바뀐다.
 *
 *   · `?room=` 가 있으면 그 방의 결정 다이얼로그를 바로 연다(방 화면 멈춤 배너·받은 요청에서 들어온다).
 *   · 없으면 **이 컴퓨터에 묶인 방 목록이 먼저** 뜬다 — 여러 방이 한 컴퓨터에 걸렸으면 방마다 따로 결정한다.
 *
 * 재바인딩 op 은 아직 옛 이름(`rebindSession`, 방 id = 세션 id — §7 이관 규칙)이다. 별칭 제거는 R4 조건에서만.
 */
import { Suspense, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { PageHead } from "@/components/PageHead";
import { RebindDialog } from "@/components/RebindDialog";
import { api, errorMessage } from "@/lib/api/client";
import { COMPUTER_ROOMS } from "@/lib/screens-v19";
import type { Session } from "@/lib/api/types";

type Target = React.ComponentProps<typeof RebindDialog>["session"];

export default function RebindPage() {
  return (
    <Suspense fallback={null}>
      <RebindInner />
    </Suspense>
  );
}

function RebindInner() {
  const { id: runtimeId } = useParams<{ id: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const roomId = search.get("room");
  const [target, setTarget] = useState<Target | null>(null);
  const [rooms, setRooms] = useState<{ id: string; title: string }[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const open = useCallback(async (id: string) => {
    try {
      const s: Session = await api.get("/sessions/{sessionId}", { path: { sessionId: id } });
      setTarget({ id: s.id, title: s.title, isolation: s.isolation, status: s.status, workspace_id: s.workspace_id, paused_detail: s.paused_detail, runtime: s.runtime ?? null });
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    if (roomId) {
      void open(roomId);
      return;
    }
    void api
      .get("/runtimes/{runtimeId}", { path: { runtimeId } })
      .then((rt) => setRooms((rt.active_sessions ?? []).map((x) => ({ id: x.id, title: x.title }))))
      .catch((e) => setError(errorMessage(e)));
  }, [roomId, runtimeId, open]);

  return (
    <div data-testid="rebind-page">
      <PageHead screen="computers" />
      {error && <p className="problem" role="alert" data-testid="rebind-error">{error}</p>}
      {target && <p className="notice" data-testid="rebind-room">{COMPUTER_ROOMS.rebind_for_room(target.title)}</p>}
      {!roomId && rooms && (
        <ul className="stack" data-testid="rebind-rooms">
          {rooms.map((r) => (
            <li key={r.id}>
              <Link href={`/runtimes/${runtimeId}/rebind?room=${r.id}`} data-testid="rebind-room-link">{r.title}</Link>
            </li>
          ))}
        </ul>
      )}
      {target && (
        <RebindDialog
          session={target}
          onClose={() => router.push(roomId ? `/rooms/${roomId}` : "/runtimes")}
          onDone={() => router.push(roomId ? `/rooms/${roomId}` : "/runtimes")}
        />
      )}
    </div>
  );
}
