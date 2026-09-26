/** 목 문장 ↔ 서버 문장 대조 (b) 세 표는 apperr.go 와 항목 단위로 같다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { goSource, goMap } from "./_shared";
import { NOT_FOUND_NOUN, notFound, STATUS_LABEL, TITLE } from "../wording";

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
  it("NotFound 명사표 — NotFoundNouns + messageNotFound 의 명사(T-R2-W4a)", () => {
    // 서버 getMessage 는 NotFoundNouns 를 거치지 않고 같은 모양의 문장을 직접 짓는다(handlers_message_get.go messageNotFound).
    const m = /func messageNotFound\(\)[\s\S]*?"(\S+)를 찾을 수 없습니다"/.exec(goSource("internal/httpapi/handlers_message_get.go"));
    expect(m?.[1]).toBe("메시지");
    expect(NOT_FOUND_NOUN).toEqual({ ...goMap(apperr, "NotFoundNouns"), message: m![1] });
    expect(notFound("message")).toBe("메시지를 찾을 수 없습니다");
  });
  it("NotFound 문장 모양 — `<명사>을/를 찾을 수 없습니다`", () => {
    expect(apperr).toContain('Josa(noun, "을", "를")+" 찾을 수 없습니다"');
    expect(notFound("session")).toBe("방을 찾을 수 없습니다");
    expect(notFound("invite")).toBe("초대를 찾을 수 없습니다");
    expect(notFound("lane")).toBe("서브 미션을 찾을 수 없습니다");
    expect(notFound("task")).toBe("할 일을 찾을 수 없습니다");
  });
});
