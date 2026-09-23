"use client";
/**
 * 우열 (나) 방 전체 칸(SCREEN §4.6 우열) — 미션 선택과 무관하게 항상 같다. **기본으로 접힌다**(SCR-C J) — 접힌 머리줄에도 수를 적는다
 * (「방 전체 · 아티팩트 7 · 결정 6 · 누적 $12.40」). 펼침은 방마다 기억한다(부르는 쪽이 들고 있다).
 *
 * 아티팩트·결정 기록은 **미션별로 묶고 「미션 없음」 묶음은 맨 뒤**, 선택된 미션 묶음은 맨 위 + ✓, 묶을 것이 하나면 머리글 없음, 묶음마다
 * 최근 5건 + 「더 보기 N」(`lib/room-view.ts` `groupByWork`). 방 누적 비용은 **미션 비용의 합이 아니다** — 그 한 줄이 화면 문자열로 들어간다.
 */
import { useState } from "react";
import Link from "next/link";
import "./session-aside.css";
import { Slot } from "./Slot";
import { groupByWork, type Group } from "@/lib/room-view";
import { relativeTime, humanDuration } from "@/lib/time";
import { CREATE_ROOM, ROOM_PANEL, WORK_PANEL } from "@/lib/wording";
import type { Artifact, Decision, Room, WorkListItem } from "@/lib/api/types";

export interface RoomPanelProps {
  room: Room;
  works: WorkListItem[];
  artifacts: Artifact[] | null;
  decisions: Decision[] | null;
  /** 우열 미션 칸에 실린 미션 — 그 묶음을 맨 위로. */
  selectedWorkId: string | null;
  open: boolean;
  onToggle: () => void;
  runtimeName?: string | null;
  defaultDirectorName?: string | null;
  /** 맥락 오간 기록의 수 — 이 화면이 본 `room_read.recorded` 만(목록 op 은 S23 몫). 모르면 null(링크만). */
  reads?: { out: number; in: number } | null;
}

function GroupHead<T>({ g }: { g: Group<T> }) {
  return (
    <h4 className="aside__h room-panel__group" data-testid="room-panel-group" data-selected={g.selected ? "true" : undefined}>
      {g.selected && <span aria-hidden="true">✓ </span>}
      {g.workId ? WORK_PANEL.title + " " + (g.title ?? "") : ROOM_PANEL.no_work_group}
    </h4>
  );
}

