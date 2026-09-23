/**
 * S14 설정의 순수 규칙(`lib/settings.ts`) — 탭 표 · 권한 · 기본값 · 영향 한 줄 · **부분 갱신 payload** · duration · 대시보드 판정.
 */
import { describe, expect, it } from "vitest";
import {
  daysIso, DEFAULT_TAB, diffSettings, DISTRIBUTION_KEYS, formatCount, formatMetricTarget, formatMetricValue, formatObservation, IMPACT, isDistribution, isoDays, isSettingsTab, metricVerdict,
  saveRight, SETTINGS_DEFAULTS, SETTINGS_TABS, NOT_MEASURABLE,
} from "./settings";
import { defaultSettings } from "./mock/store";
import type { WorkspaceSettings } from "./api/types";

const base = (): WorkspaceSettings => ({ ...defaultSettings("w1"), workdir_disk_quota_gb: 50 });

describe("탭 표 — SCREEN §4.10 의 8탭 + 대시보드", () => {
  it("9개, 순서 그대로, 이름은 §8.4 의 말", () => {
    // v0.19(T-R2-W4a): 「방 기본값」(§4.17 표 셋째 행) · 「활동 로그」(S15, §4.18 설정 안의 탭).
    expect(SETTINGS_TABS.map((t) => t.key)).toEqual(["members", "runtime", "rooms", "budget", "loop", "context", "workdir", "security", "notifications", "dashboard", "audit"]);
    expect(SETTINGS_TABS.map((t) => t.label)).toEqual(["멤버", "컴퓨터 정책", "방 기본값", "예산", "루프 상한", "컨텍스트", "작업 폴더", "보안", "알림", "대시보드", "활동 로그"]);
    for (const t of SETTINGS_TABS) expect(t.label).not.toMatch(/런타임|Workdir/);
    expect(DEFAULT_TAB).toBe("members");
    expect(isSettingsTab("loop")).toBe(true);
    expect(isSettingsTab("nope")).toBe(false);
    expect(isSettingsTab(null)).toBe(false);
  });

  it("권한 열 — 보안은 owner, 알림은 개인, 대시보드는 읽기, 나머지는 owner·admin", () => {
    const who = Object.fromEntries(SETTINGS_TABS.map((t) => [t.key, t.who]));
    expect(who.security).toBe("owner");
    expect(who.notifications).toBe("personal");
    expect(who.dashboard).toBe("read");
    for (const k of ["members", "runtime", "budget", "loop", "context", "workdir"]) expect(who[k]).toBe("admin");
  });

  it("saveRight — 역할별 저장 가능 여부와 사유", () => {
    expect(saveRight("loop", "owner")).toEqual({ ok: true });
    expect(saveRight("loop", "admin")).toEqual({ ok: true });
    expect(saveRight("loop", "member").ok).toBe(false);
    expect(saveRight("security", "admin")).toEqual({ ok: false, reason: "소유자만 바꿀 수 있습니다" });
    expect(saveRight("security", "owner")).toEqual({ ok: true });
    expect(saveRight("notifications", "member")).toEqual({ ok: true });
    expect(saveRight("dashboard", "owner").ok).toBe(false);
    expect(saveRight("loop", null).ok).toBe(false);
  });
});

describe("기본값 — PRD §7 · openapi default", () => {
  it("8 · 60 · 5 · 2000 · links · 10 · none · 14 · P7D · false", () => {
    expect(SETTINGS_DEFAULTS.loop_limits).toEqual({ max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 5 });
    expect(SETTINGS_DEFAULTS.context_reuse).toEqual({ max_summary_tokens: 2000, include_artifacts: "links" });
    expect(SETTINGS_DEFAULTS.runtime_policy.max_concurrent_tasks).toBe(10);
    expect(SETTINGS_DEFAULTS.default_isolation).toBe("none");
    expect(SETTINGS_DEFAULTS.workdir_retention_days).toBe(14);
    expect(SETTINGS_DEFAULTS.runtime_offline_grace).toBe("P7D");
    expect(SETTINGS_DEFAULTS.task_event_masking).toBe(false);
  });
  it("목 시드(defaultSettings)와 같은 값이다 — 화면의 '기본값' 표시가 목과 어긋나지 않게", () => {
    const d = defaultSettings("w");
    expect(d.loop_limits).toEqual(SETTINGS_DEFAULTS.loop_limits);
    expect(d.context_reuse).toEqual(SETTINGS_DEFAULTS.context_reuse);
    expect(d.runtime_policy.max_concurrent_tasks).toBe(SETTINGS_DEFAULTS.runtime_policy.max_concurrent_tasks);
    expect(d.default_isolation).toBe(SETTINGS_DEFAULTS.default_isolation);
    expect(d.workdir_retention_days).toBe(SETTINGS_DEFAULTS.workdir_retention_days);
    expect(d.runtime_offline_grace).toBe(SETTINGS_DEFAULTS.runtime_offline_grace);
    expect(d.task_event_masking).toBe(SETTINGS_DEFAULTS.task_event_masking);
  });
});

