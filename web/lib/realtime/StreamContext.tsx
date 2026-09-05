"use client";
/**
 * 워크스페이스 SSE **하나**를 컨텍스트로 올린다(openapi streamEvents "SSE 하나", SCREEN §6).
 * 앱 셸이 `StreamProvider` 로 연결 1개를 열고, 화면은 `useWorkspaceStream` 으로 그 연결을 구독해 자기 범위(session_id 등)로 걸러 쓴다.
 * 한 화면에 EventSource 가 2개 이상 상주하지 않는다(리뷰 R4).
 * 셸 밖(온보딩 S4 의 S12 인라인)처럼 Provider 가 없으면 자기 연결을 연다.
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { openStream, useStream, type ConnectionState } from "./stream";
import type { StreamEvent } from "@/lib/api/types";

export interface StreamListener {
  onEvent: (ev: StreamEvent) => void;
  onResync?: (reason: string) => void;
}

export interface StreamContextValue {
  workspaceId: string;
  state: ConnectionState;
  subscribe: (l: StreamListener) => () => void;
}

const StreamContext = createContext<StreamContextValue | null>(null);

export function StreamProvider({ workspaceId, children }: { workspaceId: string; children: React.ReactNode }) {
  const [state, setState] = useState<ConnectionState>("connecting");
  const listeners = useRef(new Set<StreamListener>());

  useEffect(() => {
    return openStream(workspaceId, {
      onEvent: (ev) => {
        for (const l of listeners.current) l.onEvent(ev);
      },
      onResync: (reason) => {
        for (const l of listeners.current) l.onResync?.(reason);
      },
      onState: setState,
    });
  }, [workspaceId]);

  const subscribe = useCallback((l: StreamListener) => {
    listeners.current.add(l);
    return () => {
      listeners.current.delete(l);
    };
  }, []);

  const value = useMemo<StreamContextValue>(() => ({ workspaceId, state, subscribe }), [workspaceId, state, subscribe]);
  return <StreamContext.Provider value={value}>{children}</StreamContext.Provider>;
}

/** 셸의 연결 상태(배너용). Provider 밖이면 null. */
export function useStreamState(): ConnectionState | null {
  return useContext(StreamContext)?.state ?? null;
}

/**
 * 공유 연결을 구독한다. Provider 가 같은 워크스페이스를 열고 있으면 새 연결을 만들지 않고,
 * 없으면(셸 밖) `useStream` 으로 자기 연결을 연다. handler/onResync 는 ref 로 잡아 재구독하지 않는다.
 */
export function useWorkspaceStream(
  workspaceId: string | null | undefined,
  handler: (ev: StreamEvent) => void,
  options: { onResync?: (reason: string) => void; enabled?: boolean } = {},
): ConnectionState {
  const ctx = useContext(StreamContext);
  const enabled = options.enabled ?? true;
  const shared = !!ctx && !!workspaceId && ctx.workspaceId === workspaceId;
  const handlerRef = useRef(handler);
  const resyncRef = useRef(options.onResync);
  handlerRef.current = handler;
  resyncRef.current = options.onResync;

  const own = useStream(workspaceId, (ev) => handlerRef.current(ev), {
    onResync: (r) => resyncRef.current?.(r),
    enabled: enabled && !shared,
  });

  const subscribe = ctx?.subscribe;
  useEffect(() => {
    if (!shared || !enabled || !subscribe) return;
    return subscribe({ onEvent: (ev) => handlerRef.current(ev), onResync: (r) => resyncRef.current?.(r) });
  }, [shared, enabled, subscribe]);

  return shared ? ctx.state : own;
}
