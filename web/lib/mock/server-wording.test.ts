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
 *   (f) 관측 지표 10개(`METRIC_DEFS`)는 서버 `internal/metrics/metrics.go` 의 `Defs` 와 **항목 단위로 같다**(key·unit·target·
 *       target_op·label·note — Go 소스를 파싱해 비교). S14 「대시보드」가 그대로 보이는 문장이라 서버가 정한다(T-W11).
 *   (g) T-S12(#200) 가 만든 op — 시험 대화·지표·보안 탭 403 — 의 문장은 `MOCK_ONLY` 에 남아 있지 않고 `SERVER` 에서 온다.
 */
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { josa, METRIC_DEFS, MOCK_ONLY, NOT_FOUND_NOUN, notFound, SERVER, STATUS_LABEL, TITLE, fmt, W } from "./wording";

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
    expect(Object.keys(SERVER).length).toBeGreaterThanOrEqual(65);
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
      // T-W6 가 서버보다 먼저 지어 둔 시험 대화·보안 탭 문장(MOCK_ONLY 였던 것) — T-S12 #200 의 실제 문장으로 바뀌었다(T-W11)
      "이전 답이 아직 오는 중입니다",
      "닫힌 시험 대화입니다 — 새로 열어 주세요",
      "이 컴퓨터의 연결이 끊겨 있습니다",
      "온라인 컴퓨터가 없습니다",
      "활동 기록 마스킹은 소유자만",
      "취소됨 — 시험 대화를 닫았습니다",
      "이것은 시험 대화입니다",
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

describe("(f) 관측 지표 10개 — 목 METRIC_DEFS 는 서버 metrics.Defs 와 항목 단위로 같다", () => {
  /** Go `var Defs = []Def{ {Key: "…", Unit: "…", Target: n, TargetOp: "…", Label: "…", Note: "…"}, … }` 를 파싱한다. */
  function goDefs(src: string) {
    const m = src.match(/var Defs = \[\]Def\{([\s\S]*?)\n\}/);
    if (!m) throw new Error("metrics.go 에 Defs 표가 없다");
    const out: { key: string; unit: string; target: number; target_op: string; label: string; note: string }[] = [];
    for (const item of m[1].split(/\},\s*\n/)) {
      const f = (name: string) => item.match(new RegExp(`\\b${name}:\\s*"([^"]*)"`))?.[1];
      const key = f("Key");
      if (!key) continue;
      out.push({
        key, unit: f("Unit")!, target: Number(item.match(/\bTarget:\s*([\d.]+)/)![1]), target_op: f("TargetOp")!,
        label: f("Label")!, note: f("Note")!,
      });
    }
    return out;
  }
  const go = goDefs(goSource("internal/metrics/metrics.go"));

  it("10개 · 같은 순서 · 같은 값(key·unit·target·target_op·label·note)", () => {
    expect(go).toHaveLength(10);
    expect(METRIC_DEFS.map((d) => ({ ...d }))).toEqual(go);
  });
  it("handlers.ts 는 지표 정의를 wording.ts 에서 가져온다(손으로 다시 적지 않는다)", () => {
    expect(HANDLERS).toMatch(/import \{[^}]*\bMETRIC_DEFS\b[^}]*\} from "\.\/wording"/);
    expect(HANDLERS).not.toMatch(/const METRIC_DEFS\b/);
    expect(HANDLERS).toContain("W.metrics_window_format");
  });
});

describe("(g) T-S12 #200 이 만든 op 의 문장은 MOCK_ONLY 가 아니라 SERVER 에서 온다", () => {
  it("MOCK_ONLY 에는 서버가 아직 안 만든 멤버·알림 op 의 문장만 남았다", () => {
    expect(Object.keys(MOCK_ONLY).sort()).toEqual(["last_owner", "member_is_director", "owner_demote_owner_only", "role_enum", "subscription_enum"]);
    // 그 op 들은 실제로 아직 unimplemented.go 에 있다 — 서버가 만들면 이 단언이 빨개지고, 그때 SERVER 로 옮긴다.
    // unimplemented.go 는 이미 구현된 op 의 스텁(Login 등)도 품고 있어 "스텁이 있다 = 미구현" 이 아니다.
    // 미구현의 근거는 **Server 메서드의 부재**다 — httpapi/*.go 어디에도 `func (s *Server) <Op>(` 가 없을 때.
    const serverImpl = readdirSync(join(SERVER_ROOT, "internal/httpapi")).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go") && f !== "unimplemented.go").map((f) => goSource(`internal/httpapi/${f}`)).join("\n");
    for (const op of ["UpdateMemberRole", "RemoveMember", "GetNotificationSettings", "UpdateNotificationSettings"]) expect(serverImpl).not.toContain(`func (s *Server) ${op}(`);
    for (const op of ["CreateTestChat", "PostTestChatTurn", "CloseTestChat", "GetTestChat", "GetWorkspaceMetrics"]) expect(serverImpl).toContain(`func (s *Server) ${op}(`);
  });
  it("시험 대화 — 409·410·403·404·422 의 code 와 문장이 서버와 같다", () => {
    expect(HANDLERS).toContain('new Problem(409, "turn_in_progress", W.test_chat_turn_in_progress)');
    expect(HANDLERS).toContain('new Problem(410, "test_chat_closed", W.test_chat_closed)');
    expect(HANDLERS).toContain('new Problem(409, "runtime_offline", W.test_chat_runtime_offline)');
    expect(HANDLERS).toContain('new Problem(409, "no_online_runtime", W.test_chat_no_online_runtime)');
    expect(HANDLERS).toContain('new Problem(403, "not_chat_owner", W.test_chat_not_owner)');
    expect(HANDLERS).toContain('new Problem(404, "not_found", W.test_chat_not_found)');
    expect(HANDLERS).toContain('validation([{ field: "content", message: W.test_chat_content_required }])');
    // 서버 `testChatAccess`: 워크스페이스 멤버가 아니면 404(403 not_member 가 아니다).
    const fn = HANDLERS.match(/function testChatOf[\s\S]*?\n\}/)![0];
    expect(fn).not.toContain("requireMember(");
    expect(fn).toContain("W.test_chat_not_found");
  });
  it("보안 탭(S-70) — admin 의 task_event_masking PATCH 는 403 owner_required 서버 문장 · 목 전용 분기 없음", () => {
    expect(HANDLERS).toContain('new Problem(403, "owner_required", W.masking_owner_required)');
    expect(HANDLERS).not.toContain("masking_owner_only");
    expect(readFileSync(join(__dirname, "wording.ts"), "utf8")).not.toContain("masking_owner_only");
    expect(goSource("internal/httpapi/handlers_settings.go")).toContain('apperr.Forbidden("owner_required", "');
  });
  it("403 admin 의 code 도 서버(admin_required)와 같다", () => {
    expect(HANDLERS).not.toContain('"not_admin"');
    expect(goSource("internal/httpapi/principal.go")).toContain('apperr.Forbidden("admin_required", "');
  });
});
