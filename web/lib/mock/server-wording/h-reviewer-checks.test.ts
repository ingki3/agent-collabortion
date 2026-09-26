/** 목 문장 ↔ 서버 문장 대조 (h) T-W15/T-S18 — 리뷰어 검사(계약 #232 v0.1.4)의 문장은 SERVER 에서 온다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { HANDLERS } from "./_shared";
import { MOCK_ONLY, SERVER, W } from "../wording";

describe("(h) T-W15/T-S18 — 리뷰어 검사(계약 #232 v0.1.4)의 문장은 SERVER 에서 온다", () => {
  it("리뷰어 검사 문장은 MOCK_ONLY 에 없고 handlers.ts 는 W.<key> 를 쓴다", () => {
    for (const k of Object.keys(MOCK_ONLY)) expect(k).not.toMatch(/reviewer|submitter|immutable/);
    for (const k of ["reviewer_required", "reviewer_not_participant", "submitter_not_participant"]) expect(HANDLERS).toMatch(new RegExp(`\\bW\\.${k}\\b`));
    expect(HANDLERS).not.toMatch(/\bMOCK_ONLY\.(reviewer|submitter|condition)/);
  });
  it("목 시드(옛 createSession) · 옛 updateSession 목 길(PATCH /__mock/rooms/{id}/legacy — updateWork 가 넘긴다)의 422 순서·code·field 경로 — 서버와 같은 모양(completion_condition/conditions/<i>/agent_id)", () => {
    const fn = HANDLERS.match(/function validateCondition[\s\S]*?\n\}/)![0];
    expect(fn).toContain("`completion_condition/conditions/${i}/agent_id`");
    expect(fn).toContain('code: "reviewer_required", message: W.reviewer_required');
    expect(fn).toContain('code: "reviewer_not_participant", message: a.type === "agent_approval" ? W.reviewer_not_participant : W.submitter_not_participant');
    const patch = HANDLERS.match(/on\("PATCH", "\/__mock\/rooms\/\{id\}\/legacy"[\s\S]*?\n\}\);/)![0];
    // 옛 immutable 422 셋(isolation·runtime_id·끝난 세션의 조건)은 R4 에서 op 과 함께 지워졌다.
    const order = ["requireDirector(sess, user.id, W.work_director_required)", "validateCondition(b.completion_condition", '"work.completion_progress", { work_id: sess.id, room_id: sess.id, completion_progress: sess.completion_progress }'];
    const idx = order.map((x) => patch.indexOf(x));
    expect(idx.every((i) => i >= 0)).toBe(true);
    expect([...idx].sort((a, b) => a - b)).toEqual(idx);
    // 계약: draft·active·paused 에서 completion_condition 수정 가능.
    expect(HANDLERS).toContain('const CONDITION_EDITABLE = new Set<Session["status"]>(["draft", "active", "paused"]);');
  });
});
