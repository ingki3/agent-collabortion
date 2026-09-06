"use client";
/**
 * lane 카드 펼침 — task 이력(SCREEN §4.5 O3). PRD 의 task 정체성을 볼 수 있는 유일한 자리다.
 * 정보 5종: 순번 · 트리거 · 상태/사유 · 실행(시작·종료·resume 여부) · 비용.
 * 좌열은 268px 라 5열이 들어가지 않으므로 **행을 2줄로** 놓는다(디자인 리뷰 #02 N1):
 *   1줄 순번·상태·비용 / 2줄 트리거·실행
 * 재지시는 새 task(`#2 (재지시)`), 재시도는 같은 task 의 attempt(`#2-2 (재시도)`).
 * resume 실패(콜드 스타트)는 §11 지표가 무너지는 신호이므로 눈에 띄게 표시한다.
 */
import { useEffect, useState } from "react";
import { Badge } from "./Badge";
import { clockTime, durationSince } from "@/lib/time";
import type { Task, TaskAttempt } from "@/lib/api/types";

export interface LaneTaskHistoryProps {
  laneId: string;
  load: (laneId: string) => Promise<Task[]>;
  onOpenTrigger?: (messageId: string) => void;
}

/** 순번 라벨 — 시간순 index 와 `restarted_from_task_id` 로 정한다. */
export function taskLabel(t: Task, index: number, attempt?: number): string {
  const base = `#${index + 1}${t.restarted_from_task_id ? " (재지시)" : ""}`;
  return attempt && attempt > 1 ? `#${index + 1}-${attempt} (재시도)` : base;
}

function AttemptLine({ a }: { a: TaskAttempt }) {
  return (
    <span className="lane-hist__run">
      {a.started_at ? clockTime(a.started_at) : "미시작"}
      {a.finished_at ? `–${clockTime(a.finished_at)}` : ""}
      {" · "}
      {durationSince(a.started_at, a.finished_at)}
      {a.resumed === false && <b className="lane-hist__cold" data-testid="task-cold-start"> · 콜드 스타트</b>}
      {a.resumed === true && <span className="lane-hist__resumed"> · resume</span>}
      {a.outcome ? ` · ${a.outcome}` : ""}
    </span>
  );
}

export function LaneTaskHistory({ laneId, load, onOpenTrigger }: LaneTaskHistoryProps) {
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    load(laneId)
      .then((t) => live && setTasks(t))
      .catch((e) => live && setError(e instanceof Error ? e.message : "불러오지 못했습니다"));
    return () => {
      live = false;
    };
  }, [laneId, load]);

  if (error) return <p className="lane-hist__quiet" data-testid="lane-tasks-error">{error}</p>;
  if (!tasks) return <p className="lane-hist__quiet">불러오는 중…</p>;
  if (tasks.length === 0) return <p className="lane-hist__quiet" data-testid="lane-tasks-empty">아직 task 가 없습니다.</p>;

  return (
    <ol className="lane-hist" data-testid="lane-task-history">
      {tasks.map((t, i) => {
        const attempts = t.attempts ?? [];
        return (
          <li key={t.id} className="lane-hist__item" data-task-id={t.id}>
            <div className="lane-hist__l1">
              <span className="lane-hist__no" data-testid="task-label">{taskLabel(t, i)}</span>
              <Badge kind="task" value={t.status} size="sm" />
              {t.failure_kind && <span className="lane-hist__why">{t.failure_kind}</span>}
              <span className="lane-hist__cost">
                {t.usage ? `$${t.usage.cost_usd.toFixed(2)}${t.usage.estimated ? " 추정" : ""}` : "—"}
              </span>
            </div>
            <div className="lane-hist__l2">
              {t.trigger_message_id ? (
                <button type="button" className="msg__link" onClick={() => onOpenTrigger?.(t.trigger_message_id!)} data-testid="task-trigger">
                  트리거 메시지
                </button>
              ) : (
                <span className="lane-hist__quiet">트리거 없음</span>
              )}
              {attempts.length <= 1 ? (
                <AttemptLine a={attempts[0] ?? { attempt: 1, started_at: t.started_at ?? null, finished_at: t.finished_at ?? null, resumed: t.resumed ?? null, outcome: t.failure_kind ?? t.status }} />
              ) : null}
            </div>
            {attempts.length > 1 &&
              attempts.map((a) => (
                <div key={a.attempt} className="lane-hist__l2 lane-hist__l2--attempt" data-testid="task-attempt">
                  <span className="lane-hist__no">{taskLabel(t, i, a.attempt)}</span>
                  <AttemptLine a={a} />
                </div>
              ))}
          </li>
        );
      })}
    </ol>
  );
}

export default LaneTaskHistory;
