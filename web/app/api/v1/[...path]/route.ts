/**
 * 목 API 진입점 — COLAB_MOCK_API=1 일 때만 산다. 실서버 모드에서는 next.config 의 beforeFiles rewrite 가
 * /api/v1/* 를 :8080 으로 보내므로 여기까지 오지 않는다.
 */
import { dispatch, type Req } from "@/lib/mock/handlers";

export const dynamic = "force-dynamic";

function parseCookies(h: string | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of (h ?? "").split(";")) {
    const [k, ...v] = part.trim().split("=");
    if (k) out[k] = decodeURIComponent(v.join("="));
  }
  return out;
}

async function handle(request: Request): Promise<Response> {
  if (process.env.COLAB_MOCK_API !== "1") {
    return Response.json({ type: "about:blank", title: "mock disabled", status: 404, code: "mock_disabled", detail: "COLAB_MOCK_API=1 이 아니면 목 API 는 꺼져 있습니다" }, { status: 404, headers: { "Content-Type": "application/problem+json" } });
  }
  const url = new URL(request.url);
  let body: unknown = undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    const text = await request.text();
    if (text) {
      try { body = JSON.parse(text); } catch { body = undefined; }
    }
  }
  const req: Req = {
    method: request.method,
    path: url.pathname.replace(/^\/api\/v1/, "") || "/",
    query: url.searchParams,
    headers: request.headers,
    body,
    cookies: parseCookies(request.headers.get("cookie")),
  };
  const res = await dispatch(req);
  if (res.stream) return new Response(res.stream, { status: res.status, headers: res.headers });
  if (res.body === undefined) return new Response(null, { status: res.status, headers: res.headers });
  return new Response(JSON.stringify(res.body), { status: res.status, headers: { "Content-Type": "application/json", ...res.headers } });
}

export { handle as GET, handle as POST, handle as PATCH, handle as DELETE };
