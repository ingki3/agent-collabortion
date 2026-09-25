import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname) } },
  test: {
    environment: "jsdom",
    include: ["**/*.test.{ts,tsx}"],
    exclude: ["node_modules", ".next"],
    // 재발 방지: 상한 없는 작업자(코어 수만큼)가 개당 5~7GB 로 불어 맥이 멈췄다.
    // 작업자는 최대 4개, 작업자 하나의 힙은 2GB 에서 끊는다(폭주 테스트는 OOM 으로 죽고 끝난다).
    pool: "forks",
    maxWorkers: 4,
    minWorkers: 1,
    poolOptions: {
      forks: { execArgv: ["--max-old-space-size=2048"] },
    },
  },
});
