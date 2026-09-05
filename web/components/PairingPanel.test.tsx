/**
 * G3 W-1·W-2 회귀 테스트 — S12 PairingPanel.
 *  W-1: 발급은 마운트당 1회(StrictMode 이중 effect), 재시도는 같은 Idempotency-Key.
 *  W-2: 셸의 공용 StreamProvider 로 온 `pairing.updated` 가 패널 4단계를 옮기고, 스트림이 열려 있어도 ready 전까지 5초 폴링이 돈다.
 */
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { PairingPanel, POLL_INTERVAL_MS, mergePairing } from "./PairingPanel";
import { StreamProvider } from "@/lib/realtime/StreamContext";
import type { Pairing, StreamEvent } from "@/lib/api/types";

vi.mock("@/lib/api/client", async (orig) => {
  const real = await orig<typeof import("@/lib/api/client")>();
  return { ...real, api: { ...real.api, post: vi.fn(), get: vi.fn() } };
});
import { api } from "@/lib/api/client";
const post = api.post as unknown as ReturnType<typeof vi.fn>;
const get = api.get as unknown as ReturnType<typeof vi.fn>;

/** 가짜 EventSource — 테스트가 open 을 알리고 프레임을 밀어 넣는다. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: MessageEvent<string>) => void) | null = null;
  listeners = new Map<string, EventListener[]>();
  closed = false;
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, l: EventListener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l]);
  }
  close() {
    this.closed = true;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  push(ev: StreamEvent) {
    const me = { data: JSON.stringify(ev) } as MessageEvent<string>;
    for (const l of this.listeners.get(ev.type) ?? []) (l as (e: MessageEvent<string>) => void)(me);
  }
  /** 살아 있는(닫히지 않은) 마지막 연결 — StrictMode 는 첫 연결을 닫고 다시 연다. */
  static live(): FakeEventSource {
    const alive = FakeEventSource.instances.filter((i) => !i.closed);
    if (!alive.length) throw new Error("no live EventSource");
    return alive[alive.length - 1];
  }
}

function pairing(status: Pairing["status"], extra: Partial<Pairing> = {}): Pairing {
  return {
    id: "p1",
    status,
    install_commands: ["curl -fsSL http://localhost:8080/install.sh | sh", "colab-daemon pair cpk_abc --server http://localhost:8080"],
    expires_at: "2026-09-06T01:00:00Z",
    created_at: "2026-09-06T00:00:00Z",
    ...extra,
  };
}
function frame(payload: Pairing): StreamEvent {
  return { id: "1", type: "pairing.updated", workspace_id: "w1", session_id: null, at: "2026-09-06T00:00:00Z", payload, ephemeral: false } as unknown as StreamEvent;
}
const READY_RUNTIME = {
  id: "rt1",
  workspace_id: "w1",
  name: "macbook",
  status: "online",
  capabilities: [{ kind: "claude_code", version: "2.1.0", logged_in: true, models: ["claude-opus-5"] }],
} as unknown as Pairing["runtime"];

const flush = () => act(async () => {
  await Promise.resolve();
  await Promise.resolve();
});

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  post.mockReset();
  get.mockReset();
  post.mockResolvedValue(pairing("waiting"));
  get.mockResolvedValue(pairing("waiting"));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("mergePairing — 단계는 뒤로 가지 않는다", () => {
  it("다른 id 는 무시, 늦게 온 낮은 단계 프레임은 무시, expired 는 항상 반영", () => {
    const ready = pairing("ready");
    expect(mergePairing(ready, pairing("connected", { id: "other" }))).toBe(ready);
    expect(mergePairing(ready, pairing("connected"))).toBe(ready);
    expect(mergePairing(pairing("waiting"), pairing("probing"))?.status).toBe("probing");
    expect(mergePairing(pairing("connected"), pairing("expired"))?.status).toBe("expired");
    expect(mergePairing(null, ready)).toBeNull();
  });
  it("서버가 뒤에 가린 설치 명령(<pairing_token>)·토큰은 발급 응답의 것을 유지한다", () => {
    const issued = pairing("waiting", { pairing_token: "cpk_abc" });
    const redacted = pairing("connected", { install_commands: ["curl … | sh", "colab-daemon pair <pairing_token> --server http://localhost:8080"] });
    const m = mergePairing(issued, redacted)!;
    expect(m.status).toBe("connected");
    expect(m.install_commands).toEqual(issued.install_commands);
    expect(m.pairing_token).toBe("cpk_abc");
  });
});

