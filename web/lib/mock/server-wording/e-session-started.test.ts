/** 목 문장 ↔ 서버 문장 대조 (e) 세션 시작 메시지 — 목·u1.sh·서버가 같은 머리말 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { HANDLERS, U1, goSource } from "./_shared";
import { W } from "../wording";

describe("(e) 세션 시작 메시지 — 목·u1.sh·서버가 같은 머리말", () => {
  it("서버 sessions.go 가 그 머리말로 SystemPost 한다(그 세션의 미션에 귀속 — SystemPostWork, T-R1b2)", () => {
    expect(goSource("internal/sessions/sessions.go")).toContain(`SystemPostWork(ctx, tx, sessionID, &workID, "${W.session_started}"+in.Goal)`);
  });
  it("목은 머리말 뒤에 goal 만 붙인다(둘째 줄 없음)", () => {
    expect(HANDLERS).toContain("content: `${W.session_started}${sess.goal}`");
  });
  // v0.19(T-R2-W4b): u1.sh 는 마법사 대신 S18 → 빈 방 → S19 초대 → 첫 멘션으로 간다 — 미션을 열지 않으므로
  // 이 머리말을 기다리지 않는다. 자물쇠는 「u1 이 옛말로 되돌아가지 않는다」만 남긴다.
  it("e2e/u1.sh 는 옛 세션 시작 문장을 단언하지 않는다(마법사 흐름 은퇴 뒤)", () => {
    expect(U1).not.toContain("세션 시작 — goal");
    expect(U1).not.toContain("Session started. Goal:");
  });
});
