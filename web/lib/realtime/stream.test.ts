/**
 * SSE 이벤트 이름 자물쇠(T-W13). `openStream` 은 `STREAM_EVENT_TYPES` 의 이름마다 `addEventListener` 를 건다 — 계약(openapi
 * `StreamEvent.type` enum)에 새 이벤트가 생겼는데 이 목록에 빠지면 **브라우저가 프레임을 버려서** 화면 어디에도 닿지 않는다
 * (`session.deleted` 가 그랬다 — 핸들러를 다 써 두고도 목록에 없으면 카드가 안 빠진다). 생성 타입(schema.d.ts)의 enum 과 항목 단위로 같아야 한다.
 */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { STREAM_EVENT_TYPES } from "./stream";

describe("STREAM_EVENT_TYPES ↔ openapi StreamEvent.type enum", () => {
  it("계약의 이벤트 이름 전부를 듣는다(빠짐·남음 없음)", () => {
    const schema = readFileSync(join(__dirname, "..", "api", "schema.d.ts"), "utf8");
    // `type: "resync" | "participant.updated" | …;` 한 줄 — StreamEvent 의 enum.
    const m = schema.match(/\n\s*type: ("resync"(?: \| "[a-z_.]+")+);/);
    expect(m).not.toBeNull();
    const contract = [...m![1].matchAll(/"([a-z_.]+)"/g)].map((x) => x[1]).sort();
    expect([...STREAM_EVENT_TYPES].sort()).toEqual(contract);
    expect(contract).toContain("room.deleted");
    // v0.3.0(R4, D22) — 옛 session.* 셋은 계약에서 지워졌고 목록에도 없다.
    for (const gone of ["session.updated", "session.deleted", "session.completion_progress"]) {
      expect(contract).not.toContain(gone);
      expect(STREAM_EVENT_TYPES as readonly string[]).not.toContain(gone);
    }
  });
});
