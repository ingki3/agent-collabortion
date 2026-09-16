#!/usr/bin/env node
/**
 * W-9 — `next build` 출력에 개발 전용 페이지(`/dev/*`)가 없는지 **빌드 산출물로** 잰다.
 * `npm run build` 의 postbuild 로 돈다(CI web 잡의 `npm run build` 가 곧 이 검사다).
 *
 * 읽는 것: `.next/app-path-routes-manifest.json`(app 라우트 → URL) — 프로덕션 빌드가 라우트로 인식한 전부가 여기 있다.
 * `COLAB_DEV_PAGES=1` 로 일부러 넣고 빌드한 경우는 통과시키되 한 줄 알린다.
 */
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifestPath = path.join(root, ".next", "app-path-routes-manifest.json");
const routes = Object.values(JSON.parse(readFileSync(manifestPath, "utf8")));
const dev = routes.filter((r) => r === "/dev" || r.startsWith("/dev/"));

if (dev.length === 0) {
  console.log(`assert-no-dev-routes: ok — ${routes.length} routes, /dev/* 없음`);
  process.exit(0);
}
if (process.env.COLAB_DEV_PAGES === "1") {
  console.log(`assert-no-dev-routes: COLAB_DEV_PAGES=1 — 개발 페이지 포함 빌드(${dev.join(", ")}). 배포용이 아니다.`);
  process.exit(0);
}
console.error(`assert-no-dev-routes: 프로덕션 빌드에 개발 전용 페이지가 들어 있다 — ${dev.join(", ")}\n` +
  "  app/dev/* 의 라우트 파일은 page.dev.tsx 여야 하고, next.config.mjs 의 pageExtensions 가 production 에서 dev.tsx 를 빼야 한다(W-9).");
process.exit(1);