describe("「바꿨을 때의 영향」 — 항목마다 한 줄(U14)", () => {
  it("전부 한 줄이고 내부 키를 노출하지 않는다", () => {
    for (const [k, v] of Object.entries(IMPACT)) {
      const text = typeof v === "function" ? (v as (n: number) => string)(3) : v;
      expect(text, k).not.toMatch(/\n/);
      expect(text.length, k).toBeGreaterThan(10);
      expect(text, k).toMatch(/[가-힣]/);
      expect(text, k).not.toMatch(/[가-힣]\s*\([a-z][a-z0-9]*_[a-z0-9_]+\)/);
      expect(text, k).not.toMatch(/런타임|\blane\b|\btask\b|HITL/);
    }
  });
  it("EVAL_USER U14 가 못박은 문장", () => {
    expect(IMPACT.max_pair_roundtrips).toBe("낮추면 정상적인 리뷰 왕복이 막힐 수 있습니다");
    expect(IMPACT.workdir_retention_days(3)).toBe("3일 후 병합되지 않은 워크트리는 삭제되지 않고 알림만 갑니다");
    expect(IMPACT.task_event_masking).toBe("이후 diff·셸 출력은 요약만 저장됩니다. 기존 로그는 그대로");
    expect(IMPACT.default_subscription).toContain("사람 확인 요청만");
  });
});

describe("diffSettings — 바꾼 칸만(openapi 부분 갱신 · 서버 S-26 합치기)", () => {
  it("아무것도 안 바뀌면 null", () => {
    expect(diffSettings(base(), base())).toBeNull();
  });
  it("그룹 안의 바뀐 키만 — 안 바뀐 키를 함께 보내지 않는다", () => {
    const d = base();
    d.loop_limits = { ...d.loop_limits, max_pair_roundtrips: 2 };
    expect(diffSettings(base(), d)).toEqual({ loop_limits: { max_pair_roundtrips: 2 } });
  });
  it("최상위 칸 — 격리·보존·유예·마스킹", () => {
    const d = base();
    d.default_isolation = "worktree";
    d.workdir_retention_days = 3;
    d.runtime_offline_grace = "P3D";
    d.task_event_masking = true;
    expect(diffSettings(base(), d)).toEqual({ default_isolation: "worktree", workdir_retention_days: 3, runtime_offline_grace: "P3D", task_event_masking: true });
  });
  it("용량 상한을 비우면 null 을 **보낸다**(생략이 아니다 — 서버는 null 로 SQL NULL 을 쓴다)", () => {
    const d = base();
    d.workdir_disk_quota_gb = null;
    expect(diffSettings(base(), d)).toEqual({ workdir_disk_quota_gb: null });
  });
  it("예산 칸을 비우면 그 키만 null", () => {
    const o = base();
    o.budget_policy = { ...o.budget_policy, default_session_budget_usd: 20 };
    const d = base();
    d.budget_policy = { ...d.budget_policy, default_session_budget_usd: null };
    expect(diffSettings(o, d)).toEqual({ budget_policy: { default_session_budget_usd: null } });
  });
  it("종류별 상한(per_kind)은 객체째 — 안에서 바뀐 것이 있으면 그 객체를 보낸다", () => {
    const d = base();
    d.runtime_policy = { ...d.runtime_policy, per_kind: { hermes: 2 } };
    expect(diffSettings(base(), d)).toEqual({ runtime_policy: { per_kind: { hermes: 2 } } });
  });
});

