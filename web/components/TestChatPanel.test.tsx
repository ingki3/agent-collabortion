/**
 * S10 「시험 대화」 — 열기 → 입력 → 202 → SSE delta 스트리밍 → turn 확정(실행 경로·토큰) → 닫기. 409·410 은 서버 문장 그대로.
 * 실행 경로·토큰·비용·추정 배지가 **항상** 보인다(FR-1.8.1 "설정이 틀렸는지 확인"). "세션이 아니다" 안내 한 줄.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent, Runtime, StreamEvent, TestChat } from "@/lib/api/types";
import { ApiError } from "@/lib/api/client";

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { get: (...a: unknown[]) => get(...a), post: (...a: unknown[]) => post(...a) } };
});

let streamHandler: ((ev: StreamEvent) => void) | null = null;
vi.mock("@/lib/realtime/StreamContext", () => ({
  useWorkspaceStream: (_ws: string, handler: (ev: StreamEvent) => void) => {
    streamHandler = handler;
    return "open";
  },
}));

import { TestChatPanel } from "./TestChatPanel";

const agent: Agent = {
  id: "a1", workspace_id: "w1", name: "Lead", role: "lead", role_description: "팀을 이끈다", instructions: "", tools: [], owner_id: "u1",
  respond_to: "workspace", respond_to_allowlist: [], max_concurrent_tasks: 3, status: "idle", invitable: { allowed: true },
  profiles: [
    { id: "p1", agent_id: "a1", name: "default", runtime_kind: "claude_code", model: "claude-sonnet-5", options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null, created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" },
    { id: "p2", agent_id: "a1", name: "hermes", runtime_kind: "hermes", model: "hermes-4", options: {}, env: {}, args: [], is_default: false, fallback_profile_id: null, created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z" },
  ],
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z",
};
const rt = (over: Partial<Runtime> = {}): Runtime => ({
  id: "r1", workspace_id: "w1", name: "office-pc", host: null, status: "online", daemon_version: "0.4.0", last_seen_at: null,
  capabilities: [{ kind: "claude_code", logged_in: true, models: ["claude-sonnet-5"], usage: true }],
  repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null, paused_session_count: 0,
  created_at: "2026-09-06T09:00:00Z", updated_at: "2026-09-06T09:00:00Z", ...over,
});
const chat = (over: Partial<TestChat> = {}): TestChat => ({
  id: "tc1", workspace_id: "w1", agent_id: "a1", profile_id: "p1", user_id: "u1", runtime_id: "r1", status: "open", transport: null,
  turns: [], input_tokens: 0, output_tokens: 0, cost_usd: 0, estimated: false, created_at: "2026-09-13T00:00:00Z", updated_at: "2026-09-13T00:00:00Z", closed_at: null, ...over,
});
const ev = (type: StreamEvent["type"], payload: Record<string, unknown>): StreamEvent => ({ id: "1", type, at: "2026-09-13T00:00:02Z", workspace_id: "w1", session_id: null, ephemeral: type === "test_chat.delta", payload });

beforeEach(() => { get.mockReset(); post.mockReset(); streamHandler = null; });
afterEach(cleanup);

async function openChat(runtimes = [rt()]) {
  post.mockImplementationOnce(async () => chat());
  render(<TestChatPanel agent={agent} runtimes={runtimes} workspaceId="w1" />);
  fireEvent.click(screen.getByTestId("test-chat-open"));
  await screen.findByTestId("test-chat-stats");
}

describe("TestChatPanel", () => {
  it("'세션이 아니다' 안내가 항상 있고, 열기 전에는 프로파일·컴퓨터 선택만 있다", () => {
    render(<TestChatPanel agent={agent} runtimes={[rt()]} workspaceId="w1" />);
    expect(screen.getByTestId("test-chat-not-session").textContent).toContain("메시지 게시·위임·승인 요청은 못 하고");
    expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("idle");
    expect((screen.getByTestId("test-chat-profile") as HTMLSelectElement).value).toBe("p1");
    expect(screen.queryByTestId("test-chat-input")).toBeNull();
  });

  it("실행할 수 있는 온라인 컴퓨터가 없으면 열기가 비활성 + 사유", () => {
    render(<TestChatPanel agent={agent} runtimes={[rt({ status: "offline" })]} workspaceId="w1" />);
    expect((screen.getByTestId("test-chat-open") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("test-chat-no-runtime").textContent).toContain("온라인 컴퓨터가 없습니다");
  });

  it("프로파일을 바꾸면 그 종류를 실행하는 컴퓨터만 후보다", () => {
    render(<TestChatPanel agent={agent} runtimes={[rt(), rt({ id: "r2", name: "hermes-box", capabilities: [{ kind: "hermes", logged_in: true, models: ["hermes-4"], usage: false }] })]} workspaceId="w1" />);
    const opts = () => [...(screen.getByTestId("test-chat-runtime") as HTMLSelectElement).options].map((o) => o.textContent);
    expect(opts()).toEqual(["자동 선택 — 실행할 수 있는 온라인 컴퓨터 아무거나", "office-pc"]);
    fireEvent.change(screen.getByTestId("test-chat-profile"), { target: { value: "p2" } });
    expect(opts()).toEqual(["자동 선택 — 실행할 수 있는 온라인 컴퓨터 아무거나", "hermes-box"]);
  });

  it("열기 → createTestChat(profile_id·runtime_id) → 실행 경로·토큰·비용이 보인다(아직 첫 답 전)", async () => {
    await openChat();
    expect(post.mock.calls[0][0]).toBe("/agents/{agentId}/test-chats");
    expect(post.mock.calls[0][1].body).toEqual({ profile_id: "p1", runtime_id: null });
    expect(screen.getByTestId("test-chat-transport").textContent).toBe("첫 답이 오면 표시");
    expect(screen.getByTestId("test-chat-input-tokens").textContent).toBe("0");
    expect(screen.getByTestId("test-chat-cost").textContent).toContain("$0.0000");
    expect(screen.getByTestId("test-chat-runtime-name").textContent).toBe("office-pc");
    expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("open");
  });

  it("보내기 202 → 입력 잠금(사유) → delta 스트리밍 → turn 확정(경로·토큰) → 다시 열림 → getTestChat 으로 비용", async () => {
    await openChat();
    post.mockImplementationOnce(async () => ({ role: "user", content: "안녕", at: "2026-09-13T00:00:01Z" }));
    get.mockImplementationOnce(async () => chat({ turns: [{ role: "user", content: "안녕", at: "2026-09-13T00:00:01Z" }, { role: "agent", content: "안녕하세요", at: "2026-09-13T00:00:03Z" }], transport: "acp", input_tokens: 42, output_tokens: 30, cost_usd: 0.00057 }));
    fireEvent.change(screen.getByTestId("test-chat-input"), { target: { value: "안녕" } });
    fireEvent.click(screen.getByTestId("test-chat-send"));
    await waitFor(() => expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("streaming"));
    expect(post.mock.calls[1][0]).toBe("/test-chats/{testChatId}/turns");
    expect(post.mock.calls[1][1].body).toEqual({ content: "안녕" });
    expect(post.mock.calls[1][1].idempotencyKey).toBeTruthy();
    expect((screen.getByTestId("test-chat-input") as HTMLTextAreaElement).disabled).toBe(true);
    expect(screen.getByTestId("test-chat-lock").textContent).toBe("답을 기다리는 중입니다 — 끝나면 다시 보낼 수 있습니다");
    expect(screen.getAllByTestId("test-chat-turn-user")).toHaveLength(1);
    act(() => { streamHandler?.(ev("test_chat.delta", { test_chat_id: "tc1", text: "안녕" })); });
    act(() => { streamHandler?.(ev("test_chat.delta", { test_chat_id: "tc1", text: "하세요" })); });
    expect(screen.getByTestId("test-chat-stream").textContent).toContain("안녕하세요");
    // 다른 채팅의 조각은 무시한다.
    act(() => { streamHandler?.(ev("test_chat.delta", { test_chat_id: "other", text: "잡음" })); });
    expect(screen.getByTestId("test-chat-stream").textContent).not.toContain("잡음");
    act(() => {
      streamHandler?.(ev("test_chat.turn", { test_chat_id: "tc1", turn: { role: "agent", content: "안녕하세요", at: "2026-09-13T00:00:03Z", usage: { input_tokens: 42, output_tokens: 30 } }, transport: "acp", input_tokens: 42, output_tokens: 30 }));
    });
    expect(screen.queryByTestId("test-chat-stream")).toBeNull();
    expect(screen.getByTestId("test-chat-turn-agent").textContent).toContain("안녕하세요");
    expect(screen.getByTestId("test-chat-transport").textContent).toBe("ACP");
    expect(screen.getByTestId("test-chat-input-tokens").textContent).toBe("42");
    expect(screen.getByTestId("test-chat-output-tokens").textContent).toBe("30");
    expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("open");
    expect((screen.getByTestId("test-chat-input") as HTMLTextAreaElement).disabled).toBe(false);
    await waitFor(() => expect(screen.getByTestId("test-chat-cost").textContent).toContain("$0.0006"));
    expect(get).toHaveBeenCalledWith("/test-chats/{testChatId}", { path: { testChatId: "tc1" } });
  });

  it("추정 배지 — 사용량을 보고하지 않는 컴퓨터면 비용 옆에 '추정'", async () => {
    post.mockImplementationOnce(async () => chat({ estimated: true }));
    render(<TestChatPanel agent={agent} runtimes={[rt()]} workspaceId="w1" />);
    fireEvent.click(screen.getByTestId("test-chat-open"));
    await screen.findByTestId("test-chat-estimated");
  });

  it("409(이전 턴 진행 중)는 서버 문장을 보이고 열린 채로 남는다", async () => {
    await openChat();
    post.mockImplementationOnce(async () => { throw new ApiError({ type: "about:blank", title: "지금은 할 수 없음", status: 409, code: "turn_in_progress", detail: "이전 답이 아직 오는 중입니다 — 끝난 뒤 보내 주세요" }); });
    fireEvent.change(screen.getByTestId("test-chat-input"), { target: { value: "또" } });
    fireEvent.click(screen.getByTestId("test-chat-send"));
    await waitFor(() => expect(screen.getByTestId("test-chat-error").textContent).toBe("이전 답이 아직 오는 중입니다 — 끝난 뒤 보내 주세요"));
    expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("open");
  });

  it("410(닫힘)은 closed 로 — 입력 잠금 + '새로 열기'", async () => {
    await openChat();
    post.mockImplementationOnce(async () => { throw new ApiError({ type: "about:blank", title: "더 이상 쓸 수 없음", status: 410, code: "test_chat_closed", detail: "닫힌 시험 대화입니다 — 새로 열어 주세요" }); });
    fireEvent.change(screen.getByTestId("test-chat-input"), { target: { value: "x" } });
    fireEvent.click(screen.getByTestId("test-chat-send"));
    await waitFor(() => expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("closed"));
    expect(screen.getByTestId("test-chat-status").textContent).toBe("닫힘");
    expect((screen.getByTestId("test-chat-input") as HTMLTextAreaElement).disabled).toBe(true);
    expect(screen.getByTestId("test-chat-open").textContent).toBe("새로 열기");
  });

  it("열 때 컴퓨터가 오프라인이면(409 runtime_offline) 사유를 보이고 다시 고를 수 있다", async () => {
    post.mockImplementationOnce(async () => { throw new ApiError({ type: "about:blank", title: "지금은 할 수 없음", status: 409, code: "runtime_offline", detail: "이 컴퓨터의 연결이 끊겨 있습니다 — 다른 컴퓨터를 골라 주세요" }); });
    render(<TestChatPanel agent={agent} runtimes={[rt()]} workspaceId="w1" />);
    fireEvent.change(screen.getByTestId("test-chat-runtime"), { target: { value: "r1" } });
    fireEvent.click(screen.getByTestId("test-chat-open"));
    await waitFor(() => expect(screen.getByTestId("test-chat-error").textContent).toContain("연결이 끊겨"));
    expect(post.mock.calls[0][1].body).toEqual({ profile_id: "p1", runtime_id: "r1" });
    expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("idle");
    expect((screen.getByTestId("test-chat-open") as HTMLButtonElement).disabled).toBe(false);
  });

  it("닫기 → closeTestChat → closed(닫힘)", async () => {
    await openChat();
    post.mockImplementationOnce(async () => chat({ status: "closed", closed_at: "2026-09-13T00:01:00Z" }));
    fireEvent.click(screen.getByTestId("test-chat-close"));
    await waitFor(() => expect(screen.getByTestId("test-chat").getAttribute("data-phase")).toBe("closed"));
    expect(post.mock.calls[1][0]).toBe("/test-chats/{testChatId}/close");
    expect((screen.getByTestId("test-chat-close") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("test-chat-lock").textContent).toBe("닫힌 시험 대화입니다 — 새로 열어 주세요");
  });
});