describe("W-1 — createPairing 은 마운트당 정확히 1회", () => {
  it("StrictMode(이중 effect)에서도 POST 1회, Idempotency-Key 를 보낸다", async () => {
    render(
      <StrictMode>
        <PairingPanel workspaceId="w1" canManage />
      </StrictMode>,
    );
    await flush();
    expect((await screen.findByTestId("pairing-panel")).getAttribute("data-status")).toBe("waiting");
    expect(post).toHaveBeenCalledTimes(1);
    const opts = post.mock.calls[0][1] as { idempotencyKey?: string };
    expect(typeof opts.idempotencyKey).toBe("string");
    expect(opts.idempotencyKey!.length).toBeGreaterThan(8);
  });

  it("실패 뒤 '다시 시도' 는 같은 Idempotency-Key 로 재요청한다(서버가 같은 페어링을 돌려준다)", async () => {
    post.mockRejectedValueOnce(new Error("network"));
    render(<PairingPanel workspaceId="w1" canManage />);
    await flush();
    expect(await screen.findByText("network")).toBeTruthy();
    fireEvent.click(screen.getByText("다시 시도"));
    await flush();
    expect(await screen.findByTestId("pairing-panel")).toBeTruthy();
    expect(post).toHaveBeenCalledTimes(2);
    const k1 = (post.mock.calls[0][1] as { idempotencyKey?: string }).idempotencyKey;
    const k2 = (post.mock.calls[1][1] as { idempotencyKey?: string }).idempotencyKey;
    expect(k1).toBe(k2);
  });
});

describe("W-2 — 셸 공용 StreamProvider 경로의 pairing.updated 가 패널에 닿는다", () => {
  it("waiting → connected → probing → ready 로 갱신되고 onReady 가 1회, 감지된 CLI 요약이 보인다", async () => {
    const onReady = vi.fn();
    render(
      <StrictMode>
        <StreamProvider workspaceId="w1">
          <PairingPanel workspaceId="w1" canManage onReady={onReady} />
        </StreamProvider>
      </StrictMode>,
    );
    await flush();
    const status = await screen.findByTestId("pairing-status");
    expect(status.getAttribute("data-status")).toBe("waiting");
    // 셸 연결은 1개(StrictMode 가 닫은 첫 연결 제외)
    expect(FakeEventSource.instances.filter((i) => !i.closed)).toHaveLength(1);
    const es = FakeEventSource.live();
    act(() => es.open());
    expect(screen.getByTestId("pairing-panel").getAttribute("data-conn")).toBe("open");

    const shownCmd2 = screen.getByTestId("install-cmd-2").querySelector("code")!.textContent;
    act(() => es.push(frame(pairing("connected", { install_commands: ["curl … | sh", "colab-daemon pair <pairing_token> --server http://localhost:8080"] }))));
    expect(status.getAttribute("data-status")).toBe("connected");
    expect(screen.getByTestId("install-cmd-2").querySelector("code")!.textContent).toBe(shownCmd2);
    act(() => es.push(frame(pairing("probing"))));
    expect(status.getAttribute("data-status")).toBe("probing");
    act(() => es.push(frame(pairing("ready", { runtime: READY_RUNTIME }))));
    expect(status.getAttribute("data-status")).toBe("ready");
    expect(screen.getByTestId("pairing-capabilities").textContent).toContain("Claude Code 감지됨 · 로그인됨 · 모델 1개");
    expect(screen.getByTestId("pairing-capabilities").textContent).toContain("Hermes 없음");
    expect(onReady).toHaveBeenCalledTimes(1);
    expect(onReady.mock.calls[0][0].status).toBe("ready");
    // 다른 페어링의 프레임은 무시
    act(() => es.push(frame(pairing("waiting", { id: "p-other" }))));
    expect(status.getAttribute("data-status")).toBe("ready");
  });

  it("스트림이 open 이어도 ready 전까지 5초 폴링(GET pairing)이 돌고, ready 뒤에는 멈춘다", async () => {
    vi.useFakeTimers();
    render(<PairingPanel workspaceId="w1" canManage />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByTestId("pairing-status").getAttribute("data-status")).toBe("waiting");
    act(() => FakeEventSource.live().open());
    expect(get).toHaveBeenCalledTimes(0);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });
    expect(get).toHaveBeenCalledTimes(1);
    expect((get.mock.calls[0][1] as { path: { pairingId: string } }).path.pairingId).toBe("p1");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });
    expect(get).toHaveBeenCalledTimes(2);
    // 폴링 응답이 ready 를 가져오면 단계가 옮겨지고 폴링이 멈춘다(이벤트 유실 대비 경로)
    get.mockResolvedValue(pairing("ready", { runtime: READY_RUNTIME }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });
    expect(screen.getByTestId("pairing-status").getAttribute("data-status")).toBe("ready");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    });
    expect(get).toHaveBeenCalledTimes(3);
  });
});
