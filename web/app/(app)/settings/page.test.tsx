/**
 * S14 설정 화면 — 탭 9개 렌더 · 권한(멤버는 읽기 + 비활성 사유, admin 은 보안 탭 잠김) · 저장 payload(바꾼 칸만) ·
 * 대시보드 표 10행 + "아직 잴 수 없음" 경로 · 422 필드 오류 · 알림(개인) 저장 · 「화면」(테마)은 탭 밖 상단.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { Me, Member, MetricsReport, ObservationReport, WorkspaceSettings } from "@/lib/api/types";

const push = vi.fn();
let tabParam: string | null = null;
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace: vi.fn() }),
  useSearchParams: () => ({ get: (k: string) => (k === "tab" ? tabParam : null) }),
}));

const get = vi.fn();
const patch = vi.fn();
const post = vi.fn();
const del = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a), patch: (...a: unknown[]) => patch(...a), post: (...a: unknown[]) => post(...a), delete: (...a: unknown[]) => del(...a) } };
});

let role: "owner" | "admin" | "member" = "owner";
const meOf = (): Me => ({
  user: { id: "u1", email: "me@example.com", display_name: "민지", avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
  workspaces: [{ id: "w1", name: "Colab", slug: "colab", my_role: role, created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" }],
  pending_invites: [],
});
// 실제 AuthContext 처럼 `me`·`workspace` 의 정체성이 렌더마다 바뀌지 않게 한 번만 만든다(매번 새 객체면 설정을 다시 읽어 초안이 날아간다).
let me: Me = meOf();
vi.mock("@/lib/auth/AuthContext", () => ({
  useAuth: () => ({ me, workspace: me.workspaces[0], loading: false, canManage: role !== "member", selectWorkspace: vi.fn(), refresh: vi.fn(), logout: vi.fn() }),
}));
function setRole(r: typeof role) {
  role = r;
  me = meOf();
}

import SettingsPage from "./page";
import { problemFixture } from "@/lib/mock/problem-fixture";

const settings = (): WorkspaceSettings => ({
  workspace_id: "w1",
  loop_limits: { max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 5 },
  budget_policy: { default_session_budget_usd: null, default_task_budget_usd: null, workspace_monthly_budget_usd: null, pricing_overrides: {} },
  context_reuse: { max_summary_tokens: 2000, include_artifacts: "links" },
  default_isolation: "none",
  runtime_policy: { max_concurrent_tasks: 10, per_kind: {} },
  workdir_retention_days: 14, workdir_disk_quota_gb: 50, runtime_offline_grace: "P7D", task_event_masking: false,
  updated_at: "2026-09-13T00:00:00Z",
});
const members: Member[] = [
  { id: "m1", workspace_id: "w1", user: { id: "u1", email: "me@example.com", display_name: "민지", avatar_url: null, created_at: "2026-09-06T09:00:00Z" }, role: "owner", created_at: "2026-09-06T09:00:00Z" },
  { id: "m2", workspace_id: "w1", user: { id: "u2", email: "seo@example.com", display_name: "서연", avatar_url: null, created_at: "2026-09-06T09:00:00Z" }, role: "member", created_at: "2026-09-06T09:00:00Z" },
];
const metric = (key: MetricsReport["metrics"][number]["key"], value: number | null, unit: "minutes" | "ratio" | "count", target: number, op: "lt" | "gt"): MetricsReport["metrics"][number] =>
  ({ key, label: `지표 ${key}`, value, unit, target, target_op: op, n: value == null ? 0 : 5, note: "세는 법" });
const report = (): MetricsReport => ({
  workspace_id: "w1", window: "P30D", computed_at: "2026-09-13T00:00:00Z",
  metrics: [
    metric("f1_minutes", null, "minutes", 15, "lt"),
    metric("auto_complete_rate", 0.67, "ratio", 0.6, "gt"),
    metric("hitl_response_minutes", 31, "minutes", 30, "lt"),
    metric("delegation_autonomous_rate", 0.58, "ratio", 0.7, "gt"),
    metric("parallel_wallclock_reduction", 0.44, "ratio", 0.4, "gt"),
    { ...metric("task_success_rate_by_runtime", 0.83, "ratio", 0.85, "gt"), breakdown: [{ kind: "claude_code", value: 0.97, target: 0.95, n: 40 }, { kind: "hermes", value: null, target: 0.85, n: 0 }] },
    metric("duplicate_after_resume_rate", null, "ratio", 0.01, "lt"),
    metric("resume_success_rate", 0.92, "ratio", 0.9, "gt"),
    metric("blocked_response_minutes", 3.5, "minutes", 5, "lt"),
    metric("weekly_active_sessions", 7, "count", 5, "gt"),
  ],
});

/** 「관찰」 표(v1.1 K-18) — 분포형 둘(하나는 p95 없음) · 표본 0 하나 · 비율형 둘(하나는 breakdown). */
const observations = (): ObservationReport => ({
  workspace_id: "w1", window: "P30D", computed_at: "2026-09-15T09:00:00Z",
  rows: [
    { key: "chain_scale", label: "트리거 사슬 규모", note: "사람 메시지 하나가 만든 할 일 수", n: 14, value: null, median: 2, p95: 6 },
    { key: "chain_depth", label: "트리거 사슬 깊이", note: "가장 깊은 인과 사슬", n: 3, value: null, median: 3, p95: null },
    { key: "join_breadth", label: "합류 폭", note: "한 위임에서 갈라진 자식 수", n: 0, value: null, median: null, p95: null },
    { key: "routing_concentration", label: "라우팅 집중", note: "규칙 번호 분포", n: 20, value: 0.35, median: null, p95: null,
      breakdown: [{ kind: "2", share: 0.4, n: 8 }, { kind: "6", share: 0.3, n: 6 }, { kind: "7", share: 0.05, n: 1 }, { kind: "platform", share: 0.25, n: 5 }] },
    { key: "empty_turn_rate", label: "빈 턴 비율", note: "아무것도 안 한 실행 비율", n: 31, value: 0.129, median: null, p95: null },
  ],
});

