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

export type LaneBoardProps = Omit<LaneCardProps, "lane" | "emptyTurnNote" | "workLabel" | "queuedReason" | "pausedLayer"> & {
  lanes: Lane[];
  emptyHint?: string;
  /** 작업 줄기 id → 빈 턴 문장(FR-7.2) — 이 화면이 그 이벤트를 본 줄기만. */
  emptyTurns?: Record<string, string>;
  /** v0.19 방 화면(T-R2-W2) — 카드마다 미션 라벨·대기 사유·예산 층을 붙인다. 없으면 옛 S7 그대로. */
  decorate?: (lane: Lane) => Pick<LaneCardProps, "workLabel" | "queuedReason" | "pausedLayer">;
  /**
   * v0.19 — 기본으로 접는 묶음(방은 끝나지 않으므로 `done`·`failed` 만 무한히 쌓인다, SCREEN §4.6 SCR-C I). 펼친 묶음은 `open` 이고
   * 부르는 쪽이 방마다 기억한다. 없으면 전부 펼친다(옛 S7).
   */
  fold?: { statuses: ReadonlySet<LaneStatus>; open: ReadonlySet<LaneStatus>; onToggle: (s: LaneStatus) => void; labels: { open: string; close: string } };
};

export function LaneBoard({ lanes, emptyHint, emptyTurns, decorate, fold, ...card }: LaneBoardProps) {
  if (lanes.length === 0) {
    return (
      <div className="board" data-testid="lane-board">
        <p className="small muted-3" data-testid="lane-board-empty">
          {emptyHint ?? "아직 시작한 일이 없습니다 — @로 에이전트를 부르면 작업 줄기가 하나 생깁니다."}
        </p>
      </div>
    );
  }
  return (
    <div className="board" data-testid="lane-board">
      {LANE_GROUP_ORDER.map((status) => {
        const group = lanes.filter((l) => l.status === status);
        if (group.length === 0) return null;
        const foldable = !!fold?.statuses.has(status);
        const shown = !foldable || fold!.open.has(status);
        return (
          <section key={status} className="board__group" data-testid={`lane-group-${status}`} data-status={status} data-folded={foldable && !shown ? "true" : undefined}>
            <h3 className="board__label">
              <span aria-hidden="true">{badgeSpec("lane", status).glyph}</span>
              {badgeSpec("lane", status).label}
              <span className="board__count">{group.length}</span>
              {foldable && (
                <button
                  type="button"
                  className="msg__link board__fold"
                  aria-expanded={shown}
                  onClick={() => fold!.onToggle(status)}
                  data-testid={`lane-group-toggle-${status}`}
                >
                  {shown ? fold!.labels.close : fold!.labels.open} {shown ? "▴" : "▾"}
                </button>
              )}
            </h3>
            {shown && group.map((l) => (
              <LaneCard key={l.id} lane={l} emptyTurnNote={emptyTurns?.[l.id] ?? null} {...card} {...decorate?.(l)} />
            ))}
          </section>
        );
      })}
    </div>
  );
}

export default LaneBoard;
