/**
 * S14 설정(SCREEN §4.10) — 화면이 아닌 **순수 규칙**만: 탭 표, 기본값(PRD §7 · openapi default), 「바꿨을 때의 영향」 한 줄,
 * 부분 갱신 payload(바꾼 칸만), ISO 8601 duration ↔ 일수, 대시보드(PRD §11) 표 계산.
 *
 * 화면(`app/(app)/settings/page.tsx`)은 이 표를 그리기만 한다. 테스트는 여기(payload 모양·판정)와 화면(탭·권한)을 따로 잰다.
 */
import { ROOM_DEFAULTS_TAB, ROOM_READ } from "@/lib/screens-v19";
import { AUDIT } from "@/lib/audit";
import type { IsolationKind, MemberRole, Metric, ObservationKey, ObservationRow, RuntimeKind, WorkspaceSettings, WorkspaceSettingsUpdate } from "@/lib/api/types";

// ── 탭 ──────────────────────────────────────────────────────────────────────
export type SettingsTab = "members" | "runtime" | "rooms" | "budget" | "loop" | "context" | "workdir" | "security" | "notifications" | "dashboard" | "audit";

/**
 * SCREEN §4.10 표의 8탭 + 9번째 「대시보드」(PRD §11, G9). `who` 는 그 표의 권한 열 —
 * `admin` = owner·admin 만 저장, `owner` = owner 만, `personal` = 개인(누구나), `read` = 읽기만(멤버 전원).
 * 이름은 §8.4 의 말이다 — 표의 "런타임 정책" 은 "컴퓨터 정책"(런타임 → 컴퓨터, 산문까지 전부), "Workdir" 은 "작업 폴더".
 */
export const SETTINGS_TABS: readonly { key: SettingsTab; label: string; who: "admin" | "owner" | "personal" | "read"; desc: string }[] = [
  { key: "members", label: "멤버", who: "admin", desc: "누가 이 워크스페이스에 있고 무엇을 할 수 있는지" },
  { key: "runtime", label: "컴퓨터 정책", who: "admin", desc: "컴퓨터 한 대가 동시에 맡는 할 일의 상한" },
  // v0.19(§4.17, T-R2-W4a) — 방 만들기가 이름 한 칸이 되면서 이 값이 모든 방의 실제 값이 된다. 문구는 lib/screens-v19.ts.
  { key: "rooms", label: ROOM_DEFAULTS_TAB.label, who: "admin", desc: ROOM_DEFAULTS_TAB.desc },
  { key: "budget", label: "예산", who: "admin", desc: "새 미션·할 일에 기본으로 붙는 비용 상한" },
  { key: "loop", label: "루프 상한", who: "admin", desc: "에이전트끼리 주고받기가 끝없이 돌지 않게 하는 상한" },
  // v0.19: 「컨텍스트 재사용 상한」(FR-4.4)은 버렸다(§12.1-7) — 이 탭은 다른 방 읽기 상한(FR-4.5)만 남는다.
  { key: "context", label: "컨텍스트", who: "admin", desc: ROOM_READ.tab_desc },
  { key: "workdir", label: "작업 폴더", who: "admin", desc: "기본 격리 방식과 작업 폴더의 보존·용량·연결 끊김 유예" },
  { key: "security", label: "보안", who: "owner", desc: "활동 기록에 무엇을 남길지" },
  { key: "notifications", label: "알림", who: "personal", desc: "내게 오는 이메일·푸시와 미션 구독 기본값" },
  { key: "dashboard", label: "대시보드", who: "read", desc: "팀이 목표 지표(10개)에 닿았는지 · 목표 없는 관찰 5행" },
  // v0.19 M7 — S15 활동 로그는 설정 안의 탭으로(최상위 내비가 아니다, §4.18). 누르면 `/settings/audit` 로 간다.
  { key: "audit", label: AUDIT.tab, who: "admin", desc: AUDIT.tab_desc },
];
export const DEFAULT_TAB: SettingsTab = "members";
export const isSettingsTab = (v: string | null | undefined): v is SettingsTab => SETTINGS_TABS.some((t) => t.key === v);

