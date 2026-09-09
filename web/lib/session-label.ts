/** S5 세션 상태 배지 라벨 — paused 는 사유를 함께, active 는 실행 중인 작업 줄기 수(SCREEN §4.3). */
import type { SessionListItem } from "@/lib/api/types";

/** 배지 안에 들어가는 짧은 사유(자리가 좁다). */
const PAUSE_LABEL: Record<string, string> = {
  budget: "예산", time: "시간", loop: "주고받기 상한", runtime_offline: "컴퓨터 연결 끊김", director: "수동",
};

/** 문장 안에 들어가는 사유 — 인박스 카드가 쓴다(계약 `PauseReason` 5종). */
export const PAUSE_REASON_LABEL: Record<string, string> = {
  budget: "예산 상한을 넘어 멈췄습니다",
  time: "시간 상한에 닿아 멈췄습니다",
  loop: "에이전트끼리 주고받기가 상한에 닿아 멈췄습니다",
  runtime_offline: "컴퓨터가 오프라인이라 멈췄습니다",
  director: "Director 가 멈췄습니다",
};

export function sessionBadgeLabel(s: Pick<SessionListItem, "status" | "paused_reason" | "running_lane_count">): string | undefined {
  if (s.status === "paused") return `일시정지 · ${PAUSE_LABEL[s.paused_reason ?? ""] ?? s.paused_reason ?? ""}`;
  if (s.status === "active" && s.running_lane_count > 0) return `진행 중 · ${s.running_lane_count}개 실행 중`;
  return undefined;
}