describe("ISO 8601 duration ↔ 일수", () => {
  it("P7D ↔ 7, 일 단위가 아니면 null(원문 보존)", () => {
    expect(isoDays("P7D")).toBe(7);
    expect(isoDays("P0D")).toBe(0);
    expect(isoDays("PT3600S")).toBeNull();
    expect(isoDays("P7DT1H")).toBeNull();
    expect(isoDays(null)).toBeNull();
    expect(daysIso(3)).toBe("P3D");
    expect(daysIso(-1)).toBe("P0D");
  });
});

describe("대시보드 판정·표기(PRD §11)", () => {
  it("lt 는 작아야, gt 는 커야 통과 · null 은 아직 잴 수 없음", () => {
    expect(metricVerdict({ value: 12, target: 15, target_op: "lt" })).toBe("met");
    expect(metricVerdict({ value: 16, target: 15, target_op: "lt" })).toBe("missed");
    expect(metricVerdict({ value: 0.7, target: 0.6, target_op: "gt" })).toBe("met");
    expect(metricVerdict({ value: 0.6, target: 0.6, target_op: "gt" })).toBe("missed");
    expect(metricVerdict({ value: null, target: 0.6, target_op: "gt" })).toBe("unknown");
    expect(metricVerdict({ value: 0, target: 0.01, target_op: "lt" })).toBe("met"); // 0 도 값이다 — null 과 다르다
  });
  it("단위별 표기", () => {
    expect(formatMetricValue(null, "ratio")).toBe(NOT_MEASURABLE);
    expect(formatMetricValue(0.674, "ratio")).toBe("67.4%");
    expect(formatMetricValue(12.34, "minutes")).toBe("12.3분");
    expect(formatMetricValue(7, "count")).toBe("7");
    expect(formatMetricTarget({ target: 15, target_op: "lt", unit: "minutes" })).toBe("< 15분");
    expect(formatMetricTarget({ target: 0.6, target_op: "gt", unit: "ratio" })).toBe("> 60%");
    expect(formatMetricTarget({ target: 5, target_op: "gt", unit: "count" })).toBe("> 5");
  });
});

// ── 「관찰」 표(v1.1 K-18, T-W16) — 값 칸 표기 ──────────────────────────────
describe("formatObservation — 분포형은 중앙값·p95, 비율형은 %, 표본 0 은 '아직 잴 수 없음'", () => {
  const words = { median: "중앙값", p95: "p95" };
  it("분포형", () => {
    expect(formatObservation({ key: "chain_scale", n: 14, value: null, median: 2, p95: 6 }, words)).toBe("중앙값 2 · p95 6");
    expect(formatObservation({ key: "chain_depth", n: 3, value: null, median: 3, p95: null }, words)).toBe("중앙값 3");
    expect(formatObservation({ key: "join_breadth", n: 5, value: null, median: 2.5, p95: 4.25 }, words)).toBe("중앙값 2.5 · p95 4.3");
    expect(formatObservation({ key: "join_breadth", n: 0, value: null, median: null, p95: null }, words)).toBe("아직 잴 수 없음");
    // n 이 있어도 값이 없으면 같은 말 — 0 을 만들어 내지 않는다.
    expect(formatObservation({ key: "join_breadth", n: 2, value: null, median: null, p95: null }, words)).toBe("아직 잴 수 없음");
  });
  it("비율형", () => {
    expect(formatObservation({ key: "empty_turn_rate", n: 31, value: 0.129, median: null, p95: null }, words)).toBe("12.9%");
    expect(formatObservation({ key: "routing_concentration", n: 20, value: 0, median: null, p95: null }, words)).toBe("0%"); // 0 은 값이다
    expect(formatObservation({ key: "empty_turn_rate", n: 0, value: null, median: null, p95: null }, words)).toBe("아직 잴 수 없음");
    expect(formatObservation({ key: "empty_turn_rate", n: 4, value: null, median: null, p95: null }, words)).toBe("아직 잴 수 없음");
  });
  it("분포형 셋 · 개수 표기", () => {
    expect([...DISTRIBUTION_KEYS].sort()).toEqual(["chain_depth", "chain_scale", "join_breadth"]);
    expect(isDistribution({ key: "routing_concentration" })).toBe(false);
    expect(formatCount(3)).toBe("3");
    expect(formatCount(2.75)).toBe("2.8");
    expect(formatCount(null)).toBe("아직 잴 수 없음");
  });
});
