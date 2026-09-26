"use client";
/**
 * S10 「시험 대화」 구역(SCREEN §4.7 · FR-1.8.1) — 세션을 만들지 않고 이 에이전트와 1:1 로 대화해 **설정이 맞는지 확인**한다.
 *
 * 흐름: 프로파일·컴퓨터 선택 → 열기(createTestChat 201) → 입력(postTestChatTurn 202) → SSE `test_chat.delta` 스트리밍 →
 * `test_chat.turn` 으로 확정 → 닫기(closeTestChat). 실행 경로(acp/cli)·토큰(입력/출력)·비용(+추정 배지)은 **항상** 보인다.
 * 409(이전 턴 진행 중 · 컴퓨터 연결 끊김)·410(닫힘)은 서버 `detail` 을 그대로 화면에 — 문장을 지어내지 않는다.
 * 상태 전이는 `lib/test-chat.ts` 의 reducer 가 맡고, 이 컴포넌트는 그리기와 호출만 한다.
 *
 * 멘션 입력기는 쓰지 않는다 — 세션이 아니라서 멘션이 아무것도 깨우지 않는다(계약: 토큰 미발급, 게시·위임·사람 확인 불가).
 */
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { canSend, INITIAL, inputLockReason, testChatReducer, transportLabel } from "@/lib/test-chat";
import { clockTime } from "@/lib/time";
import type { Agent, Runtime, StreamEvent, TestChat, TestChatTurn } from "@/lib/api/types";
import "./test-chat.css";

export interface TestChatPanelProps {
  agent: Agent;
  runtimes: Runtime[];
  workspaceId: string;
}

const RUNTIME_KIND_LABEL: Record<string, string> = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" };

