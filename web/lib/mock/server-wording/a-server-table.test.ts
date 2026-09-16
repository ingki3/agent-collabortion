/** 목 문장 ↔ 서버 문장 대조 (a) SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { goSource } from "./_shared";
import { SERVER } from "../wording";

describe("(a) SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다", () => {
  it.each(Object.entries(SERVER))("%s", (_key, { text, at }) => {
    expect(goSource(at)).toContain(text);
  });
  it("표가 비어 있지 않고 파일 경로는 server/ 기준 internal/… 이다", () => {
    expect(Object.keys(SERVER).length).toBeGreaterThanOrEqual(65);
    for (const { at } of Object.values(SERVER)) expect(at).toMatch(/^internal\/[\w/]+\.go$/);
  });
});
