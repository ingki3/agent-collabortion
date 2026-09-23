/**
 * 목 문장 ↔ 서버 문장 대조 (j) T-R2-W3 — 방의 다이얼로그·설정 op(참여자 · 방 설정 · 참고 방 링크 · 맥락 읽기 기록 · 미션 열기 · 미션 제안)의
 * 문장은 `RD_SERVER`(rooms-dialogs-wording.ts)에서만 오고, 각 항목은 `server/<at>` 소스에 **글자 단위로** 있다. 전체 설명은 `_shared.ts`.
 *
 * 표를 `SERVER` 와 나눈 이유는 머지 충돌(S7 재작성이 같은 표 끝을 늘린다)이다 — 규칙은 같다. 두 표가 같은 키를 두 벌로 들지 않는지도 잰다.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { goSource, MOCK_DIR } from "./_shared";
import { RD_SERVER } from "../rooms-dialogs-wording";
import { SERVER } from "../wording";

const MODULE = readFileSync(join(MOCK_DIR, "rooms-dialogs.ts"), "utf8");

describe("(j) RD_SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다 (T-R2-W3)", () => {
  it.each(Object.entries(RD_SERVER))("%s", (_key, { text, at }) => {
    expect(goSource(at)).toContain(text);
  });

  it("표의 모든 키를 목 모듈이 쓴다(죽은 행 없음) · 경로는 internal/… · SERVER 와 같은 문장을 두 벌로 들지 않는다", () => {
    for (const k of Object.keys(RD_SERVER)) expect(MODULE, k).toMatch(new RegExp(`RW\\.${k}\\b`));
    for (const { at } of Object.values(RD_SERVER)) expect(at).toMatch(/^internal\/[\w/]+\.go$/);
    const serverTexts = new Set<string>(Object.values(SERVER).map((v) => v.text));
    // 조각(앞뒤 공백으로 이어 붙이는 것)은 우연히 겹칠 수 있다 — 문장(마침표·대시가 있는 것)만 잰다.
    const dup = Object.entries(RD_SERVER).filter(([, v]) => /[.—]/.test(v.text) && serverTexts.has(v.text)).map(([k]) => k);
    expect(dup).toEqual([]);
  });

  it("목 모듈에는 한국어 문장 리터럴이 없다 — 문장은 표에서만(RW · W)", () => {
    const code = MODULE.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    const literals = [...code.matchAll(/"([^"\n]*[가-힣][^"\n]*)"|`([^`\n]*[가-힣][^`\n]*)`/g)].map((m) => m[1] ?? m[2]);
    // 조사 인자(josa 의 "이"·"가"·"을"·"를")·「 님」 이음·알 수 없는 이름 폴백만 허용한다.
    expect(literals.filter((t) => !/^(이|가|을|를| 님|알 수 없는 사람)$/.test(t))).toEqual([]);
  });

  it("판정 순서 — createWork 는 서버 openWork 순서(검증 422 → 보관 → 멈춤 → 동시 상한 → Director → 담당 → 종료 조건 → 원 메시지)", () => {
    const go = goSource("internal/httpapi/handlers_works.go");
    const order = ["goal\", \"required", "room_archived", "room_blocked", "concurrentWorksConflict", "director_user_id\", \"not_member", "assignee_agent_id\", \"not_participant", "ValidateReviewers", "message_has_work"];
    const at = order.map((k) => go.indexOf(k));
    expect(at.every((i) => i > 0)).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    const mock = ["RW.goal_required", "\"room_archived\"", "\"room_blocked\"", "\"max_concurrent_works\"", "RW.director_not_member", "RW.assignee_not_participant", "ctx.validateCondition", "\"message_has_work\""];
    const body = MODULE.slice(MODULE.indexOf("const openWork = "));
    const mat = mock.map((k) => body.indexOf(k));
    expect(mat.every((i) => i >= 0)).toBe(true);
    expect([...mat].sort((a, b) => a - b)).toEqual(mat);
  });
});
