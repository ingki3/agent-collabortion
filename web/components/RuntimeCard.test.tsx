/**
 * S11 런타임 카드 — 계약 v0.4.1 `RuntimeCapability` 새 키(W-1)와 probe 최상위 `colab_cli`.
 * 능력이 **없다는 것도 결과와 함께** 말해야 한다(SCREEN §1 원칙 3·5) — 침묵과 미지원은 다르다.
 */
import { describe, expect, it, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { RuntimeCard, capabilityNotes } from "./RuntimeCard";
import type { Runtime } from "@/lib/api/types";

afterEach(cleanup);

function rt(over: Partial<Runtime> = {}): Runtime {
  return {
    id: "r1", workspace_id: "w1", name: "MacBook", host: "macbook.local", status: "online",
    daemon_version: "0.4.0", last_seen_at: "2026-09-06T10:00:00Z",
    capabilities: [
      { kind: "claude_code", version: "2.1.258", adapter_version: "0.74.0", logged_in: true, models: ["claude-sonnet-5"], protocol_version: 1, resume: true, usage: true, tool_disallow: true, brief_transport: "acp_meta_system_prompt", allow_once_missing: false },
    ],
    repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0,
    colab_cli: { present: true, version: "0.4.0" },
    offline_since: null, grace_ends_at: null, paused_session_count: 0,
    created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T10:00:00Z", ...over,
  };
}

describe("RuntimeCard — RuntimeCapability 새 키(W-1)", () => {
  it("adapter_version · protocol_version · brief_transport 를 읽어 보여준다", () => {
    render(<RuntimeCard rt={rt()} />);
    const cap = screen.getByTestId("runtime-capability");
    expect(cap.getAttribute("data-kind")).toBe("claude_code");
    expect(cap.textContent).toContain("어댑터 0.74.0");
    expect(cap.textContent).toContain("ACP protocol v1");
    expect(cap.textContent).toContain("ACP _meta 시스템 프롬프트");
  });

  it("없는 능력은 결과와 함께 말한다 — usage:false 는 추정 비용, resume:false 는 콜드 스타트", () => {
    const notes = capabilityNotes({
      kind: "hermes", version: "0.20.6", adapter_version: null, logged_in: true, models: ["hermes-4"],
      protocol_version: 1, resume: false, usage: false, tool_disallow: false, brief_transport: "instruction_file", allow_once_missing: true,
    }).join(" · ");
    expect(notes).toContain("어댑터 버전 실측 실패");
    expect(notes).toContain("재진입이 늘 콜드 스타트");
    expect(notes).toContain("비용이 추정치");
    expect(notes).toContain("툴 허용 목록이 강제되지 않습니다");
    expect(notes).toContain("allow_once 부재");
  });

  it("로그인 안 된 런타임은 그 결과까지 말한다", () => {
    expect(capabilityNotes({ kind: "claude_code", logged_in: false })[0]).toContain("실행할 수 없습니다");
  });
});

describe("RuntimeCard — colab_cli 는 probe 최상위(머신 속성)", () => {
  it("present:false 면 경고 — 없으면 세션이 조용히 아무 말도 못 한다", () => {
    render(<RuntimeCard rt={rt({ colab_cli: { present: false, version: "" } })} />);
    const alarm = screen.getByTestId("colab-cli-missing");
    expect(alarm.getAttribute("role")).toBe("alert");
    expect(alarm.textContent).toContain("조용히 아무 말도 못 합니다");
    expect(screen.queryByTestId("colab-cli-present")).toBeNull();
  });

  it("present:true 면 버전만 조용히, 보고가 없으면 그 사실을 말한다", () => {
    const { rerender } = render(<RuntimeCard rt={rt()} />);
    expect(screen.getByTestId("colab-cli-present").textContent).toContain("0.4.0");
    expect(screen.queryByTestId("colab-cli-missing")).toBeNull();
    rerender(<RuntimeCard rt={rt({ colab_cli: undefined })} />);
    expect(screen.getByTestId("colab-cli-unknown")).not.toBeNull();
  });

  it("감지된 CLI 가 0개면 이 머신으로는 실행할 수 없다고 말한다", () => {
    render(<RuntimeCard rt={rt({ capabilities: [] })} />);
    expect(screen.getByTestId("runtime-no-cli").textContent).toContain("실행할 수 없습니다");
  });

  it("오프라인이면 유예 만료와 묶인 세션 수를 보여준다(FR-9.2)", () => {
    render(<RuntimeCard rt={rt({ status: "offline", offline_since: "2026-08-30T00:00:00Z", grace_ends_at: "2026-09-06T00:00:00Z", paused_session_count: 2 })} />);
    expect(screen.getByTestId("runtime-grace").textContent).toContain("세션 2개가 일시정지됨");
  });
});
