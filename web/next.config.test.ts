/**
 * G3 W-2 회귀 가드 — Next 의 응답 압축이 켜져 있으면 rewrite 프록시를 지나는 SSE(`/workspaces/{id}/stream`)가 gzip 으로
 * 버퍼링돼 브라우저 EventSource 가 열리기만 하고 프레임을 못 받는다(S12 가 `대기 중` 에 머묾). `compress: false` 가 유지돼야 한다.
 *
 * W-9 — 개발 전용 페이지(`app/dev/*`)가 프로덕션 빌드에 들어가지 않는다. 두 층으로 잰다:
 *   (1) 여기(정적) — `pageExtensions` 가 production 에서 `dev.tsx` 를 빼고, `app/dev/**` 의 라우트 파일(page·layout·route…)은 전부
 *       `.dev.tsx` 라 production 에서는 어느 것도 라우트가 될 수 없다.
 *   (2) `scripts/assert-no-dev-routes.mjs`(postbuild) — 실제 `next build` 매니페스트에 `/dev/*` 가 없다.
 */
import { readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const ROOT = __dirname;

/** next.config.mjs 를 NODE_ENV 별로 새로 평가한다(모듈 캐시를 피하려고 쿼리를 붙인다). */
async function configUnder(env: string, extra: Record<string, string | undefined> = {}) {
  const penv = process.env as Record<string, string | undefined>; // NODE_ENV 는 타입상 읽기 전용 — 테스트에서만 잠시 바꾼다
  const saved = { NODE_ENV: penv.NODE_ENV, COLAB_DEV_PAGES: penv.COLAB_DEV_PAGES };
  penv.NODE_ENV = env;
  penv.COLAB_DEV_PAGES = extra.COLAB_DEV_PAGES;
  try {
    const m = await import(/* @vite-ignore */ `./next.config.mjs?env=${env}&dev=${extra.COLAB_DEV_PAGES ?? ""}`);
    return m.default as { pageExtensions: string[]; compress: boolean };
  } finally {
    penv.NODE_ENV = saved.NODE_ENV;
    penv.COLAB_DEV_PAGES = saved.COLAB_DEV_PAGES;
  }
}

const ROUTE_FILES = /^(page|layout|template|loading|error|not-found|route|default)\./;
function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

describe("next.config.mjs", () => {
  it("compress: false — SSE 프록시 버퍼링 방지(W-2)", () => {
    const src = readFileSync(path.join(ROOT, "next.config.mjs"), "utf8");
    expect(src).toMatch(/^\s*compress:\s*false,/m);
    expect(src).toMatch(/reactStrictMode:\s*true/);
  });

  describe("W-9 — app/dev/* 는 프로덕션 빌드에 없다", () => {
    it("production 의 pageExtensions 에는 dev.tsx 가 없고, development 에는 있다", async () => {
      expect((await configUnder("production")).pageExtensions).toEqual(["tsx", "ts", "jsx", "js"]);
      expect((await configUnder("development")).pageExtensions).toEqual(["dev.tsx", "tsx", "ts", "jsx", "js"]);
    });

    it("COLAB_DEV_PAGES=1 이면 production 빌드에도 넣는다(스크린샷용 탈출구) — 기본은 아니다", async () => {
      expect((await configUnder("production", { COLAB_DEV_PAGES: "1" })).pageExtensions[0]).toBe("dev.tsx");
      expect((await configUnder("production", { COLAB_DEV_PAGES: "0" })).pageExtensions).not.toContain("dev.tsx");
    });

    it("app/dev/** 의 라우트 파일은 전부 .dev.tsx — production 확장자로는 어느 것도 라우트가 되지 않는다", () => {
      const files = walk(path.join(ROOT, "app", "dev")).map((f) => path.relative(ROOT, f));
      const routeFiles = files.filter((f) => ROUTE_FILES.test(path.basename(f)));
      expect(routeFiles.length).toBeGreaterThanOrEqual(2); // /dev/badges · /dev/components
      for (const f of routeFiles) expect(f, `${f} 는 page.dev.tsx 모양이어야 한다(W-9)`).toMatch(/\.dev\.tsx$/);
      // 회귀 재현: 누가 page.tsx 로 되돌리면 production 확장자(tsx)에 걸려 라우트가 된다.
      const prod = ["tsx", "ts", "jsx", "js"];
      const isRoute = (name: string, exts: string[]) => exts.some((e) => new RegExp(`^(page|layout|route)\\.${e.replace(".", "\\.")}$`).test(name));
      expect(isRoute("page.tsx", prod)).toBe(true);
      for (const f of routeFiles) expect(isRoute(path.basename(f), prod)).toBe(false);
      for (const f of routeFiles) expect(isRoute(path.basename(f), ["dev.tsx", ...prod])).toBe(true);
    });

    it("빌드 산출물 검사가 npm run build 에 붙어 있다(postbuild)", () => {
      const pkg = JSON.parse(readFileSync(path.join(ROOT, "package.json"), "utf8")) as { scripts: Record<string, string> };
      expect(pkg.scripts.postbuild).toBe("node scripts/assert-no-dev-routes.mjs");
    });
  });
});

// ── v0.19 T-R2-W1 — `/sessions…` → `/rooms…` 307, 마법사(`/sessions/new`)만 예외 ─────────────────────────────
describe("세션 → 방 주소 (T-R2-W1)", () => {
  // Next 가 redirects 의 source 를 해석하는 것과 같은 path-to-regexp(next 에 묶인 사본)로 잰다.
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { match, compile } = require("next/dist/compiled/path-to-regexp") as {
    match: (src: string, o?: { decode?: (s: string) => string }) => (p: string) => false | { params: Record<string, unknown> };
    compile: (dst: string) => (params: Record<string, string | string[]>) => string;
  };
  async function redirectOf(pathname: string): Promise<{ to: string; status: number } | null> {
    const cfg = (await configUnder("production")) as unknown as { redirects: () => Promise<{ source: string; destination: string; statusCode: number }[]> };
    for (const r of await cfg.redirects()) {
      const m = match(r.source, { decode: decodeURIComponent })(pathname);
      if (m) return { to: compile(r.destination)(m.params as Record<string, string | string[]>), status: r.statusCode };
    }
    return null;
  }

  it("목록·상세·그 아래 경로는 같은 조각으로 /rooms 로 307", async () => {
    expect(await redirectOf("/sessions")).toEqual({ to: "/rooms", status: 307 });
    expect(await redirectOf("/sessions/0b9e6a4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b")).toEqual({ to: "/rooms/0b9e6a4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b", status: 307 });
    expect(await redirectOf("/sessions/abc/settings")).toEqual({ to: "/rooms/abc/settings", status: 307 });
  });

  it("마법사 /sessions/new 는 예외 — 옮기지 않는다(삭제는 W4 뒤)", async () => {
    expect(await redirectOf("/sessions/new")).toBeNull();
    // 이름이 new 로 시작할 뿐인 id 는 예외가 아니다.
    expect(await redirectOf("/sessions/newer")).toEqual({ to: "/rooms/newer", status: 307 });
  });

  it("영구(308)가 아니다 — 되돌릴 수 있는 이관이라 브라우저가 캐시하지 않게", () => {
    const src = readFileSync(path.join(ROOT, "next.config.mjs"), "utf8");
    expect(src).not.toMatch(/permanent:\s*true/);
    expect(src.match(/statusCode: 307/g)).toHaveLength(3);
  });

  it("앱 안의 기본 착지점은 /rooms 다 — 옛 /sessions 로 보내 한 번 더 튕기지 않는다", () => {
    for (const f of ["app/page.tsx", "app/login/page.tsx", "app/signup/page.tsx", "app/invite/[token]/page.tsx", "app/onboarding/page.tsx"]) {
      const src = readFileSync(path.join(ROOT, f), "utf8");
      expect(src, f).not.toMatch(/["'`]\/sessions["'`?]/);
    }
  });
});
