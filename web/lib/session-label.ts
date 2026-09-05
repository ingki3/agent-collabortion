/** S5 세션 상태 배지 라벨 — paused 는 사유를 함께, active 는 running lane 수(SCREEN §4.3). */
import type { SessionListItem } from "@/lib/api/types";

const PAUSE_LABEL: Record<string, string> = {
  budget: "예산", time: "시간", loop: "루프 상한", runtime_offline: "런타임 오프라인", director: "수동",
};

export function sessionBadgeLabel(s: Pick<SessionListItem, "status" | "paused_reason" | "running_lane_count">): string | undefined {
  if (s.status === "paused") return `일시정지 · ${PAUSE_LABEL[s.paused_reason ?? ""] ?? s.paused_reason ?? ""}`;
  if (s.status === "active" && s.running_lane_count > 0) return `진행 중 · ${s.running_lane_count} running`;
  return undefined;
}

