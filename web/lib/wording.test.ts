/**
 * 문구 자물쇠 — §8.4 용어표를 테스트로 못박는다(COMPONENTS §8.4 "문구는 테스트로 못박는다").
 *
 * T-W7 의 `app/typography.test.ts` 가 px 리터럴에 대해 한 일을 문구에 대해 한다.
 * 근거: PR #188 리뷰 B1 — 같은 파일 안에서 새 상수는 "컴퓨터" 로 고치고 **옛 상수를 지나쳐**
 * S5 배지만 `일시정지 · 런타임 오프라인` 을 냈다. 244개 테스트가 전부 초록인 채로 계약 위반이 남았다.
 * 이 파일이 있으면 그 한 줄이 CI 에서 빨갛게 된다.
 *
 * **문구만 센다.** 변수명 `lane_id` 나 URL `/v1/tasks` 는 코드지 문구가 아니다 —
 * 소스를 훑어 **JSX 텍스트 노드**와 **화면에 닿는 문자열 리터럴**(한글이 있거나 공백이 있는 산문)만 모은다.
 * 리뷰어가 검증에 쓴 `/tmp/review188-terms.py` 를 그대로 옮긴 것이다.
 */
import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync, existsSync } from "node:fs";
import { join } from "node:path";
import { capabilityAlerts, capabilityDetails } from "@/components/RuntimeCard";
import { sessionBadgeLabel, PAUSE_REASON_LABEL } from "@/lib/session-label";

const ROOT = join(__dirname, "..");

/** 화면 문구가 사는 곳 — 여기 전부를 센다. 하나라도 빠지면 아래 "범위" 테스트가 걸린다. */
const SCAN = ["app", "components", "lib"];
const SKIP_DIR = new Set(["node_modules", ".next", "__screenshots__", ".git"]);

/**
 * 범위에서 빼는 두 곳. **좁히려는 것이 아니라 이 저장소가 쓰지 않는 글이다.**
 * - `lib/mock/` — 목이 흉내 내는 것은 **서버가 쓴 `Problem.detail`** 이다(서버는 지금도 "런타임…" 이라고 쓴다).
 *   여기를 고치면 목만 서버와 다른 말을 하게 된다. 서버 쪽 문구는 §8.4 범위 밖이고 T-W8 의 금지 구역이다.
 * - `lib/api/schema.d.ts` — `contracts/openapi` 에서 생성된 파일(주석까지 계약의 글).
 * - `app/dev/` — 개발 전용 컴포넌트 갤러리다(내비 링크가 없고 `COMPONENTS.md` 의 컴포넌트 이름을
 *   제목으로 그대로 쓴다 — "Lane Card"·"Inbox Item"). 리뷰도 같은 이유로 범위 밖으로 봤다.
 */
const EXCLUDE = ["lib/mock", "lib/api/schema.d.ts", "app/dev"];

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIR.has(name)) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

export interface Visible {
  /** ROOT 기준 상대 경로. */
  file: string;
  line: number;
  text: string;
}