function Grouped<T extends { id: string; work_id?: string | null; created_at: string }>(props: {
  items: T[]; works: WorkListItem[]; selected: string | null; row: (it: T) => React.ReactNode; testId: string;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const { groups, headers } = groupByWork(props.items, props.works, props.selected, Number.MAX_SAFE_INTEGER);
  return (
    <>
      {groups.map((g) => {
        const k = g.workId ?? "none";
        const all = g.items;
        const shown = expanded.has(k) ? all : all.slice(0, 5);
        const more = all.length - shown.length;
        return (
          <div key={k} data-testid={props.testId} data-work-id={g.workId ?? undefined}>
            {headers && <GroupHead g={g} />}
            <ul className="aside__list">{shown.map((it) => props.row(it))}</ul>
            {more > 0 && (
              <button type="button" className="aside__link" onClick={() => setExpanded((s) => new Set(s).add(k))} data-testid="room-panel-more">
                <Slot text={ROOM_PANEL.more} n={more} />
              </button>
            )}
          </div>
        );
      })}
    </>
  );
}

export function RoomPanel(props: RoomPanelProps) {
  const { room } = props;
  const arts = props.artifacts ?? [];
  const decs = props.decisions ?? [];
  const limit = room.limits?.budget_usd ?? null;
  const cost = room.cost_usd ?? 0;
  const pct = limit ? Math.round((cost / limit) * 100) : null;
  const iso = room.isolation?.kind ?? "none";
  return (
    <section className="room-panel" data-testid="room-panel" data-open={props.open ? "true" : "false"}>
      <button type="button" className="room-panel__head" aria-expanded={props.open} onClick={props.onToggle} data-testid="room-panel-toggle">
        <b>{ROOM_PANEL.title}</b>
        {" · "}
        <Slot text={ROOM_PANEL.count_artifacts} n={arts.length} />
        {" · "}
        <Slot text={ROOM_PANEL.count_decisions} n={decs.length} />
        {" · "}
        <Slot text={ROOM_PANEL.count_cost} n={cost.toFixed(2)} />
        <span aria-hidden="true">{props.open ? " ▴" : " ▾"}</span>
      </button>
      {props.open && (
        <div className="room-panel__body">
          <section className="aside__sec" data-testid="room-artifacts">
            <h3 className="aside__h">{ROOM_PANEL.artifacts}</h3>
            {arts.length === 0 ? (
              <p className="aside__quiet" data-testid="room-artifacts-empty">{ROOM_PANEL.artifacts_empty}</p>
            ) : (
              <Grouped
                items={arts}
                works={props.works}
                selected={props.selectedWorkId}
                testId="room-artifact-group"
                row={(a) => (
                  <li key={a.id} data-testid="artifact-row" data-artifact-id={a.id}>
                    <span className="aside__name">{a.name}</span>
                    <span className="aside__ver" data-testid="artifact-version"> v{a.version}</span>
                    <span className="aside__quiet"> · {a.type} · {a.submitted_by?.agent_name ?? "—"} · {relativeTime(a.created_at)}</span>
                  </li>
                )}
              />
            )}
          </section>
          <section className="aside__sec" data-testid="room-decisions">
            <h3 className="aside__h">{ROOM_PANEL.decisions}</h3>
            {decs.length === 0 ? (
              <p className="aside__quiet" data-testid="room-decisions-empty">{ROOM_PANEL.decisions_empty}</p>
            ) : (
              <Grouped
                items={decs}
                works={props.works}
                selected={props.selectedWorkId}
                testId="room-decision-group"
                row={(d) => (
                  <li key={d.id} data-testid="decision-row">
                    <span className="aside__name">{d.summary}</span>
                    <span className="aside__quiet"> · {d.source === "hitl" ? ROOM_PANEL.from_hitl : ROOM_PANEL.from_agent} · {relativeTime(d.created_at)}</span>
                  </li>
                )}
              />
            )}
          </section>
          <section className="aside__sec" data-testid="room-cost">
            <h3 className="aside__h">{ROOM_PANEL.room_cost}</h3>
            <p className="aside__cost" data-testid="room-cost-line">
              ${cost.toFixed(2)}
              {limit != null ? ` / $${limit}` : ""}
              {pct != null ? ` (${pct}%)` : ""}
            </p>
            <p className="aside__quiet" data-testid="room-cost-not-sum">{ROOM_PANEL.not_sum}</p>
          </section>
          <section className="aside__sec" data-testid="room-reads">
            <h3 className="aside__h">{ROOM_PANEL.reads}</h3>
            <p className="aside__quiet" data-testid="room-reads-line">
              {props.reads && props.reads.out + props.reads.in === 0 && ROOM_PANEL.reads_empty}
              {props.reads && props.reads.out + props.reads.in > 0 && (
                <>
                  <Slot text={ROOM_PANEL.reads_out} n={props.reads.out} />
                  {" · "}
                  <Slot text={ROOM_PANEL.reads_in} n={props.reads.in} />
                </>
              )}
              {props.reads && " · "}
              <Link href={`/rooms/${room.id}/reads`} className="aside__link" data-testid="room-reads-link">{ROOM_PANEL.reads_link}</Link>
            </p>
          </section>
          <section className="aside__sec" data-testid="room-settings-summary">
            <h3 className="aside__h">{ROOM_PANEL.settings}</h3>
            <dl className="aside__dl">
              <dt>{ROOM_PANEL.row_computer}</dt>
              <dd>{props.runtimeName ?? ROOM_PANEL.computer_first_run}</dd>
              <dt>{ROOM_PANEL.row_isolation}</dt>
              <dd>{CREATE_ROOM.isolation[iso]}</dd>
              <dt>{ROOM_PANEL.row_autonomy}</dt>
              <dd>{ROOM_PANEL.autonomy[room.autonomy]}</dd>
              <dt>{ROOM_PANEL.row_limits}</dt>
              <dd>
                {limit != null ? `$${limit}` : ROOM_PANEL.no_budget}
                {" · "}
                {room.limits?.time_limit ? humanDuration(room.limits.time_limit) : ROOM_PANEL.no_time}
                {" · "}
                <Slot text={ROOM_PANEL.concurrent_works} n={room.limits?.max_concurrent_works ?? 3} />
                {" · "}
                <Slot text={ROOM_PANEL.concurrent_lanes} n={room.limits?.max_parallel_lanes ?? 5} />
              </dd>
              <dt>{ROOM_PANEL.row_director}</dt>
              <dd>{props.defaultDirectorName ?? ROOM_PANEL.director_opener}</dd>
              <dt>{ROOM_PANEL.row_visibility}</dt>
              <dd>{ROOM_PANEL.visibility[room.visibility]}</dd>
            </dl>
            <Link href={`/rooms/${room.id}/settings`} className="aside__link" data-testid="room-settings-link">{ROOM_PANEL.settings_link}</Link>
          </section>
        </div>
      )}
    </section>
  );
}

export default RoomPanel;
