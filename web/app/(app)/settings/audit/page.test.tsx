/**
 * S15 활동 로그(T-R2-W4a, SCREEN §4.18) — 목 서버에 물려 잰다: owner·admin 만 · 표 여섯 칸 · 필터가 서버 파라미터로 · 거부 행은 대상 방 숨김 ·
 * 마스킹 표시 · 실시간 없음 안내 · 멤버는 화면을 숨기지 않고 사유.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("@/lib/auth/AuthContext", async () => {
  const kit = await import("@/lib/mock/room-dialogs-testkit");
  return { useAuth: () => kit.auth };
});
vi.mock("next/link", () => ({ default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => <a href={href} {...rest}>{children}</a> }));

import AuditPage from "./page";
import { setup } from "@/lib/mock/room-dialogs-testkit";
import type { FetchBridge } from "@/lib/mock/fetch-bridge";
import { AUDIT } from "@/lib/audit";

let bridge: FetchBridge;
let as: (email: string) => Promise<void>;
let wsId = "";
beforeEach(async () => {
  ({ bridge, as, wsId } = await setup());
  await bridge.call("POST", "/__mock/activity/seed", {});
});
afterEach(() => {
  cleanup();
  bridge.restore();
});

describe("S15 활동 로그", () => {
  it("owner — 표 여섯 칸 · 사람 말 행위 · 거부 행은 대상 방 숨김 · 지워진 방 · 실시간 없음 안내", async () => {
    render(<AuditPage />);
    const rows = await screen.findAllByTestId("audit-row");
    expect(rows.length).toBe(8);
    expect(screen.getByTestId("audit-static")).toHaveTextContent(AUDIT.static_note);
    const headers = within(screen.getByTestId("audit-table")).getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual(Object.values({ a: AUDIT.cols.at, b: AUDIT.cols.actor, c: AUDIT.cols.action, d: AUDIT.cols.object, e: AUDIT.cols.room, f: AUDIT.cols.payload }));
    const denied = rows.find((r) => r.getAttribute("data-action") === "room.read.denied")!;
    expect(within(denied).getByTestId("audit-action")).toHaveTextContent("다른 방 읽기 거부");
    expect(within(denied).getByTestId("audit-object")).toHaveTextContent(AUDIT.hidden_target);
    const deleted = rows.find((r) => r.getAttribute("data-action") === "room.deleted")!;
    expect(within(deleted).getByTestId("audit-room")).toHaveTextContent(`지난 분기 회고 ${AUDIT.deleted_room}`);
  });

  it("필터 — 행위를 고르고 거르면 서버가 그 행만 준다 · 지우면 전부", async () => {
    render(<AuditPage />);
    await screen.findAllByTestId("audit-row");
    fireEvent.change(screen.getByTestId("audit-filter-action"), { target: { value: "room.audit_viewed" } });
    fireEvent.click(screen.getByTestId("audit-apply"));
    await waitFor(() => expect(screen.getAllByTestId("audit-row")).toHaveLength(1));
    expect(screen.getByTestId("audit-action")).toHaveTextContent("감사 열람");
    fireEvent.click(screen.getByTestId("audit-clear"));
    await waitFor(() => expect(screen.getAllByTestId("audit-row")).toHaveLength(8));
  });

  it("마스킹이 켜진 워크스페이스 — 긴 내용은 앞부분만 + 「마스킹됨」", async () => {
    await bridge.call("PATCH", `/workspaces/${wsId}/settings`, { task_event_masking: true });
    render(<AuditPage />);
    const rows = await screen.findAllByTestId("audit-row");
    const masked = rows.filter((r) => r.getAttribute("data-masked") === "true");
    expect(masked).toHaveLength(1);
    expect(within(masked[0]).getByTestId("audit-masked")).toHaveTextContent(AUDIT.masked);
  });

  it("멤버 — 화면은 숨기지 않고 사유(표는 없다)", async () => {
    await as("seoyeon@colab.dev");
    render(<AuditPage />);
    expect(await screen.findByTestId("audit-forbidden")).toHaveTextContent(AUDIT.forbidden);
    expect(screen.queryByTestId("audit-table")).toBeNull();
  });
});
