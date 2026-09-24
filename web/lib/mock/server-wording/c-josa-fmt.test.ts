/** 목 문장 ↔ 서버 문장 대조 (c) josa 는 apperr.Josa 와 같은 규칙이다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { goSource } from "./_shared";
import { josa, fmt, W } from "../wording";

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
    expect(fmt(W.runtime_has_active_sessions, 3)).toBe("이 컴퓨터를 쓰는 중인 방이 3개 있습니다 — 먼저 다른 컴퓨터로 옮기거나 미션을 종료해 주세요");
    expect(fmt(W.budget_too_low, 1.5)).toBe("이미 $1.50를 썼습니다 — 새 상한은 그보다 커야 합니다");
    expect(fmt(W.deputy_not_yet, "14:30")).toBe("Director 응답 대기 중 · 14:30부터 승인 가능");
  });
});
