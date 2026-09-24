/** 목 문장 ↔ 서버 문장 대조 (d) handlers.ts 는 표를 전부 쓰고 옛 문장을 남기지 않았다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { HANDLERS, MOCK_DIR } from "./_shared";
import { MOCK_ONLY, SERVER, W } from "../wording";

describe("(d) handlers.ts 는 표를 전부 쓰고 옛 문장을 남기지 않았다", () => {
  // handlers.ts 가 등록 한 줄만 두고 본문을 넘긴 모듈(rooms-dialogs·r2w4a·work-edit)도 목이다 — 방 참여자 문장(participant_joined)은
  // v0.3.0(R4)에서 옛 세션 참여자 op 이 지워진 뒤 그 모듈에서만 쓰인다.
  const MOCK_SRC = [HANDLERS, ...["rooms-dialogs.ts", "r2w4a.ts", "work-edit.ts"].map((f) => readFileSync(join(MOCK_DIR, f), "utf8"))].join("\n");
  it.each(Object.keys(SERVER))("W.%s 가 쓰인다", (key) => {
    expect(MOCK_SRC).toMatch(new RegExp(`\\bW\\.${key}\\b`));
  });
  it("PR #192 가 지목한 옛 서버 문장 흉내가 없다", () => {
    for (const old of [
      "런타임이 없습니다 — 먼저 컴퓨터를 연결하세요\"",
      "Director·deputy 만 lane 을 중단할 수 있습니다",
      "active 세션만 일시정지할 수 있습니다",
      "running·failed·paused lane 만 다시 지시할 수 있습니다",
      "paused 세션만 재개할 수 있습니다",
      "active·paused 세션만 취소할 수 있습니다",
      "런타임 오프라인은 재바인딩하거나",
      "paused(runtime_offline) 세션만 재바인딩",
      "후보가 아닌 런타임입니다",
      "유실 경고 확인이 필요합니다",
      "assignee 는 제거할 수 없습니다",
      "정지 상태입니다(respond_to: nobody)",
      "Director 만 할 수 있습니다",
      "페어링이 만료되었습니다",
      "owner·admin 만 할 수 있습니다",
      "비밀번호 불일치",
      "세션 시작 — goal",
      // T-W6 가 서버보다 먼저 지어 둔 시험 대화·보안 탭 문장(MOCK_ONLY 였던 것) — T-S12 #200 의 실제 문장으로 바뀌었다(T-W11)
      "이전 답이 아직 오는 중입니다",
      "닫힌 시험 대화입니다 — 새로 열어 주세요",
      "이 컴퓨터의 연결이 끊겨 있습니다",
      "온라인 컴퓨터가 없습니다",
      "활동 기록 마스킹은 소유자만",
      "취소됨 — 시험 대화를 닫았습니다",
      "이것은 시험 대화입니다",
    ]) expect(HANDLERS, old).not.toContain(old);
  });
  it("Problem.title 을 손으로 적지 않는다 — 상태에서 정한다(apperr.Title)", () => {
    // 둘째 인자는 code 다 — 옛 영어 title 이 그 자리에 남지 않았는지 잰다. `unauthorized` 는 서버 principal.go 의 401 code 그대로라 예외(T-W12).
    expect(HANDLERS).not.toMatch(/new Problem\(\d+, "(?:forbidden|not found|gone|validation failed|conflict)"/);
    expect(HANDLERS).not.toMatch(/new Problem\((?!401)\d+, "unauthorized"/);
    expect(HANDLERS).toContain("title: titleOf(status)");
  });
  it("404 는 명사표로 만든다 — detail 없는 not_found 가 없다", () => {
    expect(HANDLERS).not.toContain('new Problem(404, "not_found")');
  });
});
