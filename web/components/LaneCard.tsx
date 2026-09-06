"use client";
/**
 * Lane Card(COMPONENTS §2.1 `x1YCq` · SCREEN §4.5 좌열) — lane 보드의 카드. **7상태를 하나의 컴포넌트로.**
 *
 * lane 보드는 장식이 아니라 유일한 제어판이다(SCREEN §1 원칙 2) — 어떤 메시지도 진행 중인 턴을 취소하지 않고,
 * 취소는 여기 버튼이다. 버튼 집합은 상태가 아니라 **서버가 준 `actions`** 로 정한다(권한은 서버 판정).
 * 권한이 없으면 숨기지 않고 비활성 + 사유 툴팁(SCREEN §1 원칙 4).
 *
 * 상태별 조합 (COMPONENTS §2.1 표):
 *   queued 대기 순번 · running 경과+현재 동작+중단/재지시 · waiting_human 무엇을 기다리는지+Inbox
 *   blocked 질문 배지+요약+질문 카드로 · paused 초과 금액+계속 진행 승인 · done 산출물 · failed 분류+다시 지시
 *
 * **`paused`는 `failed`가 아니다**(SCREEN §4.5 C2) — 예산 초과는 오류가 아니라 정책이고 승인하면 같은 경로로 이어간다.
 * **일반 "재시도" 버튼을 두지 않는다**(m6) — 사람은 항상 "다시 지시"로 맥락을 더한다.
 */
import { useState } from "react";
import "./lane-card.css";
import { Badge } from "./Badge";
import { LaneTaskHistory } from "./LaneTaskHistory";
import { durationSince, relativeTime } from "@/lib/time";
import type { Lane, Task } from "@/lib/api/types";

export type LaneAction = NonNullable<Lane["actions"]>[number];

export interface LaneCardProps {
  lane: Lane;
  /** 호출자가 쓸 수 없는 동작의 사유(툴팁) — 없으면 "Director·deputy 만". */
  disabledReason?: string;
  onRestart?: (lane: Lane) => void;
  onCancel?: (lane: Lane) => void;
  onOpenQuestion?: (messageId: string) => void;
  onRespondHitl?: (lane: Lane) => void;
  onApproveBudget?: (lane: Lane) => void;
  onSelect?: (lane: Lane) => void;
  /** task 이력(O3) 로더 — 펼칠 때 호출한다. */
  loadTasks?: (laneId: string) => Promise<Task[]>;
  selected?: boolean;
  now?: number;
}

/** 상태별 "부가 정보" 한 줄(COMPONENTS §2.1 부가 열). 없으면 null — 자리만 차지하지 않는다. */
export function laneNote(lane: Lane): string | null {
  switch (lane.status) {
    case "queued":
      return lane.queue_position != null ? `대기 순번 ${lane.queue_position}` : "대기 중";
    case "running":
      return lane.current_activity ?? "실행 중 — 취소는 즉시 가능";
    case "blocked":
      return `? ${lane.waiting_for ? `@${lane.waiting_for}의 답을 기다림` : "답을 기다림"}${lane.blocked_note ? ` — ${lane.blocked_note}` : ""}`;
    case "paused":
      return lane.paused_over_usd != null
        ? `예산 초과로 대기 중 — $${lane.paused_over_usd.toFixed(2)} 초과 · 계속 진행 승인 필요`
        : "계속 진행 승인 필요";
    case "waiting_human":
      return `⏳ ${lane.waiting_for ?? "Director 승인 대기"} · Inbox`;
    case "done":
      return lane.brief ?? null;
    case "failed":
      return lane.failure_kind ? `실패 분류 ${lane.failure_kind}${lane.failure_kind === "cancelled" ? " — 사람이 중단함" : " · 자동 재시도 소진"}` : "실패";
    default:
      return null;
  }
}

