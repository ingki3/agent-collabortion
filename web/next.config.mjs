/** @type {import('next').NextConfig} */
const serverUrl = process.env.COLAB_SERVER_URL ?? "http://localhost:8080";
const mock = process.env.COLAB_MOCK_API === "1";

const nextConfig = {
  reactStrictMode: true,
  // SSE(`GET /workspaces/{id}/stream`)가 rewrite 프록시를 지나는데, Next 의 응답 압축이 브라우저의 `Accept-Encoding: gzip` 을 보고
  // 스트림을 gzip 으로 감싸 **버퍼링**한다 → EventSource 는 열리지만(onopen) 프레임이 한 건도 안 온다(G3 W-2, S12 가 `대기 중` 에 머묾).
  // curl 은 Accept-Encoding 을 안 보내 재현되지 않았다. 압축은 배포의 리버스 프록시가 맡는다.
  compress: false,
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