/** 이 역할이 그 탭을 **저장**할 수 있는가. 못 하면 사유(DisabledHint 문구)를 돌려준다. */
export function saveRight(tab: SettingsTab, role: MemberRole | null | undefined): { ok: true } | { ok: false; reason: string } {
  const who = SETTINGS_TABS.find((t) => t.key === tab)?.who ?? "admin";
  if (who === "personal") return { ok: true };
  if (who === "read") return { ok: false, reason: "지표는 읽기만 합니다 — 서버가 세고 화면은 보여 줍니다" };
  if (who === "owner") return role === "owner" ? { ok: true } : { ok: false, reason: "소유자만 바꿀 수 있습니다" };
  return role === "owner" || role === "admin" ? { ok: true } : { ok: false, reason: "소유자·관리자만 바꿀 수 있습니다 — 멤버는 읽기만 합니다" };
}

// ── 기본값(PRD §7 · openapi default) ──────────────────────────────────────────
/** 화면의 "기본값 표시"(U14) 와 목 시드(`lib/mock/store.ts defaultSettings`)가 같은 값이어야 한다 — 테스트가 대조한다. */
export const SETTINGS_DEFAULTS = {
  loop_limits: { max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 5 },
  context_reuse: { max_summary_tokens: 2000, include_artifacts: "links" as const },
  runtime_policy: { max_concurrent_tasks: 10 },
  default_isolation: "none" as IsolationKind,
  workdir_retention_days: 14,
  runtime_offline_grace: "P7D",
  task_event_masking: false,
} as const;

export const RUNTIME_KINDS: readonly RuntimeKind[] = ["claude_code", "hermes", "antigravity"];
export const RUNTIME_KIND_LABEL: Record<RuntimeKind, string> = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" };
export const ISOLATION_LABEL: Record<IsolationKind, string> = { none: "격리 없음", worktree: "워크트리", container: "컨테이너" };
export const INCLUDE_ARTIFACTS_LABEL = { links: "링크만", none: "넣지 않음", full: "본문 전체" } as const;
export const SUBSCRIPTION_LABEL = { all: "전부", hitl_only: "사람 확인만", completion_only: "종료만" } as const;

// ── 「바꿨을 때의 영향」 한 줄(SCREEN §4.10 · EVAL_USER U14) ──────────────────
/**
 * 항목마다 **한 줄**. 값에 따라 달라지는 문장은 함수다(U14-2: "3일 후 …"). 사용자가 바꾸기 전에 읽는 문장이므로
 * "무엇이 막히거나 무엇이 사라지는지"를 말하고, 내부 키는 쓰지 않는다(문구 자물쇠 NN5).
 */
export const IMPACT = {
  max_chain_depth: "낮추면 깊은 위임 사슬(Lead → 팀원 → 팀원)이 도중에 막힐 수 있습니다",
  max_hops_per_hour: "낮추면 활발한 방이 한 시간 안에 멈춰 방장의 재개를 기다립니다",
  max_pair_roundtrips: "낮추면 정상적인 리뷰 왕복이 막힐 수 있습니다",
  max_concurrent_tasks: "낮추면 그 이상의 할 일은 컴퓨터마다 줄을 서고, 높이면 컴퓨터가 느려질 수 있습니다",
  per_kind: "종류별 상한은 전체 상한 안에서만 적용됩니다 — 비우면 전체 상한을 따릅니다",
  default_session_budget_usd: "새 미션의 기본 상한입니다 — 넘으면 미션이 일시정지되고 Director 에게 계속할지 묻습니다",
  default_task_budget_usd: "할 일 하나의 기본 상한입니다 — 넘으면 그 서브 미션만 멈추고 미션은 계속됩니다",
  // PRD §9 "세션·에이전트·워크스페이스 예산 상한. 초과 시 자동 paused" — 서버가 어느 세션을 멈추는지는 계약이 아직 못박지 않았다.
  workspace_monthly_budget_usd: "이 달 워크스페이스 누적이 넘으면 미션이 자동으로 일시정지됩니다 — Director 가 계속을 승인해야 이어집니다",
  pricing_overrides: "사용량을 보고하지 않는 컴퓨터의 추정 비용 계산에만 쓰입니다 — 실측 비용은 바뀌지 않습니다",
  max_summary_tokens: "낮추면 이전 미션의 맥락이 덜 넘어가고, 높이면 새 미션의 첫 턴 비용이 올라갑니다",
  include_artifacts: "「본문 전체」는 첫 턴이 길어지고, 「넣지 않음」은 에이전트가 이전 아티팩트를 모릅니다",
  default_isolation: "새 방의 기본 선택만 바꿉니다 — 이미 만든 방의 격리는 그대로입니다",
  workdir_retention_days: (days: number) => `${days}일 후 병합되지 않은 워크트리는 삭제되지 않고 알림만 갑니다`,
  workdir_disk_quota_gb: "넘으면 새 작업 폴더를 만들지 못해 첫 실행이 막힙니다 — 비우면 상한 없음",
  runtime_offline_grace: (days: number | null) => `컴퓨터 연결이 끊긴 채 ${days == null ? "유예" : `${days}일`}가 지나면 그 방이 멈추고 다른 컴퓨터로 옮기거나 미션을 모두 취소해야 합니다`,
  task_event_masking: "이후 diff·셸 출력은 요약만 저장됩니다. 기존 로그는 그대로",
  email: "끄면 사람 확인 요청도 메일로 오지 않습니다 — 받은 요청 화면에서만 봅니다",
  push: "브라우저 알림 권한이 있어야 옵니다",
  default_subscription: "「사람 확인만」이면 이후 멘션·미션 종료 알림은 오지 않고 사람 확인 요청만 옵니다",
  loop_limits_apply: "루프 상한은 다음 트리거부터 적용됩니다",
  sweep_apply: "보존 기한과 연결 끊김 유예는 다음 정리 주기부터 적용됩니다",
} as const;