function wireGet() {
  get.mockImplementation(async (path: string) => {
    if (path === "/workspaces/{workspaceId}/observations") return observations();
    if (path === "/workspaces/{workspaceId}/settings") return settings();
    if (path === "/workspaces/{workspaceId}/members") return { items: members, next_cursor: null };
    if (path === "/workspaces/{workspaceId}/invites") return [];
    if (path === "/me/notification-settings") return { email: true, push: false, default_subscription: "all" };
    if (path === "/workspaces/{workspaceId}/metrics") return report();
    throw new Error(`unexpected GET ${path}`);
  });
}

beforeEach(() => {
  setRole("owner");
  tabParam = null;
  get.mockReset(); patch.mockReset(); post.mockReset(); del.mockReset(); push.mockReset();
  wireGet();
});
afterEach(cleanup);

describe("S14 — 탭과 상단", () => {
  it("탭 9개(8 + 대시보드)가 순서대로 있고, 「화면」(테마)은 탭 밖 상단에 있다", async () => {
    render(<SettingsPage />);
    const tabs = within(screen.getByTestId("settings-tabs")).getAllByRole("tab");
    expect(tabs.map((t) => t.textContent?.replace(/(소유자·관리자|소유자|개인|읽기)$/, ""))).toEqual(["멤버", "컴퓨터 정책", "예산", "루프 상한", "컨텍스트", "작업 폴더", "보안", "알림", "대시보드"]);
    // 테마 섹션이 탭 목록보다 앞(DOM 순서)에 있다 — 탭 안으로 옮기지 않았다.
    const page = screen.getByTestId("settings-page");
    const order = [...page.querySelectorAll("[data-testid]")].map((e) => e.getAttribute("data-testid"));
    expect(order.indexOf("settings-appearance")).toBeLessThan(order.indexOf("settings-tabs"));
    expect(screen.getByTestId("theme-select")).toBeTruthy();
    expect(screen.getByTestId("page-head").getAttribute("data-screen")).toBe("settings");
    await waitFor(() => expect(screen.getByTestId("settings-tab-members")).toBeTruthy());
  });

  it("탭을 누르면 ?tab= 으로 push 한다 — 뒤로가기가 탭을 기억한다", async () => {
    render(<SettingsPage />);
    fireEvent.click(screen.getByTestId("tab-loop"));
    expect(push).toHaveBeenCalledWith("/settings?tab=loop");
  });

  it("모르는 tab 값이면 기본 탭(멤버)", async () => {
    tabParam = "nope";
    render(<SettingsPage />);
    expect(screen.getByTestId("settings-page").getAttribute("data-tab")).toBe("members");
  });
});

