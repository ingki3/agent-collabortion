"use client";
/**
 * lane 보드(SCREEN §4.5 좌열) — lane 카드를 **상태별로 묶어** 보여준다.
 * 순서는 "사람이 할 일"이 위로 오게 둔다: blocked·waiting_human·paused → running → queued → failed → done.
 * 빈 보드도 렌더한다(§7 — 침묵도 정보다).
 */
import "./lane-card.css";
import { LaneCard, type LaneCardProps } from "./LaneCard";
import { badgeSpec } from "./badge-map";
import type { Lane, LaneStatus } from "@/lib/api/types";

/** 사람이 할 일 우선. COMPONENTS §2.1 의 7상태 전부가 여기 있다. */
export const LANE_GROUP_ORDER: readonly LaneStatus[] = [
  "blocked",
  "waiting_human",
  "paused",
  "running",
  "queued",
  "failed",
  "done",
];

export type LaneBoardProps = Omit<LaneCardProps, "lane"> & {
  lanes: Lane[];
  emptyHint?: string;
};

export function LaneBoard({ lanes, emptyHint, ...card }: LaneBoardProps) {
  if (lanes.length === 0) {
    return (
      <div className="board" data-testid="lane-board">
        <p className="small muted-3" data-testid="lane-board-empty">
          {emptyHint ?? "아직 lane 이 없습니다 — @로 에이전트를 부르면 lane 이 생깁니다."}
        </p>
      </div>
    );
  }
  return (
    <div className="board" data-testid="lane-board">
      {LANE_GROUP_ORDER.map((status) => {
        const group = lanes.filter((l) => l.status === status);
        if (group.length === 0) return null;
        return (
          <section key={status} className="board__group" data-testid={`lane-group-${status}`} data-status={status}>
            <h3 className="board__label">
              <span aria-hidden="true">{badgeSpec("lane", status).glyph}</span>
              {badgeSpec("lane", status).label}
              <span className="board__count">{group.length}</span>
            </h3>
            {group.map((l) => (
              <LaneCard key={l.id} lane={l} {...card} />
            ))}
          </section>
        );
      })}
    </div>
  );
}

export default LaneBoard;
