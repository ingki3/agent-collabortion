/**
 * `/rooms/:id`(T-R2-W1) — 옛 세션이 있는 방은 옛 S7 을 그대로, 새로 만든 방(옛 세션 없음 → 404)은 getRoom 으로 임시 화면.
 */
import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

vi.mock("next/navigation", () => ({ useParams: () => ({ id: "r1" }) }));
vi.mock("../../sessions/[id]/page", () => ({ default: () => <div data-testid="session-detail-stub" /> }));
const get = vi.fn();
vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, api: { ...actual.api, get: (...a: unknown[]) => get(...a) } };
});

import RoomPage from "./page";
import { problemFixture } from "@/lib/mock/problem-fixture";
import { notFound } from "@/lib/mock/wording";

beforeEach(() => get.mockReset());
afterEach(cleanup);

describe("/rooms/:id", () => {
  it("옛 세션이 있으면 옛 S7 을 그린다", async () => {
    get.mockResolvedValueOnce({ id: "r1" });
    render(<RoomPage />);
    expect(await screen.findByTestId("session-detail-stub")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/sessions/{sessionId}", { path: { sessionId: "r1" } });
  });

  it("새 방(옛 세션 404) — 방 이름·설명과 임시 안내 · 방 목록으로", async () => {
    get.mockRejectedValueOnce(problemFixture("not_found", 404, { detail: notFound("session") }));
    get.mockResolvedValueOnce({ id: "r1", name: "결제팀", description: "결제 관련 논의와 작업" });
    render(<RoomPage />);
    expect(await screen.findByTestId("room-title")).toHaveTextContent("결제팀");
    expect(screen.getByTestId("room-pending")).toHaveTextContent("결제 관련 논의와 작업");
    expect(screen.getByRole("link", { name: /방 목록으로/ })).toHaveAttribute("href", "/rooms");
  });

  it("방도 없으면(404) 서버 문장 그대로", async () => {
    get.mockRejectedValueOnce(problemFixture("not_found", 404, { detail: notFound("session") }));
    get.mockRejectedValueOnce(problemFixture("not_found", 404, { detail: notFound("room") }));
    render(<RoomPage />);
    expect(await screen.findByTestId("room-error")).toHaveTextContent("방을 찾을 수 없습니다");
  });

  it("404 가 아닌 오류는 옛 S7 이 제 문장으로 말한다", async () => {
    get.mockRejectedValueOnce(problemFixture("forbidden", 403, { detail: "권한 없음" }));
    render(<RoomPage />);
    expect(await screen.findByTestId("session-detail-stub")).toBeInTheDocument();
  });
});
