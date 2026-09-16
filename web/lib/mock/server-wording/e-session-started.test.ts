/** 목 문장 ↔ 서버 문장 대조 (e) 세션 시작 메시지 — 목·u1.sh·서버가 같은 머리말 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { HANDLERS, U1, goSource } from "./_shared";
import { W } from "../wording";

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
