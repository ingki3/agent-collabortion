/** @type {import('next').NextConfig} */
const serverUrl = process.env.COLAB_SERVER_URL ?? "http://localhost:8080";
const mock = process.env.COLAB_MOCK_API === "1";
/**
 * 개발 전용 페이지(`app/dev/*` — 배지 조합표·컴포넌트 전시)는 **프로덕션 빌드에 들어가지 않는다**(W-9, PR #188 리뷰 NN2).
 * 그 페이지는 `page.dev.tsx` 로 이름 붙어 있고, `next dev`(NODE_ENV=development) 에서만 `pageExtensions` 가 `dev.tsx` 를 라우트로
 * 인식한다. `next build` 는 NODE_ENV=production 이라 `page.dev.tsx` 가 라우트가 아닌 그냥 파일이 되어 `/dev/*` 가 출력에 없다
 * (`scripts/assert-no-dev-routes.mjs` 가 빌드 뒤 매니페스트로 잰다). 빌드에 넣어야 할 때(스크린샷 스크립트 등)만 `COLAB_DEV_PAGES=1`.
 */
const devPages = process.env.NODE_ENV !== "production" || process.env.COLAB_DEV_PAGES === "1";
export const pageExtensions = devPages ? ["dev.tsx", "tsx", "ts", "jsx", "js"] : ["tsx", "ts", "jsx", "js"];

export const SESSION_REDIRECTS = [
  { source: "/sessions", destination: "/rooms", statusCode: 307 },
  // 마법사(`/sessions/new`)는 지워졌다(T-R2-W4b) — 옛 링크·북마크는 방 만들기(S18)로.
  { source: "/sessions/new", destination: "/rooms/new", statusCode: 307 },
  { source: "/sessions/:id", destination: "/rooms/:id", statusCode: 307 },
  { source: "/sessions/:id/:rest+", destination: "/rooms/:id/:rest+", statusCode: 307 },
];

const nextConfig = {
  reactStrictMode: true,
  pageExtensions,
  // SSE(`GET /workspaces/{id}/stream`)가 rewrite 프록시를 지나는데, Next 의 응답 압축이 브라우저의 `Accept-Encoding: gzip` 을 보고
  // 스트림을 gzip 으로 감싸 **버퍼링**한다 → EventSource 는 열리지만(onopen) 프레임이 한 건도 안 온다(G3 W-2, S12 가 `대기 중` 에 머묾).
  // curl 은 Accept-Encoding 을 안 보내 재현되지 않았다. 압축은 배포의 리버스 프록시가 맡는다.
  compress: false,
  // v0.19 (T-R2-W1): 세션 → 방. 옛 주소 `/sessions`·`/sessions/<id>…` 는 `/rooms/…` 로 **307**(임시 — 되돌릴 수 있고, 브라우저가 영구
  // 캐시하지 않는다). 방 id = 옛 세션 id(§7 이관 규칙)라 경로 조각은 그대로 옮긴다. 쿼리(`?deleted=` 등)는 Next 가 그대로 넘긴다.
  // `/sessions/new`(마법사)는 T-R2-W4b 에서 지웠다 — 방 만들기(`/rooms/new`)로 307. 순서가 뜻이다: `:id` 보다 먼저 와야 한다.
  async redirects() {
    return SESSION_REDIRECTS;
  },
  // 실서버 모드: /api/v1/* 를 Go 서버(:8080)로 프록시한다(같은 오리진 → 쿠키 그대로, openapi `servers[0]`).
  // 목 모드(COLAB_MOCK_API=1): app/api/v1/[...path]/route.ts 가 받는다(프록시 없음).
  async rewrites() {
    if (mock) return { beforeFiles: [], afterFiles: [], fallback: [] };
    return {
      beforeFiles: [{ source: "/api/v1/:path*", destination: `${serverUrl}/api/v1/:path*` }],
      afterFiles: [],
      fallback: [],
    };
  },
};

export default nextConfig;
