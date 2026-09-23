"use client";
/**
 * S24 참고 방 링크(`/rooms/:id/settings/links`, 다이얼로그) — SCREEN v0.19.2 §4.12, T-R2-W3.
 *
 * **없으면 ⑤가 반쪽이 된다** — FR-4.5 권한 2번 조건(「현재 방에 참고 방 링크로 연결되어 있으면」)을 거는 유일한 화면이다.
 *   - 연결된 방: 이름 · 설명 · 마지막 활동 · 연결한 사람·시각 · 「연결 풀기」.
 *   - 방 찾아 연결: 검색 한 칸 + **내가 참여한 방만**(`listRooms` 기본 `participating=true` — 모르는 방을 링크로 탐색하지 못하게, 서버 403
 *     `not_participant_of_target` 와 같은 선) + 「연결」.
 *   - 설명 한 줄(읽기만, 양쪽 기록) · 아래 한 줄 「연결은 에이전트 쪽 조건만 풉니다…」(FR-4.5 조건 1 — 링크가 있어도 요청자가 저 방의
 *     참여자가 아니면 403).
 * 권한 밖이면 읽기 전용(연결된 방 목록은 참여자 전원이 본다 — 맥락이 어디로 샐 수 있는지는 공개 정보). 실시간: `room_link.updated`
 * (반대쪽 방장이 풀 수 있다).
 */
import { useCallback, useEffect, useId, useState } from "react";
import { DisabledHint } from "./PageHead";
import { RoomDialogShell, useRoomEvents } from "./RoomDialogShell";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { relativeTime } from "@/lib/time";
import { COMMON, LINKS, linkGate, personName, type RoomLink } from "@/lib/room-dialogs";
import { pageItems, type Room, type RoomListItem } from "@/lib/api/types";

export interface RoomLinksDialogProps {
  roomId: string;
  onClose: () => void;
}

const LINK_EVENTS = ["room_link.updated", "room.updated"] as const;

