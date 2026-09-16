/** 목 문장 ↔ 서버 문장 대조 (i) T-W16/T-S19 — 빈 턴 문장은 SERVER 에서, 관찰 표 정의는 서버 observations.Defs 와 항목 단위로 같다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { CONTRACTS_ROOT, HANDLERS, goSource } from "./_shared";
import { MOCK_ONLY, OBSERVATION_DEFS, SERVER, W } from "../wording";
import { EMPTY_TURN } from "@/lib/wording";

describe("(i) T-W16/T-S19 — 빈 턴 문장은 SERVER 에서, 관찰 표 정의는 서버 observations.Defs 와 항목 단위로 같다", () => {
  const OPENAPI = readFileSync(join(CONTRACTS_ROOT, "openapi.yaml"), "utf8");
  function goObsDefs(src: string) {
    const m = src.match(/var Defs = \[\]Def\{([\s\S]*?)\n\}/);
    if (!m) throw new Error("observations.go 에 Defs 표가 없다");
    const out: { key: string; label: string; note: string }[] = [];
    for (const item of m[1].split(/\{Key:/).slice(1)) {
      const key = item.match(/^\s*"([^"]+)"/)?.[1];
      const label = item.match(/Label:\s*"([^"]*)"/)?.[1];
      const note = item.match(/Note:\s*"([^"]*)"/)?.[1];
      if (key && label !== undefined && note !== undefined) out.push({ key, label, note });
    }
    return out;
  }
  it("MOCK_ONLY 는 비어 있고 빈 턴 문장은 SERVER.empty_turn_note(internal/tasks/emptyturn.go)에서 온다", () => {
    expect(Object.keys(MOCK_ONLY)).toEqual([]);
    expect(EMPTY_TURN.note).toBe(W.empty_turn_note);
    expect(HANDLERS).toContain('class: "status", verb: "turn_end", object_ref: "empty_turn", outcome: "info", payload: { command: "turn_end", args: { note: W.empty_turn_note } }');
    expect(goSource("internal/httpapi/unimplemented.go")).not.toContain("GetWorkspaceObservations(");
  });
  it("OBSERVATION_DEFS 는 계약 enum 순서의 5행이고 key·label·note 가 서버 observations.Defs 와 같다", () => {
    expect(OPENAPI).toMatch(/enum: \[chain_scale, chain_depth, join_breadth, routing_concentration, empty_turn_rate\]/);
    const go = goObsDefs(goSource("internal/observations/observations.go"));
    expect(go).toHaveLength(5);
    expect(OBSERVATION_DEFS.map((d) => ({ key: d.key, label: d.label, note: d.note }))).toEqual(go);
    expect(HANDLERS).toMatch(/import \{[^}]*\bOBSERVATION_DEFS\b[^}]*\} from "\.\/wording"/);
  });
});
