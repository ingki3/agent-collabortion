/**
 * 대비 회귀 가드(COMPONENTS §8.5) — **밝음·어두움 두 벌 전부**.
 *
 * 색을 손으로 옮겨 적지 않는다. `tokens.css` 를 파싱해서 실제 토큰 값으로 재기 때문에,
 * 누가 토큰 하나를 바꾸면 그 색이 닿는 모든 조합이 여기서 다시 계산된다.
 *
 * 기준(WCAG 2.1): 텍스트 4.5:1. 테두리·점 같은 비텍스트 신호는 3:1(1.4.11).
 * soft 배경은 합성해서 잰다 — `--soft-alpha` 가 밝음 12% / 어두움 20% 라 두 벌의 배경색이 다르다.
 */
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const CSS = readFileSync(join(__dirname, "tokens.css"), "utf8");

function decl(name: string): string {
  const m = CSS.match(new RegExp(`${name}: *(#[0-9a-f]{6});`, "i"));
  if (!m) throw new Error(`tokens.css 에 ${name} 이 없다`);
  return m[1];
}

const TONES = ["run", "wait", "block", "pause", "done", "fail"] as const;

const LIGHT = {
  label: "밝음", alpha: 0.12,
  bg: decl("--bg"), surface: decl("--surface"),
  ink: decl("--ink"), ink2: decl("--ink-2"), ink3: decl("--ink-3"),
  solid: Object.fromEntries(TONES.map((t) => [t, decl(`--s-${t}`)])),
  text: Object.fromEntries(TONES.map((t) => [t, decl(`--s-${t}-text`)])),
};
const DARK = {
  label: "어두움", alpha: 0.2,
  bg: decl("--dk-bg"), surface: decl("--dk-surface"),
  ink: decl("--dk-ink"), ink2: decl("--dk-ink-2"), ink3: decl("--dk-ink-3"),
  solid: Object.fromEntries(TONES.map((t) => [t, decl(`--dk-s-${t}`)])),
  text: Object.fromEntries(TONES.map((t) => [t, decl(`--dk-s-${t}-text`)])),
};

const chan = (h: string, i: number) => parseInt(h.slice(1 + i * 2, 3 + i * 2), 16);
const lin = (c: number) => (c /= 255) <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
const lum = (h: string) => 0.2126 * lin(chan(h, 0)) + 0.7152 * lin(chan(h, 1)) + 0.0722 * lin(chan(h, 2));
function ratio(a: string, b: string) {
  const [la, lb] = [lum(a), lum(b)];
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}
/** color-mix(in srgb, fg <p>, transparent) 를 bg 위에 합성한 결과. */
function over(fg: string, bg: string, p: number) {
  return "#" + [0, 1, 2].map((i) => Math.round(chan(fg, i) * p + chan(bg, i) * (1 - p)).toString(16).padStart(2, "0")).join("");
}

for (const T of [LIGHT, DARK]) {
  describe(`${T.label} 대비`, () => {
    const planes = [["--bg", T.bg], ["--surface", T.surface]] as const;

    it.each(planes)("본문 --ink 가 %s 위에서 4.5:1 이상", (_l, plane) => {
      expect(ratio(T.ink, plane)).toBeGreaterThanOrEqual(4.5);
    });

    it.each(planes)("보조 --ink-2 가 %s 위에서 4.5:1 이상", (_l, plane) => {
      expect(ratio(T.ink2, plane)).toBeGreaterThanOrEqual(4.5);
    });

    // §8.5 의 근거 — 이 단정이 깨지면 ink-3 을 텍스트에 써도 된다는 뜻이고, 그때는 §8.5 를 다시 본다.
    it("--ink-3 은 어느 쪽에서든 텍스트 기준을 넘지 못한다 — 글리프·막대 전용", () => {
      const worst = Math.min(...planes.map(([, p]) => ratio(T.ink3, p)));
      expect(worst).toBeLessThan(4.5);
      expect(worst).toBeGreaterThanOrEqual(3.0); // 비텍스트 신호로는 쓸 수 있다
    });

    it.each(TONES)("--s-%s-text 가 자기 soft 배경(--bg 위) 에서 4.5:1 이상", (tone) => {
      expect(ratio(T.text[tone], over(T.solid[tone], T.bg, T.alpha))).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("--s-%s-text 가 평면 배경 두 곳에서 4.5:1 이상", (tone) => {
      for (const [, plane] of planes) expect(ratio(T.text[tone], plane)).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("보조 --ink-2 가 %s soft 카드(--bg 위) 안에서 4.5:1 이상", (tone) => {
      // HITL 카드·배너처럼 soft 배경 위에 보조 문구가 얹히는 자리. 이 카드들은 --bg 위에 놓인다.
      expect(ratio(T.ink2, over(T.solid[tone], T.bg, T.alpha))).toBeGreaterThanOrEqual(4.5);
    });

    // solid 배지는 조합표상 fail 뿐이다(badge-map: failed·error·offline).
    it("solid 배지(--bg 글자 on --s-fail)가 4.5:1 이상", () => {
      expect(ratio(T.bg, T.solid.fail)).toBeGreaterThanOrEqual(4.5);
    });

    it("primary 버튼·cmd(--bg 글자 on --ink)가 4.5:1 이상", () => {
      expect(ratio(T.bg, T.ink)).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("--s-%s 테두리·점이 --bg 위에서 3:1 이상(비텍스트)", (tone) => {
      expect(ratio(T.solid[tone], T.bg)).toBeGreaterThanOrEqual(3.0);
    });
  });
}
