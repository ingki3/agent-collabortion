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
import { BADGE_MAP } from "@/components/badge-map";
import { ARCHIVE_DIALOG, CREATE_ROOM, DELETE_ROOM_DIALOG, ROOM_BLOCKED_LABEL, ROOM_DELETED_NOTICE, ROOM_LIST, ROOM_MENU, roomDefaultsLine } from "@/lib/wording";
import { BLOCK_DIALOG, ROOM_BANNER, ROOM_CENTER, ROOM_HEAD, ROOM_LEFT, ROOM_NOTICES, ROOM_PANEL, ROOM_TABS, SUMMARIZE_DIALOG, WORK_CHIPS, WORK_PANEL, WORK_PAUSE_LABEL, WORK_SELECTOR } from "@/lib/wording";
import { MESSAGE_LAYERS, PROCESS_ACTION } from "@/lib/wording";
import { ROOM_RENAME } from "@/lib/wording";
import { BLOCKED_REASON, COMMAND_LABEL, CONDITION_EDITOR, CONDITION_NAME, EMPTY_TURN, FIX_CONDITION, OBSERVATIONS, PROGRESS, ROLE_COMMANDS, ROUTING_PLATFORM_LABEL, ROUTING_RULE_LABEL, conditionName, routingKindLabel } from "@/lib/wording";

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
  // 백틱도 — `${n ? ` · 쓰는 방 ${n}개` : ""}` 처럼 보간 안에 템플릿 리터럴이 겹치면 그 문구가 사각지대였다(R1.5 에서 RuntimeCard 의 「세션」 하나가 여기 숨어 있었다).
  // 안쪽 백틱은 큰따옴표로 바꿔 남긴다 — 그대로 두면 바깥 템플릿의 백틱과 짝이 어긋나 가운데 문구가 두 리터럴 사이로 빠진다.
  const quoted = [...expr.matchAll(/"[^"\n]*"|'[^'\n]*'|`[^`\n]*`/g)].map((m) => (m[0].startsWith("`") ? `"${m[0].slice(1, -1)}"` : m[0]));
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
  // 여러 줄에 걸친 JSX 텍스트 — `<p className="small">\n  이 세션을 종료합니다 …\n</p>` 는 어느 한 줄에도 `>…<` 가 없어 위 루프가
  // 못 줍는다(PR #317 NN1 — 사람에게 보이는 「세션」 6곳이 여기 숨어 있었다). body 전체에서 줄을 넘는 `>…<` 만 골라 줄마다 센다.
  // 줄을 넘는 구간은 `a > 0 && (\n <p` 같은 코드 조각일 수도 있어 **한글이 있는 줄만** 넣는다(영어 산문은 한 줄 규칙 몫).
  for (const m of body.matchAll(/>([^<>{}]+)</g)) {
    if (!m[1].includes("\n")) continue;
    const start = body.slice(0, m.index! + 1).split("\n").length;
    m[1].split("\n").forEach((piece, k) => {
      const t = piece.split(/\s+/).filter(Boolean).join(" ");
      if (!t || !/[가-힣]/.test(t) || /^(\/\/|\*|\/\*)/.test(t)) return;
      out.push({ file, line: start + k, text: t });
    });
  }
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
      "components/CreateWorkDialog.tsx", // S6 마법사가 지워진 자리(T-R2-W4b) — 조건 편집기를 그리는 곳
      "app/(app)/settings/page.tsx",
      "app/onboarding/page.tsx",
      // T-W6 — S14 8탭·대시보드 · S10 시험 대화 · W-10 의 문구가 사는 곳
      "components/SettingsTabs.tsx",
      "components/MembersTab.tsx",
      "components/MetricsTable.tsx",
      "components/TestChatPanel.tsx",
      "lib/settings.ts",
      "lib/test-chat.ts",
      // T-W13 — 화면 문구 표(옛 S5 카드 옵션·삭제 다이얼로그는 R1.5b 에서 옛 세션 화면과 함께 지웠다)
      "lib/wording.ts",
      // T-W15 — 종료 조건(마법사 6단계 · S7 진행률 · 조건 고치기)의 문구가 사는 곳
      "lib/completion.ts",
      "components/ConditionRow.tsx",
      "components/ConditionEditor.tsx",
      "components/WorkEditDialogs.tsx", // 조건 고치기 — 옛 FixConditionDialog(옛 세션 화면 전용)는 R1.5b 에서 지웠다
      "components/SessionAside.tsx",
      // T-W16 — 관찰 표 · 역할의 허용 명령 · 빈 턴 카드의 문구가 사는 곳
      "components/ObservationsTable.tsx",
      "components/RoleCommands.tsx",
      "components/ActivityFeed.tsx",
      "components/LaneCard.tsx",
      "lib/commands.ts",
      "lib/feed.ts",
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
    ["걸린 세션 → 쓰는 중인 방", /걸린 세션/],
    ["Runtimes → 연결된 컴퓨터", /\bRuntimes\b/],
    ["Sessions → 방", /\bSessions\b/],
    // v0.19 R1.5 (PRD §3.2 · SCREEN §3.4) — 「세션」은 문맥에 따라 방 또는 미션, 「작업 줄기」는 서브 미션
    ["세션 → 방 · 미션", /세션/],
    ["작업 줄기 → 서브 미션", /작업\s*줄기/],
    ["산출물 → 아티팩트 (바꾸지 않는다 — PRD §3.2)", /산출물/],
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

// ── R1.5 — PRD §3.2 화면 용어(Director 확정 2026-09-23) · SCREEN §3.4(d) 다섯 가지 ─────────────────
// 1 은 위 §8.4 표의 행(세션 · 작업 줄기 · 산출물 · 걸린 세션)이 잰다. 여기는 2~5 — 「옛말 0건」만 재면 문구가 통째로 사라져도 초록이다.
describe("R1.5 화면 용어 — 방 · 미션 · 서브 미션 · 할 일 (PRD §3.2 · SCREEN §3.4(d))", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));

  it("(2) 새말이 실제로 쓰인다 — 방 · 미션 · 서브 미션 · 아티팩트 · 할 일 · 작업 폴더 · 결정 기록", () => {
    for (const w of ["방", "미션", "서브 미션", "아티팩트", "할 일", "작업 폴더", "결정 기록"]) {
      expect(POOL.filter((v) => v.text.includes(w)).length, w).toBeGreaterThan(0);
    }
    // 제자리: 내비의 「방」 · 방 머리의 세 층 요약(§3.2 「미션 3개 · 서브 미션 5개 · 할 일 12개」) · 서브 미션 보드 · S21 의 제출자.
    expect(NAV_ITEMS.map((i) => i.label)).toContain("방");
    expect([ROOM_HEAD.layer_works[0], ROOM_HEAD.layer_lanes[0], ROOM_HEAD.layer_tasks[0]]).toEqual(["미션 ", "서브 미션 ", "할 일 "]);
    expect(inPool("components/LaneBoard.tsx", "서브 미션이 하나 생깁니다")).toBe(true);
    expect(inPool("components/Composer.tsx", "새 서브 미션으로 보내기")).toBe(true);
    expect(CONDITION_NAME.artifact_submitted).toBe("아티팩트 제출");
    expect(COMMAND_LABEL).toMatchObject({ room_get: "방 읽기", artifact_get: "아티팩트 읽기", artifact_submit: "아티팩트 제출" });
  });

  it("(3) 「일」 단독을 층 이름으로 쓰지 않는다 — 「일 밖」·「일 하나당」·「동시에 맡는 일」은 할 일/미션 중 무엇인지 말하지 않는다", () => {
    expect(hits(/(?<!할 )(?<![가-힣])일 (밖|하나당|하나의)|동시에 맡(을 수 있|)는 일(?![가-힣])|(?<![가-힣])새 일(?![가-힣])|그 이상의 일(?![가-힣])/)).toEqual([]);
  });

  it("(5) 「멈춤」은 방, 「일시정지」는 미션·서브 미션·할 일 — 층이 섞이지 않는다", () => {
    for (const t of Object.values(ROOM_BLOCKED_LABEL)) {
      expect(t).toMatch(/멈춤/);
      expect(t).not.toMatch(/일시정지/);
    }
    for (const t of Object.values(ROOM_BANNER).flat()) expect(t).not.toMatch(/일시정지/);
    for (const kind of ["lane", "task", "session", "work"] as const) {
      expect((BADGE_MAP[kind] as Record<string, { label: string }>).paused?.label, kind).toBe("일시정지");
    }
    for (const t of Object.values(WORK_PAUSE_LABEL)) expect(t).not.toMatch(/멈춤/);
    // U12 가 못박은 배지 둘은 그대로다(§3.4(b) 「그대로 둔다」).
    expect(sessionBadgeLabel({ status: "paused", paused_reason: "runtime_offline", running_lane_count: 0 })).toBe("일시정지 · 컴퓨터 연결 끊김");
  });

  it("같은 사유는 같은 말 — 방 배너·S17 상황 문장이 「오프라인」 대신 「연결이 끊겼습니다」(SCREEN §3.4(b) 마지막 행)", () => {
    expect([ROOM_BANNER.runtime_offline.join(""), ROOM_BANNER.runtime_offline_plain].every((t) => t.includes("연결이 끊겼습니다"))).toBe(true);
    expect(hits(/오프라인입니다|일간 오프라인/)).toEqual([]);
  });
});

// ── 내부 용어가 화면에 새지 않는다 ────────────────────────────────────────
describe("내부 용어는 각 화면의 말로", () => {
  const INTERNAL: [string, RegExp][] = [
    ["lane → 서브 미션", /\blane\b/i],
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

  it("보간 안에 겹친 템플릿 리터럴의 문구도 풀에 든다 (R1.5 — RuntimeCard 의 「세션」이 여기 숨어 있었다)", () => {
    const v = visibleStrings("x.tsx", "const t = `${a} · 만료${n ? ` · 이 컴퓨터를 쓰는 세션 ${n}개` : \"\"}`;");
    expect(v.some((x) => x.text.includes("이 컴퓨터를 쓰는 세션"))).toBe(true);
  });

  it("여러 줄에 걸친 JSX 텍스트도 풀에 든다 (PR #317 NN1)", () => {
    const v = visibleStrings("x.tsx", '<p className="small">\n  이 세션을 종료합니다 — 끝냅니다.\n</p>\n<b>\n  진행 중 <i>{n}</i> 개\n</b>');
    expect(v).toContainEqual({ file: "x.tsx", line: 2, text: "이 세션을 종료합니다 — 끝냅니다." });
    expect(v.some((x) => x.line === 5 && x.text.startsWith("진행 중"))).toBe(true);
    // 줄을 넘는 코드 조각(`a > 0 && (\n <p`)은 한글이 없으면 들지 않는다.
    expect(visibleStrings("x.tsx", "{a > 0 && (\n  <p>ok</p>)}").map((x) => x.text)).toEqual(["ok"]);
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
    expect(inPool("app/(app)/runtimes/page.tsx", "쓰는 중인 방")).toBe(true);
    expect(inPool("app/(app)/runtimes/page.tsx", "컴퓨터 연결")).toBe(true);
    expect(inPool("components/RuntimeCard.tsx", "도구 제한을 걸 수 없는 컴퓨터입니다")).toBe(true);
  });

  // §8.5 — 화면 제목 아래 한 줄 설명. 표(PAGE_COPY)에만 있고 화면이 안 쓰면 없는 것과 같다.
  const PAGE_FILES: Record<Screen, string> = {
    rooms: "app/(app)/rooms/RoomsView.tsx",
    inbox: "app/(app)/inbox/page.tsx",
    agents: "app/(app)/agents/page.tsx",
    computers: "app/(app)/runtimes/page.tsx",
    settings: "app/(app)/settings/page.tsx",
  };
  const SCREENS = Object.keys(PAGE_COPY) as Screen[];

  it("화면 설명이 §8.4 의 말이고 한 줄이다", () => {
    // v0.19 (T-R2-W1): 「방」(rooms)이 S5 다. 옛 「세션」(sessions) 행은 `/sessions` → `/rooms` 307 뒤 문구 전환(R1.5)이 옛 화면과 함께 지웠다.
    expect(SCREENS.sort()).toEqual(["agents", "computers", "inbox", "rooms", "settings"]);
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
    expect(inPool("components/TestChatPanel.tsx", "방이 아닙니다")).toBe(true);
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
    // 이름을 넘기는 호출부는 S7 방 화면이다(옛 세션 화면의 `runtimeNameOf` 호출은 R1.5b 에서 그 화면과 함께 지웠다).
    expect(readFileSync(join(ROOT, "app/(app)/rooms/[id]/page.tsx"), "utf8")).toMatch(/runtimeName=\{runtimeName\}/);
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

  // T-W13 의 S5 카드 옵션(…)·삭제 다이얼로그(SessionCardMenu·DeleteSessionDialog)와 옛 S7(`sessions/[id]/page.tsx`)은 R1.5b 에서 지웠다 —
  // `/sessions/:id` 가 `/rooms/:id` 로 307 이라 어디서도 렌더되지 않았다. 방 목록의 같은 자리는 아래 T-R2-W1 표(ROOM_MENU·DELETE_ROOM_DIALOG)가 잰다.
  it("옛 세션 화면과 그 화면만 쓰던 컴포넌트가 다시 생기지 않았다 (R1.5b)", () => {
    for (const f of ["app/(app)/sessions", "components/SessionActions.tsx", "components/ParticipantsDialog.tsx", "components/SessionCardMenu.tsx", "components/DeleteSessionDialog.tsx", "components/FixConditionDialog.tsx"]) {
      expect(existsSync(join(ROOT, f)), f).toBe(false);
    }
  });

  it("비활성 사유가 버튼 근처에 있다 — title 만으로는 안 된다 (§8.5)", () => {
    const runtimes = readFileSync(join(ROOT, PAGE_FILES.computers), "utf8");
    expect(runtimes).toMatch(/<DisabledHint id="add-computer-hint">/);
    expect(runtimes).toMatch(/aria-describedby=\{!canManage \? "add-computer-hint"/);
  });
});

// ── T-W15 — 종료 조건의 말(S-84 · W-19, SCREEN §4.4 6단계 · §4.5 "종료 조건 진행률") ─────────────────────────
describe("종료 조건 — 이름은 사람 말이고 한곳(lib/wording.ts)에서만 나온다 (T-W15)", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");

  it("계약 enum 넷 + v1.1 하나의 이름이 SCREEN §4.4 의 말이다 — 보고서 제출 · Director 승인 · <에이전트> 의 검토 승인 · 수동 종료", () => {
    expect(CONDITION_NAME).toEqual({ artifact_submitted: "아티팩트 제출", agent_approval: "에이전트 검토 승인", user_approval: "Director 승인", manual: "수동 종료", criteria_met: "성공 기준 충족" });
    expect(conditionName("agent_approval", "Lead")).toBe("Lead 의 검토 승인");
    expect(conditionName("agent_approval", null)).toBe("에이전트 검토 승인");
    for (const t of Object.values(CONDITION_NAME)) expect(inPool("lib/wording.ts", t)).toBe(true);
  });

  it("옛 이름(보고서 제출 · 에이전트 승인)과 계약 enum 이 화면 문자열에 없다 — CONDITION_LABEL 표는 사라졌다", () => {
    // R1.5(#303 NN1 · SCREEN §4.5): artifact_submitted 의 이름은 「아티팩트 제출」이다(PRD §3.2 — 아티팩트가 정본). T-W15 의 「보고서 제출」이 옛말이 됐다.
    expect(hits(/보고서 제출|에이전트 승인(?!을)/, (v) => v.file === "lib/wording.ts" && /검토 승인/.test(v.text))).toEqual([]);
    // 조건 이름을 손으로 다시 적은 자리가 없다 — 이름은 conditionName 하나에서만.
    for (const f of ["components/ConditionRow.tsx", "components/ConditionEditor.tsx", "components/SessionAside.tsx", "components/CreateWorkDialog.tsx", "components/WorkEditDialogs.tsx"]) {
      expect(src(f)).not.toMatch(/CONDITION_LABEL/);
      expect(src(f)).not.toContain('"아티팩트 제출"');
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
    expect(PROGRESS.summary(["아티팩트 제출", "Director 승인"], 0, "and")).toBe("남은 것: 아티팩트 제출 1개 · Director 승인 1개"); // 2개 이상도 같은 어순(W-20)
    expect(PROGRESS.summary(["아티팩트 제출", "Director 승인"], 0, "or")).toBe("남은 것: 아티팩트 제출 1개 · Director 승인 1개 — 하나만 충족하면 끝");
    expect(PROGRESS.summary(["Director 승인"], 0, "single")).toBe("남은 것: Director 승인 1개");
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
    // 미션 열기·편집(S21)과 조건 고치기가 **같은 편집기**를 그린다 — 두 자리가 다른 편집기를 가지면 한쪽에서만 리뷰어를 잊는다.
    // (S6 마법사는 T-R2-W4b 에서 지워졌다 — 그 6단계가 S21 로 왔다.)
    expect(existsSync(join(ROOT, "app/(app)/sessions/new"))).toBe(false);
    for (const f of ["components/CreateWorkDialog.tsx", "components/WorkEditDialogs.tsx"]) {
      expect(src(f)).toMatch(/<ConditionEditor\b/);
      expect(src(f)).not.toMatch(/submitter-select|reviewer-select/); // 편집기 안에만 있다
    }
  });

  it("「조건 고치기」 — 제목·안내·버튼이 표에 있고 다이얼로그가 그 표를 쓴다 · S7 이 Director 에게만 넘긴다", () => {
    expect(FIX_CONDITION.button).toBe("조건 고치기");
    for (const t of Object.values(FIX_CONDITION)) expect(inPool("lib/wording.ts", t)).toBe(true);
    const dlg = src("components/WorkEditDialogs.tsx");
    for (const k of ["title", "note", "save", "cancel", "busy"]) expect(dlg).toContain(`FIX_CONDITION.${k}`);
    expect(dlg).toMatch(/aria-describedby=\{!gate\.ok \? hintId : undefined\}/); // 저장 비활성 사유는 근처에(§8.5)
    expect(src("components/WorkPanel.tsx")).toMatch(/mayResolve && props\.onFixCondition && \(/); // Director(결정권자)에게만 버튼
  });

  it("내부 역할명 assignee 가 화면 문자열에 없다 — 담당 에이전트", () => {
    // 옛 마법사 행 "(assignee)" 와 요약 "제출자 assignee" 가 그 자리다. S6 요약의 `assignee` 상태 변수(JSX 식 안)는 코드지 문구가 아니다.
    expect(hits(/\(assignee\)|제출자 assignee/)).toEqual([]);
    expect(hits(/\bassignee\b/, (v) => /invitable\.find/.test(v.text))).toEqual([]);
  });
});

// ── T-W16 — v1.1 첫 라운드의 말(관찰 표 K-18 · 역할의 허용 명령 K-19 · 빈 턴 카드 FR-7.2) ─────────────────────────
describe("v1.1 — 관찰 표·허용 명령·빈 턴의 말은 한곳(lib/wording.ts)에서만 나온다 (T-W16)", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
  /** 주석을 뺀 소스 — "손으로 다시 적지 않았다" 는 코드에 대한 말이다(주석이 사건을 설명하는 것은 막지 않는다). */
  const code = (f: string) => src(f).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

  it("ColabCommand 16개 전부에 사람 말이 있고, 명령 이름(밑줄 표기)은 화면 문자열에 없다", () => {
    const openapi = readFileSync(join(ROOT, "..", "contracts", "openapi.yaml"), "utf8");
    const m = openapi.match(/ColabCommand:\n\s+type: string\n[^\n]*\n\s+enum: \[([^\]]+)\]/)!;
    const names = m[1].split(",").map((x) => x.trim());
    expect(names).toHaveLength(16);
    expect(Object.keys(COMMAND_LABEL).sort()).toEqual([...names].sort());
    for (const v of Object.values(COMMAND_LABEL)) expect(inPool("lib/wording.ts", v)).toBe(true);
    // `lane_delegate`·`hitl_ask` 같은 명령 이름은 코드다 — 화면 문자열 풀에 없다.
    expect(hits(new RegExp(`\\b(${names.join("|")})\\b`))).toEqual([]);
    // 화면은 이 표만 그린다 — 컴포넌트에 명령의 사람 말 리터럴이 없다.
    for (const f of ["components/RoleCommands.tsx", "lib/commands.ts", "app/(app)/agents/[id]/page.tsx", "app/(app)/agents/new/page.tsx"]) {
      expect(src(f)).not.toContain('"아티팩트 제출"');
      expect(src(f)).not.toContain('"보고서 제출"');
      expect(src(f)).not.toContain('"위임"');
    }
  });

  it("S10 역할 구역 — 머리말·전부·못 하는 것·읽기 전용 안내가 표에 있고 RoleCommands 가 그 표를 그린다 · 두 S10 화면이 그 컴포넌트를 쓴다", () => {
    expect(ROLE_COMMANDS.head).toBe("이 에이전트가 할 수 있는 일:");
    expect(ROLE_COMMANDS.all).toBe("전부");
    expect(ROLE_COMMANDS.cannot("위임")).toBe("위임은 못 합니다");
    expect(ROLE_COMMANDS.reason_worker).toContain("Lead 의 일");
    for (const t of [ROLE_COMMANDS.head, ROLE_COMMANDS.all, ROLE_COMMANDS.all_lead, ROLE_COMMANDS.all_custom, ROLE_COMMANDS.reason_worker, ROLE_COMMANDS.reason_reviewer, ROLE_COMMANDS.preview, ROLE_COMMANDS.readonly]) expect(inPool("lib/wording.ts", t)).toBe(true);
    const rc = src("components/RoleCommands.tsx");
    for (const k of ["head", "preview", "readonly"]) expect(rc).toContain(`ROLE_COMMANDS.${k}`);
    expect(rc).toMatch(/summarizeCommands\(/);
    expect(src("app/(app)/agents/[id]/page.tsx")).toMatch(/<RoleCommands role=\{role\} commands=\{agent\.allowed_commands\} preview=\{role !== agent\.role\} \/>/);
    expect(src("app/(app)/agents/new/page.tsx")).toMatch(/<RoleCommands role=\{role\} \/>/);
  });

  it("관찰 표 — 제목 '관찰' · 부제 '목표치 없이 분포만 봅니다' · 열 이름 · '아직 잴 수 없음' 이 표에 있고 ObservationsTable 이 그 표를 그린다", () => {
    expect(OBSERVATIONS.title).toBe("관찰");
    expect(OBSERVATIONS.subtitle).toBe("목표치 없이 분포만 봅니다");
    expect(OBSERVATIONS.note_summary).toBe("세는 법"); // 지표 표의 접기와 같은 말
    for (const t of [OBSERVATIONS.title, OBSERVATIONS.subtitle, OBSERVATIONS.col_name, OBSERVATIONS.col_value, OBSERVATIONS.col_n, OBSERVATIONS.median, OBSERVATIONS.note_summary, OBSERVATIONS.counting]) expect(inPool("lib/wording.ts", t)).toBe(true);
    const table = src("components/ObservationsTable.tsx");
    for (const k of ["title", "subtitle", "col_name", "col_value", "col_n", "note_summary"]) expect(table).toContain(`OBSERVATIONS.${k}`);
    expect(table).toMatch(/formatObservation\(row, \{ median: OBSERVATIONS\.median, p95: OBSERVATIONS\.p95 \}\)/);
    expect(code("components/ObservationsTable.tsx")).not.toContain("목표"); // 목표 열이 없다 — 지표 표와 합치지 않았다
    expect(code("components/ObservationsTable.tsx")).not.toMatch(/Verdict|metricVerdict/);
    // 지표 표 컴포넌트가 관찰 표를 **아래에** 그린다(같은 탭, 별도 표).
    const metrics = src("components/MetricsTable.tsx");
    expect(metrics.indexOf("<MetricsTableView report={report} />")).toBeLessThan(metrics.indexOf("<ObservationsTableView report={observations} onReload={() => void load()} />"));
    // V-1: 「다시 세기」 는 지표 표와 같은 load(두 op 함께) — 관찰 표 머리와 관찰 오류 자리, 둘 다 표의 말(reload)로.
    expect(OBSERVATIONS.reload).toBe("다시 세기");
    expect(src("components/ObservationsTable.tsx")).toContain("{OBSERVATIONS.reload}");
    expect(metrics).toMatch(/observations-reload[\s\S]*\{OBSERVATIONS\.reload\}/);
    expect(inPool("lib/wording.ts", OBSERVATIONS.routing_value_hint)).toBe(true);
    expect(src("components/ObservationsTable.tsx")).toContain("{OBSERVATIONS.routing_value_hint}");
  });

  it("라우팅 규칙 번호 → 사람 말 — 1~8 과 platform 전부, '규칙 N · …' 모양, 모르는 값은 원시 값 + '(새 규칙)' (V-1)", () => {
    expect(ROUTING_RULE_LABEL).toHaveLength(8);
    expect(routingKindLabel("1")).toBe("규칙 1 · 기록만");
    expect(routingKindLabel("6")).toBe("규칙 6 · 담당 에이전트 폴백");
    expect(routingKindLabel("platform")).toBe(ROUTING_PLATFORM_LABEL);
    expect(routingKindLabel("9")).toBe("9 (새 규칙)");
    expect(routingKindLabel("delegate")).toBe("delegate (새 규칙)");
    expect(OBSERVATIONS.unknown_kind_tail).toBe("(새 규칙)");
    for (const t of [...ROUTING_RULE_LABEL, ROUTING_PLATFORM_LABEL]) expect(inPool("lib/wording.ts", t)).toBe(true);
  });

  it("빈 턴 — 문장은 PRD FR-7.2 그대로, 활동 피드·작업 줄기 카드가 같은 표(EMPTY_TURN)를 쓴다 · 오류 카드가 아니다", () => {
    expect(EMPTY_TURN.note).toBe("아무것도 하지 않고 턴을 끝냈습니다");
    expect(EMPTY_TURN.kind).toBe("정보");
    expect(readFileSync(join(ROOT, "..", "PRD.md"), "utf8")).toContain(`note: "${EMPTY_TURN.note}"`);
    for (const t of Object.values(EMPTY_TURN)) expect(inPool("lib/wording.ts", t)).toBe(true);
    expect(src("lib/feed.ts")).toContain("EMPTY_TURN.note");
    expect(src("components/ActivityFeed.tsx")).toMatch(/title=\{EMPTY_TURN\.kind\}>\{emptyTurnNote\(e\)\}/);
    expect(src("components/LaneCard.tsx")).toMatch(/title=\{EMPTY_TURN\.kind\}/);
    // 문장을 손으로 다시 적은 자리가 없다.
    for (const f of ["components/ActivityFeed.tsx", "components/LaneCard.tsx", "lib/feed.ts", "app/(app)/rooms/[id]/page.tsx"]) expect(code(f)).not.toContain("아무것도 하지 않고");
    // 정보 카드 — 실패 색·error 클래스를 타지 않는다.
    expect(src("components/activity-feed.css")).toMatch(/\.feed__row\[data-info="true"\] \.feed__glyph \{ color: var\(--ink-2\); \}/);
    expect(src("components/lane-card.css")).toMatch(/\.lane__note--info \{ color: var\(--ink-2\); \}/);
  });
});

// ── v0.19 T-R2-W1 — S5 방 목록 · S25 방 찾기 · S18 방 만들기의 **새 문구**(SCREEN §4.3~§4.5). 옛 「세션」 문구 전환은 R1.5 몫이다. ─────────
describe("v0.19 방 — 새 화면의 말은 한곳(lib/wording.ts)에서만 나오고 화면이 그 표를 그린다 (T-R2-W1)", () => {
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
  const code = (f: string) => src(f).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  const ROOM_FILES = [
    "app/(app)/rooms/RoomsView.tsx", "app/(app)/rooms/[id]/page.tsx", "components/RoomCard.tsx", "components/RoomCardMenu.tsx", "components/RoomSearchBar.tsx", "components/RoomDialogs.tsx", "components/CreateRoomDialog.tsx", "lib/rooms.ts",
    // T-R2-W2 — S7 방 화면 · S22 미션 패널
    "components/RoomHead.tsx", "components/RoomBlockedBanner.tsx", "components/RoomParticipants.tsx", "components/WorkChipRow.tsx", "components/WorkPanel.tsx", "components/RoomPanel.tsx", "lib/room-view.ts",
  ];
  /** 표의 문장 전부(함수는 예시 인자로, 슬롯은 두 토막으로). */
  const texts = (o: object, fns = true): string[] =>
    Object.values(o).flatMap((v) => (typeof v === "string" ? [v] : typeof v === "function" ? (fns ? [v("X")] : []) : Array.isArray(v) ? v.filter((x) => typeof x === "string" && x.length >= 2) : typeof v === "object" && v ? texts(v, fns) : []));

  it("새 화면 파일이 전부 문구 풀 범위 안이다", () => {
    for (const f of ROOM_FILES) expect(FILES).toContain(f);
  });

  it("방 멈춤 라벨 4종은 「멈춤」이다 — 「일시정지」가 아니다(§8.4 v0.19 층 분담) · manual 은 역할 없이 「직접 멈춤」", () => {
    expect(ROOM_BLOCKED_LABEL).toEqual({ budget: "예산으로 멈춤", runtime_offline: "컴퓨터 연결 끊김으로 멈춤", loop: "루프 상한으로 멈춤", manual: "직접 멈춤" });
    for (const t of Object.values(ROOM_BLOCKED_LABEL)) expect(inPool("lib/wording.ts", t)).toBe(true);
    // 방 층 표에는 「일시정지」가 한 번도 없다.
    for (const t of texts({ ROOM_BLOCKED_LABEL, ROOM_LIST, ROOM_MENU, ARCHIVE_DIALOG, DELETE_ROOM_DIALOG, CREATE_ROOM })) expect(t).not.toContain("일시정지");
    expect(src("components/badge-map.ts")).toMatch(/room: \{[\s\S]*ROOM_BLOCKED_LABEL\.manual/);
  });

  it("새 표의 문장이 옛말(세션·작업 줄기·걸린 세션·산출물)을 쓰지 않는다 — 옛 문구 전환 전에도 새 문구는 새말로(§3.4 d-1)", () => {
    for (const t of texts({ ROOM_BLOCKED_LABEL, ROOM_LIST, ROOM_MENU, ARCHIVE_DIALOG, DELETE_ROOM_DIALOG, ROOM_DELETED_NOTICE, CREATE_ROOM })) {
      expect(t).not.toMatch(/세션|작업 줄기|산출물/);
    }
    // 새 화면 파일의 화면 문자열도 같다.
    expect(hits(/세션|작업 줄기|산출물/, (v) => !ROOM_FILES.includes(v.file))).toEqual([]);
  });

  it("S5 — 한 줄 설명·카드·빈 상태 문장이 SCREEN §4.3 그대로이고 표에서 나온다", () => {
    expect(PAGE_COPY.rooms).toEqual({ title: "방", desc: "같은 팀과 계속 이야기하고, 끝낼 일이 생기면 미션을 엽니다. 예산·컴퓨터·격리는 방마다 따로 겁니다." });
    expect(NAV_ITEMS.find((i) => i.key === "rooms")).toMatchObject({ href: "/rooms", label: "방" });
    expect(ROOM_LIST.works_none).toBe("열린 미션 없음");
    expect(ROOM_LIST.empty_title).toBe("첫 방을 만들어 보세요");
    expect(ROOM_LIST.empty_examples).toEqual(["결제팀 — 결제 관련 논의와 작업", "인프라 — 배포·모니터링"]);
    expect(ROOM_LIST.empty_no_computer).toBe("컴퓨터를 연결하면 에이전트가 일을 시작할 수 있습니다");
    expect(ARCHIVE_DIALOG.body).toBe("새 대화와 새 미션만 막습니다. 메시지·미션·아티팩트는 그대로 남고 검색에도 걸립니다. 언제든 되돌릴 수 있습니다.");
    // 삭제는 사라지는 것을 나열한다(FR-2.6) — 사람 확인 요청·활동 기록·결정 기록까지, 작업 폴더는 지우는 목록에 없다.
    for (const w of ["메시지", "미션", "서브 미션", "할 일", "사람 확인 요청", "활동 기록", "아티팩트", "결정 기록", "비용 기록", "워크스페이스 집계"]) expect(DELETE_ROOM_DIALOG.loses).toContain(w);
    expect(DELETE_ROOM_DIALOG.loses).not.toContain("작업 폴더");
    expect(DELETE_ROOM_DIALOG.irreversible).toMatch(/되돌릴 수 없습니다/);
    expect(ROOM_MENU.delete_tail).toBe("되돌릴 수 없음");
    // 함수 문장(제목 「〈이름〉」)은 풀에 `「…」 …` 로 든다 — 여기서는 고정 문장만 잰다.
    for (const t of texts({ ROOM_LIST, ROOM_MENU, ARCHIVE_DIALOG, DELETE_ROOM_DIALOG, CREATE_ROOM }, false)) expect(inPool("lib/wording.ts", t), t).toBe(true);
    expect(inPool("lib/wording.ts", "「…」 방을 삭제할까요?")).toBe(true);
  });

  it("수는 문장에 보간하지 않는다 — 수가 드는 문장은 두 토막(Slotted)이고 화면은 <Slot> 으로 그린다(COMPONENTS §8.5 v0.19)", () => {
    for (const s of [ROOM_LIST.works_active, ROOM_LIST.attention_hitl, ROOM_LIST.attention_blocked, ROOM_LIST.attention_failed, ROOM_LIST.more_public, ROOM_LIST.unread_label, ROOM_LIST.participants_label, ROOM_MENU.delete_works]) {
      expect(Array.isArray(s)).toBe(true);
      expect(s).toHaveLength(2);
    }
    expect(ROOM_MENU.delete_works.join("2")).toBe("미션 2개가 진행 중입니다 — 먼저 끝내거나 취소하세요");
    expect(ROOM_LIST.more_public.join("3")).toBe("워크스페이스에 공개된 방이 3개 더 있습니다");
    // 새 화면 파일에 수를 넣은 한국어 템플릿 리터럴이 없다.
    for (const f of ROOM_FILES) expect(code(f), f).not.toMatch(/`[^`]*[가-힣][^`]*\$\{[^`]*`|`[^`]*\$\{[^`]*\}[^`]*[가-힣][^`]*`/);
    expect(src("components/RoomCard.tsx")).toMatch(/<Slot text=\{ROOM_LIST\.works_active\} n=\{room\.active_work_count\} \/>/);
    expect(src("components/RoomSearchBar.tsx")).toMatch(/<Slot text=\{ROOM_LIST\.more_public\} n=\{morePublic\} \/>/);
    // 숫자만 있는 요소의 라벨(§7) — 안 읽음 · 참여자 묶음.
    expect(src("components/RoomCard.tsx")).toMatch(/aria-label=\{slotText\(ROOM_LIST\.unread_label, n\)\}/);
    expect(src("components/RoomCard.tsx")).toMatch(/aria-label=\{slotText\(ROOM_LIST\.participants_label, room\.participants\.length\)\}/);
  });

  it("S25 — 정렬은 고정 표시뿐(제어 없음) · 「내가 참여한 방만」 기본 켜짐 · 공개 방 N개 줄", () => {
    expect(ROOM_LIST.sort_fixed).toBe("정렬: 마지막 활동순");
    const bar = src("components/RoomSearchBar.tsx");
    expect(bar).not.toMatch(/<select/); // 정렬 선택 상자가 없다(§12.1-11)
    expect(bar).toMatch(/DEFAULT_FILTERS: RoomFilters = \{ q: "", unread: false, mine: true, archived: false \}/);
    for (const k of ["search_placeholder", "unread_only", "participating", "include_archived", "sort_fixed", "more_public_action"]) expect(bar).toContain(`ROOM_LIST.${k}`);
  });

  it("카드 메뉴 — 비활성은 숨기지 않고 DisabledHint + aria-describedby, 사유 문장은 표에서만", () => {
    const menu = src("components/RoomCardMenu.tsx");
    expect(menu).toMatch(/aria-describedby=\{!archiveGate\.ok \? archiveHint : undefined\}/);
    expect(menu).toMatch(/aria-describedby=\{!deleteGate\.ok \? deleteHint : undefined\}/);
    expect(menu).toMatch(/<DisabledHint id=\{id\}>/);
    for (const k of ["button", "archive", "unarchive", "delete", "delete_tail"]) expect(menu).toContain(`ROOM_MENU.${k}`);
    expect(code("components/RoomCardMenu.tsx")).not.toMatch(/소유자·관리자만|진행 중입니다/);
    const dlg = src("components/RoomDialogs.tsx");
    for (const k of ["title(room.name)", "body", "confirm", "cancel", "busy"]) expect(dlg).toContain(`ARCHIVE_DIALOG.${k}`);
    for (const k of ["title(room.name)", "loses", "workdirs", "irreversible", "confirm", "cancel", "busy", "workdirs_head", "workdirs_link"]) expect(dlg).toContain(`DELETE_ROOM_DIALOG.${k}`);
    expect(src("components/ConfirmDialog.tsx")).toMatch(/role="alertdialog"/);
  });

  it("S18 — ⓘ 한 줄은 워크스페이스 기본값에서 만들고(고정 문장 아님) · 방 설정은 만들기 전 비활성 · 이름이 비면 「만들기」 비활성 + 사유", () => {
    expect(roomDefaultsLine(null)).toBe("격리 없음 · 컴퓨터는 첫 실행 때 정해집니다");
    expect(roomDefaultsLine({ default_isolation: "worktree" })).toBe("워크트리로 나눔 · 컴퓨터는 첫 실행 때 정해집니다");
    // room_defaults 가 옛 default_isolation 보다 앞선다(서버 loadRoomDefaults 와 같은 순서).
    expect(roomDefaultsLine({ default_isolation: "worktree", room_defaults: { isolation_kind: "none" } })).toBe("격리 없음 · 컴퓨터는 첫 실행 때 정해집니다");
    const dlg = src("components/CreateRoomDialog.tsx");
    expect(dlg).toContain("{roomDefaultsLine(settings)}");
    expect(dlg).toMatch(/<DisabledHint id=\{settingsHint\}>\{CREATE_ROOM\.settings_later\}<\/DisabledHint>/);
    expect(dlg).toMatch(/<DisabledHint id=\{createHint\}>\{CREATE_ROOM\.name_required\}<\/DisabledHint>/);
    expect(dlg).toMatch(/aria-describedby=\{empty \? createHint : undefined\}/);
    expect(dlg).toContain("CREATE_ROOM.duplicate");
    expect(CREATE_ROOM.duplicate).toBe("같은 이름의 방이 이미 있습니다 — 작업 폴더 브랜치 이름이 헷갈릴 수 있습니다");
    expect(code("components/CreateRoomDialog.tsx")).not.toMatch(/격리 없음|첫 실행 때/); // 문장은 표에서만
  });
});

// ── v0.19 T-R2-W2 — S7 방 화면 · S22 미션 패널의 **새 문구**(SCREEN §4.6 · §4.8 · §5 · COMPONENTS §9). 옛 「세션」 문구 전환은 R1.5 몫. ─────────
describe("v0.19 방 화면 — 새 문구는 표에서만, 층 분담 · 수는 칸 · 자동 귀속은 「자동: 」 (T-R2-W2)", () => {
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const texts = (o: object): string[] =>
    Object.values(o).flatMap((v) => (typeof v === "string" ? [v] : typeof v === "function" ? [v("X")] : Array.isArray(v) ? v.filter((x) => typeof x === "string") : typeof v === "object" && v ? texts(v) : []));
  const TABLES = { ROOM_HEAD, BLOCK_DIALOG, SUMMARIZE_DIALOG, WORK_CHIPS, WORK_PAUSE_LABEL, ROOM_BANNER, ROOM_NOTICES, ROOM_LEFT, ROOM_CENTER, WORK_SELECTOR, WORK_PANEL, ROOM_PANEL, ROOM_TABS };

  it("새 표의 문장이 옛말(세션·작업 줄기·산출물)과 내부 역할명(assignee 제외 규칙은 기존 자물쇠)을 쓰지 않는다", () => {
    for (const t of texts(TABLES)) expect(t, t).not.toMatch(/세션|작업 줄기|산출물/);
  });

  it("방 층의 표(머리·멈춤 배너·확인)에 「일시정지」가 없다 — 방은 「멈춤」(§8.4 v0.19 층 분담). 서브 미션·미션 층은 「일시정지」", () => {
    for (const t of texts({ ROOM_HEAD, ROOM_BANNER, BLOCK_DIALOG, ROOM_NOTICES })) expect(t, t).not.toContain("일시정지");
    expect(ROOM_LEFT.paused_task).toBe("⏸ 일시정지 · 할 일 예산");
    expect(ROOM_LEFT.paused_work).toBe("⏸ 일시정지 · 미션 예산");
    expect(ROOM_LEFT.paused_room).toBe("⏸ 멈춤 · 방 예산");
    expect(ROOM_LEFT.paused_task_only).toBe("이 승인은 이 할 일에만 적용됩니다");
  });

  it("수는 문장에 보간하지 않는다 — 멈춘 수 · 세 층 · 대기 상한 · 확인 문장은 두 토막(Slotted)이고 화면은 <Slot> 으로 그린다", () => {
    for (const s2 of [ROOM_BANNER.stopped, ROOM_HEAD.layer_works, ROOM_HEAD.layer_lanes, ROOM_HEAD.layer_tasks, ROOM_HEAD.needs_me, BLOCK_DIALOG.turns, BLOCK_DIALOG.works, ROOM_LEFT.queued_room_lanes, ROOM_LEFT.queued_agent_global, WORK_CHIPS.past, WORK_CHIPS.paused, ROOM_PANEL.count_artifacts]) {
      expect(Array.isArray(s2)).toBe(true);
      expect(s2).toHaveLength(2);
    }
    expect(ROOM_BANNER.stopped.join("2")).toBe("미션 2개와 대화 전부가 멈췄습니다");
    expect(BLOCK_DIALOG.turns.join("3")).toBe("이 방의 진행 중인 턴 3개가 중단되고 새 트리거가 막힙니다.");
    expect(ROOM_LEFT.queued_room_lanes.join("5")).toBe("이 방의 동시 서브 미션 상한(5)에 닿았습니다");
    expect(ROOM_LEFT.queued_agent_global.join("3")).toBe("이 에이전트가 다른 방 일로 꽉 찼습니다(동시 3개)");
    expect(src("components/RoomBlockedBanner.tsx")).toMatch(/<Slot text=\{ROOM_BANNER\.stopped\} n=\{stopped\} \/>/);
    expect(src("components/RoomHead.tsx")).toMatch(/<Slot text=\{BLOCK_DIALOG\.turns\} n=\{props\.runningTurns\} \/>/);
    expect(src("app/(app)/rooms/[id]/page.tsx")).toMatch(/<Slot text=\{ROOM_LEFT\.queued_room_lanes\}/);
  });

  it("방 멈춤 배너는 끼어들고(role=alert) 미션 배너는 조용하다(role=status) — COMPONENTS §9.4", () => {
    expect(src("components/RoomBlockedBanner.tsx")).toMatch(/className="room-banner" role="alert"/);
    expect(src("components/WorkPanel.tsx")).toMatch(/className="pbanner" role="status"/);
  });

  it("(전체)·(미션 없음)의 미션 동작 비활성 사유는 SCREEN §4.6 문장 그대로, 버튼 아래 글자(DisabledHint + aria-describedby)", () => {
    expect(WORK_PANEL.pick_first).toBe("어느 미션인지 먼저 고르세요 — 위 칩에서 미션을 누르면 이 버튼이 켜집니다");
    expect(WORK_PANEL.no_end).toBe("미션 없이 오간 대화에는 끝이 없습니다");
    const panel = src("components/WorkPanel.tsx");
    expect(panel).toMatch(/<DisabledHint id="work-actions-why">\{disabledWhy\}<\/DisabledHint>/);
    expect(panel).toMatch(/aria-describedby=\{disabledWhy \? "work-actions-why" : undefined\}/);
  });

  it("미션 선택기 — 자동 귀속은 「자동: 」 접두(열림과 시각적으로 다르다, COMPONENTS §9.2) · 문구는 §9.2 그대로", () => {
    expect(WORK_SELECTOR.auto_prefix).toBe("자동: ");
    expect(WORK_SELECTOR.into("보고서")).toBe("이 메시지는 미션 「보고서」에 들어갑니다");
    expect(WORK_SELECTOR.into_none).toBe("미션 없음에 들어갑니다");
    expect(WORK_SELECTOR.auto_tail).toBe(" — 바꾸려면 선택기를 누르세요");
    expect(src("components/Composer.tsx")).toContain("{WORK_SELECTOR.auto_prefix}");
  });

  it("칩 줄 — 특수 칩은 글자 그대로 「전체」·「미션 없음」, 그룹 이름 「미션 거르개」, ⏳ 는 aria-label 「사람 대기」", () => {
    expect([WORK_CHIPS.all, WORK_CHIPS.none, WORK_CHIPS.group, WORK_CHIPS.waiting]).toEqual(["전체", "미션 없음", "미션 거르개", "사람 대기"]);
    const row = src("components/WorkChipRow.tsx");
    expect(row).toMatch(/role="group" aria-label=\{WORK_CHIPS\.group\}/);
    expect(row).toMatch(/aria-live="polite"/);
    expect(row).toMatch(/aria-pressed=\{on\}/);
  });

  it("좁은 화면 탭 넷(§4.8) · 방 누적 비용의 한 줄 · 빈 방 안내가 표에서 나와 화면에 쓰인다", () => {
    expect([ROOM_TABS.timeline, ROOM_TABS.board, ROOM_TABS.work, ROOM_TABS.room]).toEqual(["타임라인", "보드", "미션", "방"]);
    expect(ROOM_PANEL.not_sum).toBe("미션 비용의 합이 아닙니다 — 미션 밖 대화 비용이 함께 듭니다");
    expect(ROOM_CENTER.empty_title).toBe("이제 무엇을 하나요?");
    for (const t of [ROOM_PANEL.not_sum, ROOM_CENTER.empty_title, WORK_PANEL.no_assignee, ROOM_NOTICES.audit, ROOM_LEFT.elsewhere]) expect(inPool("lib/wording.ts", t), t).toBe(true);
    expect(src("components/RoomPanel.tsx")).toContain("{ROOM_PANEL.not_sum}");
    expect(src("components/RoomParticipants.tsx")).toContain("ROOM_LEFT.elsewhere");
  });
});


// ── v0.19.3 에이전트 메시지 세 층 — 대화 · 작업 내용 · 작업 과정(PRD FR-3.1.2 · SCREEN §4.6 · COMPONENTS §9.6·§9.7) ─────────
describe("v0.19.3 메시지 세 층 — 접힌 줄·보기 전환의 말은 표(MESSAGE_LAYERS)에서만 나온다", () => {
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
  const code = (f: string) => src(f).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "").replace(/\s\/\/.*$/gm, "");
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const FILES_L = ["components/MessageLayers.tsx", "lib/message-layers.ts", "components/MessageCard.tsx", "app/(app)/rooms/[id]/page.tsx"];

  it("문구가 사는 파일이 풀 범위 안이다", () => {
    for (const f of FILES_L) expect(FILES).toContain(f);
  });

  it("SCREEN §4.6 · COMPONENTS §9.6·9.7 의 말 그대로 — 작업 내용 · 작업 과정 · 자동으로 접음 · 대화만 · 작업 내용 펼침 · 새 창으로 보기 · 보기", () => {
    expect(MESSAGE_LAYERS).toMatchObject({
      detail: "작업 내용", process: "작업 과정", auto_folded: "자동으로 접음", view_label: "보기",
      view_conversation: "대화만", view_detail: "작업 내용 펼침", open_window: "새 창으로 보기", artifact: "아티팩트", artifact_open: "열기",
    });
    // 수가 드는 말은 두 토막 — 「실패 1」 · 「표 3개」 · 「850자」 · 「1.5만 자」.
    expect(MESSAGE_LAYERS.failures.join("1")).toBe("실패 1");
    expect(MESSAGE_LAYERS.tables.join("3")).toBe("표 3개");
    expect(MESSAGE_LAYERS.chars.join("850")).toBe("850자");
    expect(MESSAGE_LAYERS.chars_man.join("1.5")).toBe("1.5만 자");
    // 「활동 피드 없음」(SCREEN §7) 규약 — 대기 중 / 구조화 미지원.
    expect(MESSAGE_LAYERS.process_waiting).toBe("대기 중…");
    // 풀에 든다(2글자 이상 문장만 — 1글자 토막 「자」「분」은 스캐너가 줍지 않으므로 join 으로 잰다).
    for (const t of [MESSAGE_LAYERS.detail, MESSAGE_LAYERS.process, MESSAGE_LAYERS.auto_folded, MESSAGE_LAYERS.view_label, MESSAGE_LAYERS.view_conversation, MESSAGE_LAYERS.view_detail, MESSAGE_LAYERS.open_window]) {
      expect(inPool("lib/wording.ts", t), t).toBe(true);
    }
  });

  it("작업 과정 동작 이름은 사람 말이고 두 토막 — 명령·verb 이름(밑줄)은 화면에 없다", () => {
    for (const [k, v] of Object.entries(PROCESS_ACTION)) {
      expect(k).toMatch(/^(tool|plan|status)\/[a-z_]+$/);
      expect(v).toHaveLength(2);
      expect(v.join("2"), k).toMatch(/[가-힣]/);
      expect(v.join("2"), k).not.toMatch(/_|세션|작업 줄기|산출물/);
    }
    expect(PROCESS_ACTION["tool/edit_file"].join("2")).toBe("파일 2개 편집");
    expect(PROCESS_ACTION["status/submit_artifact"].join("1")).toBe("아티팩트 제출 1건");
  });

  it("화면은 표를 그린다 — 컴포넌트·방 화면 코드에 문장을 직접 쓰지 않는다(주석 밖)", () => {
    const comp = src("components/MessageLayers.tsx");
    for (const k of ["detail", "process", "auto_folded", "view_label", "view_group", "view_conversation", "view_detail", "open_window", "failures", "tables", "process_loading", "process_waiting", "process_unstructured", "process_no_actions", "artifact", "artifact_version", "artifact_open"]) {
      expect(comp, k).toContain(`L.${k}`);
    }
    for (const f of FILES_L) expect(code(f), f).not.toMatch(/작업 내용|작업 과정|자동으로 접음|대화만|새 창으로 보기|대기 중…|도구 단위 기록 없음/);
    // 수를 넣은 한국어 템플릿 리터럴이 없다(§8.5 v0.19 — 수는 <Slot>).
    // MessageCard 의 「답글 N개 보기」는 이 작업 전부터 있던 문장이라 새 두 파일만 잰다.
    for (const f of ["components/MessageLayers.tsx", "lib/message-layers.ts"]) expect(code(f), f).not.toMatch(/`[^`]*[가-힣][^`]*\$\{[^`]*`|`[^`]*\$\{[^`]*\}[^`]*[가-힣][^`]*`/);
    expect(comp).toMatch(/<Slot text=\{L\.failures\} n=\{fails\} \/>/);
  });

  it("접힌 줄은 button + aria-expanded + aria-controls, 전환은 두 칸 aria-pressed(§4.6 키보드·접근성)", () => {
    const comp = src("components/MessageLayers.tsx");
    expect(comp).toMatch(/<button type="button" className="fold" aria-expanded=\{open\} aria-controls=\{regionId\}/);
    expect(comp).toMatch(/id=\{regionId\}/);
    expect(comp).toMatch(/role="group" aria-label=\{L\.view_group\}/);
    expect(comp).toMatch(/aria-pressed=\{value === v\}/);
  });
});

// ── v0.19.5 방 이름·설명 바꾸기 — 세 자리(S7 머리 · S20 · S5 카드)가 같은 말(PRD FR-2.1.2 · SCREEN §4.3·§4.6·§4.11 · COMPONENTS §9.9) ─────────
describe("v0.19.5 방 이름 바꾸기 — 말은 표(ROOM_RENAME)에서만, 세 자리가 한 컴포넌트", () => {
  const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
  const code = (f: string) => src(f).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "").replace(/\s\/\/.*$/gm, "").replace(/\{\/\*[\s\S]*?\*\/\}/g, "");
  const inPool = (file: string, text: string) => POOL.some((v) => v.file === file && v.text.includes(text));
  const FILES_R = ["components/InlineTitleEdit.tsx", "components/RoomHead.tsx", "components/RoomCard.tsx", "components/RoomCardMenu.tsx", "components/RoomSettingsForm.tsx"];

  it("SCREEN §4.6·§4.11·§4.3 의 말 그대로", () => {
    expect(ROOM_RENAME).toMatchObject({
      edit: "방 이름 바꾸기", menu_item: "이름 바꾸기", input_label: "방 이름", save: "저장", saving: "저장 중…", cancel: "취소",
      help: "방 이름은 1~200자입니다", required: "방 이름을 적어 주세요", forbidden: "방장·부방장·워크스페이스 관리자만 바꿀 수 있습니다",
      group_title: "이름·설명", group_impact: "이름은 방 목록·받은 요청·참고 방 링크에 바로 반영됩니다. 이전 메시지에 적힌 옛 이름은 그대로 남습니다",
    });
    expect(ROOM_RENAME.changed_elsewhere.join("리서치")).toBe("다른 사람이 이름을 리서치(으)로 바꿨습니다");
    for (const t of [ROOM_RENAME.edit, ROOM_RENAME.help, ROOM_RENAME.required, ROOM_RENAME.forbidden, ROOM_RENAME.group_impact]) expect(inPool("lib/wording.ts", t), t).toBe(true);
    for (const f of FILES_R) expect(FILES).toContain(f);
  });

  it("화면은 표를 그린다 — 세 자리의 코드에 문장을 직접 쓰지 않는다 · 한 컴포넌트를 공유한다", () => {
    for (const f of FILES_R) expect(code(f), f).not.toMatch(/방 이름을 적어|1~200자|다른 사람이 이름을|이름 바꾸기"|저장 중…"/);
    expect(src("components/RoomHead.tsx")).toMatch(/<InlineTitleEdit as="h1"/);
    expect(src("components/RoomCard.tsx")).toMatch(/<InlineTitleEdit /);
    expect(src("components/RoomSettingsForm.tsx")).toMatch(/import \{ renameErrorText, roomNameProblem \} from "\.\/InlineTitleEdit"/);
    const comp = src("components/InlineTitleEdit.tsx");
    for (const k of ["edit", "input_label", "save", "saving", "cancel", "help", "required", "forbidden", "changed_elsewhere"]) expect(comp, k).toContain(`ROOM_RENAME.${k}`);
  });

  it("접근성 — ✎ 는 button aria-label 「방 이름 바꾸기」, 입력 칸 aria-label 「방 이름」 + aria-describedby", () => {
    const comp = src("components/InlineTitleEdit.tsx");
    expect(comp).toMatch(/<button ref=\{trigger\} type="button" className="title-edit__pencil" aria-label=\{ROOM_RENAME\.edit\}/);
    expect(comp).toMatch(/aria-label=\{ROOM_RENAME\.input_label\}/);
    expect(comp).toMatch(/aria-describedby=\{helpId\}/);
  });
});
