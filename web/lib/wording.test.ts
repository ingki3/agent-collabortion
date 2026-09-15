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
import { VERDICT_LABEL, NOT_MEASURABLE } from "@/lib/settings";
import { transportLabel } from "@/lib/test-chat";
import { PAGE_COPY, type Screen } from "@/components/PageHead";
import { NAV_ITEMS } from "@/components/AppNav";
import { BLOCKED_REASON, CONDITION_EDITOR, CONDITION_NAME, DELETE_DIALOG, FIX_CONDITION, PROGRESS, SESSION_DELETED_NOTICE, SESSION_MENU, conditionName } from "@/lib/wording";

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

/** `${cond ? "a" : "b"}` → ` "a" "b" ` — 보간 안의 문자열 리터럴만 남긴다. 리터럴이 없으면 `…`. */
function keepQuoted(expr: string): string {
  const quoted = [...expr.matchAll(/"[^"\n]*"|'[^'\n]*'/g)].map((m) => m[0]);
  return quoted.length ? ` ${quoted.join(" ")} ` : "…";
}

/** 사람이 읽을 수 있는 것만 — JSX 텍스트 노드 + 산문처럼 생긴 문자열 리터럴. */
function visibleStrings(file: string, src: string): Visible[] {
  const out: Visible[] = [];
  const body = src
    .replace(/<style>\{`[\s\S]*?`\}<\/style>/g, "") // CSS 블록
    .replace(/^\s*import\s.*$/gm, "") // 모듈 경로
    // `${lane.queue_position}` 은 코드다 — 보간 안의 **식별자**만 지우고 **따옴표 문자열은 살린다**(중첩 삼항까지
    // 안쪽부터). 통째로 지우면 `${ok ? "런타임 오프라인" : "…"}` 같은 삼항 분기 문구가 사각지대가 된다(PR #188 NN1 —
    // 주입 INJ2 가 359 초록으로 통과했다).
    .replace(/\$\{[^{}]*\}/g, keepQuoted)
    .replace(/\$\{[^{}]*\}/g, keepQuoted)
    .replace(/\$\{[^{}]*\}/g, keepQuoted)
    // className·data-* 등 화면에 읽히지 않는 속성 값도 코드다.
    .replace(/\b(className|class|data-[a-z-]+|key|href|src|htmlFor|role|id)=\{?["'`][^"'`]*["'`]\}?/g, "");
  // 치환한 `body` 의 줄을 읽는다 — 원본 `src` 가 아니다(PR #199 리뷰 NN2 가 의심한 자리. 아래 "NN2" 테스트가 원본에는 없는
  // 치환 결과만이 풀에 드는 것으로 이를 잰다).
  body.split("\n").forEach((bodyLine, i) => {
    const s = bodyLine.trim();
    if (s.startsWith("//") || s.startsWith("*") || s.startsWith("/*")) return; // 주석은 문구가 아니다
    for (const m of bodyLine.matchAll(/>([^<>{}]+)</g)) {
      const t = m[1].split(/\s+/).filter(Boolean).join(" ");
      if (t && !/^[\s;,.()[\]/*+&|-]*$/.test(t)) out.push({ file, line: i + 1, text: t });
    }
    for (const m of bodyLine.matchAll(/"([^"\n]{2,})"|'([^'\n]{2,})'|`([^`\n]{2,})`/g)) {
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
      // T-W6 — S14 8탭·대시보드 · S10 시험 대화 · W-10 의 문구가 사는 곳
      "components/SettingsTabs.tsx",
      "components/MembersTab.tsx",
      "components/MetricsTable.tsx",
      "components/TestChatPanel.tsx",
      "lib/settings.ts",
      "lib/test-chat.ts",
      // T-W13 — S5 카드 옵션(…)·삭제 다이얼로그의 문구가 사는 곳
      "lib/wording.ts",
      "components/SessionCardMenu.tsx",
      "components/DeleteSessionDialog.tsx",
      // T-W15 — 종료 조건(마법사 6단계 · S7 진행률 · 조건 고치기)의 문구가 사는 곳
      "lib/completion.ts",
      "components/ConditionRow.tsx",
      "components/ConditionEditor.tsx",
      "components/FixConditionDialog.tsx",
      "components/SessionAside.tsx",
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

// ── 자물쇠 확장 (a) — `${…}` 안의 문구도 본다 (PR #188 NN1) ────────────────
describe("보간식 안의 문구도 풀에 든다 (NN1)", () => {
  it("삼항 분기의 문자열 리터럴이 살아남는다", () => {
    const v = visibleStrings("x.tsx", '<b>{`${ok ? "이어서 실행" : "처음부터 실행"} · ${lane.queue_position}`}</b>');
    // 템플릿 리터럴 하나가 풀의 항목 하나다 — 안의 문구는 남고 식별자는 … 로 지워진다.
    const texts = v.map((x) => x.text);
    expect(texts.some((t) => t.includes("이어서 실행") && t.includes("처음부터 실행"))).toBe(true);
    expect(texts.some((t) => /queue_position|\bok\b/.test(t))).toBe(false);
  });

  it("옛말을 보간식에 숨기면 걸린다 — INJ2 재현", () => {
    const v = visibleStrings("x.tsx", 'const l = `${paused ? "일시정지 · 런타임 오프라인" : "진행 중"}`;');
    expect(v.some((x) => /런타임 오프라인/.test(x.text))).toBe(true);
  });

  it("루프는 치환한 body 의 줄을 읽는다 — 원본에는 없는 치환 결과가 풀에 든다 (PR #199 NN2)", () => {
    // 여러 줄에 걸친 `${…}` 는 원본에서는 어느 한 줄의 백틱 리터럴로도 잡히지 않는다(`[^`\n]`). keepQuoted 가 한 줄로 접은 뒤의
    // body 를 읽어야만 안의 문구가 풀에 든다 — 원본 `line` 을 읽었다면 이 단언은 실패한다.
    const v = visibleStrings("x.tsx", "const l = `${paused\n  ? \"일시정지 · 런타임 오프라인\"\n  : \"진행 중\"}`;");
    expect(v.map((x) => x.text)).toEqual([' "일시정지 · 런타임 오프라인" "진행 중" ']);
    // 같은 이유로 식별자는 지워진 채다 — 원본을 읽었다면 `paused` 가 남는다.
    expect(v.some((x) => /\bpaused\b/.test(x.text))).toBe(false);
  });

  it("실제 소스에서 보간식 안에 사는 문구가 풀에 있다", () => {
    // RebindDialog 의 확인 버튼 — `${chosen.runtime.name} 으로 옮기기` 는 식별자와 문구가 한 리터럴에 섞인 예다.
    expect(POOL.some((v) => v.file === "components/RebindDialog.tsx" && /으로 옮기기/.test(v.text))).toBe(true);
  });
});

// ── 자물쇠 확장 (d) — 새 문구가 **실제로 쓰이는지** (PR #188 NN4) ─────────
describe("새 문구의 존재 — 옛말 0건만으로는 안 잰다 (NN4)", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));

  it("EVAL_USER 가 이름으로 못박은 다이얼로그 제목 — `다른 컴퓨터로 옮기기`", () => {
    expect(inPool("components/RebindDialog.tsx", "다른 컴퓨터로 옮기기")).toBe(true);
  });

  it("§8.4 표의 새말이 각자 제자리에 있다", () => {
    expect(inPool("components/AppNav.tsx", "연결된 컴퓨터")).toBe(true);
    expect(inPool("components/AppNav.tsx", "받은 요청")).toBe(true);
    expect(inPool("app/(app)/runtimes/page.tsx", "작업 폴더")).toBe(true);
    expect(inPool("app/(app)/runtimes/page.tsx", "쓰는 중인 세션")).toBe(true);
    expect(inPool("app/(app)/runtimes/page.tsx", "컴퓨터 연결")).toBe(true);
    expect(inPool("components/RuntimeCard.tsx", "도구 제한을 걸 수 없는 컴퓨터입니다")).toBe(true);
  });

  // §8.5 — 화면 제목 아래 한 줄 설명. 표(PAGE_COPY)에만 있고 화면이 안 쓰면 없는 것과 같다.
  const PAGE_FILES: Record<Screen, string> = {
    sessions: "app/(app)/sessions/page.tsx",
    inbox: "app/(app)/inbox/page.tsx",
    agents: "app/(app)/agents/page.tsx",
    computers: "app/(app)/runtimes/page.tsx",
    settings: "app/(app)/settings/page.tsx",
  };
  const SCREENS = Object.keys(PAGE_COPY) as Screen[];

  it("다섯 화면 설명이 §8.4 의 말이고 한 줄이다", () => {
    expect(SCREENS.sort()).toEqual(["agents", "computers", "inbox", "sessions", "settings"]);
    for (const k of SCREENS) {
      const { title, desc } = PAGE_COPY[k];
      expect(desc.length).toBeGreaterThanOrEqual(15);
      expect(desc.length).toBeLessThanOrEqual(60);
      expect(desc).not.toMatch(/\n/);
      expect(desc).toMatch(/[가-힣]/);
      // 제목은 내비 라벨과 같은 말 — 메뉴에서 누른 것과 화면에 적힌 것이 다르면 안 된다.
      expect(NAV_ITEMS.map((i) => i.label)).toContain(title);
      // 설명은 문구 풀에 들어 있어야 자물쇠(옛말 0건)가 본다.
      expect(inPool("components/PageHead.tsx", desc)).toBe(true);
    }
  });

  it.each(SCREENS)("%s 화면이 PageHead 로 그 설명을 실제로 그린다", (k) => {
    const src = readFileSync(join(ROOT, PAGE_FILES[k]), "utf8");
    expect(src).toMatch(new RegExp(`<PageHead screen="${k}"`));
  });

  // T-W6 — S14 · S10 · W-10 의 새말이 제자리에 있고 실제로 그려진다.
  it("S14 탭 이름은 §8.4 의 말이고(런타임 정책 → 컴퓨터 정책 · Workdir → 작업 폴더) 탭 표가 화면에 쓰인다", () => {
    for (const label of ["멤버", "컴퓨터 정책", "예산", "루프 상한", "컨텍스트", "작업 폴더", "보안", "알림", "대시보드"]) {
      expect(inPool("lib/settings.ts", label)).toBe(true);
    }
    expect(readFileSync(join(ROOT, PAGE_FILES.settings), "utf8")).toMatch(/SETTINGS_TABS\.map\(/);
  });

  it("「바꿨을 때의 영향」 — U14 가 못박은 문장이 표에 있고 화면이 그 표를 그린다", () => {
    expect(inPool("lib/settings.ts", "낮추면 정상적인 리뷰 왕복이 막힐 수 있습니다")).toBe(true);
    expect(inPool("lib/settings.ts", "이후 diff·셸 출력은 요약만 저장됩니다. 기존 로그는 그대로")).toBe(true);
    const tabs = readFileSync(join(ROOT, "components/SettingsTabs.tsx"), "utf8");
    expect(tabs).toMatch(/impact=\{IMPACT\.max_pair_roundtrips\}/);
    expect(tabs).toMatch(/impact=\{IMPACT\.task_event_masking\}/);
  });

  it("대시보드 — 표본이 없으면 '아직 잴 수 없음'(0 을 실측처럼 보이지 않는다)", () => {
    expect(inPool("lib/settings.ts", "아직 잴 수 없음")).toBe(true);
    expect(readFileSync(join(ROOT, "components/MetricsTable.tsx"), "utf8")).toMatch(/formatMetricValue\(m\.value, m\.unit\)/);
  });

  it("대시보드 판정 라벨 셋 — 화면 텍스트+title 로 그려지는 VERDICT_LABEL 이 못박혀 있다 (PR #199 NN1)", () => {
    // `"미측정"` 으로 바꿔도 초록이던 구멍 — 세 라벨의 존재를 재고, unknown 은 값 칸의 NOT_MEASURABLE 과 같은 말이어야 한다
    // (같은 사건을 두 칸이 다른 말로 부르면 안 된다).
    expect(VERDICT_LABEL).toEqual({ met: "목표 충족", missed: "목표 미달", unknown: NOT_MEASURABLE });
    for (const label of Object.values(VERDICT_LABEL)) expect(inPool("lib/settings.ts", label)).toBe(true);
    const table = readFileSync(join(ROOT, "components/MetricsTable.tsx"), "utf8");
    expect(table).toMatch(/VERDICT_LABEL\[/);
  });

  it("시험 대화 — '세션이 아니다' 안내 한 줄과 잠금 사유가 화면에 있다", () => {
    expect(inPool("components/TestChatPanel.tsx", "세션이 아닙니다")).toBe(true);
    expect(inPool("lib/test-chat.ts", "답을 기다리는 중입니다")).toBe(true);
    expect(inPool("lib/test-chat.ts", "닫힌 시험 대화입니다")).toBe(true);
  });

  it("시험 대화 — 실행 경로 자리는 값이 없을 때 '첫 답이 오면 표시'(FR-1.8.1 상시 배지) (PR #199 NN5)", () => {
    expect(transportLabel(null)).toBe("첫 답이 오면 표시");
    expect(transportLabel(undefined)).toBe("첫 답이 오면 표시");
    expect(transportLabel("acp")).toBe("ACP");
    expect(inPool("lib/test-chat.ts", "첫 답이 오면 표시")).toBe(true);
    expect(readFileSync(join(ROOT, "components/TestChatPanel.tsx"), "utf8")).toMatch(/transportLabel\(/);
  });

  it("W-10 — 세션 설정의 컴퓨터는 이름이고, 없으면 '연결 끊긴 컴퓨터'. id 앞 8자(slice(0, 8))는 사라졌다", () => {
    expect(inPool("lib/session-label.ts", "연결 끊긴 컴퓨터")).toBe(true);
    const aside = readFileSync(join(ROOT, "components/SessionAside.tsx"), "utf8");
    expect(aside).not.toMatch(/runtime_id\.slice\(0, 8\)/);
    expect(readFileSync(join(ROOT, "app/(app)/sessions/[id]/page.tsx"), "utf8")).toMatch(/runtimeName=\{runtimeNameOf\(/);
  });

  it("W-8 — 컴퓨터 카드의 브리프 문구는 probe 값 그대로, 옛 설명(CLAUDE.md·AGENTS.md)은 없다", () => {
    expect(hits(/CLAUDE\.md·AGENTS\.md|지시 파일\(/)).toEqual([]);
    expect(inPool("components/RuntimeCard.tsx", "브리프 전달:")).toBe(true);
  });

  it("S14 비활성 사유 — 저장 버튼이 DisabledHint 를 aria-describedby 로 가리킨다(§8.5)", () => {
    const tabs = readFileSync(join(ROOT, "components/SettingsTabs.tsx"), "utf8");
    expect(tabs).toMatch(/<DisabledHint id=\{hintId\}>/);
    expect(tabs).toMatch(/aria-describedby=\{!right\.ok \? hintId : undefined\}/);
    const members = readFileSync(join(ROOT, "components/MembersTab.tsx"), "utf8");
    expect(members).toMatch(/<DisabledHint id="members-manage-hint">/);
  });

  // T-W13 — S5 카드 옵션(…) · 삭제 확인 다이얼로그(SCREEN §4.3 · §5). 표(lib/wording.ts)에만 있고 화면이 안 그리면 없는 것과 같다.
  it("카드 옵션 — 메뉴 항목 둘 · 비활성 사유 둘(SCREEN §4.3 문장 그대로)이 표에 있고 메뉴가 그 표를 그린다", () => {
    expect(SESSION_MENU).toMatchObject({ button: "세션 옵션", open: "세션 열기", delete: "삭제" });
    expect(SESSION_MENU.blocked_active).toBe("진행 중인 세션은 먼저 종료하세요");
    expect(SESSION_MENU.blocked_role).toBe("Director 나 소유자·관리자만 삭제할 수 있습니다");
    for (const t of Object.values(SESSION_MENU)) expect(inPool("lib/wording.ts", t)).toBe(true);
    const menu = readFileSync(join(ROOT, "components/SessionCardMenu.tsx"), "utf8");
    expect(menu).toMatch(/aria-label=\{SESSION_MENU\.button\}/);
    expect(menu).toMatch(/\{SESSION_MENU\.open\}/);
    expect(menu).toMatch(/\{SESSION_MENU\.delete\}/);
    // 비활성 사유는 항목 바로 아래 DisabledHint + aria-describedby (§8.5) — title 만이 아니다.
    expect(menu).toMatch(/<DisabledHint id=\{hintId\}>\{gate\.reason\}<\/DisabledHint>/);
    expect(menu).toMatch(/aria-describedby=\{!gate\.ok \? hintId : undefined\}/);
    // 사유는 이 표에서만 — 메뉴 파일에 사유 문장 리터럴이 없다.
    expect(menu).not.toContain("먼저 종료하세요");
    expect(menu).not.toContain("소유자·관리자만");
  });

  it("삭제 다이얼로그 — §5 규칙(무엇이 사라지는지 · 되돌릴 수 없음 · 이 컴퓨터의 작업 폴더)과 409 머리말·S13 링크가 표에 있고 다이얼로그가 그 표를 그린다", () => {
    expect(DELETE_DIALOG.title("X")).toContain("X");
    expect(DELETE_DIALOG.loses).toMatch(/메시지/);
    expect(DELETE_DIALOG.loses).toMatch(/작업 줄기/);
    expect(DELETE_DIALOG.loses).toMatch(/아티팩트/);
    expect(DELETE_DIALOG.loses).toMatch(/비용 기록/);
    expect(DELETE_DIALOG.irreversible).toMatch(/되돌릴 수 없습니다/);
    expect(DELETE_DIALOG.irreversible).toMatch(/이 컴퓨터의 작업 폴더/);
    expect(DELETE_DIALOG.confirm).toBe("삭제");
    expect(DELETE_DIALOG.cancel).toBe("취소");
    expect(DELETE_DIALOG.workdirs_link).toBe("작업 폴더 관리");
    for (const t of [DELETE_DIALOG.loses, DELETE_DIALOG.irreversible, DELETE_DIALOG.workdirs_head, DELETE_DIALOG.workdirs_link, DELETE_DIALOG.busy]) expect(inPool("lib/wording.ts", t)).toBe(true);
    const dlg = readFileSync(join(ROOT, "components/DeleteSessionDialog.tsx"), "utf8");
    for (const k of ["title(session.title)", "loses", "irreversible", "workdirs_head", "workdirs_link", "confirm", "cancel", "busy"]) expect(dlg).toContain(`DELETE_DIALOG.${k}`);
    expect(dlg).toMatch(/role="alertdialog"/);
    expect(dlg).toContain('className="btn del-session__danger"'); // 「삭제」 는 위험 색
    expect(dlg).toMatch(/Link href=\{workdirsHref\(session\.runtime_id\)\}/); // S13 링크
  });

  it("S7 → S5 안내 한 줄이 표에 있고 두 화면이 그 표를 쓴다", () => {
    expect(SESSION_DELETED_NOTICE.elsewhere("X")).toContain("X");
    expect(SESSION_DELETED_NOTICE.mine("X")).toContain("X");
    const s5 = readFileSync(join(ROOT, PAGE_FILES.sessions), "utf8");
    expect(s5).toContain("SESSION_DELETED_NOTICE.elsewhere(");
    expect(s5).toContain("SESSION_DELETED_NOTICE.mine(");
    expect(readFileSync(join(ROOT, "app/(app)/sessions/[id]/page.tsx"), "utf8")).toMatch(/case "session\.deleted"/);
  });

  it("비활성 사유가 버튼 근처에 있다 — title 만으로는 안 된다 (§8.5)", () => {
    const sessions = readFileSync(join(ROOT, PAGE_FILES.sessions), "utf8");
    const runtimes = readFileSync(join(ROOT, PAGE_FILES.computers), "utf8");
    expect(sessions).toMatch(/<DisabledHint id="new-session-hint">/);
    expect(sessions).toMatch(/aria-describedby=\{noRuntime \? "new-session-hint"/);
    expect(runtimes).toMatch(/<DisabledHint id="add-computer-hint">/);
    expect(runtimes).toMatch(/aria-describedby=\{!canManage \? "add-computer-hint"/);
  });
});

// ── T-W15 — 종료 조건의 말(S-84 · W-19, SCREEN §4.4 6단계 · §4.5 "종료 조건 진행률") ─────────────────────────
describe("종료 조건 — 이름은 사람 말이고 한곳(lib/wording.ts)에서만 나온다 (T-W15)", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");

  it("계약 enum 넷 + v1.1 하나의 이름이 SCREEN §4.4 의 말이다 — 보고서 제출 · Director 승인 · <에이전트> 의 검토 승인 · 수동 종료", () => {
    expect(CONDITION_NAME).toEqual({ artifact_submitted: "보고서 제출", agent_approval: "에이전트 검토 승인", user_approval: "Director 승인", manual: "수동 종료", criteria_met: "성공 기준 충족" });
    expect(conditionName("agent_approval", "Lead")).toBe("Lead 의 검토 승인");
    expect(conditionName("agent_approval", null)).toBe("에이전트 검토 승인");
    for (const t of Object.values(CONDITION_NAME)) expect(inPool("lib/wording.ts", t)).toBe(true);
  });

  it("옛 이름(아티팩트 제출 · 에이전트 승인)과 계약 enum 이 화면 문자열에 없다 — CONDITION_LABEL 표는 사라졌다", () => {
    expect(hits(/아티팩트 제출|에이전트 승인(?!을)/, (v) => v.file === "lib/wording.ts" && /검토 승인/.test(v.text))).toEqual([]);
    // 조건 이름을 손으로 다시 적은 자리가 없다 — 이름은 conditionName 하나에서만.
    for (const f of ["components/ConditionRow.tsx", "components/ConditionEditor.tsx", "components/SessionAside.tsx", "app/(app)/sessions/new/page.tsx", "components/FixConditionDialog.tsx"]) {
      expect(src(f)).not.toMatch(/CONDITION_LABEL/);
      expect(src(f)).not.toContain('"보고서 제출"');
      expect(src(f)).not.toContain('"Director 승인"');
    }
  });

  it("막힌 이유 — 계약 blocked_reason enum 셋 전부에 문장이 있고 ConditionRow 가 그 표를 그린다", () => {
    expect(Object.keys(BLOCKED_REASON).sort()).toEqual(["agent_archived", "reviewer_missing", "reviewer_not_participant"]);
    expect(BLOCKED_REASON.reviewer_missing).toBe("리뷰어가 지정되지 않아 아무도 승인할 수 없습니다");
    for (const t of Object.values(BLOCKED_REASON)) expect(inPool("lib/wording.ts", t)).toBe(true);
    expect(src("components/ConditionRow.tsx")).toMatch(/blockedReasonText\(p\.blockedReason\)/);
    // 이유 문장은 표에서만 — 컴포넌트에 리터럴이 없다.
    expect(src("components/ConditionRow.tsx")).not.toContain("리뷰어가 지정되지 않아");
  });

  it("진행률 두 번째 줄 — 받은 요청에서 승인하세요 · <누구> 차례 · 상단 요약 '남은 것: … 막힘 N개' 가 표에 있고 화면이 그 표를 쓴다", () => {
    expect(PROGRESS.user_approval_next).toBe("받은 요청에서 승인하세요");
    expect(PROGRESS.turn("Lead")).toBe("Lead 차례");
    expect(PROGRESS.summary(["Director 승인"], 1, "and")).toBe("남은 것: Director 승인 1개 · 막힘 1개");
    expect(PROGRESS.met_by("Writer", "9/13")).toBe("Writer, 9/13");
    for (const t of [PROGRESS.user_approval_next, PROGRESS.manual_next, PROGRESS.summary_satisfied, PROGRESS.summary_completed, PROGRESS.blocked_director, PROGRESS.blocked_member]) expect(inPool("lib/wording.ts", t)).toBe(true);
    const aside = src("components/SessionAside.tsx");
    expect(aside).toMatch(/progressSummary\(prog, topOp\(s\.completion_condition\), closed\)/);
    expect(aside).toMatch(/PROGRESS\.blocked_director : PROGRESS\.blocked_member/);
    expect(aside).toMatch(/\{FIX_CONDITION\.button\}/);
  });

  it("마법사 6단계 — 리뷰어 필수 사유 · 담당 안내 · 요약 접속사(그리고/또는)가 표에 있고 편집기가 그 표를 그린다", () => {
    expect(CONDITION_EDITOR.reviewer_required).toContain("리뷰어를 고르세요");
    expect(CONDITION_EDITOR.reviewer_is_assignee).toContain("다른 에이전트를 권합니다");
    expect(CONDITION_EDITOR.join_and).toBe(" 그리고 ");
    expect(CONDITION_EDITOR.join_or).toBe(" 또는 ");
    for (const t of [CONDITION_EDITOR.reviewer_required, CONDITION_EDITOR.reviewer_is_assignee, CONDITION_EDITOR.need_one, CONDITION_EDITOR.no_human_gate, CONDITION_EDITOR.submitter_default]) expect(inPool("lib/wording.ts", t)).toBe(true);
    const editor = src("components/ConditionEditor.tsx");
    for (const k of ["reviewer_required", "reviewer_is_assignee", "reviewer_placeholder", "submitter_default", "no_human_gate", "op_and", "op_or"]) expect(editor).toContain(`CONDITION_EDITOR.${k}`);
    // 마법사와 다이얼로그가 **같은 편집기**를 그린다 — 두 자리가 다른 편집기를 가지면 한쪽에서만 리뷰어를 잊는다.
    expect(src("app/(app)/sessions/new/page.tsx")).toMatch(/<ConditionEditor\b/);
    expect(src("components/FixConditionDialog.tsx")).toMatch(/<ConditionEditor\b/);
    expect(src("app/(app)/sessions/new/page.tsx")).not.toMatch(/submitter-select|reviewer-select/); // 편집기 안에만 있다
  });

  it("「조건 고치기」 — 제목·안내·버튼이 표에 있고 다이얼로그가 그 표를 쓴다 · S7 이 Director 에게만 넘긴다", () => {
    expect(FIX_CONDITION.button).toBe("조건 고치기");
    for (const t of Object.values(FIX_CONDITION)) expect(inPool("lib/wording.ts", t)).toBe(true);
    const dlg = src("components/FixConditionDialog.tsx");
    for (const k of ["title", "note", "save", "cancel", "busy"]) expect(dlg).toContain(`FIX_CONDITION.${k}`);
    expect(dlg).toMatch(/aria-describedby=\{!gate\.ok \? hintId : undefined\}/); // 저장 비활성 사유는 근처에(§8.5)
    expect(src("app/(app)/sessions/[id]/page.tsx")).toMatch(/onFixCondition=\{session\.my_role === "director" && !closed \? \(\) => setFixCondOpen\(true\) : undefined\}/);
  });

  it("내부 역할명 assignee 가 화면 문자열에 없다 — 담당 에이전트", () => {
    // 옛 마법사 행 "(assignee)" 와 요약 "제출자 assignee" 가 그 자리다. S6 요약의 `assignee` 상태 변수(JSX 식 안)는 코드지 문구가 아니다.
    expect(hits(/\(assignee\)|제출자 assignee/)).toEqual([]);
    expect(hits(/\bassignee\b/, (v) => /invitable\.find/.test(v.text))).toEqual([]);
  });
});
