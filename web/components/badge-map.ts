/**
 * Badge 조합표 — kind × value → {glyph, tone(색 토큰), variant, label}.
 *
 * 이 테이블이 곧 COMPONENTS.md §2.1(lane)·§2.4(inbox) 조합표이자 SCREEN.md §5 상태 배지 규칙이다.
 * 여기 없는 kind/value 조합은 TypeScript 타입 오류다.
 *
 * 글리프 ⏳ ⏸ 에는 U+FE0E(텍스트 표현)를 붙여 solid 배지에서 컬러 이모지로 그려지지 않게 한다.
 *
 * 규칙 (SCREEN §5, design/DESIGN_REVIEW_FOLLOW_UP_01.md §1.1):
 * - 글리프가 1차 신호, 색은 보조.
 *   running/active/working ● · waiting_human ⏳ · blocked ? · paused ⏸ · done/completed ✓
 *   failed/offline ✕ · queued/idle ○ · disabled ⊘ · cancelled – · 인박스 ! ▲ i · agent error ⚠
 * - paused 는 테두리만(soft), failed·offline·error 는 채움(solid).
 * - idle/queued/draft 는 회색 ○ — done 의 초록이 아니다(N5).
 * - 인박스 심각도 배지의 색은 심각도가 아니라 항목의 원인 상태를 따른다(리뷰 #03 N4).
 *   여기 적힌 inbox tone 은 기본값일 뿐이고 호출부가 Badge 의 `tone` 으로 덮어쓴다.
 */

export type BadgeKind = "lane" | "task" | "session" | "agent" | "inbox";

/** 색 토큰 계열. neutral = --ink-3 / --ink-2 (상태색 아님). */
export type Tone = "run" | "wait" | "block" | "pause" | "done" | "fail" | "neutral";

export type Variant = "soft" | "solid";

export interface BadgeSpec {
  readonly glyph: string;
  readonly tone: Tone;
  readonly variant: Variant;
  readonly label: string;
}

const spec = (glyph: string, tone: Tone, label: string, variant: Variant = "soft"): BadgeSpec => ({
  glyph,
  tone,
  variant,
  label,
});

export const BADGE_MAP = {
  /** PRD FR-6.2 lane 상태 7종 — COMPONENTS §2.1 */
  lane: {
    queued: spec("○", "neutral", "대기"),
    running: spec("●", "run", "실행 중"),
    waiting_human: spec("⏳\uFE0E", "wait", "승인 대기"),
    blocked: spec("?", "block", "질문 대기"),
    paused: spec("⏸\uFE0E", "pause", "일시정지"),
    done: spec("✓", "done", "완료"),
    failed: spec("✕", "fail", "실패", "solid"),
  },
  /** PRD §7 task 상태 10종 */
  task: {
    deferred: spec("○", "neutral", "보류"),
    queued: spec("○", "neutral", "대기"),
    dispatched: spec("●", "run", "배정됨"),
    preparing: spec("●", "run", "준비 중"),
    running: spec("●", "run", "실행 중"),
    waiting_human: spec("⏳\uFE0E", "wait", "승인 대기"),
    paused: spec("⏸\uFE0E", "pause", "일시정지"),
    completed: spec("✓", "done", "완료"),
    failed: spec("✕", "fail", "실패", "solid"),
    cancelled: spec("–", "neutral", "취소됨"),
  },
  /** PRD FR-2.3 세션 상태 6종 — SCREEN §4.3 표 */
  session: {
    draft: spec("○", "neutral", "초안"),
    active: spec("●", "run", "진행 중"),
    paused: spec("⏸\uFE0E", "pause", "일시정지"),
    completing: spec("●", "run", "마무리 중"),
    completed: spec("✓", "done", "완료"),
    cancelled: spec("–", "neutral", "취소됨"),
  },
  /** PRD FR-1.3 에이전트 상태 6종 — SCREEN §4.5 좌열 */
  agent: {
    idle: spec("○", "neutral", "대기"),
    working: spec("●", "run", "작업 중"),
    waiting_human: spec("⏳\uFE0E", "wait", "승인 대기"),
    error: spec("⚠", "fail", "오류", "solid"),
    offline: spec("✕", "fail", "오프라인", "solid"),
    disabled: spec("⊘", "neutral", "비활성"),
  },
  /** SCREEN §4.6 인박스 심각도 3종 — tone 은 기본값(COMPONENTS §2.4 첫 행), 원인 상태로 덮어쓴다 */
  inbox: {
    action_required: spec("!", "wait", "조치 필요"),
    attention: spec("▲", "pause", "주의"),
    info: spec("i", "run", "알림"),
  },
} as const satisfies Record<BadgeKind, Record<string, BadgeSpec>>;

export type BadgeMap = typeof BADGE_MAP;
export type BadgeValue<K extends BadgeKind> = keyof BadgeMap[K] & string;

export const BADGE_KINDS = Object.keys(BADGE_MAP) as readonly BadgeKind[];

export function badgeValues<K extends BadgeKind>(kind: K): readonly BadgeValue<K>[] {
  return Object.keys(BADGE_MAP[kind]) as BadgeValue<K>[];
}

export function badgeSpec<K extends BadgeKind>(kind: K, value: BadgeValue<K>): BadgeSpec {
  return BADGE_MAP[kind][value] as BadgeSpec;
}

/** 전체 항목 수 — /dev/badges 의 셀 수는 이 값 × 2(variant). */
export const BADGE_ENTRY_COUNT = BADGE_KINDS.reduce((n, k) => n + badgeValues(k).length, 0);

/** tone → CSS 변수 이름. neutral 은 상태색 대신 ink 계열. */
export function toneTokens(tone: Tone): { readonly solid: string; readonly text: string } {
  return tone === "neutral"
    ? { solid: "--ink-3", text: "--ink-2" }
    : { solid: `--s-${tone}`, text: `--s-${tone}-text` };
}
