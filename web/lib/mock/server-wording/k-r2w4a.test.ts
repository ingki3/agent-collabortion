/**
 * 목 문장 ↔ 서버 문장 대조 (k) T-R2-W4a — S8 방 층 승인 · S15 활동 로그 · 알림 구독 3층의 문장은 `R4_SERVER`(r2w4a-wording.ts)에서만 오고,
 * 각 항목은 `server/<at>` 소스에 **글자 단위로** 있다. 인박스 표(심각도·동작)는 서버 `internal/inbox/inbox.go` 를 읽어 15종 전부 대조한다 —
 * 목이 옛 7종 표에 머물면 새 타입이 `info` 로 떨어져 뱃지가 틀린다(W4a 에서 실제로 그랬다: work_proposed·room_paused 가 info 였다).
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { CONTRACTS_ROOT, goSource, MOCK_DIR } from "./_shared";
import { R4_MOCK_ONLY, R4_SERVER } from "../r2w4a-wording";
import { SERVER } from "../wording";
import { inboxActions, inboxSeverity } from "../handlers";
import type { InboxItem } from "@/lib/api/types";

const MODULE = readFileSync(join(MOCK_DIR, "r2w4a.ts"), "utf8");
const INBOX_GO = goSource("internal/inbox/inbox.go");

/** `TypeRoomPaused = "room_paused"` 표. */
function goTypeConsts(): Record<string, string> {
  return Object.fromEntries([...INBOX_GO.matchAll(/(Type\w+)\s*=\s*"(\w+)"/g)].map((m) => [m[1], m[2]]));
}
/** `func Name(...) … {` 본문을 `case A, B:` 절로 쪼갠다 → [[type 값들], 절 본문]. */
function goCases(fn: string): [string[], string][] {
  const start = INBOX_GO.indexOf(`func ${fn}(`);
  const body = INBOX_GO.slice(start, INBOX_GO.indexOf("\n}\n", start));
  const consts = goTypeConsts();
  // 맨 바깥 switch 의 case 만(탭 하나) — hitl_request 절 안의 `switch hitlType` 을 쪼개지 않는다.
  const parts = body.split(/\n\tcase /).slice(1);
  return parts.map((p) => {
    const colon = p.indexOf(":");
    const names = p.slice(0, colon).split(",").map((x) => x.trim()).filter((x) => x.startsWith("Type"));
    return [names.map((n) => consts[n]), p.slice(colon + 1)];
  });
}
const CONTRACT_TYPES: InboxItem["type"][] = (() => {
  const y = readFileSync(join(CONTRACTS_ROOT, "openapi.yaml"), "utf8");
  const m = /InboxItemType:\s*\n\s*type: string\s*\n\s*enum: \[([^\]]+)\]/.exec(y)!;
  return m[1].split(",").map((x) => x.trim()) as InboxItem["type"][];
})();

describe("(k) R4_SERVER 표의 문장은 server/ 소스의 그 파일에 글자 단위로 있다 (T-R2-W4a)", () => {
  it.each(Object.entries(R4_SERVER))("%s", (_key, { text, at }) => {
    expect(goSource(at)).toContain(text);
  });

  it("표의 모든 키를 목 모듈이 쓴다(죽은 행 없음) · SERVER 와 같은 문장을 두 벌로 들지 않는다 · MOCK_ONLY 는 서버에 아직 없다", () => {
    for (const k of Object.keys(R4_SERVER)) expect(MODULE, k).toMatch(new RegExp(`RW4\\.${k}\\b`));
    for (const k of Object.keys(R4_MOCK_ONLY)) expect(MODULE, k).toMatch(new RegExp(`R4_MOCK_ONLY\\.${k}\\b`));
    const serverTexts = new Set<string>(Object.values(SERVER).map((v) => v.text));
    expect(Object.entries(R4_SERVER).filter(([, v]) => serverTexts.has(v.text)).map(([k]) => k)).toEqual([]);
    // MOCK_ONLY 가 서버에 생기면 R4_SERVER 로 옮길 때다 — 이 줄이 빨개져 알린다(방 구독 문장이 실제로 T-S-r2 #309 에서 그렇게 옮겨 왔다).
    for (const f of ["internal/httpapi/handlers_rooms.go", "internal/httpapi/handlers_activity.go"]) for (const t of Object.values(R4_MOCK_ONLY)) expect(goSource(f)).not.toContain(t);
  });

  it("목 모듈의 오류 문장은 표에서만 — Problem 생성 줄에 한국어 리터럴이 없다", () => {
    const code = MODULE.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    const lines = code.split("\n").filter((l) => /new Problem\(|enumErr\(/.test(l));
    expect(lines.length).toBeGreaterThan(5);
    expect(lines.filter((l) => /"[^"\n]*[가-힣][^"\n]*"/.test(l))).toEqual([]);
  });
});

describe("(k) 인박스 표 — 목 inboxSeverity·inboxActions 가 서버 inbox.Severity·Actions 와 15종 전부 같다", () => {
  it("계약 enum 의 타입 전부를 서버가 분류한다", () => {
    const consts = Object.values(goTypeConsts());
    for (const t of CONTRACT_TYPES) expect(consts, t).toContain(t);
  });

  it("심각도", () => {
    const table = new Map<string, string>();
    for (const [types, body] of goCases("Severity")) {
      const sev = /return (\w+)/.exec(body)![1];
      const word = { ActionRequired: "action_required", Attention: "attention", Info: "info" }[sev]!;
      for (const t of types) table.set(t, word);
    }
    for (const t of CONTRACT_TYPES) expect(inboxSeverity(t), t).toBe(table.get(t) ?? "info");
  });

  it("동작 — 응답할 수 있을 때 · 없을 때", () => {
    for (const [types, body] of goCases("Actions")) {
      const lists = [...body.matchAll(/\[\]string\{([^}]*)\}/g)].map((m) => m[1].split(",").map((x) => x.trim().replace(/"/g, "")).filter(Boolean));
      for (const t of types as InboxItem["type"][]) {
        if (t === "hitl_request") {
          expect(inboxActions(t, "approval", true)).toEqual(lists.find((l) => l.includes("approve")));
          expect(inboxActions(t, "question", true)).toEqual(lists.find((l) => l.includes("answer")));
          expect(inboxActions(t, "question", false)).toEqual(lists[0]);
          continue;
        }
        const conditional = /if canRespond/.test(body);
        expect(inboxActions(t, undefined, true), `${t} can`).toEqual(lists[0]);
        expect(inboxActions(t, undefined, false), `${t} cannot`).toEqual(conditional ? lists[lists.length - 1] : lists[0]);
      }
    }
  });
});
