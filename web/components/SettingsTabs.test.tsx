/**
 * W-21 — S14 설정 폼의 초안(draft)이 **결정적으로** 갱신된다.
 *
 * 원인이었던 것: `useEffect(() => setDraft(settings), [settings, tab])`. 마운트 직후의 passive effect 는 스케줄러가 나중에 돌리고,
 * 그 전에 들어온 첫 입력(setDraft)이 effect 의 setDraft(settings) 에 덮여 사라졌다 — `settings-dirty` 유닛이 CI 에서 가끔 흔들린
 * 이유(T-I6 PR #253). 지금은 prop(settings·tab)이 바뀐 것을 **렌더 중에** 알아채 같은 렌더에서 되돌린다 — 입력이 끼어들 창이 없다.
 *
 * 여기서 재는 것: 마운트 직후 첫 입력이 살아남는다 · settings prop 이 바뀌면 초안을 버린다 · tab prop 이 바뀌면 초안을 버린다 ·
 * 알림 탭도 같다 · 소스에 effect 로 초안을 되돌리는 코드가 없다(회귀 자물쇠).
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { NotificationSettings, WorkspaceSettings } from "@/lib/api/types";
import { NotificationsTab, WorkspaceSettingsTab } from "./SettingsTabs";

const settings = (over: Partial<WorkspaceSettings> = {}): WorkspaceSettings => ({
  workspace_id: "w1",
  loop_limits: { max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 5 },
  budget_policy: { default_session_budget_usd: null, default_task_budget_usd: null, workspace_monthly_budget_usd: null, pricing_overrides: {} },
  context_reuse: { max_summary_tokens: 2000, include_artifacts: "links" },
  default_isolation: "none",
  runtime_policy: { max_concurrent_tasks: 10, per_kind: {} },
  workdir_retention_days: 14, workdir_disk_quota_gb: 50, runtime_offline_grace: "P7D", task_event_masking: false,
  updated_at: "2026-09-13T00:00:00Z",
  ...over,
});
const onSave = vi.fn(async () => null);

afterEach(cleanup);

describe("WorkspaceSettingsTab — 초안은 렌더 중에 되돌린다(W-21)", () => {
  it("마운트 직후의 첫 입력이 살아남는다 — 바꾼 항목 1개, 저장 활성", () => {
    render(<WorkspaceSettingsTab tab="loop" settings={settings()} role="owner" onSave={onSave} />);
    // 렌더 직후 곧바로(대기 없이) 입력한다 — 예전 effect 방식이면 이 입력이 effect 에 덮일 수 있는 자리.
    fireEvent.change(screen.getByLabelText("둘이 연속으로 주고받는 횟수"), { target: { value: "2" } });
    expect(screen.getByTestId("settings-dirty").textContent).toContain("바꾼 항목: 1개");
    expect((screen.getByTestId("settings-save") as HTMLButtonElement).disabled).toBe(false);
  });

  it("settings prop 이 새 객체로 바뀌면(저장 성공·다시 읽기) 초안을 버린다", () => {
    const { rerender } = render(<WorkspaceSettingsTab tab="loop" settings={settings()} role="owner" onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("둘이 연속으로 주고받는 횟수"), { target: { value: "2" } });
    expect(screen.queryByTestId("settings-dirty")).not.toBeNull();
    rerender(<WorkspaceSettingsTab tab="loop" settings={settings({ loop_limits: { max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 3 } })} role="owner" onSave={onSave} />);
    expect(screen.queryByTestId("settings-dirty")).toBeNull();
    expect((screen.getByLabelText("둘이 연속으로 주고받는 횟수") as HTMLInputElement).value).toBe("3");
  });

  it("같은 settings 객체로 다시 렌더되면 초안을 지키지 않는 일이 없다(정체성 비교)", () => {
    const s = settings();
    const { rerender } = render(<WorkspaceSettingsTab tab="loop" settings={s} role="owner" onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("둘이 연속으로 주고받는 횟수"), { target: { value: "2" } });
    rerender(<WorkspaceSettingsTab tab="loop" settings={s} role="owner" onSave={onSave} />);
    expect(screen.getByTestId("settings-dirty").textContent).toContain("바꾼 항목: 1개");
  });

  it("tab prop 이 바뀌면 초안을 버린다 — 다른 탭의 미저장 초안이 payload 에 섞이지 않게", () => {
    const s = settings();
    const { rerender } = render(<WorkspaceSettingsTab tab="loop" settings={s} role="owner" onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("둘이 연속으로 주고받는 횟수"), { target: { value: "2" } });
    rerender(<WorkspaceSettingsTab tab="workdir" settings={s} role="owner" onSave={onSave} />);
    expect(screen.queryByTestId("settings-dirty")).toBeNull();
    expect(screen.getByTestId("settings-tab-workdir")).toBeTruthy();
  });
});

describe("NotificationsTab — 같은 규칙", () => {
  const notif = (over: Partial<NotificationSettings> = {}): NotificationSettings => ({ email: true, push: false, default_subscription: "all", ...over });

  it("마운트 직후의 첫 입력이 살아남고, settings prop 이 바뀌면 초안을 버린다", () => {
    const save = vi.fn(async () => null);
    const { rerender } = render(<NotificationsTab settings={notif()} onSave={save} />);
    fireEvent.click(screen.getByTestId("notif-push"));
    expect((screen.getByTestId("notif-push") as HTMLInputElement).checked).toBe(true);
    rerender(<NotificationsTab settings={notif({ push: false, default_subscription: "hitl_only" })} onSave={save} />);
    expect((screen.getByTestId("notif-push") as HTMLInputElement).checked).toBe(false);
    expect((screen.getByTestId("notif-subscription") as HTMLSelectElement).value).toBe("hitl_only");
  });
});

describe("회귀 자물쇠 — 초안을 effect 로 되돌리지 않는다", () => {
  it("SettingsTabs.tsx 에 useEffect(() => setDraft(…)) 가 없다", () => {
    const src = readFileSync(join(__dirname, "SettingsTabs.tsx"), "utf8").replace(/\/\/[^\n]*|\/\*[\s\S]*?\*\//g, "");
    expect(src).not.toMatch(/useEffect\(\s*\(\)\s*=>\s*setDraft\(/);
    expect(src).not.toMatch(/from "react"[^\n]*useEffect|useEffect[^\n]*from "react"/);
  });
});