// ── 부분 갱신 — 바꾼 칸만 보낸다 ─────────────────────────────────────────────
/**
 * `orig` 와 `draft` 를 비교해 **달라진 칸만** 담은 `WorkspaceSettingsUpdate` 를 만든다(openapi "부분 갱신").
 * jsonb 그룹(loop_limits·budget_policy·context_reuse·runtime_policy)은 그룹 안에서도 바뀐 키만 — 서버(S-26)가 `||` 로
 * 얕게 합치므로 바꾸지 않은 키를 함께 보내면 그 값이 "바꾼 것"으로 기록된다. 아무것도 안 바뀌면 `null`.
 */
export function diffSettings(orig: WorkspaceSettings, draft: WorkspaceSettings): WorkspaceSettingsUpdate | null {
  const out: WorkspaceSettingsUpdate = {};
  const group = <K extends "loop_limits" | "budget_policy" | "context_reuse" | "runtime_policy">(k: K) => {
    const a = (orig[k] ?? {}) as Record<string, unknown>;
    const b = (draft[k] ?? {}) as Record<string, unknown>;
    const changed: Record<string, unknown> = {};
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
      if (JSON.stringify(a[key] ?? null) !== JSON.stringify(b[key] ?? null)) changed[key] = b[key];
    }
    if (Object.keys(changed).length) (out as Record<string, unknown>)[k] = changed;
  };
  group("loop_limits");
  group("budget_policy");
  group("context_reuse");
  group("runtime_policy");
  // v0.19 두 묶음(room_defaults · room_read)도 같은 규칙 — 바뀐 키만. room_defaults.limits 는 한 칸이라 통째로(서버가 limits 를 한 값으로 받는다).
  for (const k of ["room_defaults", "room_read"] as const) {
    const a = (orig[k] ?? {}) as Record<string, unknown>;
    const b = (draft[k] ?? {}) as Record<string, unknown>;
    const changed: Record<string, unknown> = {};
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
      if (JSON.stringify(a[key] ?? null) !== JSON.stringify(b[key] ?? null)) changed[key] = b[key];
    }
    if (Object.keys(changed).length) (out as Record<string, unknown>)[k] = changed;
  }
  if (orig.default_isolation !== draft.default_isolation) out.default_isolation = draft.default_isolation;
  if (orig.workdir_retention_days !== draft.workdir_retention_days) out.workdir_retention_days = draft.workdir_retention_days;
  if ((orig.workdir_disk_quota_gb ?? null) !== (draft.workdir_disk_quota_gb ?? null)) out.workdir_disk_quota_gb = draft.workdir_disk_quota_gb ?? null;
  if (orig.runtime_offline_grace !== draft.runtime_offline_grace) out.runtime_offline_grace = draft.runtime_offline_grace;
  if (orig.task_event_masking !== draft.task_event_masking) out.task_event_masking = draft.task_event_masking;
  return Object.keys(out).length ? out : null;
}

