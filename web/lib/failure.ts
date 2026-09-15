/**
 * `FailureKind` 9종을 사람의 말로(COMPONENTS §8.4).
 *
 * 원문 enum(`quota`·`stall`·`config`…)은 화면에 그대로 나오면 **사람이 다음에 할 일을 말해 주지 않는다** —
 * "실패 분류 quota" 를 읽고 무엇을 해야 하는지 아는 사람은 이 코드를 쓴 사람뿐이다.
 * lane 카드(부가 줄)와 이전 작업 이력(실패 사유 칸)이 같은 표를 쓴다 — 두 자리가 다른 말을 하면 안 된다.
 */
import type { FailureKind } from "@/lib/api/types";

export const FAILURE_LABEL: Record<FailureKind, string> = {
  auth: "로그인이 풀렸습니다",
  quota: "사용량 한도에 걸렸습니다",
  config: "설정이 잘못됐습니다",
  network: "네트워크가 끊겼습니다",
  runtime_offline: "컴퓨터가 오프라인입니다",
  stall: "응답이 멈췄습니다",
  timeout: "시간이 초과됐습니다",
  cancelled: "사람이 중단했습니다",
  other: "알 수 없는 이유로 실패했습니다",
};

/** 표에 없는 값이 와도 화면이 비지 않게 — 서버가 새 분류를 늘리면 원문이라도 보인다. */
export function failureLabel(kind: FailureKind | null | undefined): string | null {
  if (!kind) return null;
  return FAILURE_LABEL[kind] ?? kind;
}