export function TestChatPanel({ agent, runtimes, workspaceId }: TestChatPanelProps) {
  const [state, dispatch] = useReducer(testChatReducer, INITIAL);
  const [profileId, setProfileId] = useState<string>(agent.profiles.find((p) => p.is_default)?.id ?? agent.profiles[0]?.id ?? "");
  const [runtimeId, setRuntimeId] = useState<string>("");
  const [text, setText] = useState("");
  const chatIdRef = useRef<string | null>(null);
  chatIdRef.current = state.chat?.id ?? null;
  const turnsEnd = useRef<HTMLDivElement>(null);

  const profile = agent.profiles.find((p) => p.id === profileId) ?? null;
  /** 그 프로파일의 종류를 실행할 수 있는 온라인 컴퓨터만 고른다 — 오프라인은 서버가 409 로 막지만 먼저 보이지 않는 게 낫다. */
  const candidates = useMemo(
    () => runtimes.filter((r) => r.status === "online" && (!profile || r.capabilities.some((c) => c.kind === profile.runtime_kind && c.logged_in))),
    [runtimes, profile],
  );

  const refresh = useCallback(async (id: string) => {
    try {
      const chat = await api.get("/test-chats/{testChatId}", { path: { testChatId: id } });
      dispatch({ type: "refreshed", chat });
    } catch {
      /* 스트림이 이미 턴을 확정했다 — 비용만 못 채운다 */
    }
  }, []);

  // 워크스페이스 SSE 하나를 공유해 이 채팅의 이벤트만 거른다(리뷰 R4 — 화면당 EventSource 2개 금지).
  useWorkspaceStream(workspaceId, (ev: StreamEvent) => {
    const p = ev.payload as { test_chat_id?: string; text?: string; turn?: TestChatTurn; transport?: TestChat["transport"]; input_tokens?: number; output_tokens?: number };
    if (!chatIdRef.current || p.test_chat_id !== chatIdRef.current) return;
    if (ev.type === "test_chat.delta") dispatch({ type: "delta", text: p.text ?? "" });
    if (ev.type === "test_chat.turn" && p.turn) {
      dispatch({ type: "turn", turn: p.turn, transport: p.transport ?? null, input_tokens: p.input_tokens ?? 0, output_tokens: p.output_tokens ?? 0 });
      void refresh(chatIdRef.current);
    }
  }, { enabled: !!state.chat });

  useEffect(() => { turnsEnd.current?.scrollIntoView?.({ block: "end" }); }, [state.chat?.turns.length, state.streamText]);

  async function open() {
    dispatch({ type: "open_start" });
    try {
      const chat = await api.post("/agents/{agentId}/test-chats", {
        path: { agentId: agent.id }, idempotencyKey: newIdempotencyKey(),
        body: { profile_id: profileId || null, runtime_id: runtimeId || null },
      });
      dispatch({ type: "opened", chat });
    } catch (e) {
      dispatch({ type: "open_failed", error: errorMessage(e) });
    }
  }
  async function send() {
    const chat = state.chat;
    const content = text.trim();
    if (!chat || !content) return;
    try {
      const turn = await api.post("/test-chats/{testChatId}/turns", { path: { testChatId: chat.id }, idempotencyKey: newIdempotencyKey(), body: { content } });
      setText("");
      dispatch({ type: "turn_sent", turn });
    } catch (e) {
      dispatch({ type: "turn_failed", status: isApiError(e) ? e.status : 0, error: errorMessage(e) });
    }
  }
  async function close() {
    const chat = state.chat;
    if (!chat) return;
    dispatch({ type: "close_start" });
    try {
      dispatch({ type: "closed", chat: await api.post("/test-chats/{testChatId}/close", { path: { testChatId: chat.id } }) });
    } catch (e) {
      dispatch({ type: "close_failed", error: errorMessage(e) });
    }
  }

  const chat = state.chat;
  const lock = inputLockReason(state);
  const runtimeName = chat?.runtime_id ? runtimes.find((r) => r.id === chat.runtime_id)?.name ?? "연결 끊긴 컴퓨터" : "자동 선택";

  return (
    <div className="tchat" data-testid="test-chat" data-phase={state.phase}>
      {/* 한 줄에 둔다 — 문구 자물쇠(lib/wording.test.ts)는 JSX 텍스트를 줄 단위로 읽는다. */}
      <p className="tchat__note" data-testid="test-chat-not-session">방이 아닙니다 — 메시지 게시·위임·승인 요청은 못 하고 답만 합니다. 실행 경로와 토큰을 보고 설정이 맞는지 확인하세요.</p>

      {(state.phase === "idle" || state.phase === "opening" || state.phase === "closed") && (
        <div className="tchat__setup" data-testid="test-chat-setup">
          <label className="field">
            <span className="field__label">프로파일</span>
            <select className="select" value={profileId} disabled={state.phase === "opening"} onChange={(e) => { setProfileId(e.target.value); setRuntimeId(""); }} data-testid="test-chat-profile">
              {agent.profiles.map((p) => (
                <option key={p.id} value={p.id}>{p.name}{p.is_default ? " (기본)" : ""} — {RUNTIME_KIND_LABEL[p.runtime_kind] ?? p.runtime_kind} · {p.model}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span className="field__label">컴퓨터</span>
            <select className="select" value={runtimeId} disabled={state.phase === "opening"} onChange={(e) => setRuntimeId(e.target.value)} data-testid="test-chat-runtime">
              <option value="">자동 선택 — 실행할 수 있는 온라인 컴퓨터 아무거나</option>
              {candidates.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
            </select>
          </label>
          <button
            type="button"
            className="btn btn--primary"
            disabled={state.phase === "opening" || !profileId || candidates.length === 0}
            title={candidates.length === 0 ? "이 프로파일을 실행할 수 있는 온라인 컴퓨터가 없습니다" : undefined}
            onClick={() => void open()}
            data-testid="test-chat-open"
          >
            {state.phase === "closed" ? "새로 열기" : "시험 대화 열기"}
          </button>
          {candidates.length === 0 && <p className="tchat__note" data-testid="test-chat-no-runtime">이 프로파일을 실행할 수 있는 온라인 컴퓨터가 없습니다 — 먼저 컴퓨터를 연결하세요.</p>}
        </div>
      )}

      {state.error && <p className="problem" role="alert" data-testid="test-chat-error">{state.error}</p>}

      {chat && (
        <>
          <dl className="tchat__stats" data-testid="test-chat-stats">
            <div className="tchat__stat"><dt>실행 경로</dt><dd data-testid="test-chat-transport">{transportLabel(chat.transport)}</dd></div>
            <div className="tchat__stat"><dt>입력 토큰</dt><dd data-testid="test-chat-input-tokens">{chat.input_tokens.toLocaleString("ko-KR")}</dd></div>
            <div className="tchat__stat"><dt>출력 토큰</dt><dd data-testid="test-chat-output-tokens">{chat.output_tokens.toLocaleString("ko-KR")}</dd></div>
            <div className="tchat__stat">
              <dt>비용</dt>
              <dd data-testid="test-chat-cost">
                ${chat.cost_usd.toFixed(4)}
                {chat.estimated && <span className="tchat__badge" data-testid="test-chat-estimated" title="이 컴퓨터가 사용량을 보고하지 않아 추정치입니다">추정</span>}
              </dd>
            </div>
            <div className="tchat__stat"><dt>컴퓨터</dt><dd data-testid="test-chat-runtime-name">{runtimeName}</dd></div>
            <div className="tchat__stat"><dt>상태</dt><dd data-testid="test-chat-status">{chat.status === "closed" ? "닫힘" : "열림"}</dd></div>
          </dl>

          <div className="tchat__turns" data-testid="test-chat-turns">
            {chat.turns.length === 0 && !state.streamText && <div className="tchat__empty">첫 메시지를 보내면 실행 경로와 토큰이 채워집니다.</div>}
            {chat.turns.map((t, i) => (
              <div key={i} className={`tchat__turn tchat__turn--${t.role}${t.error ? " tchat__turn--error" : ""}`} data-testid={`test-chat-turn-${t.role}`}>
                <span className="tchat__meta">{t.role === "user" ? "나" : `@${agent.name}`} · {clockTime(t.at)}{t.usage ? ` · 입력 ${t.usage.input_tokens ?? 0} / 출력 ${t.usage.output_tokens ?? 0}` : ""}</span>
                {t.error ? <span data-testid="test-chat-turn-error">{t.error}</span> : t.content}
              </div>
            ))}
            {state.phase === "streaming" && (
              <div className="tchat__turn tchat__turn--agent tchat__turn--stream" data-testid="test-chat-stream" aria-live="polite">
                <span className="tchat__meta">@{agent.name} · 답하는 중…</span>
                {state.streamText || "…"}
              </div>
            )}
            <div ref={turnsEnd} />
          </div>

          <div className="tchat__input">
            <textarea
              className="textarea"
              value={text}
              disabled={!canSend(state)}
              placeholder={lock ?? "이 에이전트에게 보낼 말"}
              aria-label="시험 대화 입력"
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void send(); } }}
              data-testid="test-chat-input"
            />
            <div className="tchat__foot">
              <span className="tchat__note" data-testid="test-chat-lock">{lock ?? "⌘/Ctrl + Enter 로 보내기"}</span>
              <div className="row">
                <button type="button" className="btn btn--sm" disabled={state.phase === "closed" || state.phase === "closing"} onClick={() => void close()} data-testid="test-chat-close">
                  {state.phase === "closing" ? "닫는 중…" : "닫기"}
                </button>
                <button type="button" className="btn btn--sm btn--primary" disabled={!canSend(state) || !text.trim()} title={lock ?? undefined} onClick={() => void send()} data-testid="test-chat-send">
                  보내기
                </button>
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