describe("S14 — 워크스페이스 탭 · 권한 · 저장 payload", () => {
  it("루프 상한 탭: 기본값 표시 + 영향 한 줄(U14-1) + 바꾼 칸만 PATCH", async () => {
    tabParam = "loop";
    patch.mockImplementation(async (_p: string, opts: { body: unknown }) => ({ ...settings(), loop_limits: { max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 2 }, ...(opts.body as object) }));
    render(<SettingsPage />);
    const row = await screen.findByTestId("row-pair-roundtrips");
    expect(row.textContent).toContain("기본값 5");
    expect(screen.getByTestId("row-pair-roundtrips-impact").textContent).toBe("낮추면 정상적인 리뷰 왕복이 막힐 수 있습니다");
    const save = screen.getByTestId("settings-save") as HTMLButtonElement;
    expect(save.disabled).toBe(true); // 바꾼 것이 없다
    // W-21: 행이 보인 직후의 입력이 결정적으로 남는다 — 초안 되돌림이 effect 가 아니라 렌더 중이라(SettingsTabs.test.tsx) 대기가 필요 없다.
    fireEvent.change(screen.getByLabelText("둘이 연속으로 주고받는 횟수"), { target: { value: "2" } });
    expect(screen.getByTestId("settings-dirty").textContent).toContain("바꾼 항목: 1개");
    expect(save.disabled).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0][1]).toEqual({ path: { workspaceId: "w1" }, body: { loop_limits: { max_pair_roundtrips: 2 } } });
    await screen.findByTestId("settings-saved");
  });

  it("작업 폴더 탭: 보존 일수를 바꾸면 영향 문장이 그 값으로(U14-2) · 유예는 일수로 편집돼 ISO 로 나간다", async () => {
    tabParam = "workdir";
    patch.mockImplementation(async () => settings());
    render(<SettingsPage />);
    await screen.findByTestId("row-retention");
    fireEvent.change(screen.getByLabelText("작업 폴더 보존"), { target: { value: "3" } });
    expect(screen.getByTestId("row-retention-impact").textContent).toBe("3일 후 병합되지 않은 워크트리는 삭제되지 않고 알림만 갑니다");
    expect((screen.getByLabelText("컴퓨터 연결 끊김 유예") as HTMLInputElement).value).toBe("7");
    fireEvent.change(screen.getByLabelText("컴퓨터 연결 끊김 유예"), { target: { value: "3" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0][1].body).toEqual({ workdir_retention_days: 3, runtime_offline_grace: "P3D" });
  });

  it("보안 탭(owner 만): admin 은 잠기고 사유가 버튼 아래에(DisabledHint), owner 는 마스킹을 켤 수 있다(U14-3)", async () => {
    tabParam = "security";
    setRole("admin");
    render(<SettingsPage />);
    await screen.findByTestId("row-masking");
    expect((screen.getByTestId("masking-toggle") as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByTestId("settings-security-hint").textContent).toBe("소유자만 바꿀 수 있습니다");
    expect(screen.getByTestId("settings-save").getAttribute("aria-describedby")).toBe("settings-security-hint");
    expect(screen.getByTestId("row-masking-impact").textContent).toBe("이후 diff·셸 출력은 요약만 저장됩니다. 기존 로그는 그대로");
    cleanup();
    setRole("owner");
    patch.mockImplementation(async () => ({ ...settings(), task_event_masking: true }));
    render(<SettingsPage />);
    await screen.findByTestId("row-masking");
    expect(screen.queryByTestId("settings-security-hint")).toBeNull();
    fireEvent.click(screen.getByTestId("masking-toggle"));
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0][1].body).toEqual({ task_event_masking: true });
  });

  it("멤버(member): 읽기 안내 + 입력 전부 잠김 + 저장 비활성 사유", async () => {
    tabParam = "budget";
    setRole("member");
    render(<SettingsPage />);
    expect(screen.getByTestId("settings-readonly")).toBeTruthy();
    await screen.findByTestId("row-session-budget");
    for (const el of screen.getByTestId("settings-tab-budget").querySelectorAll("input")) expect((el as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByTestId("settings-save") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("settings-budget-hint").textContent).toContain("소유자·관리자만");
  });

  it("422 는 errors[] 를 그 칸 옆에 그린다(서버 문장 그대로)", async () => {
    tabParam = "loop";
    patch.mockImplementation(async () => {
      throw problemFixture("validation_failed", 422, { detail: "입력값을 확인해 주세요", errors: [{ field: "loop_limits.max_chain_depth", code: "out_of_range", message: "1~100 사이여야 합니다" }] });
    });
    render(<SettingsPage />);
    await screen.findByTestId("row-chain-depth");
    fireEvent.change(screen.getByLabelText("위임 사슬 깊이"), { target: { value: "0" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(within(screen.getByTestId("row-chain-depth")).getByRole("alert").textContent).toBe("1~100 사이여야 합니다"));
  });

  it("탭이 바뀌면 이전 탭의 미저장 초안이 payload 에 섞이지 않는다", async () => {
    tabParam = "loop";
    patch.mockImplementation(async () => settings());
    const view = render(<SettingsPage />);
    await screen.findByTestId("row-chain-depth");
    fireEvent.change(screen.getByLabelText("위임 사슬 깊이"), { target: { value: "3" } });
    tabParam = "context";
    view.rerender(<SettingsPage />);
    await screen.findByTestId("row-summary-tokens");
    expect((screen.getByTestId("settings-save") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("이전 세션 요약의 최대 토큰"), { target: { value: "1000" } });
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch.mock.calls[0][1].body).toEqual({ context_reuse: { max_summary_tokens: 1000 } });
  });

  it("서버가 설정 읽기를 거절하면(403 — P2 서버는 admin 을 요구한다) 문장을 그대로 보이고 폼을 그리지 않는다", async () => {
    tabParam = "loop";
    get.mockImplementation(async (path: string) => {
      if (path === "/workspaces/{workspaceId}/settings") throw problemFixture("admin_required", 403, { detail: "소유자·관리자만 할 수 있습니다" });
      return {};
    });
    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByTestId("settings-error").textContent).toBe("소유자·관리자만 할 수 있습니다"));
    expect(screen.queryByTestId("settings-tab-loop")).toBeNull();
  });
});

