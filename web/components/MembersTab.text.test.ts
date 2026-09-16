/**
 * W-13 — 자기 강등 확인 문장은 **출발 역할별**로 다르다(PR #212 리뷰 NN3: 소유자 강등에도 관리자 문장을 썼다).
 * SCREEN §2.3: 관리자도 멤버 초대·역할 변경·설정 변경은 한다 — 소유자→관리자에서 사라지는 것은 소유자만의 일이다.
 * 소유자로 되돌리는 것은 다른 소유자만(§2.3 "owner 역할은 owner 만", 계약 updateMemberRole v0.1.6).
 */
import { describe, expect, it } from "vitest";
import { isSelfDemotion, ROLE_LABEL, roleChangeRight, selfDemotionText } from "./MembersTab";
import type { Member } from "@/lib/api/types";

const member = (role: Member["role"], userId = "u1"): Member => ({
  id: `m-${userId}`, workspace_id: "w1", role, created_at: "2026-09-06T09:00:00Z",
  user: { id: userId, email: `${userId}@example.com`, display_name: userId, avatar_url: null, created_at: "2026-09-06T09:00:00Z" },
});

describe("selfDemotionText — 출발 역할별 문장", () => {
  it("관리자 → 멤버: 초대·역할·설정이 사라지고, 소유자·관리자 누구나 되돌린다", () => {
    expect(selfDemotionText("admin", "member")).toBe(
      "내 역할을 관리자에서 멤버로 내립니다. 멤버 초대·역할 변경·워크스페이스 설정 변경을 더는 할 수 없고, 되돌리려면 다른 소유자·관리자가 올려 줘야 합니다.",
    );
  });
  it("소유자 → 관리자: 사라지는 것은 소유자만의 일(소유자 역할 주고 거두기 · 보안 설정), 되돌리는 것은 다른 소유자만", () => {
    expect(selfDemotionText("owner", "admin")).toBe(
      "내 역할을 소유자에서 관리자로 내립니다. 소유자 역할을 주거나 거두는 것과 보안 설정 변경을 더는 할 수 없고, 되돌리려면 다른 소유자가 올려 줘야 합니다.",
    );
    // 관리자 문장을 그대로 쓰지 않는다 — 관리자는 초대·역할·설정을 여전히 한다(§2.3).
    expect(selfDemotionText("owner", "admin")).not.toContain("멤버 초대·역할 변경·워크스페이스 설정 변경");
  });
  it("소유자 → 멤버: 초대·역할·설정이 사라지고, 되돌리는 것은 다른 소유자만", () => {
    expect(selfDemotionText("owner", "member")).toBe(
      "내 역할을 소유자에서 멤버로 내립니다. 멤버 초대·역할 변경·워크스페이스 설정 변경을 더는 할 수 없고, 되돌리려면 다른 소유자가 올려 줘야 합니다.",
    );
  });
  it("세 문장 전부 역할 이름은 ROLE_LABEL 의 말이고 '(나)' 같은 내부 표기가 없다", () => {
    for (const [f, t] of [["owner", "admin"], ["owner", "member"], ["admin", "member"]] as const) {
      const text = selfDemotionText(f, t);
      expect(text.startsWith(`내 역할을 ${ROLE_LABEL[f]}에서 ${ROLE_LABEL[t]}로 내립니다.`)).toBe(true);
      expect(text).not.toMatch(/\b(owner|admin|member)\b/);
    }
  });
});

describe("isSelfDemotion · roleChangeRight — 문장이 보이는 경로", () => {
  it("자기 행에서 낮은 역할을 고른 것만 강등이다", () => {
    expect(isSelfDemotion(member("admin"), "member", "u1")).toBe(true);
    expect(isSelfDemotion(member("admin"), "owner", "u1")).toBe(false);
    expect(isSelfDemotion(member("admin"), "member", "u2")).toBe(false);
  });
  it("소유자 행은 소유자 자신도 잠근다(다른 소유자가 바꾼다) — 그래서 소유자 출발 문장은 지금 화면에 경로가 없고, 문장만 옳게 둔다", () => {
    expect(roleChangeRight("owner", member("owner"), "u1")).toEqual({ ok: false, reason: "내 소유자 역할은 다른 소유자가 바꿔야 합니다" });
    expect(roleChangeRight("owner", member("admin", "u2"), "u1")).toEqual({ ok: true });
  });
});
