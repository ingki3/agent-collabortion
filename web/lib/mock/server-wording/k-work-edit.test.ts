/**
 * 목 문장 ↔ 서버 문장 대조 (k) T-R2-W4b — 미션 설정 편집·조건 고치기(`updateWork`)·Director 교체(`changeWorkDirector`)의 문장은
 * `WE_SERVER`(work-edit.ts)와 이미 있는 표(`W`·`RW`)에서만 오고, 각 항목은 `server/<at>` 소스에 **글자 단위로** 있다. 전체 설명은 `_shared.ts`.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { goSource, MOCK_DIR } from "./_shared";
import { WE_SERVER } from "../work-edit";
import { RD_SERVER } from "../rooms-dialogs-wording";
import { SERVER } from "../wording";

const MODULE = readFileSync(join(MOCK_DIR, "work-edit.ts"), "utf8");

describe("(k) WE_SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다 (T-R2-W4b)", () => {
  it.each(Object.entries(WE_SERVER))("%s", (_key, { text, at }) => {
    expect(goSource(at)).toContain(text);
  });

  it("표의 모든 키를 목 모듈이 쓴다 · 다른 표와 같은 문장을 두 벌로 들지 않는다", () => {
    for (const k of Object.keys(WE_SERVER)) expect(MODULE, k).toMatch(new RegExp(`WE\\.${k}\\b`));
    const others = new Set<string>([...Object.values(SERVER), ...Object.values(RD_SERVER)].map((v) => v.text));
    expect(Object.entries(WE_SERVER).filter(([, v]) => others.has(v.text)).map(([k]) => k)).toEqual([]);
  });

  it("목 모듈에는 한국어 문장 리터럴이 없다 — 문장은 표에서만(WE · RW · W)", () => {
    const code = MODULE.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    const table = code.slice(code.indexOf("export const WE_SERVER"), code.indexOf("} satisfies"));
    const rest = code.replace(table, "");
    const literals = [...rest.matchAll(/"([^"\n]*[가-힣][^"\n]*)"|`([^`\n]*[가-힣][^`\n]*)`/g)].map((m) => m[1] ?? m[2]);
    expect(literals).toEqual([]);
  });

  it("판정 순서 — updateWork 는 서버 순서(Director 403 → 한도 422 → 끝난 미션 409 → 제목 → 목표 → 자율성 → 담당 → deputy → 종료 조건)", () => {
    const go = goSource("internal/httpapi/handlers_works.go");
    const fn = go.slice(go.indexOf("func (s *Server) UpdateWork("), go.indexOf("func (s *Server) DeleteWork("));
    const order = ["requireWorkDirector", "validateWorkLimits", "work_closed", "\"title\", \"length\"", "\"goal\", \"required\"", "\"autonomy\", \"unsupported\"", "assignee_agent_id\", \"not_participant", "requireMember", "ValidateReviewers"];
    const at = order.map((k) => fn.indexOf(k));
    expect(at.every((i) => i > 0)).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    const body = MODULE.slice(MODULE.indexOf("on(\"PATCH\", \"/works/{id}\""), MODULE.indexOf("on(\"PUT\", \"/works/{id}/director\""));
    const mock = ["W.work_director_required", "RW.work_budget_min", "WE.work_closed_edit", "W.title_1_200", "RW.goal_required", "RW.supervised_unsupported", "RW.assignee_not_participant", "RW.default_director_not_member", "ctx.validateCondition"];
    const mat = mock.map((k) => body.indexOf(k));
    expect(mat.every((i) => i >= 0)).toBe(true);
    expect([...mat].sort((a, b) => a - b)).toEqual(mat);
  });

  it("changeWorkDirector — 권한은 현재 Director · ws owner·admin(방장은 아니다), 시스템 메시지는 이름 + 꼬리", () => {
    const go = goSource("internal/httpapi/handlers_works.go");
    const fn = go.slice(go.indexOf("func (s *Server) ChangeWorkDirector("), go.indexOf("func (s *Server) SetWorkSubscription("));
    expect(fn).toMatch(/u\.Id != wk\.DirectorUserId && a\.WorkspaceRole != "owner" && a\.WorkspaceRole != "admin"/);
    expect(fn).not.toContain("RoleOwner");
    expect(fn).toContain(`displayName(r.Context(), tx, to) + "${WE_SERVER.sys_director_tail.text}"`);
  });
});