// ── ISO 8601 duration ↔ 일수 ─────────────────────────────────────────────────
/** `P7D` → 7. 일 단위가 아니면(`PT3600S`·`P7DT1H`) null — 화면은 그때 원문을 그대로 보인다. */
export function isoDays(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const m = /^P(\d+)D$/.exec(iso.trim());
  if (m) return Number(m[1]);
  if (/^P(T0S)?$/.test(iso.trim()) || iso.trim() === "PT0S") return 0;
  return null;
}
export const daysIso = (days: number): string => `P${Math.max(0, Math.floor(days))}D`;

// ── 대시보드(PRD §11) ────────────────────────────────────────────────────────
export type MetricVerdict = "met" | "missed" | "unknown";
/** 목표 충족 — `lt` 는 작아야, `gt` 는 커야. 값이 없으면 "아직 잴 수 없음"(0 을 실측처럼 보이지 않는다). */
export function metricVerdict(m: Pick<Metric, "value" | "target" | "target_op">): MetricVerdict {
  if (m.value == null) return "unknown";
  return (m.target_op === "lt" ? m.value < m.target : m.value > m.target) ? "met" : "missed";
}
export const VERDICT_GLYPH: Record<MetricVerdict, string> = { met: "✓", missed: "✕", unknown: "–" };
export const VERDICT_LABEL: Record<MetricVerdict, string> = { met: "목표 충족", missed: "목표 미달", unknown: "아직 잴 수 없음" };
export const NOT_MEASURABLE = "아직 잴 수 없음";

/** 단위별 표기 — 분은 소수 한 자리, 비율은 %, 개수는 정수. */
export function formatMetricValue(value: number | null, unit: Metric["unit"]): string {
  if (value == null) return NOT_MEASURABLE;
  if (unit === "ratio") return `${Math.round(value * 1000) / 10}%`;
  if (unit === "minutes") return `${Math.round(value * 10) / 10}분`;
  return String(Math.round(value));
}
/** 목표 열 — `lt`/`gt` 를 사람 말로. */
export function formatMetricTarget(m: Pick<Metric, "target" | "target_op" | "unit">): string {
  return `${m.target_op === "lt" ? "<" : ">"} ${formatMetricValue(m.target, m.unit)}`;
}

// ── 「관찰」 표(PRD §11 관찰 행 · openapi getWorkspaceObservations, v1.1 K-18) ──────────────────────
/** 분포형 셋(중앙값·p95) — 나머지 둘(라우팅 집중·빈 턴 비율)은 비율형(`value`). 계약 ObservationRow description 그대로. */
export const DISTRIBUTION_KEYS: ReadonlySet<ObservationKey> = new Set<ObservationKey>(["chain_scale", "chain_depth", "join_breadth"]);
export const isDistribution = (row: Pick<ObservationRow, "key">): boolean => DISTRIBUTION_KEYS.has(row.key);

/** 개수 — 정수면 그대로, 아니면 소수 한 자리(중앙값 2.5 같은 것). */
export function formatCount(value: number | null): string {
  if (value == null) return NOT_MEASURABLE;
  return Number.isInteger(value) ? String(value) : String(Math.round(value * 10) / 10);
}

/**
 * 값 칸 — 분포형은 "중앙값 3 · p95 9", 비율형은 "12.5%". **표본이 0 이면 "아직 잴 수 없음"**(지표 표와 같은 규칙 — 0 을 실측처럼
 * 보이지 않는다). 값이 null 이어도 같은 말.
 */
export function formatObservation(row: Pick<ObservationRow, "key" | "n" | "value" | "median" | "p95">, words: { median: string; p95: string }): string {
  if (row.n === 0) return NOT_MEASURABLE;
  if (isDistribution(row)) {
    if (row.median == null) return NOT_MEASURABLE;
    const parts = [`${words.median} ${formatCount(row.median)}`];
    if (row.p95 != null) parts.push(`${words.p95} ${formatCount(row.p95)}`);
    return parts.join(" · ");
  }
  return formatMetricValue(row.value, "ratio");
}