describe("S14 — 대시보드(PRD §11)", () => {
  it("표 10행 · null 은 '아직 잴 수 없음'(n 0) · 판정 글리프 · breakdown 하위 행", async () => {
    tabParam = "dashboard";
    render(<SettingsPage />);
    await screen.findByTestId("metrics-table");
    const rows = screen.getAllByTestId("metric-row");
    expect(rows).toHaveLength(10);
    expect(rows.map((r) => r.getAttribute("data-key"))).toEqual(report().metrics.map((m) => m.key));
    const f1 = rows[0];
    expect(within(f1).getByTestId("metric-value").textContent).toBe("아직 잴 수 없음");
    expect(within(f1).getByTestId("metric-n").textContent).toBe("0");
    expect(f1.getAttribute("data-verdict")).toBe("unknown");
    expect(within(f1).getByTestId("metric-verdict").textContent).toContain("–");
    expect(rows[1].getAttribute("data-verdict")).toBe("met");
    expect(within(rows[1]).getByTestId("metric-value").textContent).toBe("67%");
    expect(within(rows[1]).getByTestId("metric-verdict").textContent).toContain("✓");
    expect(rows[2].getAttribute("data-verdict")).toBe("missed");
    expect(within(rows[2]).getByTestId("metric-verdict").textContent).toContain("✕");
    const sub = screen.getAllByTestId("metric-breakdown");
    expect(sub.map((r) => r.getAttribute("data-kind"))).toEqual(["claude_code", "hermes"]);
    expect(sub[1].textContent).toContain("아직 잴 수 없음");
    // 0 은 값이다 — null 과 다르게 실측으로 보인다.
    expect(screen.getAllByTestId("metric-verdict").filter((v) => v.getAttribute("data-verdict") === "unknown")).toHaveLength(3);
  });

  it("서버가 501 이면(T-S12 전) 그 문장을 그대로", async () => {
    tabParam = "dashboard";
    get.mockImplementation(async () => { throw problemFixture("not_implemented", 501, { detail: "아직 지원하지 않는 기능입니다 (GetWorkspaceMetrics)" }); });
    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByTestId("metrics-error").textContent).toContain("아직 지원하지 않는 기능입니다"));
  });

  // ── 「관찰」 표(v1.1 K-18, T-W16) — 지표 표 **아래** 별도 표, 목표치 없음 ──
  it("관찰 표 — 지표 표 아래 · 제목 아래 '목표치 없이 분포만 봅니다' · 5행 enum 순서 · 목표·판정 열 없음", async () => {
    tabParam = "dashboard";
    render(<SettingsPage />);
    await screen.findByTestId("observations-table");
    const metrics = screen.getByTestId("metrics-table");
    const obs = screen.getByTestId("observations-table");
    // DOM 순서: 지표 표 → 관찰 표. 한 표로 합치지 않았다(Director 결정).
    expect(metrics.compareDocumentPosition(obs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByTestId("observations-title").textContent).toBe("관찰");
    expect(screen.getByTestId("observations-subtitle").textContent).toBe("목표치 없이 분포만 봅니다");
    const rows = screen.getAllByTestId("observation-row");
    expect(rows.map((r) => r.getAttribute("data-key"))).toEqual(["chain_scale", "chain_depth", "join_breadth", "routing_concentration", "empty_turn_rate"]);
    const heads = within(obs).getAllByRole("columnheader").map((h) => h.textContent);
    expect(heads).toEqual(["관찰", "값", "표본"]);
    expect(within(obs).queryAllByTestId("metric-verdict")).toHaveLength(0);
    // 이름은 서버 label 그대로, 세는 법은 접혀 있다.
    expect(within(rows[0]).getByText("트리거 사슬 규모")).toBeTruthy();
    expect(within(rows[0]).getByText("세는 법")).toBeTruthy();
    expect(within(rows[0]).getByText("사람 메시지 하나가 만든 할 일 수")).toBeTruthy();
  });

  it("관찰 표 — 분포형은 '중앙값 · p95'(p95 없으면 중앙값만) · 비율형은 % · 표본 0 은 '아직 잴 수 없음'", async () => {
    tabParam = "dashboard";
    render(<SettingsPage />);
    await screen.findByTestId("observations-table");
    const rows = screen.getAllByTestId("observation-row");
    const val = (i: number) => within(rows[i]).getByTestId("observation-value").textContent;
    const n = (i: number) => within(rows[i]).getByTestId("observation-n").textContent;
    expect(val(0)).toBe("중앙값 2 · p95 6");
    expect(n(0)).toBe("14");
    expect(val(1)).toBe("중앙값 3");
    expect(val(2)).toBe("아직 잴 수 없음");
    expect(n(2)).toBe("0");
    expect(rows[2].getAttribute("data-measurable")).toBe("false");
    // routing_concentration 의 값 옆에는 무엇의 비율인지 한 줄(V-1) — 값 자체는 35%.
    expect(val(3)).toBe("35%규칙 6·7 폴백 비율 — 아래는 규칙별 분포");
    expect(within(rows[3]).getByTestId("observation-value-hint").textContent).toBe("규칙 6·7 폴백 비율 — 아래는 규칙별 분포");
    expect(within(rows[4]).queryByTestId("observation-value-hint")).toBeNull();
    expect(val(4)).toBe("12.9%");
    expect(rows[4].getAttribute("data-measurable")).toBe("true");
  });

  it("관찰 표 — routing_concentration 의 breakdown 이 하위 행(규칙 번호 + 사람 말 · 비율 · n)으로, 다른 행에는 없다", async () => {
    tabParam = "dashboard";
    render(<SettingsPage />);
    await screen.findByTestId("observations-table");
    const sub = screen.getAllByTestId("observation-breakdown");
    expect(sub.map((r) => r.getAttribute("data-kind"))).toEqual(["2", "6", "7", "platform"]);
    expect(sub[0].textContent).toContain("규칙 2 · 에이전트 멘션");
    expect(sub[0].textContent).toContain("40%");
    expect(sub[0].textContent).toContain("8");
    expect(sub[1].textContent).toContain("규칙 6 · 담당 에이전트 폴백");
    expect(sub[3].textContent).toContain("플랫폼");
    // 하위 행은 routing_concentration 바로 아래에 붙는다.
    const rows = screen.getAllByTestId("observation-row");
    expect(rows[3].nextElementSibling).toBe(sub[0]);
    expect(sub[3].nextElementSibling).toBe(rows[4]);
  });

  it("관찰 표 「다시 세기」 — 지표 표의 버튼과 같은 load: 두 op 을 함께 다시 부른다(V-1) · 모르는 kind 는 원시 값 + (새 규칙)", async () => {
    tabParam = "dashboard";
    get.mockImplementation(async (path: string) => {
      if (path === "/workspaces/{workspaceId}/observations") {
        const o = observations();
        o.rows[3].breakdown = [...(o.rows[3].breakdown ?? []), { kind: "9", share: 0, n: 0 }];
        return o;
      }
      if (path === "/workspaces/{workspaceId}/metrics") return report();
      throw new Error(`unexpected GET ${path}`);
    });
    render(<SettingsPage />);
    await screen.findByTestId("observations-table");
    const calls = () => get.mock.calls.map((c) => c[0] as string);
    expect(calls().filter((p) => p.endsWith("/metrics"))).toHaveLength(1);
    expect(calls().filter((p) => p.endsWith("/observations"))).toHaveLength(1);
    fireEvent.click(within(screen.getByTestId("observations-wrap")).getByTestId("observations-reload"));
    await waitFor(() => expect(calls().filter((p) => p.endsWith("/observations"))).toHaveLength(2));
    expect(calls().filter((p) => p.endsWith("/metrics"))).toHaveLength(2);
    // 상단 버튼도 같은 동작.
    fireEvent.click(screen.getByTestId("metrics-reload"));
    await waitFor(() => expect(calls().filter((p) => p.endsWith("/observations"))).toHaveLength(3));
    expect(calls().filter((p) => p.endsWith("/metrics"))).toHaveLength(3);
    const unknown = screen.getAllByTestId("observation-breakdown").find((r) => r.getAttribute("data-kind") === "9")!;
    expect(unknown.textContent).toContain("9 (새 규칙)");
  });

  it("관찰 op 만 501 이어도(T-S19 전) 지표 표는 그대로 — 오류는 관찰 표 자리에", async () => {
    tabParam = "dashboard";
    get.mockImplementation(async (path: string) => {
      if (path === "/workspaces/{workspaceId}/metrics") return report();
      if (path === "/workspaces/{workspaceId}/observations") throw problemFixture("not_implemented", 501, { detail: "아직 지원하지 않는 기능입니다 (GetWorkspaceObservations)" });
      if (path === "/workspaces/{workspaceId}/settings") return settings();
      throw new Error(`unexpected GET ${path}`);
    });
    render(<SettingsPage />);
    await screen.findByTestId("metrics-table");
    await waitFor(() => expect(screen.getByTestId("observations-error").textContent).toContain("GetWorkspaceObservations"));
    expect(screen.getAllByTestId("metric-row")).toHaveLength(10);
    expect(screen.queryByTestId("observations-table")).toBeNull();
    expect(screen.queryByTestId("metrics-error")).toBeNull();
    // 오류 자리에도 「다시 세기」 — 같은 load(두 op 함께).
    fireEvent.click(within(screen.getByTestId("observations-error")).getByTestId("observations-reload"));
    await waitFor(() => expect(get.mock.calls.filter((c) => (c[0] as string).endsWith("/observations"))).toHaveLength(2));
    expect(get.mock.calls.filter((c) => (c[0] as string).endsWith("/metrics"))).toHaveLength(2);
  });
});