/** 사람이 읽을 수 있는 것만 — JSX 텍스트 노드 + 산문처럼 생긴 문자열 리터럴. */
function visibleStrings(file: string, src: string): Visible[] {
  const out: Visible[] = [];
  const body = src
    .replace(/<style>\{`[\s\S]*?`\}<\/style>/g, "") // CSS 블록
    .replace(/^\s*import\s.*$/gm, "") // 모듈 경로
    // `${lane.queue_position}` 은 코드다 — 보간 안의 식별자를 문구로 세면 안 된다(중첩 삼항까지 안쪽부터).
    .replace(/\$\{[^{}]*\}/g, "…")
    .replace(/\$\{[^{}]*\}/g, "…")
    .replace(/\$\{[^{}]*\}/g, "…")
    // className·data-* 등 화면에 읽히지 않는 속성 값도 코드다.
    .replace(/\b(className|class|data-[a-z-]+|key|href|src|htmlFor|role|id)=\{?["'`][^"'`]*["'`]\}?/g, "");
  body.split("\n").forEach((line, i) => {
    const s = line.trim();
    if (s.startsWith("//") || s.startsWith("*") || s.startsWith("/*")) return; // 주석은 문구가 아니다
    for (const m of line.matchAll(/>([^<>{}]+)</g)) {
      const t = m[1].split(/\s+/).filter(Boolean).join(" ");
      if (t && !/^[\s;,.()[\]/*+&|-]*$/.test(t)) out.push({ file, line: i + 1, text: t });
    }
    for (const m of line.matchAll(/"([^"\n]{2,})"|'([^'\n]{2,})'|`([^`\n]{2,})`/g)) {
      const t = m[1] ?? m[2] ?? m[3];
      const prose = /[가-힣]/.test(t) || (t.includes(" ") && !/^(\/|http|@\/|\.)/.test(t));
      if (!prose) continue;
      if (t.includes("var(--") || /^[\d.]+(px|em|rem|%)$/.test(t)) continue;
      out.push({ file, line: i + 1, text: t });
    }
  });
  return out;
}

const FILES = SCAN.flatMap((d) => walk(join(ROOT, d)))
  .map((p) => p.slice(ROOT.length + 1))
  .filter((f) => !EXCLUDE.some((e) => f === e || f.startsWith(`${e}/`)))
  .sort();

const POOL: Visible[] = FILES.flatMap((f) => visibleStrings(f, readFileSync(join(ROOT, f), "utf8")));

/** 옛말이 남은 자리를 사람이 읽을 수 있게 — `expect(hits).toEqual([])` 가 그대로 목록을 찍는다. */
function hits(re: RegExp, allow: (v: Visible) => boolean = () => false): string[] {
  return POOL.filter((v) => re.test(v.text) && !allow(v)).map((v) => `${v.file}:${v.line}  ${v.text}`);
}

// ── 범위 — 자물쇠가 조용히 헐거워지지 않게 ────────────────────────────────
describe("문구 자물쇠의 범위", () => {
  it("app·components·lib 을 전부 훑는다", () => {
    for (const d of SCAN) expect(FILES.some((f) => f.startsWith(`${d}/`))).toBe(true);
    expect(FILES.length).toBeGreaterThanOrEqual(55);
    expect(POOL.length).toBeGreaterThan(800);
  });

  it("문구가 사는 파일이 하나도 빠지지 않았다", () => {
    // 리뷰가 짚은 자리들 — 여기가 풀에 없으면 자물쇠는 잠긴 척만 한다.
    for (const f of [
      "lib/session-label.ts",
      "components/RuntimeCard.tsx",
      "components/RebindDialog.tsx",
      "components/PausedBanner.tsx",
      "app/(app)/sessions/new/page.tsx",
      "app/(app)/settings/page.tsx",
      "app/onboarding/page.tsx",
    ]) {
      expect(FILES).toContain(f);
    }
  });

  it("범위에서 뺀 곳은 이 셋뿐이고, 전부 실제로 있다", () => {
    expect(EXCLUDE).toEqual(["lib/mock", "lib/api/schema.d.ts", "app/dev"]);
    for (const e of EXCLUDE) expect(existsSync(join(ROOT, e))).toBe(true);
  });
});

// ── §8.4 용어표 — 행마다 옛말 0건 ─────────────────────────────────────────
describe("§8.4 용어표 — 옛말이 화면 문자열에 0건", () => {
  const ROWS: [string, RegExp][] = [
    ["Workdir 관리 → 작업 폴더", /Workdir|workdir/],
    ["실행 중 task 0 → 지금 하는 일 없음", /실행 중 task/],
    ["걸린 세션 → 쓰는 중인 세션", /걸린 세션/],
    ["Runtimes → 연결된 컴퓨터", /\bRuntimes\b/],
    ["Sessions → 세션", /\bSessions\b/],
    ["Inbox → 받은 요청", /\bInbox\b/],
    ["Agents → 에이전트", /\bAgents\b/],
    ["Settings → 설정", /\bSettings\b/],
    ["Add a computer → 컴퓨터 연결", /Add a computer/i],
    ["툴 차단 수단 없음 → 도구 제한을 걸 수 없는 컴퓨터입니다", /툴 차단 수단 없음|툴 허용 목록/],
    ["런타임 → 컴퓨터 (산문까지 전부)", /런타임/],
    ["일시정지 · 런타임 오프라인 → 컴퓨터 연결 끊김", /런타임 오프라인/],
  ];
  for (const [label, re] of ROWS) {
    it(label, () => expect(hits(re)).toEqual([]));
  }
});

// ── 내부 용어가 화면에 새지 않는다 ────────────────────────────────────────
describe("내부 용어는 각 화면의 말로", () => {
  const INTERNAL: [string, RegExp][] = [
    ["lane → 작업 줄기", /\blane\b/i],
    ["task → 할 일", /\btask\b/i],
    ["attempt → 실행", /\battempt\b/i],
    ["HITL → 사람 확인", /\bHITL\b/],
    ["머신 → 컴퓨터", /머신/],
    ["seq", /\bseq\b/],
    ["payload", /\bpayload\b/],
    ["idempotency", /idempotenc/i],
    ["probe", /\bprobe\b/i],
    ["uuid", /\buuid\b/i],
    ["slug", /\bslug\b/i],
    ["pgid", /\bpgid\b/i],
    ["stall", /\bstall\b/i],
  ];
  for (const [label, re] of INTERNAL) {
    it(label, () => expect(hits(re)).toEqual([]));
  }

  it("한국어 문장 안에 내부 키를 괄호로 노출하지 않는다 (NN5)", () => {
    // `연쇄 깊이(chain_depth)` 같은 것. 진단 키가 필요하면 data-* 속성이나 「자세히 보기」 안으로.
    expect(hits(/[가-힣]\s*\([a-z][a-z0-9]*_[a-z0-9_]+\)/)).toEqual([]);
  });
});

// ── 예외 하나 — 역할명은 영어를 유지한다 ──────────────────────────────────
describe("§8.4 예외 — 역할명", () => {
  it("Director 를 한국어로 옮기지 않았다", () => {
    expect(hits(/디렉터(?!리)|감독관|연출자/)).toEqual([]);
  });
});

// ── 진단 원문은 「자세히 보기」 안에서만 ──────────────────────────────────
describe("진단 원문의 자리", () => {
  // `데몬` 은 사용자가 직접 설치하는 물건이라 이름을 지우면 오히려 설명이 안 된다(§8.4 표에도 없다).
  const DIAG = /\bACP\b|어댑터|\bprotocol\b/;

  it("진단 용어는 컴퓨터 카드 한 곳에만 있다", () => {
    const outside = hits(DIAG, (v) => v.file === "components/RuntimeCard.tsx");
    expect(outside).toEqual([]);
  });

  it("사람이 지금 해야 할 일(밖에 남는 줄)에는 진단 용어가 없다", () => {
    const worst = {
      kind: "claude_code" as const,
      logged_in: false,
      adapter_version: null,
      protocol_version: 1,
      resume: false,
      usage: false,
      tool_disallow: false,
      brief_transport: "instruction_file" as const,
      allow_once_missing: true,
      models: [],
    };
    const alerts = capabilityAlerts(worst);
    expect(alerts.some((a) => DIAG.test(a))).toBe(false);
    expect(alerts.some((a) => /런타임/.test(a))).toBe(false);
    // 진단 원문은 접힌 쪽에 그대로 있어야 한다 — 로그와 대조하려면 원문이어야 하기 때문이다.
    expect(capabilityDetails(worst).some((d) => DIAG.test(d))).toBe(true);
  });
});

// ── B1 이 다시 나면 여기서 걸린다 ─────────────────────────────────────────
describe("S5 배지 — EVAL_USER U12 가 못박은 말(B1)", () => {
  it("컴퓨터가 끊겨 멈춘 세션의 배지는 `일시정지 · 컴퓨터 연결 끊김`", () => {
    expect(sessionBadgeLabel({ status: "paused", paused_reason: "runtime_offline", running_lane_count: 0 })).toBe(
      "일시정지 · 컴퓨터 연결 끊김",
    );
  });

  it("같은 사건을 인박스와 배지가 같은 말로 부른다", () => {
    expect(PAUSE_REASON_LABEL.runtime_offline).toContain("컴퓨터");
    expect(PAUSE_REASON_LABEL.runtime_offline).not.toContain("런타임");
  });
});
