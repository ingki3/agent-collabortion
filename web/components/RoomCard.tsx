"use client";
/**
 * S5 방 카드(SCREEN §4.3, T-R2-W1) — **상태 배지와 goal 이 없다**(방에는 둘 다 없다). 그 자리를 무엇이 얼마나 밀려 있는가로 채운다:
 * 이름 · 안 읽음 배지 · 방 멈춤 배지(사유별) · 보관됨 · 한 줄 설명 · 참여자(최대 5 + `+N`) | 진행 중인 미션 N · 주의 배지 셋 · 마지막 활동.
 *
 * 카드는 세션 카드(T-W13)와 같은 구조다 — `article` 안에 링크(내용 전부)와 「…」 메뉴가 형제(a 안에 button 을 둘 수 없다).
 * 숫자만 있는 요소에는 라벨을 단다(§7): 안 읽음 `aria-label="안 읽은 메시지 3개"` · 참여자 묶음 `aria-label="참여자 7명"` · 주의 배지 셋은 라벨 + 수.
 * 수는 문장에 보간하지 않고 슬롯에 둔다(COMPONENTS §8.5 v0.19, `<Slot>`).
 */
import { useState } from "react";
import Link from "next/link";
import { Badge } from "./Badge";
import { InlineTitleEdit } from "./InlineTitleEdit";
import { RoomCardMenu } from "./RoomCardMenu";
import { Slot, slotText } from "./Slot";
import { relativeTime } from "@/lib/time";
import { ROOM_LIST, archiveGate, deleteRoomGate, renameGate } from "@/lib/wording";
import type { RoomListItem } from "@/lib/api/types";
import "./room-card.css";

/** 참여자 묶음에서 이름으로 보이는 최대 수(§4.3 "최대 5 + `+N`"). */
export const PEOPLE_MAX = 5;

/** 안 읽음 배지(`UnreadBadge`, SCREEN §5 새로 만들 것) — 0 이면 그리지 않는다. */
export function UnreadBadge({ n }: { n: number }) {
  if (n <= 0) return null;
  return (
    <span className="unread-badge" role="img" aria-label={slotText(ROOM_LIST.unread_label, n)} data-testid="room-unread">
      {n > 99 ? "99+" : n}
    </span>
  );
}

export interface RoomCardProps {
  room: RoomListItem;
  canManage: boolean;
  onArchive: () => void;
  onUnarchive: () => void;
  onDelete: () => void;
  /** 방 이름 바꾸기(FR-2.1.2) — 「…」 「이름 바꾸기」가 이름 줄을 편집 칸으로 바꾼다. 실패는 throw. */
  onRename?: (name: string) => Promise<unknown>;
}

export function RoomCard({ room, canManage, onArchive, onUnarchive, onDelete, onRename }: RoomCardProps) {
  const archived = room.status === "archived";
  const canRename = !!onRename && renameGate(room, { canManage }).ok;
  const [renaming, setRenaming] = useState(false);
  const shown = room.participants.slice(0, PEOPLE_MAX);
  const more = room.participants.length - shown.length;
  const { hitl_open, blocked, failed } = room.attention;
  return (
    <article
      className={`room-card${archived ? " room-card--archived" : ""}`}
      data-testid="room-row"
      data-room-id={room.id}
      data-status={room.status}
      data-blocked={room.blocked_reason ?? undefined}
    >
      {/* 이름을 고치는 동안에는 이름 줄이 링크 밖의 편집 칸이 된다(a 안에 input·button 을 둘 수 없다). */}
      {renaming && canRename && (
        <div className="room-card__rename">
          <InlineTitleEdit className="room-card__name-input" value={room.name} canEdit editing onEditingChange={setRenaming} onSave={(n) => onRename!(n)} testId="room-rename" />
        </div>
      )}
      <Link href={`/rooms/${room.id}`} className="room-card__link" data-testid="room-link">
        <span className="room-card__main">
          <span className="room-card__top">
            {!renaming && <span className="room-card__name" title={room.name} data-testid="room-name">{room.name}</span>}
            <UnreadBadge n={room.unread_count} />
            {room.blocked_reason && <Badge kind="room" value={room.blocked_reason} size="sm" />}
            {archived && <span className="room-card__chip" data-testid="room-archived">{ROOM_LIST.archived}</span>}
          </span>
          {room.description && <span className="room-card__desc" title={room.description}>{room.description}</span>}
          {room.participants.length > 0 && (
            <span className="room-card__people" role="group" aria-label={slotText(ROOM_LIST.participants_label, room.participants.length)} data-testid="room-people">
              {shown.map((p, i) => (
                <span key={`${p.kind}:${p.id}`} className={`room-card__person room-card__person--${p.kind}`}>
                  {i > 0 && <span aria-hidden="true"> · </span>}
                  {p.kind === "agent" ? `@${p.name}` : p.name}
                </span>
              ))}
              {more > 0 && (
                <span className="room-card__person" data-testid="room-people-more">
                  {" · "}
                  <Slot text={ROOM_LIST.more_participants} n={more} />
                </span>
              )}
            </span>
          )}
        </span>
        <span className="room-card__side">
          <span className="room-card__works" data-testid="room-works">
            {room.active_work_count > 0 ? <Slot text={ROOM_LIST.works_active} n={room.active_work_count} /> : ROOM_LIST.works_none}
          </span>
          {hitl_open + blocked + failed > 0 && (
            <span className="room-card__attention" data-testid="room-attention">
              {hitl_open > 0 && <span className="room-card__att room-card__att--wait"><span aria-hidden="true">⏳︎ </span><Slot text={ROOM_LIST.attention_hitl} n={hitl_open} /></span>}
              {blocked > 0 && <span className="room-card__att room-card__att--block"><span aria-hidden="true">? </span><Slot text={ROOM_LIST.attention_blocked} n={blocked} /></span>}
              {failed > 0 && <span className="room-card__att room-card__att--fail"><span aria-hidden="true">✕ </span><Slot text={ROOM_LIST.attention_failed} n={failed} /></span>}
            </span>
          )}
          {room.last_activity_at && <span className="room-card__time">{relativeTime(room.last_activity_at)}</span>}
        </span>
      </Link>
      <RoomCardMenu
        archived={archived}
        archiveGate={archiveGate(room, { canManage })}
        deleteGate={deleteRoomGate(room, { canManage })}
        onArchive={onArchive}
        onUnarchive={onUnarchive}
        onDelete={onDelete}
        onRename={canRename ? () => setRenaming(true) : undefined}
        testId={room.id}
      />
    </article>
  );
}

export default RoomCard;
