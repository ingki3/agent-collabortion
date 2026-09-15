/**
 * 시험 대화 상태기(`lib/test-chat.ts`) — 열기 → 턴 진행 중 입력 잠금 → 확정 → 닫기, 409/410.
 */
import { describe, expect, it } from "vitest";
import { canSend, INITIAL, inputLockReason, testChatReducer, transportLabel, turnPending, type TestChatState } from "./test-chat";
import type { TestChat, TestChatTurn } from "./api/types";

const chat = (over: Partial<TestChat> = {}): TestChat => ({
  id: "tc1", workspace_id: "w1", agent_id: "a1", profile_id: "p1", user_id: "u1", runtime_id: "r1", status: "open", transport: null,
  turns: [], input_tokens: 0, output_tokens: 0, cost_usd: 0, estimated: false, created_at: "2026-09-13T00:00:00Z", updated_at: "2026-09-13T00:00:00Z", closed_at: null, ...over,
});
const userTurn: TestChatTurn = { role: "user", content: "안녕", at: "2026-09-13T00:00:01Z" };
const agentTurn: TestChatTurn = { role: "agent", content: "안녕하세요", at: "2026-09-13T00:00:03Z", usage: { input_tokens: 42, output_tokens: 30 } };

function run(actions: Parameters<typeof testChatReducer>[1][], from: TestChatState = INITIAL): TestChatState {
  return actions.reduce(testChatReducer, from);
}

describe("시험 대화 상태기", () => {
  it("열기 전엔 입력이 잠기고 사유가 있다", () => {
    expect(canSend(INITIAL)).toBe(false);
    expect(inputLockReason(INITIAL)).toBe("먼저 시험 대화를 열어 주세요");
  });

  it("열기 → open → 턴 202 → streaming(입력 잠금) → delta 누적 → turn 확정 → open(다시 보낼 수 있다)", () => {
    let s = run([{ type: "open_start" }]);
    expect(s.phase).toBe("opening");
    s = run([{ type: "opened", chat: chat() }], s);
    expect(s.phase).toBe("open");
    expect(canSend(s)).toBe(true);
    s = run([{ type: "turn_sent", turn: userTurn }], s);
    expect(s.phase).toBe("streaming");
    expect(canSend(s)).toBe(false);
    expect(inputLockReason(s)).toBe("답을 기다리는 중입니다 — 끝나면 다시 보낼 수 있습니다");
    expect(s.chat?.turns).toEqual([userTurn]);
    s = run([{ type: "delta", text: "안녕" }, { type: "delta", text: "하세요" }], s);
    expect(s.streamText).toBe("안녕하세요");
    s = run([{ type: "turn", turn: agentTurn, transport: "acp", input_tokens: 42, output_tokens: 30 }], s);
    expect(s.phase).toBe("open");
    expect(s.streamText).toBe("");
    expect(s.chat?.turns).toEqual([userTurn, agentTurn]);
    expect(s.chat?.transport).toBe("acp");
    expect(s.chat?.input_tokens).toBe(42);
    expect(s.chat?.output_tokens).toBe(30);
    expect(canSend(s)).toBe(true);
  });

  it("delta 는 streaming 일 때만 붙는다 — 늦게 온 조각이 확정 뒤에 섞이지 않는다", () => {
    const s = run([{ type: "opened", chat: chat() }, { type: "delta", text: "늦은 조각" }]);
    expect(s.streamText).toBe("");
  });

  it("409(이전 턴 진행 중)는 열린 채로 사유만, 410(닫힘)은 closed 로", () => {
    const open = run([{ type: "opened", chat: chat() }]);
    const s409 = run([{ type: "turn_failed", status: 409, error: "이전 답이 아직 오는 중입니다 — 끝난 뒤 보내 주세요" }], open);
    expect(s409.phase).toBe("open");
    expect(s409.error).toContain("이전 답이 아직");
    const s410 = run([{ type: "turn_failed", status: 410, error: "닫힌 시험 대화입니다 — 새로 열어 주세요" }], open);
    expect(s410.phase).toBe("closed");
    expect(s410.chat?.status).toBe("closed");
    expect(canSend(s410)).toBe(false);
    expect(inputLockReason(s410)).toBe("닫힌 시험 대화입니다 — 새로 열어 주세요");
  });

  it("닫기 → closing(입력 잠금) → closed · 실패하면 다시 open", () => {
    const open = run([{ type: "opened", chat: chat() }]);
    const closing = run([{ type: "close_start" }], open);
    expect(closing.phase).toBe("closing");
    expect(canSend(closing)).toBe(false);
    const closed = run([{ type: "closed", chat: chat({ status: "closed", closed_at: "2026-09-13T00:01:00Z" }) }], closing);
    expect(closed.phase).toBe("closed");
    const failed = run([{ type: "close_failed", error: "서버 오류" }], closing);
    expect(failed.phase).toBe("open");
    expect(failed.error).toBe("서버 오류");
  });

  it("열 때 실패하면 idle 로 돌아가고 사유를 남긴다(409 컴퓨터 오프라인)", () => {
    const s = run([{ type: "open_start" }, { type: "open_failed", error: "이 컴퓨터의 연결이 끊겨 있습니다 — 다른 컴퓨터를 골라 주세요" }]);
    expect(s.phase).toBe("idle");
    expect(s.chat).toBeNull();
    expect(s.error).toContain("연결이 끊겨");
  });

  it("다시 읽은 채팅(refreshed)이 진행 중 턴을 품고 있으면 streaming, 닫혔으면 closed", () => {
    const open = run([{ type: "opened", chat: chat() }]);
    expect(run([{ type: "refreshed", chat: chat({ turns: [userTurn] }) }], open).phase).toBe("streaming");
    expect(run([{ type: "refreshed", chat: chat({ status: "closed" }) }], open).phase).toBe("closed");
    expect(run([{ type: "refreshed", chat: chat({ turns: [userTurn, agentTurn], cost_usd: 0.0012 }) }], open).chat?.cost_usd).toBe(0.0012);
  });

  it("turnPending · transportLabel", () => {
    expect(turnPending({ turns: [] })).toBe(false);
    expect(turnPending({ turns: [userTurn] })).toBe(true);
    expect(turnPending({ turns: [userTurn, agentTurn] })).toBe(false);
    expect(transportLabel("acp")).toBe("ACP");
    expect(transportLabel("cli")).toBe("CLI");
    expect(transportLabel(null)).toBe("첫 답이 오면 표시");
  });
});
