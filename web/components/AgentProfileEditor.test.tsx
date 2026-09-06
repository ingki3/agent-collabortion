/**
 * S10 프로파일 편집기 — 옵션은 `RuntimeCapability.supported_options` 가 정한다.
 * **광고된 키만 고를 수 있고, 광고가 없으면 비활성 + 사유**다. `runtime_kind` 로 추측하지 않는다.
 * 데몬이 옵션을 채우는 것은 후속이라 "광고 없음"이 당분간 첫 화면이고, 그 화면이 막다른 길로 보이면 안 된다.
 */
import { describe, expect, it, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AgentProfileEditor } from "./AgentProfileEditor";
import { capabilityIndex, supportedOptions } from "@/lib/runtime-options";
import type { AgentProfile, Runtime, RuntimeCapability } from "@/lib/api/types";

afterEach(cleanup);

const cap = (over: Partial<RuntimeCapability>): RuntimeCapability =>
  ({ kind: "claude_code", logged_in: true, models: ["claude-sonnet-5"], ...over });

function runtime(caps: RuntimeCapability[]): Runtime {
  return {
    id: "r1", workspace_id: "w1", name: "MacBook", host: null, status: "online", daemon_version: "0.4.0",
    last_seen_at: null, capabilities: caps, repos: [], max_concurrent_tasks: null, running_task_count: 0,
    workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null, paused_session_count: 0,
    created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
  };
}

function profile(over: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "p1", agent_id: "a1", name: "default", runtime_kind: "claude_code", model: "claude-sonnet-5",
    options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null,
    created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z", ...over,
  };
}

const noop = { onCreate: vi.fn(async () => {}), onUpdate: vi.fn(async () => {}), onDelete: vi.fn(async () => {}) };

describe("supportedOptions — 없거나 비면 '광고 없음'", () => {
  it("키가 없거나 값이 빈 배열이면 빈 객체다", () => {
    expect(supportedOptions(cap({}))).toEqual({});
    expect(supportedOptions(cap({ supported_options: {} }))).toEqual({});
    expect(supportedOptions(cap({ supported_options: { effort: [] } }))).toEqual({});
    expect(supportedOptions(cap({ supported_options: { effort: ["low", "high"] } }))).toEqual({ effort: ["low", "high"] });
  });

  it("여러 머신이 같은 종류를 광고하면 모델·옵션 값의 합집합이다", () => {
    const idx = capabilityIndex([
      runtime([cap({ models: ["a"], supported_options: { effort: ["low"] } })]),
      { ...runtime([cap({ models: ["b"], supported_options: { effort: ["high"] } })]), id: "r2" },
    ]);
    expect(idx.get("claude_code")!.models).toEqual(["a", "b"]);
    expect(idx.get("claude_code")!.options).toEqual({ effort: ["low", "high"] });
  });

  it("로그인 안 된 능력과 오프라인 머신은 목록에 들어가지 않는다", () => {
    expect(capabilityIndex([runtime([cap({ logged_in: false })])]).size).toBe(0);
    expect(capabilityIndex([{ ...runtime([cap({})]), status: "offline" }]).size).toBe(0);
  });
});

describe("AgentProfileEditor — 광고된 옵션만 고를 수 있다", () => {
  it("supported_options 가 있으면 그 키와 값만 선택지가 된다", async () => {
    const onUpdate = vi.fn(async () => {});
    render(
      <AgentProfileEditor
        profiles={[profile()]}
        caps={capabilityIndex([runtime([cap({ supported_options: { effort: ["low", "xhigh"] } })])])}
        canEdit
        {...noop}
        onUpdate={onUpdate}
      />,
    );
    const sel = screen.getByTestId("profile-option") as HTMLSelectElement;
    expect(sel.getAttribute("data-option")).toBe("effort");
    expect([...sel.options].map((o) => o.value)).toEqual(["", "low", "xhigh"]);
    expect(sel.disabled).toBe(false);
    fireEvent.change(sel, { target: { value: "xhigh" } });
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith("p1", { options: { effort: "xhigh" } }));
  });

  it("광고가 없으면 옵션 편집기가 사유와 함께 사라지고, 막다른 길이 아니라고 말한다", () => {
    render(
      <AgentProfileEditor
        profiles={[profile({ runtime_kind: "hermes", model: "hermes-4" })]}
        caps={capabilityIndex([runtime([cap({ kind: "hermes", models: ["hermes-4"] })])])}
        canEdit
        {...noop}
      />,
    );
    expect(screen.queryByTestId("profile-option")).toBeNull();
    const note = screen.getByTestId("profile-options-unadvertised");
    expect(note.getAttribute("data-kind")).toBe("hermes");
    expect(note.textContent).toContain("지원 범위를 광고하지 않습니다");
    expect(note.textContent).toContain("런타임 기본값으로 동작");
  });

  it("모델 목록은 probe 결과이고, 감지된 런타임이 없으면 그 사실을 말한다", () => {
    const { rerender } = render(<AgentProfileEditor profiles={[profile()]} caps={capabilityIndex([runtime([cap({ models: ["claude-sonnet-5", "claude-opus-5"] })])])} canEdit {...noop} />);
    expect([...(screen.getByTestId("profile-model") as HTMLSelectElement).options].map((o) => o.value)).toEqual(["claude-sonnet-5", "claude-opus-5"]);
    rerender(<AgentProfileEditor profiles={[profile()]} caps={new Map()} canEdit {...noop} />);
    expect(screen.getByTestId("no-probe-models").textContent).toContain("먼저 컴퓨터를 연결하세요");
  });

  it("마지막·기본 프로파일은 삭제할 수 없고 사유를 툴팁으로 말한다", () => {
    const { rerender } = render(<AgentProfileEditor profiles={[profile()]} caps={new Map()} canEdit {...noop} />);
    let del = screen.getByTestId("profile-delete") as HTMLButtonElement;
    expect(del.disabled).toBe(true);
    rerender(<AgentProfileEditor profiles={[profile(), profile({ id: "p2", name: "fast", is_default: false })]} caps={new Map()} canEdit {...noop} />);
    const dels = screen.getAllByTestId("profile-delete") as HTMLButtonElement[];
    expect(dels[0].disabled).toBe(true);
    expect(dels[0].getAttribute("title")).toContain("먼저 다른 프로파일을 기본으로");
    expect(dels[1].disabled).toBe(false);
    del = dels[1];
  });

  it("권한이 없으면 숨기지 않고 비활성 + 사유(원칙 4)", () => {
    render(<AgentProfileEditor profiles={[profile()]} caps={capabilityIndex([runtime([cap({})])])} canEdit={false} disabledReason="소유자만" {...noop} />);
    expect((screen.getByTestId("profile-model") as HTMLSelectElement).disabled).toBe(true);
    const add = screen.getByTestId("profile-add") as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    expect(add.getAttribute("title")).toBe("소유자만");
  });
});
