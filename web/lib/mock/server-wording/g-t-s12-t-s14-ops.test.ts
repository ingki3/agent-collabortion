/** 목 문장 ↔ 서버 문장 대조 (g) T-S12 #200 · T-S14 #209 가 만든 op 의 문장은 전부 SERVER 에서 온다 — MOCK_ONLY 는 비어 있다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { SERVER_ROOT, CONTRACTS_ROOT, HANDLERS, MOCK_WORDING_SRC, goSource } from "./_shared";
import { MOCK_ONLY, notFound, SERVER, fmt, W } from "../wording";

describe("(g) T-S12 #200 · T-S14 #209 가 만든 op 의 문장은 전부 SERVER 에서 온다 — MOCK_ONLY 는 비어 있다", () => {
  const serverImpl = readdirSync(join(SERVER_ROOT, "internal/httpapi")).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go") && f !== "unimplemented.go").map((f) => goSource(`internal/httpapi/${f}`)).join("\n");
  it("T-S12·T-S14 op 은 서버에 있고 그 문장은 MOCK_ONLY 에 없다", () => {
    // 근거는 **Server 메서드의 존재**다(스텁 유무가 아니다 — unimplemented.go 는 구현된 op 의 스텁도 품는다).
    for (const op of ["UpdateMemberRole", "RemoveMember", "GetNotificationSettings", "UpdateNotificationSettings",
      "CreateTestChat", "PostTestChatTurn", "CloseTestChat", "GetTestChat", "GetWorkspaceMetrics"]) expect(serverImpl).toContain(`func (s *Server) ${op}(`);
    for (const k of Object.keys(MOCK_ONLY)) expect(k).not.toMatch(/member|notification|test_chat|metrics|masking|role_enum|last_owner/);
  });
  it("deleteSession(T-S17 #220)은 v0.3.0(R4, D22)에서 지워졌다 — 목에도 그 op 이 없고 삭제 문장은 deleteRoom 의 workdir_unmerged 하나만 남는다", () => {
    for (const k of Object.keys(MOCK_ONLY)) expect(k).not.toMatch(/delete|session_active|workdir_unmerged/);
    expect(HANDLERS).not.toMatch(/on\("DELETE", "\/sessions/);
    expect(HANDLERS).not.toContain("DELETABLE_SESSION");
    // 서버가 deleteSession 과 함께 지운 두 문장(DeleteActiveDetail·DeleteForbiddenDetail)은 표에서도 빠졌다.
    expect(Object.keys(SERVER)).not.toContain("session_active");
    expect(Object.keys(SERVER)).not.toContain("delete_forbidden");
    // deleteRoom 409 는 같은 서버 문장(internal/sessions/delete.go DeleteUnmergedDetail)을 쓴다.
    const fn = HANDLERS.match(/on\("DELETE", "\/rooms\/\{id\}"[\s\S]*?\n\}\);/)![0];
    expect(fn).toContain('new Problem(409, "workdir_unmerged", W.workdir_unmerged, { workdirs: blocking })');
    expect(fn).toContain('"room.deleted", { room_id: room.id }');
    expect(fn).not.toContain("session.deleted");
  });
  it("멤버 역할 변경 — 서버 순서(권한 → enum 422 → 404 → 판정)와 PlanRoleChange 의 두 조건·code 가 같다", () => {
    const fn = HANDLERS.match(/on\("PATCH", "\/workspaces\/\{id\}\/members\/\{mid\}"[\s\S]*?\n\}\);/)![0];
    const order = ["requireAdmin(", 'code: "enum", message: W.role_enum', "memberOf(", 'new Problem(403, "owner_only", W.owner_only_role)', 'new Problem(409, "last_owner", W.last_owner_demote)'];
    const idx = order.map((x) => fn.indexOf(x));
    expect(idx.every((i) => i >= 0)).toBe(true);
    expect([...idx].sort((a, b) => a - b)).toEqual(idx);
    // 소유자 층 = 대상이 소유자 **또는** 새 역할이 소유자(서버 `touchesOwner`) — 강등만 보던 옛 목 분기는 없다.
    expect(fn).toContain('target.role === "owner" || b.role === "owner"');
    const members = goSource("internal/auth/members.go");
    expect(members).toContain('touchesOwner := c.TargetRole == "owner" || c.NewRole == "owner"');
    expect(members).toContain(`apperr.Forbidden(CodeOwnerOnly, "${W.owner_only_role}")`);
    expect(members).toContain(`apperr.Conflict(CodeLastOwner, "${W.last_owner_demote}")`);
    expect(goSource("internal/httpapi/handlers_members.go")).toContain(`apperr.Field("role", "enum", "${W.role_enum}")`);
    expect(HANDLERS).not.toContain("owner_demote_owner_only");
  });
  it("멤버 제거 — PlanRemoval 의 두 조건(403 소유자만 → 409 마지막 소유자), Director 는 거부 대신 승계(0.2.3), 승계하는 미션 집합이 서버와 같다", () => {
    const fn = HANDLERS.match(/on\("DELETE", "\/workspaces\/\{id\}\/members\/\{mid\}"[\s\S]*?\n\}\);/)![0];
    const order = ["requireAdmin(", "memberOf(", 'new Problem(403, "owner_only", W.owner_only_remove)', 'new Problem(409, "last_owner", W.last_owner_remove)'];
    const idx = order.map((x) => fn.indexOf(x));
    expect(idx.every((i) => i >= 0)).toBe(true);
    expect([...idx].sort((a, b) => a - b)).toEqual(idx);
    // openapi 0.2.3: 옛 409 member_is_director 는 목에도 서버에도 없다.
    expect(fn).not.toContain("member_is_director");
    const members = goSource("internal/auth/members.go");
    expect(members).toContain(`apperr.Forbidden(CodeOwnerOnly, "${W.owner_only_remove}")`);
    expect(members).toContain(`apperr.Conflict(CodeLastOwner, "${W.last_owner_remove}")`);
    expect(members).not.toContain("member_is_director");
    // 승계하는 미션 = 끝나지 않은 미션 전부(draft 포함) — 서버 rooms.LeaveWorkspace 의 집합과 목 집합이 같은 네 값.
    const seats = goSource("internal/rooms/seats.go");
    const goSet = seats.match(/w\.director_user_id = \$2 AND w\.status IN \(([^)]*)\)/)![1].split(",").map((x) => x.trim().replace(/'/g, "")).sort();
    const tsSet = HANDLERS.match(/const ACTIVE_SESSION = new Set<Session\["status"\]>\(\[([^\]]*)\]\)/)![1].split(",").map((x) => x.trim().replace(/"/g, "")).sort();
    expect(tsSet).toEqual(goSet);
    expect(goSet).toEqual(["active", "completing", "draft", "paused"]);
  });
  it("멤버 404 — 서버 memberNotFound 의 문장(명사표 밖) · 목은 notFound('user') 를 쓰지 않는다", () => {
    expect(goSource("internal/httpapi/handlers_members.go")).toContain(`apperr.New(http.StatusNotFound, "not_found", "${W.member_not_found}")`);
    expect(HANDLERS).toContain('new Problem(404, "not_found", W.member_not_found)');
    expect(HANDLERS.match(/function memberOf[\s\S]*?\n\}/)![0]).not.toContain('notFound("user")');
  });
  it("알림 설정 — enum 422 의 field·code·문장, 기본값, 부분 갱신이 서버와 같다", () => {
    const notif = goSource("internal/auth/notifications.go");
    expect(notif).toContain(`apperr.Field("default_subscription", "enum", "${W.subscription_enum}")`);
    expect(HANDLERS).toContain('validation([{ field: "default_subscription", code: "enum", message: W.subscription_enum }])');
    // 기본값 email true · push false · all (openapi default = 0021 열 기본값).
    expect(notif).toContain("gen.NotificationSettings{Email: true, Push: false, DefaultSubscription: gen.SubscriptionLevelAll}");
    expect(HANDLERS).toContain('const DEFAULT_NOTIFICATIONS: NotificationSettings = { email: true, push: false, default_subscription: "all" };');
    // 부분 갱신 — 빠진 키는 저장값 유지.
    expect(HANDLERS).toContain("email: b.email ?? cur.email, push: b.push ?? cur.push, default_subscription: b.default_subscription ?? cur.default_subscription");
  });
  it("시험 대화 — 409·410·403·404·422 의 code 와 문장이 서버와 같다", () => {
    expect(HANDLERS).toContain('new Problem(409, "turn_in_progress", W.test_chat_turn_in_progress)');
    expect(HANDLERS).toContain('new Problem(410, "test_chat_closed", W.test_chat_closed)');
    expect(HANDLERS).toContain('new Problem(409, "runtime_offline", W.test_chat_runtime_offline)');
    expect(HANDLERS).toContain('new Problem(409, "no_online_runtime", W.test_chat_no_online_runtime)');
    expect(HANDLERS).toContain('new Problem(403, "not_chat_owner", W.test_chat_not_owner)');
    expect(HANDLERS).toContain('new Problem(404, "not_found", W.test_chat_not_found)');
    expect(HANDLERS).toContain('validation([{ field: "content", message: W.test_chat_content_required }])');
    // 서버 `testChatAccess`: 워크스페이스 멤버가 아니면 404(403 not_member 가 아니다).
    const fn = HANDLERS.match(/function testChatOf[\s\S]*?\n\}/)![0];
    expect(fn).not.toContain("requireMember(");
    expect(fn).toContain("W.test_chat_not_found");
  });
  it("보안 탭(S-70) — admin 의 task_event_masking PATCH 는 403 owner_required 서버 문장 · 목 전용 분기 없음", () => {
    expect(HANDLERS).toContain('new Problem(403, "owner_required", W.masking_owner_required)');
    expect(HANDLERS).not.toContain("masking_owner_only");
    expect(MOCK_WORDING_SRC).not.toContain("masking_owner_only");
    expect(goSource("internal/httpapi/handlers_settings.go")).toContain('apperr.Forbidden("owner_required", "');
  });
  it("Problem 봉투 — `type` 접두(https://colab.dev/problems/<code>)와 401 code(unauthorized)가 서버 problem.go·principal.go 와 같다 (T-W12 curl 대조)", () => {
    expect(goSource("internal/httpapi/problem.go")).toContain('"type":   "https://colab.dev/problems/" + p.Code,');
    expect(HANDLERS).toContain("type: `https://colab.dev/problems/${code ?? \"\"}`");
    expect(HANDLERS).not.toContain('"about:blank"');
    expect(goSource("internal/httpapi/principal.go")).toContain(`apperr.Unauthorized("unauthorized", "${W.login_required}")`);
    expect(HANDLERS).toContain('new Problem(401, "unauthorized", W.login_required)');
    expect(HANDLERS).not.toContain('"unauthenticated"');
  });
  it("403 admin 의 code 도 서버(admin_required)와 같다", () => {
    expect(HANDLERS).not.toContain('"not_admin"');
    expect(goSource("internal/httpapi/principal.go")).toContain('apperr.Forbidden("admin_required", "');
  });
});
