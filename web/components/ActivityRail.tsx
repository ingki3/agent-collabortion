"use client";
/**
 * 활동 피드 **원본 레일** — task_event 를 시간순 텍스트로(SCREEN §4.5, contracts/task_event.schema.json).
 * P1 은 가공 렌더러(중추 5클래스) 없이 원본 레일만 둔다. 한 줄 = `시각 · class/verb · object → outcome`.
 * 제자리 갱신(superseded)은 최신 판만 보이고, 진행 중(started)은 같은 줄이 갱신된다.
 */
import "./activity-rail.css";
import { clockTime } from "@/lib/time";
import type { TaskEvent } from "@/lib/api/types";

export interface ActivityRailProps {
  events: TaskEvent[];
  /** 런타임이 구조화 이벤트를 주는가. false 면 강등 안내(SCREEN §1 원칙 5). */
  structured?: boolean;
  loading?: boolean;
  /** 최대 줄 수(오래된 것부터 생략). */
  limit?: number;
}

export function eventLine(e: TaskEvent): string {
  const obj =
    typeof e.object_ref === "string"
      ? e.object_ref
      : e.object_ref && typeof e.object_ref === "object"
        ? JSON.stringify(e.object_ref)
        : "";
  const head = `${e.class}${e.verb ? "/" + e.verb : ""}`;
  const parts = [head, obj].filter(Boolean).join(" ");
  return e.outcome ? `${parts} → ${e.outcome}` : parts;
}

/** superseded 된 판은 숨기고 seq 순으로. */
export function latestEvents(events: TaskEvent[]): TaskEvent[] {
  return events.filter((e) => !e.superseded_by).sort((a, b) => a.seq - b.seq);
}

export function ActivityRail({ events, structured = true, loading, limit = 200 }: ActivityRailProps) {
  const rows = latestEvents(events).slice(-limit);
  return (
    <div className="rail" data-testid="activity-rail">
      {!structured && (
        <div className="rail__note" data-testid="rail-unstructured">
          이 런타임은 툴 단위 로그를 제공하지 않습니다 — 메시지와 원본 출력만 표시합니다.
        </div>
      )}
      {loading && rows.length === 0 && <div className="rail__note">불러오는 중…</div>}
      {!loading && rows.length === 0 && <div className="rail__note">대기 중…</div>}
      <ol className="rail__list">
        {rows.map((e) => (
          <li key={e.id} className="rail__row" data-outcome={e.outcome ?? ""} data-class={e.class}>
            <span className="rail__time">{clockTime(e.created_at)}</span>
            <span className="rail__text">
              {e.sentence ?? eventLine(e)}
              {e.masked && <span className="rail__masked"> · 마스킹됨</span>}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export default ActivityRail;
