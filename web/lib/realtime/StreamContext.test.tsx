import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { StreamProvider, useWorkspaceStream } from "./StreamContext";
import type { StreamEvent } from "@/lib/api/types";

/** 가짜 EventSource — 생성 횟수와 URL 만 기록하고, 테스트가 프레임을 밀어 넣는다. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readyState = 0;
  url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: MessageEvent<string>) => void) | null = null;
  listeners = new Map<string, EventListener[]>();
  closed = false;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, l: EventListener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l]);
  }
  close() {
    this.closed = true;
  }
  /** 서버가 프레임을 보낸 것처럼 */
  push(ev: StreamEvent) {
    const me = { data: JSON.stringify(ev) } as MessageEvent<string>;
    for (const l of this.listeners.get(ev.type) ?? []) (l as (e: MessageEvent<string>) => void)(me);
  }
}

function frame(type: StreamEvent["type"], session_id: string | null, payload: Record<string, unknown> = {}): StreamEvent {
  return { id: "1", type, workspace_id: "w1", session_id, at: "2026-09-06T00:00:00Z", payload, ephemeral: false } as StreamEvent;
}

function Consumer({ ws, onEvent }: { ws: string; onEvent: (ev: StreamEvent) => void }) {
  useWorkspaceStream(ws, onEvent);
  return null;
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("StreamProvider / useWorkspaceStream — 한 화면 SSE 연결 1개 (R4)", () => {
  it("셸 + 화면 구독자 2개가 있어도 EventSource 는 1개만 열리고, 둘 다 같은 프레임을 받는다", () => {
    const shell = vi.fn<(ev: StreamEvent) => void>();
    const page = vi.fn<(ev: StreamEvent) => void>();
    render(
      <StreamProvider workspaceId="w1">
        <Consumer ws="w1" onEvent={shell} />
        <Consumer ws="w1" onEvent={page} />
      </StreamProvider>,
    );
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0].url).toContain("/workspaces/w1/stream");
    expect(FakeEventSource.instances[0].url).not.toContain("session_id=");

    act(() => FakeEventSource.instances[0].push(frame("session.updated", "s1", { id: "s1" })));
    expect(shell).toHaveBeenCalledTimes(1);
    expect(page).toHaveBeenCalledTimes(1);
    expect(page.mock.calls[0][0].session_id).toBe("s1");
  });

  it("Provider 밖(온보딩 S12)에서는 자기 연결을 연다", () => {
    render(<Consumer ws="w1" onEvent={() => {}} />);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("구독자가 사라져도 공유 연결은 닫히지 않는다", () => {
    const r = render(
      <StreamProvider workspaceId="w1">
        <Consumer ws="w1" onEvent={() => {}} />
      </StreamProvider>,
    );
    r.rerender(<StreamProvider workspaceId="w1">{null}</StreamProvider>);
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0].closed).toBe(false);
    r.unmount();
    expect(FakeEventSource.instances[0].closed).toBe(true);
  });
});
