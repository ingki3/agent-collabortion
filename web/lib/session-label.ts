/** S5 세션 상태 배지 라벨 — paused 는 사유를 함께, active 는 실행 중인 작업 줄기 수(SCREEN §4.3). */
import type { Runtime, Session, SessionListItem } from "@/lib/api/types";

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


/** 컴퓨터가 없을 때(삭제됨·목록에 없음)의 말 — §8.4 "컴퓨터"는 사람이 붙인 이름이다(W-10). */
export const RUNTIME_GONE = "연결 끊긴 컴퓨터";
/** 자동 선택이 아직 고정되지 않았을 때(계약 `runtime_id` M10 — 첫 실행 시 고정). */
export const RUNTIME_AUTO = "자동 선택 — 첫 실행 시 고정";

/**
 * S7 우열 「세션 설정 → 컴퓨터」의 이름(W-10). `session.runtime_id` 로 listRuntimes 결과에서 찾고, 없으면(삭제됨)
 * "연결 끊긴 컴퓨터". 목록을 아직 못 받았으면(`runtimes === null`) 세션에 실려 온 `runtime.name` 이라도 쓰고, 그것도
 * 없으면 null 을 돌려 화면이 자리 표시로 두게 한다 — id 앞 8자는 어느 경우에도 보이지 않는다.
 */
export function runtimeNameOf(
  session: Pick<Session, "runtime_id" | "runtime">,
  runtimes: Pick<Runtime, "id" | "name">[] | null | undefined,
): string | null {
  if (!session.runtime_id) return RUNTIME_AUTO;
  const found = runtimes?.find((r) => r.id === session.runtime_id);
  if (found) return found.name;
  if (runtimes) return session.runtime?.name ?? RUNTIME_GONE;
  return session.runtime?.name ?? null;
}
