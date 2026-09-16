/** 목 문장 ↔ 서버 문장 대조 (f) 관측 지표 10개 — 목 METRIC_DEFS 는 서버 metrics.Defs 와 항목 단위로 같다 — 전체 설명은 `_shared.ts` 머리 주석. */
import { describe, expect, it } from "vitest";
import { HANDLERS, goSource } from "./_shared";
import { METRIC_DEFS, W } from "../wording";

describe("(f) 관측 지표 10개 — 목 METRIC_DEFS 는 서버 metrics.Defs 와 항목 단위로 같다", () => {
  /** Go `var Defs = []Def{ {Key: "…", Unit: "…", Target: n, TargetOp: "…", Label: "…", Note: "…"}, … }` 를 파싱한다. */
  function goDefs(src: string) {
    const m = src.match(/var Defs = \[\]Def\{([\s\S]*?)\n\}/);
    if (!m) throw new Error("metrics.go 에 Defs 표가 없다");
    const out: { key: string; unit: string; target: number; target_op: string; label: string; note: string }[] = [];
    for (const item of m[1].split(/\},\s*\n/)) {
      const f = (name: string) => item.match(new RegExp(`\\b${name}:\\s*"([^"]*)"`))?.[1];
      const key = f("Key");
      if (!key) continue;
      out.push({
        key, unit: f("Unit")!, target: Number(item.match(/\bTarget:\s*([\d.]+)/)![1]), target_op: f("TargetOp")!,
        label: f("Label")!, note: f("Note")!,
      });
    }
    return out;
  }
  const go = goDefs(goSource("internal/metrics/metrics.go"));

  it("10개 · 같은 순서 · 같은 값(key·unit·target·target_op·label·note)", () => {
    expect(go).toHaveLength(10);
    expect(METRIC_DEFS.map((d) => ({ ...d }))).toEqual(go);
  });
  it("handlers.ts 는 지표 정의를 wording.ts 에서 가져온다(손으로 다시 적지 않는다)", () => {
    expect(HANDLERS).toMatch(/import \{[^}]*\bMETRIC_DEFS\b[^}]*\} from "\.\/wording"/);
    expect(HANDLERS).not.toMatch(/const METRIC_DEFS\b/);
    expect(HANDLERS).toContain("W.metrics_window_format");
  });
});
