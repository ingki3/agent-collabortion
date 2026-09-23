"use client";
/**
 * S25 방 찾기(SCREEN §4.4, T-R2-W1) — S5 상단 제어. 새 화면이 아니라 S5 의 한 상태다(주소 `/rooms?q=&unread=&archived=`).
 *
 * 검색(이름·설명 부분 일치) · 안 읽음만 · **내가 참여한 방만(기본 켜짐)** · 보관 포함. 정렬은 **마지막 활동순 하나로 고정**이라 제어가 없고,
 * 고정돼 있다는 사실만 적는다(§12.1-11). 「내가 참여한 방만」이 켜져 있어 안 보이는 공개 방이 있으면 토글 옆에 그 수와 끄는 링크를 둔다
 * (SCR-A 막힘 5 — 이 줄이 없으면 워크스페이스 공개의 실효 경로를 아무도 모른다).
 */
import { useEffect, useState } from "react";
import { Slot } from "./Slot";
import { ROOM_LIST } from "@/lib/wording";

export interface RoomFilters {
  q: string;
  unread: boolean;
  mine: boolean;
  archived: boolean;
}
export const DEFAULT_FILTERS: RoomFilters = { q: "", unread: false, mine: true, archived: false };

/** 주소 ↔ 거르개 — 기본값은 주소에 적지 않는다(`/rooms` 가 기본 상태). */
export function filtersFromParams(p: URLSearchParams): RoomFilters {
  return { q: p.get("q") ?? "", unread: p.get("unread") === "1", mine: p.get("mine") !== "0", archived: p.get("archived") === "1" };
}
export function filtersToQuery(f: RoomFilters): string {
  const qs = new URLSearchParams();
  if (f.q.trim()) qs.set("q", f.q.trim());
  if (f.unread) qs.set("unread", "1");
  if (!f.mine) qs.set("mine", "0");
  if (f.archived) qs.set("archived", "1");
  const s = qs.toString();
  return s ? `?${s}` : "";
}

export interface RoomSearchBarProps {
  value: RoomFilters;
  onChange: (next: RoomFilters) => void;
  /** 「내가 참여한 방만」 때문에 안 보이는 공개 방 수. 0·null 이면 줄을 그리지 않는다. */
  morePublic: number | null;
}

export function RoomSearchBar({ value, onChange, morePublic }: RoomSearchBarProps) {
  // 입력은 곧바로 그리고, 질의는 잠깐 멈춘 뒤에 한 번(타자마다 listRooms 를 부르지 않게).
  const [q, setQ] = useState(value.q);
  useEffect(() => setQ(value.q), [value.q]);
  useEffect(() => {
    if (q === value.q) return;
    const t = setTimeout(() => onChange({ ...value, q }), 250);
    return () => clearTimeout(t);
  }, [q, value, onChange]);
  const toggle = (k: "unread" | "mine" | "archived", testId: string, label: string) => (
    <label className="room-search__toggle">
      <input type="checkbox" checked={value[k]} onChange={(e) => onChange({ ...value, [k]: e.target.checked })} data-testid={testId} />
      <span>{label}</span>
    </label>
  );
  return (
    <div className="room-search" role="search" aria-label={ROOM_LIST.search_label} data-testid="room-search">
      <div className="room-search__row">
        <input
          type="search"
          className="input room-search__input"
          value={q}
          placeholder={ROOM_LIST.search_placeholder}
          aria-label={ROOM_LIST.search_placeholder}
          onChange={(e) => setQ(e.target.value)}
          data-testid="room-search-input"
        />
        {toggle("unread", "room-filter-unread", ROOM_LIST.unread_only)}
        {toggle("mine", "room-filter-mine", ROOM_LIST.participating)}
        {toggle("archived", "room-filter-archived", ROOM_LIST.include_archived)}
        <span className="room-search__sort" data-testid="room-sort">{ROOM_LIST.sort_fixed}</span>
      </div>
      {value.mine && !!morePublic && morePublic > 0 && (
        <p className="room-search__more" data-testid="room-more-public">
          <Slot text={ROOM_LIST.more_public} n={morePublic} />
          {" — "}
          <button type="button" className="linklike" onClick={() => onChange({ ...value, mine: false })} data-testid="room-more-public-off">
            {ROOM_LIST.more_public_action}
          </button>
        </p>
      )}
    </div>
  );
}

export default RoomSearchBar;
