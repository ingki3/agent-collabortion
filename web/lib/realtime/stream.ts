/**
 * SSE 구독(openapi `GET /workspaces/{id}/stream`, SCREEN §6).
 * - 브라우저 EventSource 는 끊기면 자동 재연결하고 Last-Event-ID 를 스스로 보낸다.
 * - 끊긴 동안 상단 배너("실시간 연결 끊김 · 재연결 중")를 띄우도록 상태를 노출한다.
 * - `resync` 프레임(보존 창 밖)이면 화면이 REST 로 다시 읽어야 하므로 onResync 를 부른다.
 */
import { useEffect, useRef, useState } from "react";
import { API_BASE } from "@/lib/api/client";
import type { StreamEvent, StreamEventType } from "@/lib/api/types";

export type ConnectionState = "connecting" | "open" | "reconnecting";

export interface StreamOptions {
  sessionIds?: string[];
  onEvent: (ev: StreamEvent) => void;
  onResync?: (reason: string) => void;
  onState?: (s: ConnectionState) => void;
}

export function streamUrl(workspaceId: string, sessionIds?: string[]): string {
  const q = new URLSearchParams();
  if (sessionIds && sessionIds.length) q.set("session_id", sessionIds.join(","));
  const s = q.toString();
  return `${API_BASE}/workspaces/${encodeURIComponent(workspaceId)}/stream${s ? `?${s}` : ""}`;
}

export const STREAM_EVENT_TYPES: readonly StreamEventType[] = [
  "resync",
  "session.updated",
  "session.completion_progress",
  "participant.updated",
  "lane.updated",
  "task.updated",
  "task_event.appended",
  "task_event.superseded",
  "message.created",
  "message.updated",
  "message.delta",
  "agent.typing",
  "hitl.created",
  "hitl.updated",
  "artifact.created",
  "decision.created",
  "inbox.item_created",
  "inbox.item_updated",
  "inbox.summary",
  "runtime.updated",
  "pairing.updated",
  "workdir.updated",
  "cost.updated",
  "test_chat.delta",
  "test_chat.turn",
];

/** EventSource 를 열고 닫는 함수를 돌려준다. React 밖에서도 쓸 수 있다. */
export function openStream(workspaceId: string, opts: StreamOptions): () => void {
  if (typeof EventSource === "undefined") return () => {};
  const es = new EventSource(streamUrl(workspaceId, opts.sessionIds), { withCredentials: true });
  let everOpened = false;
  opts.onState?.("connecting");
  es.onopen = () => {
    everOpened = true;
    opts.onState?.("open");
  };
  es.onerror = () => {
    // readyState CONNECTING = 자동 재연결 중, CLOSED = 포기(서버 오류 등)
    opts.onState?.(everOpened || es.readyState !== EventSource.CLOSED ? "reconnecting" : "reconnecting");
  };
  const handler = (raw: MessageEvent<string>) => {
    let ev: StreamEvent;
    try {
      ev = JSON.parse(raw.data) as StreamEvent;
    } catch {
      return;
    }
    if (ev.type === "resync") {
      opts.onResync?.(String((ev.payload as { reason?: string }).reason ?? "out_of_window"));
      return;
    }
    opts.onEvent(ev);
  };
  for (const t of STREAM_EVENT_TYPES) es.addEventListener(t, handler as EventListener);
  // event: 없이 오는 프레임도 받는다
  es.onmessage = handler;
  return () => es.close();
}

/**
 * React 훅. handler 는 ref 로 잡아 두어 매 렌더마다 재구독하지 않는다.
 * sessionIds 가 바뀌면 재구독한다(문자열로 비교).
 */
export function useStream(
  workspaceId: string | null | undefined,
  handler: (ev: StreamEvent) => void,
  options: { sessionIds?: string[]; onResync?: (reason: string) => void; enabled?: boolean } = {},
): ConnectionState {
  const [state, setState] = useState<ConnectionState>("connecting");
  const handlerRef = useRef(handler);
  const resyncRef = useRef(options.onResync);
  handlerRef.current = handler;
  resyncRef.current = options.onResync;
  const key = (options.sessionIds ?? []).join(",");
  const enabled = options.enabled ?? true;

  useEffect(() => {
    if (!workspaceId || !enabled) return;
    const close = openStream(workspaceId, {
      sessionIds: key ? key.split(",") : undefined,
      onEvent: (ev) => handlerRef.current(ev),
      onResync: (r) => resyncRef.current?.(r),
      onState: setState,
    });
    return close;
  }, [workspaceId, key, enabled]);

  return state;
}
