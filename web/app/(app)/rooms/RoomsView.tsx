"use client";
/**
 * S5 방 목록 + S25 방 찾기 + S18 방 만들기(모달) — SCREEN v0.19.2 §4.3·§4.4·§4.5, T-R2-W1.
 *
 * 옛 Sessions 목록의 자리다. 카드에서 상태 배지와 goal 이 빠지고(방에는 둘 다 없다) **무엇이 얼마나 밀려 있는가**(안 읽음·진행 중인 미션·
 * 내가 답할 것·방 멈춤)로 채운다. 목록은 **한 열**이고(§8.5 v0.19) 정렬은 **마지막 활동순 하나로 고정**이다(§12.1-11 — 제어가 없다).
 *
 * 데이터: `listRooms`(계약 0.2.4) 한 번 — 안 읽음은 서버가 목록에서 한 번에 센다(방마다 묻지 않는다, §4.4 [구현 주의]).
 * 「내가 참여한 방만」이 켜져 있으면 같은 거르개로 `participating=false` 를 한 번 더 물어 **안 보이는 공개 방 수**를 센다(S25 한 줄).
 * 실시간: `room.updated`(멈춤·이름·보관) · `room.unread`(내 다른 탭·기기) · `room.deleted`(= `session.deleted`) 는 그 자리에서 고치고,
 * 수가 바뀌는 나머지(미션·주의·새 메시지)는 잠깐 모아 다시 불러온다 — 어느 경우든 **재정렬하지 않는다**(lib/rooms.ts).
 * S18 은 `/rooms/new` 에서 이 화면 위에 뜬다(`creating`).
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PageHead } from "@/components/PageHead";
import { RoomCard } from "@/components/RoomCard";
import { RoomSearchBar, filtersFromParams, filtersToQuery, type RoomFilters } from "@/components/RoomSearchBar";
import { ArchiveRoomDialog, DeleteRoomDialog } from "@/components/RoomDialogs";
import { CreateRoomDialog } from "@/components/CreateRoomDialog";
import { Slot } from "@/components/Slot";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { mergeKeepOrder, roomEventEffect } from "@/lib/rooms";
import { ROOM_DELETED_NOTICE, ROOM_LIST } from "@/lib/wording";
import { pageItems, type Room, type RoomListItem, type Runtime, type StreamEvent } from "@/lib/api/types";
import "@/components/room-card.css";

/** 수가 바뀌는 프레임을 모아 한 번 다시 부르는 간격 — 에이전트가 메시지를 연달아 쓸 때 목록 요청이 폭주하지 않게. */
const RELOAD_DEBOUNCE_MS = 800;

