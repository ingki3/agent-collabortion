"use client";
/**
 * S14 설정의 v0.19 몫(SCREEN v0.19.2 §4.17, T-R2-W4a) — 「방 기본값」 탭 · 컨텍스트 탭의 다른 방 읽기 상한 · 알림 탭의 구독 단위 3층.
 *
 * 앞의 둘은 `WorkspaceSettingsTab` 의 초안(draft)에 붙는 행이다 — 저장은 그 탭의 「저장」 하나가 **바꾼 칸만** 보낸다(`diffSettings`).
 * 구독 단위는 개인 설정이고 op 이 셋으로 갈린다(계약 0.2.9): 방 `setRoomSubscription` · 미션 `setWorkSubscription` · 서브 미션 `setLaneSubscription`.
 * 고르는 즉시 저장한다(§4.17 「값 저장(즉시 반영)」) — 방·미션·서브 미션마다 저장 버튼을 두면 어느 것을 눌렀는지 모른다.
 */
import { useCallback, useEffect, useState } from "react";
import { SettingRow } from "./SettingsTabs";
import { api, errorMessage } from "@/lib/api/client";
import { AUTONOMY_TEXT } from "@/lib/room-dialogs";
import { ISOLATION_LABEL } from "@/lib/settings";
import {
  ROOM_DEFAULTS_DEFAULTS, ROOM_DEFAULTS_TAB, ROOM_READ, ROOM_READ_DEFAULTS, ROOM_SUB_LABEL, SUBSCRIPTIONS, VISIBILITY_LABEL, WORK_SUB_LABEL,
  type RoomSubscriptionLevel, type SubscriptionLevel,
} from "@/lib/screens-v19";
import type { components } from "@/lib/api/schema";
import type { Lane, RoomListItem, WorkListItem, WorkspaceSettings } from "@/lib/api/types";

type S = components["schemas"];
type RoomDefaults = NonNullable<WorkspaceSettings["room_defaults"]>;
type Set = (fn: (d: WorkspaceSettings) => WorkspaceSettings) => void;

const str = (v: number | null | undefined): string => (v == null ? "" : String(v));
const numOrNull = (v: string): number | null => (v.trim() === "" ? null : Number(v));

