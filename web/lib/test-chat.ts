/**
 * S10 시험 대화(FR-1.8.1)의 **상태기** — 화면 밖에서 잰다.
 *
 *   idle ─open_start→ opening ─opened→ open ─turn_sent(202)→ streaming ─turn(SSE test_chat.turn)→ open
 *                                        │                       │
 *                                        └─close→ closed          └─turn_failed(410)→ closed
 *
 * 입력은 `open` 에서만 열린다(이전 턴이 진행 중이면 서버가 409 — 화면은 그 전에 잠근다). 409 는 `open` 에 남고, 410 은 닫힌
 * 것이니 `closed` 로 옮긴다. `delta` 는 ephemeral 텍스트를 이어 붙이고, `turn` 이 오면 그 텍스트를 버리고 확정 턴을 붙인다.
 * 실행 경로·토큰은 `turn` 페이로드(계약 `{test_chat_id, turn, transport, input_tokens, output_tokens}`)에서, 비용·추정
 * 배지는 그 뒤 `getTestChat` 으로 다시 읽어(`refreshed`) 채운다 — SSE 페이로드에는 비용이 없다.
 */
import type { TestChat, TestChatTurn } from "@/lib/api/types";

export type TestChatPhase = "idle" | "opening" | "open" | "streaming" | "closing" | "closed";

export interface TestChatState {
  phase: TestChatPhase;
  chat: TestChat | null;
  /** 진행 중 턴의 스트리밍 텍스트(ephemeral). 확정되면 비운다. */
  streamText: string;
  error: string | null;
}

export type TestChatAction =
  | { type: "open_start" }
  | { type: "opened"; chat: TestChat }
  | { type: "open_failed"; error: string }
  | { type: "turn_sent"; turn: TestChatTurn }
  | { type: "turn_failed"; status: number; error: string }
  | { type: "delta"; text: string }
  | { type: "turn"; turn: TestChatTurn; transport: TestChat["transport"]; input_tokens: number; output_tokens: number }
  | { type: "refreshed"; chat: TestChat }
  | { type: "close_start" }
  | { type: "closed"; chat: TestChat }
  | { type: "close_failed"; error: string }
  | { type: "reset" };

export const INITIAL: TestChatState = { phase: "idle", chat: null, streamText: "", error: null };

export function testChatReducer(s: TestChatState, a: TestChatAction): TestChatState {
  switch (a.type) {
    case "open_start":
      return { ...INITIAL, phase: "opening" };
    case "opened":
      return { phase: a.chat.status === "closed" ? "closed" : turnPending(a.chat) ? "streaming" : "open", chat: a.chat, streamText: "", error: null };
    case "open_failed":
      return { ...INITIAL, phase: "idle", error: a.error };
    case "turn_sent":
      if (!s.chat) return s;
      return { ...s, phase: "streaming", streamText: "", error: null, chat: { ...s.chat, turns: [...s.chat.turns, a.turn], updated_at: a.turn.at } };
    case "turn_failed":
      // 410 = 닫혔다(test_chat_closed). 409 = 이전 턴 진행 중 — 열린 채로 사유만.
      if (a.status === 410) return { ...s, phase: "closed", error: a.error, chat: s.chat ? { ...s.chat, status: "closed" } : null };
      return { ...s, phase: s.phase === "streaming" ? "streaming" : "open", error: a.error };
    case "delta":
      if (s.phase !== "streaming") return s;
      return { ...s, streamText: s.streamText + a.text };
    case "turn": {
      if (!s.chat) return s;
      return {
        ...s,
        phase: s.chat.status === "closed" ? "closed" : "open",
        streamText: "",
        chat: { ...s.chat, turns: [...s.chat.turns, a.turn], transport: a.transport, input_tokens: a.input_tokens, output_tokens: a.output_tokens, updated_at: a.turn.at },
      };
    }
    case "refreshed":
      return { ...s, chat: a.chat, phase: a.chat.status === "closed" ? "closed" : turnPending(a.chat) ? "streaming" : "open" };
    case "close_start":
      return { ...s, phase: "closing", error: null };
    case "closed":
      return { ...s, phase: "closed", chat: a.chat, streamText: "" };
    case "close_failed":
      return { ...s, phase: s.chat?.status === "closed" ? "closed" : "open", error: a.error };
    case "reset":
      return INITIAL;
  }
}

/** 마지막 턴이 `user` 면 에이전트 답이 아직 안 왔다(서버가 409 를 낼 상태). */
export function turnPending(chat: Pick<TestChat, "turns">): boolean {
  const last = chat.turns[chat.turns.length - 1];
  return !!last && last.role === "user";
}

export const canSend = (s: TestChatState): boolean => s.phase === "open";

/** 입력이 잠긴 이유 — 화면 문장 그대로(비활성 사유는 숨기지 않는다). 열려 있으면 null. */
export function inputLockReason(s: TestChatState): string | null {
  switch (s.phase) {
    case "open":
      return null;
    case "streaming":
      return "답을 기다리는 중입니다 — 끝나면 다시 보낼 수 있습니다";
    case "closing":
      return "닫는 중입니다";
    case "closed":
      return "닫힌 시험 대화입니다 — 새로 열어 주세요";
    default:
      return "먼저 시험 대화를 열어 주세요";
  }
}

/** 실행 경로는 계약 값(`acp`·`cli`)을 대문자로 그대로 — 설정이 맞는지 확인하는 진단값이라 사람 말로 바꾸지 않는다(FR-1.8.1). */
export const transportLabel = (t: TestChat["transport"] | undefined): string => (t ? t.toUpperCase() : "첫 답이 오면 표시");
