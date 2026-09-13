/**
 * **목 문장 ↔ 서버 문장 대조**(T-W10, PR #192 후속).
 *
 * 서버(S-67)가 사용자 대면 문장 — `Problem.detail`·`title`·`errors[].message`·시스템 메시지 — 을 §8.4 의 말로
 * 바꿨다. 목이 그 문장을 **다른 말로** 흉내 내면 화면 테스트(p3-mock·p4-mock·u1)가 실서버를 대변하지 못한다.
 * 그래서 목의 문장은 `wording.ts` 한곳에 두고, 여기서 `server/` 소스의 **그 파일**에 같은 리터럴이 있는지 글자
 * 단위로 잰다. 서버가 문장을 바꾸면 이 테스트가 먼저 빨개진다 — 그때 `wording.ts` 를 따라 고친다.
 *
 * 재는 것:
 *   (a) `SERVER` 표의 항목마다 `server/<at>` 에 `text` 가 리터럴로 있다(형식 문자열은 `%d`·`%s` 그대로).
 *   (b) `TITLE`·`STATUS_LABEL`·`NOT_FOUND_NOUN` 은 `apperr.go` 의 세 표(`titles`·`statusLabels`·`NotFoundNouns`)와
 *       **항목 단위로 같다** — Go 소스를 파싱해 비교한다(하드코딩 대조 아님).
 *   (c) `josa` 는 `apperr.Josa` 와 같은 답을 낸다(받침 유무·비한글).
 *   (d) `handlers.ts` 는 표의 모든 키를 쓴다(죽은 행 없음), 옛 문장(PR #192 가 지목한 12곳의 말)은 남아 있지 않다.
 *   (e) 세션 시작 시스템 메시지 — 목·`e2e/u1.sh` 단언·서버 `sessions.go` 가 같은 머리말을 쓴다.
 */
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { josa, NOT_FOUND_NOUN, notFound, SERVER, STATUS_LABEL, TITLE, fmt, W } from "./wording";

const SERVER_ROOT = join(__dirname, "..", "..", "..", "server");
const HANDLERS = readFileSync(join(__dirname, "handlers.ts"), "utf8");
const U1 = readFileSync(join(__dirname, "..", "..", "e2e", "u1.sh"), "utf8");

function goSource(rel: string): string {
  const p = join(SERVER_ROOT, rel);
  if (!existsSync(p)) throw new Error(`server 소스가 없다: ${p} — 이 테스트는 모노레포 체크아웃(server/ 포함)에서 돈다`);
  return readFileSync(p, "utf8");
}

/** Go 의 `var name = map[K]V{ "k": "v", … }` 를 {k: v} 로. 주석 줄은 뺀다. */
function goMap(src: string, name: string): Record<string, string> {
  const m = src.match(new RegExp(`var ${name} = map\\[[^\\]]+\\][^{]+\\{([\\s\\S]*?)\\n\\}`));
  if (!m) throw new Error(`apperr.go 에 ${name} 표가 없다`);
  const out: Record<string, string> = {};
  for (const line of m[1].split("\n")) {
    const e = line.trim().match(/^([^:/]+):\s*"([^"]*)",?$/);
    if (e) out[e[1].trim().replace(/^"|"$/g, "").replace(/^http\.Status/, "")] = e[2];
  }
  return out;
}

describe("(a) SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다", () => {
  it.each(Object.entries(SERVER))("%s", (_key, { text, at }) => {
    expect(goSource(at)).toContain(text);
  });
  it("표가 비어 있지 않고 파일 경로는 server/ 기준 internal/… 이다", () => {
    expect(Object.keys(SERVER).length).toBeGreaterThanOrEqual(50);
    for (const { at } of Object.values(SERVER)) expect(at).toMatch(/^internal\/[\w/]+\.go$/);
  });
});

describe("(b) 세 표는 apperr.go 와 항목 단위로 같다", () => {
  const apperr = goSource("internal/apperr/apperr.go");
  it("Problem.title — titles", () => {
    const go = goMap(apperr, "titles");
    const byStatus: Record<string, string> = {
      BadRequest: "400", Unauthorized: "401", Forbidden: "403", NotFound: "404", Conflict: "409", Gone: "410",
      RequestEntityTooLarge: "413", UnprocessableEntity: "422", TooManyRequests: "429", InternalServerError: "500", NotImplemented: "501",
    };
    const goByCode = Object.fromEntries(Object.entries(go).map(([k, v]) => [Number(byStatus[k] ?? k), v]));
    expect(TITLE).toEqual(goByCode);
  });
  it("StatusLabel — statusLabels", () => {
    expect(STATUS_LABEL).toEqual(goMap(apperr, "statusLabels"));
  });
  it("NotFound 명사표 — NotFoundNouns", () => {
    expect(NOT_FOUND_NOUN).toEqual(goMap(apperr, "NotFoundNouns"));
  });
  it("NotFound 문장 모양 — `<명사>을/를 찾을 수 없습니다`", () => {
    expect(apperr).toContain('Josa(noun, "을", "를")+" 찾을 수 없습니다"');
    expect(notFound("session")).toBe("세션을 찾을 수 없습니다");
    expect(notFound("invite")).toBe("초대를 찾을 수 없습니다");
    expect(notFound("lane")).toBe("작업 줄기를 찾을 수 없습니다");
    expect(notFound("task")).toBe("할 일을 찾을 수 없습니다");
  });
});

