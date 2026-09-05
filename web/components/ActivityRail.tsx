"use client";
/**
 * 활동 피드 **원본 레일** — task_event 를 시간순 텍스트로(SCREEN §4.5, contracts/task_event.schema.json).
 * P1 은 가공 렌더러(중추 5클래스) 없이 원본 레일만 둔다. 한 줄 = `시각 · class/verb · object → outcome`.
 * 제자리 갱신(superseded)은 최신 판만 보이고, 진행 중(started)은 같은 줄이 갱신된다 — **상태 줄을 쌓지 않는다**.
 * 데몬은 한 툴 호출에 `started` 와 `ok|failed` 를 서로 다른 seq 로 두 번 발행하고 `superseded_by` 를 잇지 않으므로(PR #20),
 * `payload.tool_call_id`(task_event.schema.json — tool·permission 공통 키)로 같은 호출을 한 행으로 접는다(R1).
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

/** openapi `TaskEvent` 에는 `payload` 가 없다(N1 — 두 계약 문서의 불일치). 스키마 문서 쪽 필드를 열어서 읽는다. */
type TaskEventWire = TaskEvent & { payload?: { tool_call_id?: unknown } | null };

/** 같은 툴 호출을 잇는 키 — `payload.tool_call_id`. 없으면 null(접지 않는다). */
export function toolCallKey(e: TaskEvent): string | null {
  const id = (e as TaskEventWire).payload?.tool_call_id;
  if (typeof id !== "string" || !id) return null;
  return `${e.class}:${id}`;
}

/** 화면의 한 행 — 접힌 호출이면 `first` 자리에 `latest` 내용을 그린다. */
export interface RailRow {
  /** 행 key 이자 자리 기준(처음 나타난 이벤트). */
  first: TaskEvent;
  /** 지금 보여줄 판(같은 tool_call_id 중 seq 최대). */
  latest: TaskEvent;
}

/**
 * superseded 된 판은 숨기고, 같은 `tool_call_id` 의 이벤트(started → ok/failed)는 한 행으로 접어 최신 outcome 으로 제자리 갱신.
 * 행의 자리는 처음 이벤트의 seq, 내용은 마지막 이벤트. seq 순.
 */
export function foldEvents(events: TaskEvent[]): RailRow[] {
  const sorted = events.filter((e) => !e.superseded_by).sort((a, b) => a.seq - b.seq);
  const rows: RailRow[] = [];
  const byKey = new Map<string, RailRow>();
  for (const e of sorted) {
    const key = toolCallKey(e);
    const row = key ? byKey.get(key) : undefined;
    if (row) {
      if (e.seq >= row.latest.seq) row.latest = e;
      continue;
    }
    const r: RailRow = { first: e, latest: e };
    rows.push(r);
    if (key) byKey.set(key, r);
  }
  return rows;
}

/** superseded 된 판은 숨기고 접은 뒤 최신 판만 seq 순으로. */
export function latestEvents(events: TaskEvent[]): TaskEvent[] {
  return foldEvents(events).map((r) => r.latest);
}

export function ActivityRail({ events, structured = true, loading, limit = 200 }: ActivityRailProps) {
  const rows = foldEvents(events).slice(-limit);
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
        {rows.map(({ first, latest: e }) => (
          <li
            key={first.id}
            className="rail__row"
            data-outcome={e.outcome ?? ""}
            data-class={e.class}
            data-event-id={e.id}
            data-tool-call-id={(e as TaskEventWire).payload?.tool_call_id as string | undefined}
          >
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
