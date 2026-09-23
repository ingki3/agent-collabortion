"use client";
/**
 * 방 화면(S7) 위에 **쿼리로** 뜨는 두 다이얼로그의 라우팅(T-R2-W3) — S21 미션 열기(`?work=new` · `?work=from&message=:mid`) ·
 * S26 미션 제안 확인(`?work_proposal=:id`). SCREEN §3.2 「다이얼로그에도 URL 을 준다」 — 받은 요청·알림에서 바로 그 자리로 보내고,
 * 새로고침해도 돌아와야 한다.
 *
 * S7 페이지(T-R2-W2)는 **마운트 한 줄**이다: `<RoomQueryDialogs roomId={id} />`. 버튼은 훅의 여는 함수를 부른다:
 *   - 「+ 새 미션」 → `openNewWork()` · 메시지 「…」 → 「이걸 미션으로」 → `openFromMessage(message.id)` · 제안 카드 → `openProposal(id)`.
 * 미션이 열리면 `?work=<새 미션 id>` 로 바꾼다 — S22 라우트(그 미션 칩이 선택된 방 화면). 닫으면 두 쿼리만 지운다(다른 쿼리는 둔다).
 * `?work=<uuid>` 는 S22(미션 패널)의 몫이라 여기서는 `new`·`from` 두 값만 다이얼로그로 읽는다.
 */
import { useCallback, useMemo } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { CreateWorkDialog } from "./CreateWorkDialog";
import { WorkProposalDialog } from "./WorkProposalDialog";
import type { Message } from "@/lib/api/types";
import type { Work } from "@/lib/room-dialogs";

export type RoomDialogQuery = { kind: "none" } | { kind: "new_work" } | { kind: "work_from"; messageId: string } | { kind: "proposal"; proposalId: string };

/** 쿼리 → 어떤 다이얼로그인지(순수 함수 — 테스트가 표로 잰다). */
export function parseRoomDialogQuery(q: { get(name: string): string | null }): RoomDialogQuery {
  const proposal = q.get("work_proposal");
  if (proposal) return { kind: "proposal", proposalId: proposal };
  const work = q.get("work");
  if (work === "new") return { kind: "new_work" };
  if (work === "from") {
    const m = q.get("message");
    return m ? { kind: "work_from", messageId: m } : { kind: "new_work" };
  }
  return { kind: "none" };
}

const DIALOG_KEYS = ["work_proposal", "message"] as const;

/** 쿼리를 읽고 바꾸는 훅 — S7 버튼이 쓴다. */
export function useRoomDialogQuery() {
  const search = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const state = useMemo(() => parseRoomDialogQuery(search), [search]);
  const go = useCallback(
    (edit: (q: URLSearchParams) => void) => {
      const q = new URLSearchParams(search.toString());
      edit(q);
      const s = q.toString();
      router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
    },
    [search, router, pathname],
  );
  const clear = (q: URLSearchParams) => {
    for (const k of DIALOG_KEYS) q.delete(k);
    if (q.get("work") === "new" || q.get("work") === "from") q.delete("work");
  };
  return {
    state,
    openNewWork: () => go((q) => (clear(q), q.set("work", "new"))),
    openFromMessage: (messageId: string) => go((q) => (clear(q), q.set("work", "from"), q.set("message", messageId))),
    openProposal: (proposalId: string) => go((q) => (clear(q), q.set("work_proposal", proposalId))),
    close: () => go(clear),
    /** 미션이 열렸다 — 그 미션을 고른 방 화면(S22 `?work=<id>`). */
    showWork: (workId: string) => go((q) => (clear(q), q.set("work", workId))),
  };
}

export interface RoomQueryDialogsProps {
  roomId: string;
  /** S7 이 가진 메시지로 인용·멘션을 채운다(서버 `getMessage` 는 아직 없다) — id 로 찾아 돌려준다. */
  findMessage?: (id: string) => Message | null | undefined;
  /** 미션이 열린 뒤 — 기본은 `?work=<id>` 로 바꾸기. S7 이 칩 선택을 직접 하려면 넘긴다. */
  onWorkOpened?: (work: Work) => void;
}

export function RoomQueryDialogs({ roomId, findMessage, onWorkOpened }: RoomQueryDialogsProps) {
  const { state, close, showWork } = useRoomDialogQuery();
  const opened = (w: Work) => (onWorkOpened ? (close(), onWorkOpened(w)) : showWork(w.id));
  if (state.kind === "new_work") return <CreateWorkDialog key="new" roomId={roomId} mode="new" onOpened={opened} onClose={close} />;
  if (state.kind === "work_from") {
    return <CreateWorkDialog key={state.messageId} roomId={roomId} mode="from" messageId={state.messageId} message={findMessage?.(state.messageId) ?? null} onOpened={opened} onClose={close} />;
  }
  if (state.kind === "proposal") return <WorkProposalDialog key={state.proposalId} roomId={roomId} proposalId={state.proposalId} findMessage={findMessage} onOpened={opened} onClose={close} />;
  return null;
}

export default RoomQueryDialogs;
