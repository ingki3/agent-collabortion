"use client";
/**
 * Lane Card(COMPONENTS §2.1 `x1YCq` · SCREEN §4.5 좌열) — lane 보드의 카드. **7상태를 하나의 컴포넌트로.**
 *
 * lane 보드는 장식이 아니라 유일한 제어판이다(SCREEN §1 원칙 2) — 어떤 메시지도 진행 중인 턴을 취소하지 않고,
 * 취소는 여기 버튼이다. 버튼 집합은 상태가 아니라 **서버가 준 `actions`** 로 정한다(권한은 서버 판정). done 이어도 `actions` 에
 * cancel 이 있으면(현재 할 일이 아직 running — K-16, 계약 v0.1.6) 「중단」을 낸다.
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
import { renderInline } from "@/lib/markdown";
import "./lane-card.css";
import { Badge } from "./Badge";
import { LaneTaskHistory } from "./LaneTaskHistory";
import { durationSince, relativeTime } from "@/lib/time";
import { failureLabel } from "@/lib/failure";
import { EMPTY_TURN } from "@/lib/wording";
import type { Lane, Task } from "@/lib/api/types";

export type LaneAction = NonNullable<Lane["actions"]>[number];

export interface LaneCardProps {
  lane: Lane;
  /** 호출자가 쓸 수 없는 동작의 사유(툴팁) — 없으면 "Director·deputy 만"(§8.4 역할명 예외). */
  disabledReason?: string;
  onRestart?: (lane: Lane) => void;
  onCancel?: (lane: Lane) => void;
  onOpenQuestion?: (messageId: string) => void;
  onRespondHitl?: (lane: Lane) => void;
  onApproveBudget?: (lane: Lane) => void;
  onSelect?: (lane: Lane) => void;
  /** 이전 작업 이력(O3) 로더 — 펼칠 때 호출한다. */
  loadTasks?: (laneId: string) => Promise<Task[]>;
  /** 이력의 할 일 하나의 활동 피드(T-W16) — 이력 행의 「활동」 토글이 그린다. */
  renderTaskActivity?: (taskId: string) => React.ReactNode;
  selected?: boolean;
  now?: number;
  /**
   * 빈 턴(FR-7.2 v0.17) — 이 작업 줄기의 현재 할 일이 아무것도 하지 않고 턴을 끝냈다는 것을 **이 화면이 본** 경우 그 문장
   * (`payload.args.note` 그대로). 계약 `Lane` 에는 칸이 없어 정본은 활동 피드의 정보 카드이고, 카드 note 는 SSE 로 본 세션 안에서만
   * 보인다(Lead 결정 2026-09-15 — 새로고침 뒤에는 활동 보기를 열면 다시 보인다).
   */
  emptyTurnNote?: string | null;
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
      return `⏳ ${lane.waiting_for ?? "Director 승인 대기"} · 받은 요청`;
    case "done":
      return lane.brief ?? null;
    case "failed":
      return lane.failure_kind
        ? `${failureLabel(lane.failure_kind)}${lane.failure_kind === "cancelled" ? "" : " · 자동 재시도 소진"}`
        : "실패";
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
  } else if (lane.status === "done" && actions.has("cancel")) {
    // K-16(계약 v0.1.6 cancelLane): `colab status set done` 뒤에도 그 턴의 프로세스가 아직 돌면 서버가 현재 할 일로 판정해
    // `actions` 에 cancel 을 싣는다 — 카드는 서버가 준 목록 그대로 「중단」을 낸다(산출물은 이미 제출됐고 실행만 멈춘다).
    buttons.push(btn("cancel", "중단", props.onCancel && (() => props.onCancel!(lane))));
  }
  const doneStillRunning = lane.status === "done" && actions.has("cancel");

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
        <div className="lane__brief" data-testid="lane-brief">{renderInline(lane.brief)}</div>
      )}
      {note && (
        <div className="lane__note" data-testid="lane-note" data-status={lane.status}>{note}</div>
      )}
      {doneStillRunning && (
        <div className="lane__note lane__note--info" data-testid="lane-done-running">제출은 끝났지만 실행이 아직 돌고 있습니다 — 「중단」은 그 실행만 멈춥니다</div>
      )}
      {props.emptyTurnNote && (
        <div className="lane__note lane__note--info" data-testid="lane-empty-turn" title={EMPTY_TURN.kind}>
          <span aria-hidden="true">ⓘ </span>{props.emptyTurnNote}
        </div>
      )}
      <div className="lane__meta">
        <span data-testid="lane-elapsed">{durationSince(lane.created_at, lane.finished_at, props.now)}</span>
        {lane.reentry_count > 0 && (
          <span className="lane__reentry" data-testid="lane-reentry" title="이 작업 줄기가 끝났다가 다시 열린 횟수">
            재진입 {lane.reentry_count}회
          </span>
        )}
        {lane.workdir_ref && <span className="lane__workdir" data-testid="lane-workdir">{lane.workdir_ref}</span>}
        {lane.has_runtime_session === false && lane.status === "running" && (
          <span className="lane__cold" data-testid="lane-cold-start" title="이어서 실행할 수 없어 처음부터 시작합니다">콜드 스타트</span>
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
            {openTasks ? "이전 작업 접기" : "이전 작업"}
          </button>
          {openTasks && <LaneTaskHistory laneId={lane.id} load={props.loadTasks} renderActivity={props.renderTaskActivity} />}
        </div>
      )}
    </article>
  );
}

export default LaneCard;
