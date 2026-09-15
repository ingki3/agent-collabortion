/**
 * 글자 계단·다크 테마 회귀 가드(COMPONENTS §8.2·§8.3).
 *
 * T-W7 의 DoD 검사식(`grep -rn "font-size: *1[0-9]px" web/` 가 0 건)을 테스트로 고정한다.
 * 다음 사람이 컴포넌트 CSS 에 px 를 손으로 적으면 여기서 걸린다 — 눈으로 다시 세지 않아도 되게.
 */
import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const ROOT = join(__dirname, "..");
const SKIP = new Set(["node_modules", ".next", "__screenshots__", ".git"]);

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(css|tsx|ts)$/.test(name) && !name.endsWith(".test.ts") && !name.endsWith(".test.tsx")) out.push(p);
  }
  return out;
}

const FILES = walk(ROOT).map((p) => [p.slice(ROOT.length + 1), readFileSync(p, "utf8")] as const);

describe("§8.2 글자 계단", () => {
  it("하드코딩 px font-size 가 한 곳도 없다 (토큰만 남는다)", () => {
    const hits = FILES.flatMap(([f, src]) =>
      src.split("\n").flatMap((l, i) => (/font-size: *[0-9.]+px/.test(l) ? [`${f}:${i + 1} ${l.trim()}`] : [])),
    );
    expect(hits).toEqual([]);
  });

  it("인라인 style 의 fontSize 도 토큰을 쓴다", () => {
    const hits = FILES.flatMap(([f, src]) =>
      src.split("\n").flatMap((l, i) => (/fontSize: *[0-9]/.test(l) ? [`${f}:${i + 1} ${l.trim()}`] : [])),
    );
    expect(hits).toEqual([]);
  });

  it("tokens.css 가 계단 5개를 §8.2 값으로 정의한다", () => {
    const css = FILES.find(([f]) => f === "app/tokens.css")![1];
    for (const [name, value] of [
      ["--fs-title", "24px"],
      ["--fs-card", "17px"],
      ["--fs-body", "14px"],
      ["--fs-sub", "13px"],
      ["--fs-meta", "12px"],
    ]) {
      expect(css).toContain(`${name}: ${value};`);
    }
  });

  it("컴포넌트가 쓰는 fs 토큰은 계단 5개뿐이다", () => {
    const used = new Set<string>();
    for (const [, src] of FILES) for (const m of src.matchAll(/--fs-[a-z-]+/g)) used.add(m[0]);
    expect([...used].sort()).toEqual(["--fs-body", "--fs-card", "--fs-meta", "--fs-sub", "--fs-title"]);
  });
});

describe("§8.5 대비", () => {
  it("--fs-sub 이하의 텍스트에 --ink-3 을 쓰지 않는다", () => {
    const hits = FILES.flatMap(([f, src]) =>
      src.split("\n").flatMap((l, i) =>
        /font-size: *var\(--fs-(sub|meta)\)/.test(l) && /color: *var\(--ink-3\)/.test(l)
          ? [`${f}:${i + 1} ${l.trim()}`]
          : [],
      ),
    );
    expect(hits).toEqual([]);
  });
});

describe("§8.3 다크 테마", () => {
  const css = () => FILES.find(([f]) => f === "app/tokens.css")![1];

  it("v1 범위 밖이라는 주석이 사라졌다", () => {
    expect(css()).not.toContain("다크 테마는 v1 범위 밖");
  });

  it("전환 경로 둘 — prefers-color-scheme 과 [data-theme] 수동 지정", () => {
    expect(css()).toContain("@media (prefers-color-scheme: dark)");
    // 수동으로 "밝게"를 고르면 시스템이 어두워도 밝음을 지킨다
    expect(css()).toContain(':root:not([data-theme="light"])');
    expect(css()).toContain(':root[data-theme="dark"]');
  });

  it("어두움 기본 6은 §8.3 이 준 값 (ink-2 는 T-W9 정정값)", () => {
    // --dk-ink-2 는 §8.3 의 #a1a1aa 에서 #a6a6ae 로 정정했다 — soft 카드가 --surface 위에 놓이는 평면에서
    // wait 4.41 로 4.5:1 에 못 미쳤다(PR #186 NN4). 수치는 contrast.test.ts 가 잰다. tokens.css 주석 참조.
    for (const v of ["--dk-bg: #0f0f11", "--dk-surface: #18181b", "--dk-line: #2e2e33",
                     "--dk-ink: #f4f4f5", "--dk-ink-2: #a6a6ae", "--dk-ink-3: #71717a"]) {
      expect(css()).toContain(v);
    }
  });

  it("상태색 12개가 어두움 전용 값으로 짝이 맞는다(solid 6 + text 6)", () => {
    for (const tone of ["run", "wait", "block", "pause", "done", "fail"]) {
      expect(css()).toMatch(new RegExp(`--dk-s-${tone}: #`));
      expect(css()).toMatch(new RegExp(`--dk-s-${tone}-text: #`));
    }
  });

  it("soft 알파는 밝음 12% · 어두움 20%", () => {
    expect(css()).toContain("--soft-alpha: 12%;");
    expect(css().match(/--soft-alpha: 20%;/g)?.length).toBe(2); // media + [data-theme="dark"]
  });

  it("soft 배경을 손으로 적은 곳이 없다 — 전부 --soft-alpha 를 탄다", () => {
    const hits = FILES.flatMap(([f, src]) =>
      src.split("\n").flatMap((l, i) =>
        /color-mix\(in srgb, var\(--s-[a-z]+\) *[0-9]+%/.test(l) ? [`${f}:${i + 1} ${l.trim()}`] : [],
      ),
    );
    expect(hits).toEqual([]);
  });
});
