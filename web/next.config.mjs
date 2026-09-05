/** @type {import('next').NextConfig} */
const serverUrl = process.env.COLAB_SERVER_URL ?? "http://localhost:8080";
const mock = process.env.COLAB_MOCK_API === "1";

const nextConfig = {
  reactStrictMode: true,
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