export function RoomLinksDialog({ roomId, onClose }: RoomLinksDialogProps) {
  const { workspace } = useAuth();
  const [room, setRoom] = useState<Room | null>(null);
  const [links, setLinks] = useState<RoomLink[] | null>(null);
  const [mine, setMine] = useState<RoomListItem[] | null>(null);
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ id: string; text: string } | null>(null);
  const base = useId();
  const hintId = `${base}-link-hint`;

  const load = useCallback(async () => {
    try {
      const [r, l] = await Promise.all([api.get("/rooms/{roomId}", { path: { roomId } }), api.get("/rooms/{roomId}/links", { path: { roomId } })]);
      setRoom(r);
      setLinks(l.items);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId]);
  useEffect(() => {
    void load();
  }, [load]);
  // 후보는 내가 참여한 방만 — 이름 검색은 서버에 맡긴다(설명까지 걸린다). 보관된 방도 읽을 맥락이라 포함한다.
  useEffect(() => {
    if (!workspace) return;
    let live = true;
    const t = setTimeout(() => {
      api
        .get("/workspaces/{workspaceId}/rooms", { path: { workspaceId: workspace.id }, query: { q: q.trim() || undefined, participating: true, include_archived: true } })
        .then((p) => live && setMine(pageItems<RoomListItem>(p)), () => live && setMine([]));
    }, 200);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [workspace, q]);
  useRoomEvents(workspace?.id, roomId, LINK_EVENTS, () => void load());

  const gate = room ? linkGate(room) : { ok: false as const, reason: "" };
  const linked = new Set((links ?? []).map((l) => l.target_room.id));
  const candidates = (mine ?? []).filter((r) => r.id !== roomId && !linked.has(r.id));

  async function link(targetId: string) {
    setBusy(`link:${targetId}`);
    setRowError(null);
    try {
      await api.post("/rooms/{roomId}/links", { path: { roomId }, body: { target_room_id: targetId }, idempotencyKey: newIdempotencyKey() });
      await load();
    } catch (e) {
      setRowError({ id: targetId, text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }
  async function unlink(l: RoomLink) {
    setBusy(`unlink:${l.id}`);
    setRowError(null);
    try {
      await api.delete("/rooms/{roomId}/links/{roomLinkId}", { path: { roomId, roomLinkId: l.id } });
      await load();
    } catch (e) {
      setRowError({ id: l.id, text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }

  return (
    <RoomDialogShell title={LINKS.title} sub={LINKS.explain} testId="rd-links" onClose={onClose}>
      {error && <p className="problem" role="alert" data-testid="rd-links-error">{error}</p>}
      {!gate.ok && room && <DisabledHint id={hintId}>{gate.reason}</DisabledHint>}
      <section className="rd-section" aria-labelledby={`${base}-linked`}>
        <h3 className="rd-section__title" id={`${base}-linked`}>{LINKS.section_linked}</h3>
        {!links ? (
          <p className="rd-hint">{COMMON.loading}</p>
        ) : links.length === 0 ? (
          <p className="rd-hint" data-testid="rd-links-empty">{LINKS.empty}</p>
        ) : (
          <ul className="rd-list" data-testid="rd-links-list">
            {links.map((l) => (
              <li key={l.id} className="rd-row" data-testid="rd-link-row">
                <div className="rd-row__main">
                  <span className="rd-row__name">{l.target_room.name}</span>
                  {l.target_room.description && <span className="rd-row__meta">{l.target_room.description}</span>}
                  <span className="rd-row__meta">
                    {LINKS.last_activity} {l.target_room.last_activity_at ? relativeTime(l.target_room.last_activity_at) : LINKS.no_activity}
                    {" · "}
                    {LINKS.linked_by} {personName(l.created_by)} · {relativeTime(l.created_at)}
                  </span>
                  {rowError?.id === l.id && <p className="rd-err" role="alert">{rowError.text}</p>}
                </div>
                <div className="rd-row__side">
                  <button
                    type="button"
                    className="btn btn--sm"
                    aria-disabled={!gate.ok || undefined}
                    aria-describedby={!gate.ok ? hintId : undefined}
                    disabled={busy === `unlink:${l.id}`}
                    onClick={() => gate.ok && void unlink(l)}
                    data-testid="rd-link-unlink"
                  >
                    {busy === `unlink:${l.id}` ? LINKS.unlinking : LINKS.unlink}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rd-section" aria-labelledby={`${base}-find`}>
        <h3 className="rd-section__title" id={`${base}-find`}>{LINKS.section_find}</h3>
        <label className="rd-field">
          <span className="rd-field__label">{LINKS.search_label}</span>
          <input className="input" value={q} placeholder={LINKS.search_placeholder} onChange={(e) => setQ(e.target.value)} data-testid="rd-links-search" />
        </label>
        {mine && candidates.length === 0 ? (
          <p className="rd-hint" data-testid="rd-links-search-empty">{LINKS.search_empty}</p>
        ) : (
          <ul className="rd-list" data-testid="rd-links-candidates">
            {candidates.map((r) => (
              <li key={r.id} className="rd-row" data-testid="rd-link-candidate">
                <div className="rd-row__main">
                  <span className="rd-row__name">{r.name}</span>
                  {r.description && <span className="rd-row__meta">{r.description}</span>}
                  {rowError?.id === r.id && <p className="rd-err" role="alert">{rowError.text}</p>}
                </div>
                <div className="rd-row__side">
                  <button
                    type="button"
                    className="btn btn--sm btn--primary"
                    aria-disabled={!gate.ok || undefined}
                    aria-describedby={!gate.ok ? hintId : undefined}
                    disabled={busy === `link:${r.id}`}
                    onClick={() => gate.ok && void link(r.id)}
                    data-testid="rd-link-add"
                  >
                    {busy === `link:${r.id}` ? LINKS.linking : LINKS.link}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
      <p className="rd-warn" data-testid="rd-links-agent-side">{LINKS.agent_side_only}</p>
    </RoomDialogShell>
  );
}

export default RoomLinksDialog;
