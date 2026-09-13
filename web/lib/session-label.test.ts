/**
 * W-10 — S7 「세션 설정 → 컴퓨터」는 id 앞 8자가 아니라 **이름**이다(§8.4 "컴퓨터"는 사람이 붙인 이름).
 */
import { describe, expect, it } from "vitest";
import { RUNTIME_AUTO, RUNTIME_GONE, runtimeNameOf } from "./session-label";

const rts = [{ id: "21fccb22-0000-4000-8000-000000000001", name: "office-pc" }, { id: "r2", name: "laptop" }];

describe("runtimeNameOf (W-10)", () => {
  it("listRuntimes 에서 찾은 이름을 돌려준다 — id 앞 8자는 어디에도 없다", () => {
    const name = runtimeNameOf({ runtime_id: rts[0].id, runtime: undefined }, rts);
    expect(name).toBe("office-pc");
    expect(name).not.toContain("21fccb22");
  });
  it("목록에 없으면(삭제됨) '연결 끊긴 컴퓨터'", () => {
    expect(runtimeNameOf({ runtime_id: "gone", runtime: undefined }, rts)).toBe(RUNTIME_GONE);
    expect(RUNTIME_GONE).toBe("연결 끊긴 컴퓨터");
  });
  it("runtime_id 가 없으면 자동 선택(첫 실행 시 고정)", () => {
    expect(runtimeNameOf({ runtime_id: null, runtime: undefined }, rts)).toBe(RUNTIME_AUTO);
  });
  it("목록을 아직 못 받았으면 세션에 실린 runtime.name 이라도, 그것도 없으면 null(자리 표시)", () => {
    const rt = { id: "r9", name: "embedded" } as unknown as NonNullable<Parameters<typeof runtimeNameOf>[0]["runtime"]>;
    expect(runtimeNameOf({ runtime_id: "r9", runtime: rt }, null)).toBe("embedded");
    expect(runtimeNameOf({ runtime_id: "r9", runtime: undefined }, null)).toBeNull();
    // 목록은 받았는데 거기 없고 세션엔 실려 있으면 그 이름(서버가 조인해 준 것이 더 최신일 수 있다).
    expect(runtimeNameOf({ runtime_id: "r9", runtime: rt }, rts)).toBe("embedded");
  });
});