export function LaneCard(props: LaneCardProps) {
  const { lane } = props;
  const [openTasks, setOpenTasks] = useState(false);
  const actions = new Set<LaneAction>(lane.actions ?? []);
  const note = laneNote(lane);
  const reason = props.disabledReason ?? "Director·deputy 만 할 수 있습니다";

  const btn = (key: LaneAction, label: string, onClick: (() => void) | undefined, primary = false) => {
    const allowed = actions.has(key);
    return (
      <button
        key={key}
        type="button"
        className={`btn btn--sm${primary ? " btn--primary" : ""}`}
        disabled={!allowed || !onClick}
        title={!allowed ? reason : undefined}
        onClick={onClick}
        data-testid={`lane-action-${key}`}
      >
        {label}
      </button>
    );
  };

  const buttons: React.ReactNode[] = [];
  if (lane.status === "running") {
    buttons.push(btn("restart", "중단하고 다시 지시", props.onRestart && (() => props.onRestart!(lane))));
    buttons.push(btn("cancel", "중단", props.onCancel && (() => props.onCancel!(lane))));
  } else if (lane.status === "blocked") {
    buttons.push(
      <button
        key="open_question"
        type="button"
        className="btn btn--sm"
        disabled={!lane.blocked_message_id || !props.onOpenQuestion}
        onClick={() => lane.blocked_message_id && props.onOpenQuestion?.(lane.blocked_message_id)}
        data-testid="lane-action-open_question"
      >
        질문 카드로 이동
      </button>,
    );
  } else if (lane.status === "waiting_human") {
    buttons.push(btn("respond_hitl", "응답하러 가기", props.onRespondHitl && (() => props.onRespondHitl!(lane)), true));
  } else if (lane.status === "paused") {
    buttons.push(btn("approve_budget", "계속 진행 승인", props.onApproveBudget && (() => props.onApproveBudget!(lane)), true));
  } else if (lane.status === "failed") {
    // 실패 분류별로 사람이 할 일이 다르다(SCREEN §4.5 m6). runtime_offline 은 재바인딩이라 여기 버튼이 없다.
    if (lane.failure_kind !== "runtime_offline") {
      buttons.push(btn("restart", "다시 지시", props.onRestart && (() => props.onRestart!(lane))));
    }
  } else if (lane.status === "queued") {
    buttons.push(btn("cancel", "중단", props.onCancel && (() => props.onCancel!(lane))));
  }

  return (
    <article
      className={`lane${props.selected ? " lane--selected" : ""}`}
      data-testid="lane-card"
      data-lane-id={lane.id}
      data-status={lane.status}
      onClick={props.onSelect ? () => props.onSelect!(lane) : undefined}
    >
      <div className="lane__head">
        <span className="lane__agent" data-testid="lane-agent">@{lane.agent_name ?? "agent"}</span>
        <Badge kind="lane" value={lane.status} size="sm" />
      </div>
      {lane.brief && lane.status !== "done" && (
        <div className="lane__brief" data-testid="lane-brief">{lane.brief}</div>
      )}
      {note && (
        <div className="lane__note" data-testid="lane-note" data-status={lane.status}>{note}</div>
      )}
      <div className="lane__meta">
        <span data-testid="lane-elapsed">{durationSince(lane.created_at, lane.finished_at, props.now)}</span>
        {lane.reentry_count > 0 && (
          <span className="lane__reentry" data-testid="lane-reentry" title="이 lane 이 done·blocked 에서 다시 열린 횟수(FR-6.2)">
            재진입 {lane.reentry_count}회
          </span>
        )}
        {lane.workdir_ref && <span className="lane__workdir" data-testid="lane-workdir">{lane.workdir_ref}</span>}
        {lane.has_runtime_session === false && lane.status === "running" && (
          <span className="lane__cold" data-testid="lane-cold-start" title="런타임 세션이 없어 콜드 스타트합니다(재개 성공률 지표)">콜드 스타트</span>
        )}
        {lane.finished_at && <span className="lane__quiet">{relativeTime(lane.finished_at, props.now)}</span>}
      </div>
      {buttons.length > 0 && (
        <div className="lane__actions" data-testid="lane-actions" onClick={(e) => e.stopPropagation()}>
          {buttons}
        </div>
      )}
      {props.loadTasks && (
        <div onClick={(e) => e.stopPropagation()}>
          <button
            type="button"
            className="msg__link lane__more"
            aria-expanded={openTasks}
            onClick={() => setOpenTasks((v) => !v)}
            data-testid="lane-tasks-toggle"
          >
            {openTasks ? "task 이력 접기" : "task 이력"}
          </button>
          {openTasks && <LaneTaskHistory laneId={lane.id} load={props.loadTasks} />}
        </div>
      )}
    </article>
  );
}

export default LaneCard;