/** 「방 기본값」 탭(§4.17 표 셋째 행) — 머리에 「이 값이 새로 만드는 모든 방의 기본값입니다」. */
export function RoomDefaultsRows({ draft, set, lock, err }: { draft: WorkspaceSettings; set: Set; lock: boolean; err: (k: string) => string | null }) {
  const d: RoomDefaults = draft.room_defaults ?? {};
  const limits = d.limits ?? {};
  const put = (patch: Partial<RoomDefaults>) => set((x) => ({ ...x, room_defaults: { ...(x.room_defaults ?? {}), ...patch } }));
  const putLimits = (patch: Partial<NonNullable<RoomDefaults["limits"]>>) => put({ limits: { ...limits, ...patch } });
  const iso = d.isolation_kind ?? draft.default_isolation ?? "none";
  return (
    <>
      <p className="notice notice--info small" data-testid="room-defaults-head">{ROOM_DEFAULTS_TAB.head}</p>
      <SettingRow label={ROOM_DEFAULTS_TAB.isolation} defaultValue={ISOLATION_LABEL[ROOM_DEFAULTS_DEFAULTS.isolation_kind]} impact={ROOM_DEFAULTS_TAB.impact.isolation} error={err("room_defaults.isolation_kind")} testid="row-room-isolation">
        {/* 새 방의 기본 격리는 없음·워크트리 둘뿐이다(서버 422 room_defaults.isolation_kind — 컨테이너는 아직). */}
        <select className="select" disabled={lock} value={iso} aria-label={ROOM_DEFAULTS_TAB.isolation} onChange={(e) => put({ isolation_kind: e.target.value as S["IsolationKind"] })} data-testid="room-default-isolation">
          {(["none", "worktree"] as const).map((k) => <option key={k} value={k}>{ISOLATION_LABEL[k]}</option>)}
        </select>
        {iso === "none" && <span className="small muted-3" data-testid="room-default-isolation-note">{ROOM_DEFAULTS_TAB.isolation_none_note}</span>}
      </SettingRow>
      <SettingRow label={ROOM_DEFAULTS_TAB.visibility} defaultValue={VISIBILITY_LABEL.workspace} impact={ROOM_DEFAULTS_TAB.impact.visibility} error={err("room_defaults.visibility")} testid="row-room-visibility">
        <select className="select" disabled={lock} value={d.visibility ?? ROOM_DEFAULTS_DEFAULTS.visibility} aria-label={ROOM_DEFAULTS_TAB.visibility} onChange={(e) => put({ visibility: e.target.value as S["RoomVisibility"] })} data-testid="room-default-visibility">
          {(Object.keys(VISIBILITY_LABEL) as S["RoomVisibility"][]).map((k) => <option key={k} value={k}>{VISIBILITY_LABEL[k]}</option>)}
        </select>
      </SettingRow>
      <SettingRow label={ROOM_DEFAULTS_TAB.autonomy} defaultValue={AUTONOMY_TEXT.guided.label} impact={ROOM_DEFAULTS_TAB.impact.autonomy} error={err("room_defaults.autonomy")} testid="row-room-autonomy">
        {/* 매번 확인(supervised)은 v1.1 — 서버가 422 로 막는다. 고를 수 없게 비활성으로 보인다. */}
        <select className="select" disabled={lock} value={d.autonomy ?? ROOM_DEFAULTS_DEFAULTS.autonomy} aria-label={ROOM_DEFAULTS_TAB.autonomy} onChange={(e) => put({ autonomy: e.target.value as S["AutonomyLevel"] })} data-testid="room-default-autonomy">
          <option value="guided">{AUTONOMY_TEXT.guided.label}</option>
          <option value="autonomous">{AUTONOMY_TEXT.autonomous.label}</option>
          <option value="supervised" disabled>{`${AUTONOMY_TEXT.supervised.label} (${AUTONOMY_TEXT.next_version})`}</option>
        </select>
        <span className="small muted-3">{AUTONOMY_TEXT[(d.autonomy ?? "guided") as "guided"].note}</span>
      </SettingRow>
      <SettingRow label={ROOM_DEFAULTS_TAB.budget} defaultValue={ROOM_DEFAULTS_TAB.none} impact={ROOM_DEFAULTS_TAB.impact.budget} error={err("room_defaults.limits.budget_usd")} testid="row-room-budget">
        <input className="input input--num" type="number" min={0} step="0.5" disabled={lock} value={str(limits.budget_usd)} placeholder={ROOM_DEFAULTS_TAB.none} aria-label={ROOM_DEFAULTS_TAB.budget} onChange={(e) => putLimits({ budget_usd: numOrNull(e.target.value) })} data-testid="room-default-budget" />
      </SettingRow>
      <SettingRow label={ROOM_DEFAULTS_TAB.max_works} defaultValue={ROOM_DEFAULTS_DEFAULTS.max_concurrent_works} impact={ROOM_DEFAULTS_TAB.impact.max_works} error={err("room_defaults.limits.max_concurrent_works")} testid="row-room-max-works">
        <input className="input input--num" type="number" min={1} disabled={lock} value={str(limits.max_concurrent_works)} aria-label={ROOM_DEFAULTS_TAB.max_works} onChange={(e) => putLimits({ max_concurrent_works: Number(e.target.value) || undefined })} data-testid="room-default-max-works" />
      </SettingRow>
      <SettingRow label={ROOM_DEFAULTS_TAB.max_lanes} defaultValue={ROOM_DEFAULTS_DEFAULTS.max_parallel_lanes} impact={ROOM_DEFAULTS_TAB.impact.max_lanes} error={err("room_defaults.limits.max_parallel_lanes")} testid="row-room-max-lanes">
        <input className="input input--num" type="number" min={1} disabled={lock} value={str(limits.max_parallel_lanes)} aria-label={ROOM_DEFAULTS_TAB.max_lanes} onChange={(e) => putLimits({ max_parallel_lanes: Number(e.target.value) || undefined })} data-testid="room-default-max-lanes" />
      </SettingRow>
    </>
  );
}

/** 컨텍스트 탭 — 다른 방 읽기 분량 상한(FR-4.5). */
export function RoomReadRows({ draft, set, lock, err }: { draft: WorkspaceSettings; set: Set; lock: boolean; err: (k: string) => string | null }) {
  const r = draft.room_read ?? {};
  const put = (patch: Partial<NonNullable<WorkspaceSettings["room_read"]>>) => set((x) => ({ ...x, room_read: { ...(x.room_read ?? {}), ...patch } }));
  return (
    <>
      <h3 className="srow__group" data-testid="room-read-head">{ROOM_READ.head}</h3>
      <SettingRow label={ROOM_READ.max_rooms} defaultValue={ROOM_READ_DEFAULTS.max_rooms_per_turn} impact={ROOM_READ.impact_rooms} error={err("room_read.max_rooms_per_turn")} testid="row-room-read-rooms">
        <input className="input input--num" type="number" min={1} disabled={lock} value={str(r.max_rooms_per_turn)} aria-label={ROOM_READ.max_rooms} onChange={(e) => put({ max_rooms_per_turn: Number(e.target.value) || undefined })} data-testid="room-read-rooms" />
      </SettingRow>
      <SettingRow label={ROOM_READ.max_tokens} defaultValue={ROOM_READ_DEFAULTS.max_tokens} impact={ROOM_READ.impact_tokens} error={err("room_read.max_tokens")} testid="row-room-read-tokens">
        <input className="input input--num" type="number" min={500} step={500} disabled={lock} value={str(r.max_tokens)} aria-label={ROOM_READ.max_tokens} onChange={(e) => put({ max_tokens: Number(e.target.value) || undefined })} data-testid="room-read-tokens" />
      </SettingRow>
    </>
  );
}

