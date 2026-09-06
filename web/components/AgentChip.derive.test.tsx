/**
 * 파생 상태(PRD FR-1.3 · W-5) — **`working` 은 `running` task 뿐**이다.
 * `dispatched`·`preparing` 은 아직 턴이 시작되지 않았고, 그것을 working 으로 세면 데몬이 claim 만 하고 멈춰도
 * 칩이 계속 "작업 중"이라 침묵과 실행을 구분할 수 없다.
 */
import { describe, expect, it } from "vitest";
import { AGENT_STATUS_ORDER, deriveAgentStatus } from "./AgentChip";

describe("deriveAgentStatus — FR-1.3 우선순위", () => {
  it("running 만 working 이다 — dispatched·preparing 은 아니다(W-5)", () => {
    expect(deriveAgentStatus({ taskStatuses: ["running"] })).toBe("working");
    expect(deriveAgentStatus({ taskStatuses: ["dispatched"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["preparing"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["dispatched", "preparing", "queued"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["dispatched", "running"] })).toBe("working");
  });

  it("위에서부터 첫 번째로 맞는 것 — disabled > offline > error > working > waiting_human > idle", () => {
    expect(AGENT_STATUS_ORDER).toEqual(["disabled", "offline", "error", "working", "waiting_human", "idle"]);
    expect(deriveAgentStatus({ disabled: true, offline: true, error: true, taskStatuses: ["running"] })).toBe("disabled");
    expect(deriveAgentStatus({ offline: true, error: true, taskStatuses: ["running"] })).toBe("offline");
    expect(deriveAgentStatus({ error: true, taskStatuses: ["running"] })).toBe("error");
    expect(deriveAgentStatus({ taskStatuses: ["waiting_human"] })).toBe("waiting_human");
    expect(deriveAgentStatus({})).toBe("idle");
  });

  it("blocked·paused lane 은 파생에 들어가지 않는다(C1′) — 그 에이전트는 idle 이고 이유는 lane 카드가 말한다", () => {
    expect(deriveAgentStatus({ taskStatuses: ["paused"] })).toBe("idle");
    expect(deriveAgentStatus({ taskStatuses: ["failed", "completed", "cancelled"] })).toBe("idle");
  });
});