export function RoomsView({ creating = false }: { creating?: boolean }) {
  const { workspace, canManage } = useAuth();
  const router = useRouter();
  const search = useSearchParams();
  const filters = useMemo(() => filtersFromParams(new URLSearchParams(search.toString())), [search]);
  const [items, setItems] = useState<RoomListItem[] | null>(null);
  const [morePublic, setMorePublic] = useState<number | null>(null);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [archiving, setArchiving] = useState<RoomListItem | null>(null);
  const [deleting, setDeleting] = useState<RoomListItem | null>(null);

  // S7 에서 방이 지워져 돌아온 사람 — 안내 한 줄을 보이고 주소는 깨끗이(새로고침해도 다시 뜨지 않게).
  const deletedName = search.get("deleted");
  useEffect(() => {
    if (deletedName == null) return;
    setNotice(ROOM_DELETED_NOTICE.elsewhere(deletedName));
    router.replace("/rooms");
  }, [deletedName, router]);

  // 거르개가 바뀌면 목록을 새로 받는다(재정렬 금지는 같은 거르개 안의 실시간 갱신 얘기다 — 사람이 거르개를 바꾸면 새 목록이다).
  const filterKey = filtersToQuery(filters);
  const lastKey = useRef<string | null>(null);
  const load = useCallback(async () => {
    if (!workspace) return;
    const base = { q: filters.q || undefined, unread_only: filters.unread, include_archived: filters.archived };
    try {
      const [page, all, rts] = await Promise.all([
        api.get("/workspaces/{workspaceId}/rooms", { path: { workspaceId: workspace.id }, query: { ...base, participating: filters.mine } }),
        filters.mine
          ? api.get("/workspaces/{workspaceId}/rooms", { path: { workspaceId: workspace.id }, query: { ...base, participating: false } })
          : Promise.resolve(null),
        api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
      ]);
      const fresh = pageItems<RoomListItem>(page);
      const sameFilters = lastKey.current === filterKey;
      lastKey.current = filterKey;
      setItems((cur) => (sameFilters ? mergeKeepOrder(cur, fresh) : fresh));
      setMorePublic(all ? pageItems<RoomListItem>(all).filter((r) => r.my_room_role == null).length : null);
      setRuntimes(rts);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspace, filters, filterKey]);

  useEffect(() => {
    void load();
  }, [load]);

  const reloadTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scheduleReload = useCallback(() => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
    reloadTimer.current = setTimeout(() => void load(), RELOAD_DEBOUNCE_MS);
  }, [load]);
  useEffect(() => () => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
  }, []);

  const itemsRef = useRef(items);
  itemsRef.current = items;
  const onEvent = useCallback(
    (ev: StreamEvent) => {
      const cur = itemsRef.current;
      if (!cur) return;
      const eff = roomEventEffect(cur, ev);
      if (eff.kind === "set") setItems(eff.items);
      else if (eff.kind === "reload") scheduleReload();
      if (ev.type === "runtime.updated") scheduleReload();
    },
    [scheduleReload],
  );
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  const setFilters = useCallback((next: RoomFilters) => router.replace(`/rooms${filtersToQuery(next)}`), [router]);
  const removeRoom = useCallback((id: string) => setItems((cur) => (cur ? cur.filter((r) => r.id !== id) : cur)), []);
  const patchRoom = useCallback(
    (r: Pick<Room, "id" | "status" | "name" | "description" | "blocked_reason">) =>
      setItems((cur) => (cur ? cur.map((x) => (x.id === r.id ? { ...x, status: r.status, name: r.name, description: r.description, blocked_reason: r.blocked_reason } : x)) : cur)),
    [],
  );
  async function unarchive(room: RoomListItem) {
    try {
      patchRoom(await api.post("/rooms/{roomId}/unarchive", { path: { roomId: room.id } }));
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  const noComputer = runtimes !== null && runtimes.filter((r) => r.status === "online").length === 0;
  const filtered = !!filters.q.trim() || filters.unread || filters.archived || !filters.mine;
  const newRoomHref = `/rooms/new${filterKey}`;

  return (
    <div>
      <PageHead screen="rooms">
        <Link href={newRoomHref} className="btn btn--primary" data-testid="new-room">
          {ROOM_LIST.new_room}
        </Link>
      </PageHead>
      <RoomSearchBar value={filters} onChange={setFilters} morePublic={morePublic} />
      {error && <p className="problem">{error}</p>}
      {notice && (
        <p className="notice notice--info" role="status" data-testid="room-deleted-notice">
          {notice}
        </p>
      )}
      {items === null ? (
        <p className="muted">{ROOM_LIST.loading}</p>
      ) : items.length === 0 && !filtered ? (
        <div className="empty" data-testid="empty-no-room">
          <div className="empty__title">{ROOM_LIST.empty_title}</div>
          <ul className="room-empty__examples empty__body">
            {ROOM_LIST.empty_examples.map((x) => (
              <li key={x}>{x}</li>
            ))}
          </ul>
          <Link href={newRoomHref} className="btn btn--primary" data-testid="empty-new-room">
            {ROOM_LIST.new_room}
          </Link>
          {/* 컴퓨터가 없어도 막지 않는다(§2.1) — 보조로만 말한다. */}
          {noComputer && (
            <p className="room-empty__aside" data-testid="empty-no-computer">
              {ROOM_LIST.empty_no_computer} · <Link href="/runtimes/new">{ROOM_LIST.empty_no_computer_link}</Link>
            </p>
          )}
        </div>
      ) : items.length === 0 ? (
        <div className="empty" data-testid="empty-no-match">
          <div className="empty__title">{filters.q.trim() ? <Slot text={ROOM_LIST.no_match} n={filters.q.trim()} /> : ROOM_LIST.no_match_filters}</div>
          {!filters.archived && (
            <button type="button" className="btn" onClick={() => setFilters({ ...filters, archived: true })} data-testid="empty-retry-archived">
              {ROOM_LIST.retry_with_archived}
            </button>
          )}
        </div>
      ) : (
        <div className="room-list" data-testid="room-list">
          {items.map((r) => (
            <RoomCard
              key={r.id}
              room={r}
              canManage={canManage}
              onArchive={() => setArchiving(r)}
              onUnarchive={() => void unarchive(r)}
              onDelete={() => setDeleting(r)}
              onRename={async (name) => patchRoom(await api.patch("/rooms/{roomId}", { path: { roomId: r.id }, body: { name } }))}
            />
          ))}
        </div>
      )}
      {archiving && <ArchiveRoomDialog room={archiving} onArchived={patchRoom} onClose={() => setArchiving(null)} />}
      {deleting && (
        <DeleteRoomDialog
          room={deleting}
          onDeleted={(id) => {
            removeRoom(id);
            setNotice(ROOM_DELETED_NOTICE.mine(deleting.name));
          }}
          onClose={() => setDeleting(null)}
        />
      )}
      {creating && workspace && (
        <CreateRoomDialog
          workspaceId={workspace.id}
          onCreated={(room) => router.push(`/rooms/${room.id}`)}
          onClose={() => router.push(`/rooms${filterKey}`)}
        />
      )}
    </div>
  );
}
