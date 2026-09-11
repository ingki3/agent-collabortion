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
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { BADGE_MAP, type Tone, type BadgeSpec } from "@/components/badge-map";

const CSS = readFileSync(join(__dirname, "tokens.css"), "utf8");
const ROOT = join(__dirname, "..");

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

type Theme = typeof LIGHT;

/** `var(--s-wait)` · `var(--bg)` · `var(--ink)` 같은 토큰을 테마 값으로. 모르는 토큰이면 던진다 — 조용히 통과하지 않게. */
function resolve(T: Theme, tok: string): string {
  const m = tok.match(/^var\(--([a-z0-9-]+)\)$/);
  if (!m) throw new Error(`census: 토큰이 아닌 색 ${tok} — 리터럴 색은 tokens.css 밖에서 쓰지 않는다`);
  const n = m[1];
  if (n === "bg") return T.bg;
  if (n === "surface") return T.surface;
  if (n === "ink") return T.ink;
  if (n === "ink-2") return T.ink2;
  if (n === "ink-3") return T.ink3;
  const st = n.match(/^s-([a-z]+)(-text)?$/);
  if (st && (TONES as readonly string[]).includes(st[1])) return st[2] ? T.text[st[1]] : T.solid[st[1]];
  throw new Error(`census: 모르는 토큰 ${tok}`);
}

/**
 * census — "상태색(또는 ink)을 `background` 로 쓰면서 같은 규칙에 `color` 를 둔 자리". solid 만 본다
 * (`color-mix` 는 soft 라 위의 soft 단정이 맡는다). 결과는 [어디, 배경 토큰, 글자 토큰].
 * PR #186 NN3 의 재현: 내비 배지를 `background: var(--s-wait); color: var(--bg)` 로 되돌리면 여기 잡힌다.
 */
interface SolidRule { where: string; bg: string; fg: string }
function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (["node_modules", ".next", "__screenshots__", ".git", "dev"].includes(name)) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(css|tsx)$/.test(name) && !/\.test\.tsx$/.test(name)) out.push(p);
  }
  return out;
}
function solidRules(): SolidRule[] {
  const out: SolidRule[] = [];
  for (const f of ["app", "components"].flatMap((d) => walk(join(ROOT, d)))) {
    const src = readFileSync(f, "utf8");
    const rel = f.slice(ROOT.length + 1);
    const raw = rel.endsWith(".css") ? src : [...src.matchAll(/<style>\{`([\s\S]*?)`\}<\/style>/g)].map((m) => m[1]).join("\n");
    const css = raw.replace(/\/\*[\s\S]*?\*\//g, "");
    for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      const [, sel, body] = m;
      const bg = body.match(/(?:^|;)\s*background(?:-color)?:\s*(var\(--(?:s-[a-z]+|ink)\))\s*(?:;|$)/);
      const fg = body.match(/(?:^|;)\s*color:\s*([^;]+?)\s*(?:;|$)/);
      if (bg && fg) out.push({ where: `${rel} ${sel.trim().replace(/\s+/g, " ")}`, bg: bg[1], fg: fg[1] });
    }
  }
  return out;
}
const SOLID_RULES = solidRules();

describe("solid census 의 범위", () => {
  it("상태색·ink 를 배경으로 깔고 글자를 얹는 자리가 잡힌다 — primary 버튼·cmd·solid 배지", () => {
    const where = SOLID_RULES.map((r) => r.where);
    expect(where.some((w) => /\.btn--primary\b/.test(w))).toBe(true);
    expect(where.some((w) => /\.cmd\b/.test(w))).toBe(true);
    expect(SOLID_RULES.length).toBeGreaterThanOrEqual(2);
  });
});

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

    /*
     * soft 카드는 두 평면에 놓인다(§8.5 자물쇠 확장 (c), PR #186 NN4): --bg 위(HITL 카드·배너·인박스 항목)와
     * --surface 위(내비의 받은 요청 배지, 마법사 요약 카드 안의 배지, 요약 메시지 안의 멘션). soft 는 알파라
     * 밑 평면이 어두울수록 배경이 어두워져 --surface 쪽이 늘 더 빡빡하다 — 어두움 ink-2 는 이 평면에서
     * #a1a1aa 가 wait 4.41 로 걸려 #a6a6ae 로 정정했다(tokens.css 주석).
     */
    const softPlanes = [["--bg", T.bg], ["--surface", T.surface]] as const;

    it.each(TONES)("보조 --ink-2 가 %s soft 카드 안에서 4.5:1 이상 — --bg·--surface 두 평면", (tone) => {
      for (const [, plane] of softPlanes) expect(ratio(T.ink2, over(T.solid[tone], plane, T.alpha))).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("--s-%s-text 가 자기 soft 배경(--surface 위)에서 4.5:1 이상", (tone) => {
      expect(ratio(T.text[tone], over(T.solid[tone], T.surface, T.alpha))).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("본문 --ink 가 %s soft 카드 안에서 4.5:1 이상 — 두 평면", (tone) => {
      for (const [, plane] of softPlanes) expect(ratio(T.ink, over(T.solid[tone], plane, T.alpha))).toBeGreaterThanOrEqual(4.5);
    });

    /*
     * solid 배지(§8.5 자물쇠 확장 (b), PR #186 NN3): 예전엔 fail 톤 하나만 쟀다. 조합표(badge-map)에서
     * variant:"solid" 인 항목의 톤을 **전부** 모아 잰다 — 누가 조합표에서 waiting_human 을 solid 로 바꾸면
     * 밝음 wait 는 3.19 라 여기서 걸린다.
     */
    const solidTones = [...new Set(Object.values(BADGE_MAP).flatMap((m) => Object.values(m as Record<string, BadgeSpec>).filter((sp) => sp.variant === "solid").map((sp) => sp.tone)))] as Exclude<Tone, "neutral">[];

    it("조합표의 solid 톤이 하나 이상 있고 neutral 은 solid 가 아니다", () => {
      expect(solidTones.length).toBeGreaterThan(0);
      expect((solidTones as string[]).includes("neutral")).toBe(false);
    });

    it.each(solidTones)("solid 배지(--bg 글자 on --s-%s)가 4.5:1 이상 — 조합표의 solid 톤 전부", (tone) => {
      expect(ratio(T.bg, T.solid[tone])).toBeGreaterThanOrEqual(4.5);
    });

    // 상태색을 배경으로 쓰면서 글자를 얹는 자리의 census — CSS 파일과 tsx 안의 <style> 블록 전부.
    it.each(SOLID_RULES.map((r) => [r.where, r.bg, r.fg] as const))("%s — %s 배경에 %s 글자가 4.5:1 이상", (_w, bgTok, fgTok) => {
      const bg = resolve(T, bgTok);
      const fg = resolve(T, fgTok);
      expect(ratio(fg, bg)).toBeGreaterThanOrEqual(4.5);
    });

    it("primary 버튼·cmd(--bg 글자 on --ink)가 4.5:1 이상", () => {
      expect(ratio(T.bg, T.ink)).toBeGreaterThanOrEqual(4.5);
    });

    it.each(TONES)("--s-%s 테두리·점이 --bg 위에서 3:1 이상(비텍스트)", (tone) => {
      expect(ratio(T.solid[tone], T.bg)).toBeGreaterThanOrEqual(3.0);
    });
  });
}
