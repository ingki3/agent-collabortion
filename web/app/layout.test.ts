/**
 * §8.5 레이아웃 자물쇠(T-W10, PR #191 R1·NN4).
 *
 *   (1) 카드 열의 최소 폭은 토큰 하나(`--card-min`, tokens.css)다 — `.cards`(app.css)가 그 토큰을 읽고, 픽셀을
 *       직접 적은 `minmax(NNNpx, …)` 카드 목록이 app/·components/ 에 없다. 열 규칙이 두 곳이면 다음에 한쪽만 고쳐진다.
 *   (2) 인박스(받은 요청)는 **한 열**이다 — `.s8__list` 에 `auto-fill`·`auto-fit` 이 없고, 열 폭은 같은 토큰을 하한으로 쓴다.
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const ROOT = join(__dirname, "..");
const read = (p: string) => readFileSync(join(ROOT, p), "utf8");
const TOKENS = read("app/tokens.css");
const APP = read("app/app.css");
const INBOX = read("app/(app)/inbox/page.tsx");

function walk(dir: string, out: string[] = []): string[] {
  for (const f of readdirSync(dir)) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(css|tsx)$/.test(f) && !/\.test\./.test(f)) out.push(p);
  }
  return out;
}

describe("(1) --card-min 토큰 하나", () => {
  it("tokens.css 가 --card-min 을 정의한다", () => {
    expect(TOKENS).toMatch(/--card-min:\s*\d+px;/);
  });
  it(".cards 는 토큰을 읽는다", () => {
    const rule = APP.match(/\.cards\s*\{[^}]*\}/)?.[0] ?? "";
    expect(rule).toContain("repeat(auto-fill, minmax(var(--card-min), 1fr))");
  });
  it("픽셀을 직접 적은 카드 목록 열 규칙이 없다(dev 전용 .story__grid 제외)", () => {
    const hits: string[] = [];
    for (const f of [...walk(join(ROOT, "app")), ...walk(join(ROOT, "components"))]) {
      const src = readFileSync(f, "utf8");
      for (const m of src.matchAll(/repeat\(auto-(?:fill|fit),\s*minmax\((\d+px)/g)) {
        if (src.slice(Math.max(0, m.index! - 120), m.index!).includes(".story__grid")) continue;
        hits.push(`${f.slice(ROOT.length + 1)}: ${m[0]}`);
      }
    }
    expect(hits).toEqual([]);
  });
});

describe("(2) 인박스는 한 열", () => {
  const rule = INBOX.match(/\.s8__list\s*\{[^}]*\}/)?.[0] ?? "";
  it(".s8__list 가 있고 auto-fill·auto-fit 이 없다", () => {
    expect(rule).not.toBe("");
    expect(rule).not.toMatch(/auto-fill|auto-fit/);
  });
  it("열 하나의 폭은 --card-min 이상, 읽기 좋은 상한 이하", () => {
    expect(rule).toMatch(/grid-template-columns:\s*minmax\(var\(--card-min\),\s*\d+px\)/);
  });
});
