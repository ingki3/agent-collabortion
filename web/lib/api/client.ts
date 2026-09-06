/**
 * openapi.yaml → schema.d.ts(생성물, `npm run gen:api`) 위의 얇은 fetch 래퍼.
 * - 경로·메서드·요청 본문·응답 타입이 openapi 와 묶인다(없는 경로는 타입 오류).
 * - 오류는 RFC 9457 Problem(application/problem+json)을 ApiError 로 던진다.
 * - 쿠키 인증(UserSession)이므로 credentials: "same-origin". 서버는 next.config 의 rewrite 로 같은 오리진.
 */
import type { components, paths } from "./schema";

export type Problem = components["schemas"]["Problem"];

export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem;
  constructor(problem: Problem) {
    super(problem.detail ?? problem.title);
    this.name = "ApiError";
    this.status = problem.status;
    this.problem = problem;
  }
  /** 기계용 식별자(`account_not_found` · `no_runtime` …). */
  get code(): string | undefined {
    return this.problem.code;
  }
}

type Method = "get" | "post" | "patch" | "delete";
type PathsFor<M extends Method> = { [P in keyof paths]: paths[P] extends Record<M, unknown> ? P : never }[keyof paths];
type Op<P extends keyof paths, M extends Method> = paths[P] extends Record<M, infer O> ? O : never;

type JsonOf<R> = R extends { content: { "application/json": infer T } } ? T : undefined;
/** 성공 응답 하나를 고른다. `202`(취소·재지시처럼 절차만 시작하는 응답)도 성공이다 — 완료는 SSE 로 온다. */
type SuccessOf<O> = O extends { responses: infer R }
  ? R extends { 200: infer S }
    ? JsonOf<S>
    : R extends { 201: infer S }
      ? JsonOf<S>
      : R extends { 202: infer S }
        ? JsonOf<S>
        : R extends { 204: unknown }
          ? undefined
          : never
  : never;
type BodyOf<O> = O extends { requestBody: { content: { "application/json": infer B } } }
  ? B
  : O extends { requestBody?: { content: { "application/json": infer B } } }
    ? B | undefined
    : undefined;
type PathParamsOf<O> = O extends { parameters: { path: infer P } } ? P : Record<string, never>;
type QueryOf<O> = O extends { parameters: { query?: infer Q } } ? Q : Record<string, never>;

export interface RequestOptions<O> {
  path?: PathParamsOf<O>;
  query?: QueryOf<O> extends undefined ? never : Partial<NonNullable<QueryOf<O>>>;
  body?: BodyOf<O>;
  /** 멱등키. 메시지 게시는 필수(openapi) — 호출부가 uuid 를 넘긴다. */
  idempotencyKey?: string;
  signal?: AbortSignal;
}

export const API_BASE = "/api/v1";

function buildUrl(path: string, pathParams?: Record<string, unknown>, query?: Record<string, unknown>): string {
  let url = path.replace(/\{(\w+)\}/g, (_, k: string) => {
    const v = pathParams?.[k];
    if (v === undefined) throw new Error(`missing path param ${k} for ${path}`);
    return encodeURIComponent(String(v));
  });
  if (query) {
    const qs = new URLSearchParams();
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null) continue;
      qs.set(k, Array.isArray(v) ? v.join(",") : String(v)); // style: form, explode: false
    }
    const s = qs.toString();
    if (s) url += `?${s}`;
  }
  return API_BASE + url;
}

async function request<O>(method: Method, path: string, opts: RequestOptions<O> = {}): Promise<SuccessOf<O>> {
  const headers: Record<string, string> = { Accept: "application/json, application/problem+json" };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  if (opts.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;
  const res = await fetch(
    buildUrl(path, opts.path as Record<string, unknown> | undefined, opts.query as Record<string, unknown> | undefined),
    {
      method: method.toUpperCase(),
      headers,
      credentials: "same-origin",
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    },
  );
  if (res.status === 204) return undefined as SuccessOf<O>;
  const text = await res.text();
  let json: unknown = undefined;
  if (text) {
    try {
      json = JSON.parse(text);
    } catch {
      json = undefined;
    }
  }
  if (!res.ok) {
    const p = (json as Partial<Problem>) ?? {};
    throw new ApiError({
      title: p.title ?? res.statusText ?? "요청 실패",
      status: p.status ?? res.status,
      detail: p.detail ?? (json === undefined && text ? text.slice(0, 200) : undefined),
      code: p.code,
      errors: p.errors,
      type: p.type ?? "about:blank",
    });
  }
  return json as SuccessOf<O>;
}

export const api = {
  get: <P extends PathsFor<"get">>(path: P, opts?: RequestOptions<Op<P, "get">>) =>
    request<Op<P, "get">>("get", path, opts),
  post: <P extends PathsFor<"post">>(path: P, opts?: RequestOptions<Op<P, "post">>) =>
    request<Op<P, "post">>("post", path, opts),
  patch: <P extends PathsFor<"patch">>(path: P, opts?: RequestOptions<Op<P, "patch">>) =>
    request<Op<P, "patch">>("patch", path, opts),
  delete: <P extends PathsFor<"delete">>(path: P, opts?: RequestOptions<Op<P, "delete">>) =>
    request<Op<P, "delete">>("delete", path, opts),
};

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** 화면에 보여줄 오류 문구. Problem.detail > title > 일반 문구. */
export function errorMessage(e: unknown): string {
  if (isApiError(e)) return e.problem.detail ?? e.problem.title;
  if (e instanceof Error) return e.message;
  return "알 수 없는 오류";
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
