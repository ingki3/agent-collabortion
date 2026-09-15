// 조합표에 없는 값은 타입 오류여야 한다 — tsc 로만 검사하는 파일 (런타임 import 없음).
import type { BadgeProps } from "./Badge";

const ok: BadgeProps[] = [
  { kind: "lane", value: "paused" },
  { kind: "inbox", value: "info", tone: "done" },
];
// @ts-expect-error task 에는 blocked 가 없다 (lane 전용)
const badValue: BadgeProps = { kind: "task", value: "blocked" };
// @ts-expect-error runtime 은 kind 가 아니다
const badKind: BadgeProps = { kind: "runtime", value: "online" };
export { ok, badValue, badKind };