/**
 * 알림 탭의 구독 단위 3층(§4.17 표). 방을 하나 고르면 그 방의 구독 · 열린 미션들의 구독 · 서브 미션 켜기/끄기가 펼쳐진다.
 * 현재 값: 방 `Room.my_subscription` · 미션 `Work.subscription` · 서브 미션 `Lane.my_subscription`(null = 따로 정하지 않음).
 */
export function SubscriptionsSection({ workspaceId }: { workspaceId: string }) {
  const [rooms, setRooms] = useState<RoomListItem[] | null>(null);
  const [roomId, setRoomId] = useState("");
  const [roomLevel, setRoomLevel] = useState<RoomSubscriptionLevel | null>(null);
  const [works, setWorks] = useState<{ id: string; title: string; level: SubscriptionLevel | null }[]>([]);
  const [lanes, setLanes] = useState<{ id: string; label: string; on: boolean | null }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  useEffect(() => {
    void api.get("/workspaces/{workspaceId}/rooms", { path: { workspaceId }, query: { participating: true, limit: 200 } })
      .then((p) => setRooms(p.items as RoomListItem[]))
      .catch((e) => setError(errorMessage(e)));
  }, [workspaceId]);

  const loadRoom = useCallback(async (id: string) => {
    setError(null);
    setSaved(null);
    if (!id) {
      setRoomLevel(null);
      setWorks([]);
      setLanes([]);
      return;
    }
    try {
      const [room, list, lanePage] = await Promise.all([
        api.get("/rooms/{roomId}", { path: { roomId: id } }),
        api.get("/rooms/{roomId}/works", { path: { roomId: id }, query: { limit: 200 } }),
        api.get("/sessions/{sessionId}/lanes", { path: { sessionId: id } }).catch(() => null),
      ]);
      setRoomLevel(room.my_subscription ?? "all");
      const open = ((list.items ?? []) as WorkListItem[]).filter((w) => w.status === "active" || w.status === "paused" || w.status === "draft");
      // 미션의 지금 구독은 목록에 없고 `getWork` 에만 있다 — 열린 미션만(방당 상한 3, §4.17 기본값) 읽는다.
      const detail = await Promise.all(open.map((w) => api.get("/works/{workId}", { path: { workId: w.id } }).catch(() => null)));
      setWorks(open.map((w, i) => ({ id: w.id, title: w.title, level: (detail[i]?.subscription as SubscriptionLevel | undefined) ?? null })));
      const ls = ((Array.isArray(lanePage) ? lanePage : ((lanePage as { items?: Lane[] } | null)?.items ?? [])) as Lane[]).filter((l) => l.status !== "done");
      setLanes(ls.map((l) => ({ id: l.id, label: [l.agent_name ? `@${l.agent_name}` : null, l.work_title ?? l.brief].filter(Boolean).join(" · "), on: l.my_subscription ?? null })));
    } catch (e) {
      setError(errorMessage(e));
    }
  }, []);
  useEffect(() => {
    void loadRoom(roomId);
  }, [roomId, loadRoom]);

  const done = (key: string) => {
    setSaved(key);
    setTimeout(() => setSaved((cur) => (cur === key ? null : cur)), 1500);
  };
  async function putRoom(level: RoomSubscriptionLevel) {
    try {
      await api.put("/rooms/{roomId}/subscription", { path: { roomId }, body: { level } });
      setRoomLevel(level);
      done("room");
    } catch (e) {
      setError(errorMessage(e));
    }
  }
  async function putWork(id: string, level: SubscriptionLevel | null) {
    try {
      await api.put("/works/{workId}/subscription", { path: { workId: id }, body: { level } });
      setWorks((cur) => cur.map((w) => (w.id === id ? { ...w, level } : w)));
      done(`work:${id}`);
    } catch (e) {
      setError(errorMessage(e));
    }
  }
  async function putLane(id: string, enabled: boolean) {
    try {
      await api.put("/lanes/{laneId}/subscription", { path: { laneId: id }, body: { enabled } });
      setLanes((cur) => cur.map((l) => (l.id === id ? { ...l, on: enabled } : l)));
      done(`lane:${id}`);
    } catch (e) {
      setError(errorMessage(e));
    }
  }
  const tick = (key: string) => (saved === key ? <span className="small" style={{ color: "var(--s-done-text)" }} data-testid="sub-saved">{SUBSCRIPTIONS.saved}</span> : null);

  return (
    <div className="subs" data-testid="subscriptions">
      <h3 className="srow__group">{SUBSCRIPTIONS.head}</h3>
      <p className="small muted-3" style={{ marginTop: 0 }}>{SUBSCRIPTIONS.desc}</p>
      {error && <p className="problem" role="alert" data-testid="subs-error">{error}</p>}
      <label className="subs__pick">
        <span>{SUBSCRIPTIONS.room_pick}</span>
        <select className="select" value={roomId} onChange={(e) => setRoomId(e.target.value)} aria-label={SUBSCRIPTIONS.room_pick} data-testid="subs-room">
          <option value="">{SUBSCRIPTIONS.room_pick_none}</option>
          {(rooms ?? []).map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
        </select>
      </label>
      {roomId && roomLevel && (
        <div className="subs__layers">
          <div className="subs__row" data-testid="subs-room-row">
            <b>{SUBSCRIPTIONS.room_level}</b>
            <select className="select" value={roomLevel} onChange={(e) => void putRoom(e.target.value as RoomSubscriptionLevel)} aria-label={SUBSCRIPTIONS.room_level} data-testid="subs-room-level">
              {(Object.keys(ROOM_SUB_LABEL) as RoomSubscriptionLevel[]).map((k) => <option key={k} value={k}>{ROOM_SUB_LABEL[k]}</option>)}
            </select>
            {tick("room")}
          </div>
          <div className="subs__group" data-testid="subs-works">
            <b>{SUBSCRIPTIONS.work_level}</b>
            {works.length === 0 ? <span className="small muted-3">{SUBSCRIPTIONS.no_works}</span> : works.map((w) => (
              <div key={w.id} className="subs__row" data-testid="subs-work-row">
                <span className="subs__name" title={w.title}>{w.title}</span>
                <select className="select" value={w.level ?? ""} onChange={(e) => void putWork(w.id, (e.target.value || null) as SubscriptionLevel | null)} aria-label={`${SUBSCRIPTIONS.work_level} ${w.title}`} data-testid="subs-work-level">
                  <option value="">{SUBSCRIPTIONS.follow_room}</option>
                  {(Object.keys(WORK_SUB_LABEL) as SubscriptionLevel[]).map((k) => <option key={k} value={k}>{WORK_SUB_LABEL[k]}</option>)}
                </select>
                {tick(`work:${w.id}`)}
              </div>
            ))}
          </div>
          <div className="subs__group" data-testid="subs-lanes">
            <b>{SUBSCRIPTIONS.lane_level}</b>
            {lanes.length === 0 ? <span className="small muted-3">{SUBSCRIPTIONS.no_lanes}</span> : lanes.map((l) => (
              <div key={l.id} className="subs__row" data-testid="subs-lane-row" data-on={l.on == null ? "" : String(l.on)}>
                <span className="subs__name" title={l.label}>{l.label}</span>
                <div className="subs__toggle" role="radiogroup" aria-label={`${SUBSCRIPTIONS.lane_level} ${l.label}`}>
                  <button type="button" className={`btn btn--sm${l.on === true ? " btn--primary" : ""}`} aria-pressed={l.on === true} onClick={() => void putLane(l.id, true)} data-testid="subs-lane-on">{SUBSCRIPTIONS.lane_on}</button>
                  <button type="button" className={`btn btn--sm${l.on === false ? " btn--primary" : ""}`} aria-pressed={l.on === false} onClick={() => void putLane(l.id, false)} data-testid="subs-lane-off">{SUBSCRIPTIONS.lane_off}</button>
                </div>
                {l.on == null && <span className="small muted-3">{SUBSCRIPTIONS.lane_follow}</span>}
                {tick(`lane:${l.id}`)}
              </div>
            ))}
          </div>
        </div>
      )}
      <style>{`
        .subs { margin-top: 16px; border-top: 1px solid var(--line); padding-top: 12px; }
        .subs__pick { display: flex; flex-direction: column; gap: 3px; font-size: var(--fs-sub); color: var(--ink-2); max-width: 320px; }
        .subs__layers { display: flex; flex-direction: column; gap: 10px; margin-top: 10px; }
        .subs__group { display: flex; flex-direction: column; gap: 6px; }
        .subs__row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
        .subs__row .select { width: auto; max-width: 100%; }
        .subs__name { min-width: 0; max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .subs__toggle { display: flex; gap: 4px; }
      `}</style>
    </div>
  );
}
