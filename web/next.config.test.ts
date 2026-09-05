/**
 * G3 W-2 회귀 가드 — Next 의 응답 압축이 켜져 있으면 rewrite 프록시를 지나는 SSE(`/workspaces/{id}/stream`)가 gzip 으로
 * 버퍼링돼 브라우저 EventSource 가 열리기만 하고 프레임을 못 받는다(S12 가 `대기 중` 에 머묾). `compress: false` 가 유지돼야 한다.
 */
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

describe("next.config.mjs", () => {
  it("compress: false — SSE 프록시 버퍼링 방지(W-2)", () => {
    const src = readFileSync(path.join(__dirname, "next.config.mjs"), "utf8");
    expect(src).toMatch(/^\s*compress:\s*false,/m);
    expect(src).toMatch(/reactStrictMode:\s*true/);
  });
});