describe("(c) josa 는 apperr.Josa 와 같은 규칙이다", () => {
  it("받침 있으면 with, 없으면 without, 한글이 아니면 with(without)", () => {
    expect(josa("Researcher", "이", "가")).toBe("Researcher이(가)");
    expect(josa("연구원", "이", "가")).toBe("연구원이");
    expect(josa("리뷰어", "이", "가")).toBe("리뷰어가");
    expect(josa("", "이", "가")).toBe("이(가)");
  });
  it("Go 쪽 상수(가~힣 · 종성 28)가 그대로다", () => {
    const go = goSource("internal/apperr/apperr.go");
    expect(go).toContain("last < 0xAC00 || last > 0xD7A3");
    expect(go).toContain("(last-0xAC00)%28 == 0");
  });
  it("fmt 는 %d·%s·%.2f 를 Go 처럼 채운다", () => {
    expect(fmt(W.runtime_has_active_sessions, 3)).toBe("이 컴퓨터를 쓰는 중인 세션이 3개 있습니다 — 먼저 다른 컴퓨터로 옮기거나 세션을 종료해 주세요");
    expect(fmt(W.budget_too_low, 1.5)).toBe("이미 $1.50를 썼습니다 — 새 상한은 그보다 커야 합니다");
    expect(fmt(W.deputy_not_yet, "14:30")).toBe("Director 응답 대기 중 · 14:30부터 승인 가능");
  });
});

describe("(d) handlers.ts 는 표를 전부 쓰고 옛 문장을 남기지 않았다", () => {
  it.each(Object.keys(SERVER))("W.%s 가 쓰인다", (key) => {
    expect(HANDLERS).toMatch(new RegExp(`\\bW\\.${key}\\b`));
  });
  it("PR #192 가 지목한 옛 서버 문장 흉내가 없다", () => {
    for (const old of [
      "런타임이 없습니다 — 먼저 컴퓨터를 연결하세요\"",
      "Director·deputy 만 lane 을 중단할 수 있습니다",
      "active 세션만 일시정지할 수 있습니다",
      "running·failed·paused lane 만 다시 지시할 수 있습니다",
      "paused 세션만 재개할 수 있습니다",
      "active·paused 세션만 취소할 수 있습니다",
      "런타임 오프라인은 재바인딩하거나",
      "paused(runtime_offline) 세션만 재바인딩",
      "후보가 아닌 런타임입니다",
      "유실 경고 확인이 필요합니다",
      "assignee 는 제거할 수 없습니다",
      "정지 상태입니다(respond_to: nobody)",
      "Director 만 할 수 있습니다",
      "페어링이 만료되었습니다",
      "owner·admin 만 할 수 있습니다",
      "비밀번호 불일치",
      "세션 시작 — goal",
    ]) expect(HANDLERS, old).not.toContain(old);
  });
  it("Problem.title 을 손으로 적지 않는다 — 상태에서 정한다(apperr.Title)", () => {
    expect(HANDLERS).not.toMatch(/new Problem\(\d+, "(?:unauthorized|forbidden|not found|gone|validation failed|conflict)"/);
    expect(HANDLERS).toContain("title: titleOf(status)");
  });
  it("404 는 명사표로 만든다 — detail 없는 not_found 가 없다", () => {
    expect(HANDLERS).not.toContain('new Problem(404, "not_found")');
  });
});

describe("(e) 세션 시작 메시지 — 목·u1.sh·서버가 같은 머리말", () => {
  it("서버 sessions.go 가 그 머리말로 SystemPost 한다", () => {
    expect(goSource("internal/sessions/sessions.go")).toContain(`SystemPost(ctx, tx, sessionID, "${W.session_started}"+in.Goal)`);
  });
  it("목은 머리말 뒤에 goal 만 붙인다(둘째 줄 없음)", () => {
    expect(HANDLERS).toContain("content: `${W.session_started}${sess.goal}`");
  });
  it("e2e/u1.sh 의 단언이 그 머리말을 찾는다(옛말 둘 다 아님)", () => {
    expect(U1).toContain(W.session_started.trimEnd());
    expect(U1).not.toContain("세션 시작 — goal");
    expect(U1).not.toContain("Session started. Goal:");
  });
});