describe("S14 — 알림(개인) · 멤버", () => {
  it("알림은 멤버도 저장할 수 있고, 구독 기본값을 바꾸면 그 값으로 PATCH(U14-4)", async () => {
    tabParam = "notifications";
    setRole("member");
    patch.mockImplementation(async (_p: string, opts: { body: unknown }) => opts.body);
    render(<SettingsPage />);
    await screen.findByTestId("row-subscription");
    expect(screen.queryByTestId("settings-notifications-hint")).toBeNull();
    fireEvent.change(screen.getByTestId("notif-subscription"), { target: { value: "hitl_only" } });
    expect(screen.getByTestId("row-subscription-impact").textContent).toContain("사람 확인 요청만");
    fireEvent.click(screen.getByTestId("settings-save"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/me/notification-settings", { body: { email: true, push: false, default_subscription: "hitl_only" } }));
  });

  it("멤버 탭: 목록 · 역할 변경 PATCH · 자기 소유자 행은 잠김 · 초대 링크 201", async () => {
    tabParam = "members";
    patch.mockImplementation(async (_p: string, opts: { path: { memberId: string }; body: { role: string } }) => ({ ...members[1], role: opts.body.role }));
    post.mockImplementation(async () => ({ id: "i1", workspace_id: "w1", email: null, role: "member", token: "inv-abc", url: "http://x/invite/inv-abc", expires_at: "2026-09-20T00:00:00Z", created_at: "2026-09-13T00:00:00Z", accepted_at: null, status: "pending" }));
    render(<SettingsPage />);
    const rows = await screen.findAllByTestId("member-row");
    expect(rows).toHaveLength(2);
    const mine = rows.find((r) => r.textContent?.includes("(나)"))!;
    expect((within(mine).getByTestId("member-role") as HTMLSelectElement).disabled).toBe(true);
    expect(within(mine).getByTestId("member-role-why").textContent).toContain("다른 소유자가");
    const other = rows.find((r) => r !== mine)!;
    fireEvent.change(within(other).getByTestId("member-role"), { target: { value: "admin" } });
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId: "w1", memberId: "m2" }, body: { role: "admin" } }));
    fireEvent.click(screen.getByTestId("invite-create"));
    await screen.findByTestId("invite-created");
    expect(screen.getByTestId("invite-url").textContent).toBe("http://x/invite/inv-abc");
    expect(post.mock.calls[0][1].body).toEqual({ email: null, role: "member", expires_in_hours: 168 });
    expect(post.mock.calls[0][1].idempotencyKey).toBeTruthy();
  });

  it("멤버 탭(admin): 자기 역할을 내리면 확인 다이얼로그(무엇이 사라지는지) — 취소면 PATCH 없음, 내리기면 PATCH (PR #209 NN5)", async () => {
    tabParam = "members";
    setRole("admin");
    const mine = { ...members[0], role: "admin" as const };
    const owner: Member = { id: "m3", workspace_id: "w1", user: { id: "u3", email: "own@example.com", display_name: "지훈", avatar_url: null, created_at: "2026-09-06T09:00:00Z" }, role: "owner", created_at: "2026-09-06T09:00:00Z" };
    get.mockImplementation(async (path: string) => {
      if (path === "/workspaces/{workspaceId}/members") return { items: [mine, members[1], owner], next_cursor: null };
      if (path === "/workspaces/{workspaceId}/invites") return [];
      throw new Error(`unexpected GET ${path}`);
    });
    const all = [mine, members[1], owner];
    patch.mockImplementation(async (_p: string, opts: { path: { memberId: string }; body: { role: string } }) => ({ ...all.find((m) => m.id === opts.path.memberId)!, role: opts.body.role }));
    render(<SettingsPage />);
    const rows = await screen.findAllByTestId("member-row");
    const me = rows.find((r) => r.textContent?.includes("(나)"))!;
    const sel = within(me).getByTestId("member-role") as HTMLSelectElement;
    expect(sel.disabled).toBe(false);
    // 소유자로 올리는 것은 소유자만(서버 PlanRoleChange) — 관리자에게 「소유자」 항목은 꺼져 있다.
    expect((within(me).getByRole("option", { name: "소유자" }) as HTMLOptionElement).disabled).toBe(true);
    fireEvent.change(sel, { target: { value: "member" } });
    const dialog = await screen.findByTestId("member-self-demote");
    expect(dialog.textContent).toContain("내 역할을 관리자에서 멤버로 내립니다");
    expect(dialog.textContent).toContain("멤버 초대·역할 변경·워크스페이스 설정 변경을 더는 할 수 없");
    expect(dialog.textContent).toContain("다른 소유자·관리자가 올려 줘야");
    expect(patch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("member-self-demote-no"));
    expect(screen.queryByTestId("member-self-demote")).toBeNull();
    expect(patch).not.toHaveBeenCalled();
    fireEvent.change(sel, { target: { value: "member" } });
    fireEvent.click(await screen.findByTestId("member-self-demote-yes"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId: "w1", memberId: "m1" }, body: { role: "member" } }));
    await waitFor(() => expect(screen.queryByTestId("member-self-demote")).toBeNull());
    // 다른 멤버를 올리는 것(admin 이 member → admin)은 확인 없이 바로 PATCH.
    const other = rows.find((r) => r.textContent?.includes("서연"))!;
    fireEvent.change(within(other).getByTestId("member-role"), { target: { value: "admin" } });
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId: "w1", memberId: "m2" }, body: { role: "admin" } }));
    expect(screen.queryByTestId("member-self-demote")).toBeNull();
    // 소유자 행 — 관리자는 역할도 못 바꾸고(사유) 내보내기도 잠긴다(서버 owner_only 를 화면이 미리 안다).
    const ownerRow = rows.find((r) => r.textContent?.includes("지훈"))!;
    expect((within(ownerRow).getByTestId("member-role") as HTMLSelectElement).disabled).toBe(true);
    expect(within(ownerRow).getByTestId("member-role-why").textContent).toContain("소유자만");
    expect((within(ownerRow).getByTestId("member-remove") as HTMLButtonElement).disabled).toBe(true);
    expect(within(ownerRow).getByTestId("member-remove").getAttribute("title")).toBe("소유자는 소유자만 내보낼 수 있습니다");
  });

  it("멤버 탭(owner): 다른 멤버를 내리는 것은 확인 없이 PATCH · 제거 409 의 detail 은 서버 문장 그대로(확장 칸 없음)", async () => {
    tabParam = "members";
    const members3: Member[] = [...members, { id: "m3", workspace_id: "w1", user: { id: "u3", email: "own@example.com", display_name: "지훈", avatar_url: null, created_at: "2026-09-06T09:00:00Z" }, role: "admin", created_at: "2026-09-06T09:00:00Z" }];
    get.mockImplementation(async (path: string) => {
      if (path === "/workspaces/{workspaceId}/members") return { items: members3, next_cursor: null };
      if (path === "/workspaces/{workspaceId}/invites") return [];
      throw new Error(`unexpected GET ${path}`);
    });
    patch.mockImplementation(async (_p: string, opts: { body: { role: string } }) => ({ ...members3[2], role: opts.body.role }));
    del.mockRejectedValue(problemFixture("last_owner", 409, { detail: "마지막 소유자는 내보낼 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요" }));
    render(<SettingsPage />);
    const rows = await screen.findAllByTestId("member-row");
    const admin = rows.find((r) => r.textContent?.includes("지훈"))!;
    fireEvent.change(within(admin).getByTestId("member-role"), { target: { value: "member" } });
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId: "w1", memberId: "m3" }, body: { role: "member" } }));
    expect(screen.queryByTestId("member-self-demote")).toBeNull();
    fireEvent.click(within(admin).getByTestId("member-remove"));
    fireEvent.click(within(admin).getByTestId("member-remove-yes"));
    await waitFor(() => expect(screen.getByTestId("members-error").textContent).toBe("마지막 소유자는 내보낼 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요"));
  });

  it("멤버 탭(member): 초대 구역이 없고 사유가 있다 · 역할 선택 잠김", async () => {
    tabParam = "members";
    setRole("member");
    render(<SettingsPage />);
    await screen.findAllByTestId("member-row");
    expect(screen.queryByTestId("invite-section")).toBeNull();
    expect(screen.getByTestId("members-manage-hint").textContent).toContain("소유자·관리자만");
    for (const sel of screen.getAllByTestId("member-role")) expect((sel as HTMLSelectElement).disabled).toBe(true);
  });
});
