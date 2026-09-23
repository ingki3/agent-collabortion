/**
 * 화면 테스트용 다리(T-R2-W3) — `fetch` 를 목 `dispatch` 로 바로 잇는다. 화면이 `api` 클라이언트로 부르는 경로·본문·Problem 이
 * 브라우저 → `/api/v1/*` → 목과 **같은 길**을 지난다(api 를 vi.fn 으로 갈아 끼우면 경로 오타·본문 모양 오류를 못 잡는다).
 *
 * 쓰는 법: `const bridge = await installFetchBridge()` → `bridge.login("seoyeon@colab.dev")` 로 사람을 바꾼다. 테스트가 끝나면 `bridge.restore()`.
 */
import { vi } from "vitest";
import { dispatch, type Req } from "./handlers";
import { resetStore } from "./store";

export interface FetchBridge {
  login: (email?: string) => Promise<void>;
  /** 테스트가 목을 직접 부를 때(시드 등) — 화면과 같은 쿠키로. */
  call: <T = unknown>(method: string, path: string, body?: unknown) => Promise<{ status: number; body: T }>;
  restore: () => void;
}

export async function installFetchBridge(): Promise<FetchBridge> {
  resetStore();
  let cookie = "";
  const run = async (method: string, url: string, body: unknown, headers: Headers) => {
    const u = new URL(url, "http://localhost");
    const req: Req = { method, path: u.pathname.replace(/^\/api\/v1/, ""), query: u.searchParams, headers, body, cookies: cookie ? { colab_session: cookie } : {} };
    return dispatch(req);
  };
  const orig = globalThis.fetch;
  const fake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const res = await run(init?.method ?? "GET", String(input), init?.body ? JSON.parse(String(init.body)) : undefined, new Headers(init?.headers as HeadersInit));
    return new Response(res.status === 204 || res.body === undefined ? null : JSON.stringify(res.body), { status: res.status, headers: res.headers });
  });
  globalThis.fetch = fake as unknown as typeof fetch;
  const bridge: FetchBridge = {
    async login(email = "demo@colab.dev") {
      const res = await run("POST", "/auth/login", { email, password: "password123" }, new Headers());
      cookie = /colab_session=([^;]+)/.exec((res.headers ?? {})["Set-Cookie"] ?? "")?.[1] ?? "";
    },
    async call(method, path, body) {
      const res = await run(method, path, body, new Headers());
      return { status: res.status, body: res.body as never };
    },
    restore() {
      globalThis.fetch = orig;
    },
  };
  await bridge.login();
  return bridge;
}
