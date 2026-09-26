/**
 * 역할별 허용 명령(v1.1 K-19) — 웹 표(`lib/commands.ts`)와 목 표(`lib/mock/store.ts allowedCommands`)를 **둘 다** `contracts/colab-cli.md` §2.5
 * 원문 표를 파싱한 결과와 대조한다. 둘이 같은 표를 공유하지 않는 이유: 한쪽이 틀리면 다른 쪽이 잡아야 한다(목이 구현과 같은 오답을 공유하면
 * 못 잡는다 — T-C5 교훈). 그리고 `ColabCommand` ↔ 사람 말 표(`COMMAND_LABEL`)가 계약 enum 16개(v0.8 방 명령 셋 포함) **전부**를 덮는지, 요약 문장이 6역할에서 어떻게
 * 나오는지 잰다.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { ALL_COMMANDS, commandsForRole, ROLE_COMMAND_TABLE, summarizeCommands } from "./commands";
import { COMMAND_LABEL, ROLE_COMMANDS } from "./wording";
import { allowedCommands } from "./mock/store";
import type { AgentRole, ColabCommand } from "@/lib/api/types";

const CONTRACTS = join(__dirname, "..", "..", "contracts");
const ROLES: AgentRole[] = ["lead", "researcher", "writer", "engineer", "reviewer", "custom"];

/** openapi `ColabCommand` enum — 계약이 정본. */
function enumFromOpenapi(): ColabCommand[] {
  const y = readFileSync(join(CONTRACTS, "openapi.yaml"), "utf8");
  const m = y.match(/ColabCommand:\n\s+type: string\n[^\n]*\n\s+enum: \[([^\]]+)\]/);
  if (!m) throw new Error("openapi.yaml 에 ColabCommand enum 이 없다");
  return m[1].split(",").map((x) => x.trim()) as ColabCommand[];
}

/** colab-cli.md §2.5 표 → 역할별 허용 집합. 열: lead | researcher·writer·engineer | reviewer | custom. */
function tableFromCli(): Record<AgentRole, Set<ColabCommand>> {
  const md = readFileSync(join(CONTRACTS, "colab-cli.md"), "utf8");
  const start = md.indexOf("### 2.5 역할별 허용 명령");
  const sec = md.slice(start, md.indexOf("\n## ", start));
  const rows = sec.split("\n").filter((l) => l.startsWith("| `"));
  const out: Record<AgentRole, Set<ColabCommand>> = { lead: new Set(), researcher: new Set(), writer: new Set(), engineer: new Set(), reviewer: new Set(), custom: new Set() };
  const cols: AgentRole[][] = [["lead"], ["researcher", "writer", "engineer"], ["reviewer"], ["custom"]];
  for (const row of rows) {
    const cells = row.split("|").slice(1, -1).map((c) => c.trim());
    const cmds = [...cells[0].matchAll(/`([a-z_]+)`/g)].map((m) => m[1] as ColabCommand);
    cells.slice(1).forEach((cell, i) => {
      if (cell === "✓") for (const r of cols[i]) for (const c of cmds) out[r].add(c);
      else expect(cell).toBe("—");
    });
  }
  return out;
}

describe("§2.5 표 — 웹 표와 목 표가 각각 계약 원문과 같다", () => {
  const spec = tableFromCli();
  const ENUM = enumFromOpenapi();

  it("계약 enum 은 16개이고 ALL_COMMANDS 가 같은 순서다", () => {
    expect(ENUM).toHaveLength(16);
    expect([...ALL_COMMANDS]).toEqual(ENUM);
    // §2.5 표가 enum 전부를 다룬다(빠진 명령 없음).
    expect([...spec.lead].sort()).toEqual([...ENUM].sort());
  });

  it.each(ROLES)("%s — 웹 commandsForRole · 목 allowedCommands · §2.5 원문이 같다", (role) => {
    const want = ENUM.filter((c) => spec[role].has(c));
    expect(commandsForRole(role)).toEqual(want);
    expect(allowedCommands(role)).toEqual(want);
    expect([...ROLE_COMMAND_TABLE[role]].sort()).toEqual([...want].sort());
  });

  it("lead·custom 은 전부, 실무자 셋은 11개(위임·검토 승인/반려·완료 승인 요청·미션 제안 없음), reviewer 는 12개(위임·아티팩트 제출·완료 승인 요청·미션 제안 없음) — 다른 방 목록·읽기는 모두", () => {
    expect(commandsForRole("lead")).toHaveLength(16);
    expect(commandsForRole("custom")).toHaveLength(16);
    for (const r of ["researcher", "writer", "engineer"] as const) {
      expect(commandsForRole(r)).toHaveLength(11);
      expect(commandsForRole(r)).not.toContain("work_propose");
      expect(commandsForRole(r)).toContain("room_list");
      expect(commandsForRole(r)).toContain("room_read");
      expect(commandsForRole(r)).not.toContain("lane_delegate");
      expect(commandsForRole(r)).not.toContain("review_approve");
      expect(commandsForRole(r)).not.toContain("review_reject");
      expect(commandsForRole(r)).not.toContain("hitl_approve_request");
      expect(commandsForRole(r)).toContain("artifact_submit");
    }
    expect(commandsForRole("reviewer")).toHaveLength(12);
    expect(commandsForRole("reviewer")).not.toContain("work_propose");
    expect(commandsForRole("reviewer")).toContain("room_read");
    expect(commandsForRole("reviewer")).not.toContain("artifact_submit");
    expect(commandsForRole("reviewer")).toContain("review_approve");
    expect(commandsForRole("reviewer")).toContain("review_reject");
  });
});

describe("ColabCommand ↔ 사람 말 — 16개 전부", () => {
  it("COMMAND_LABEL 의 키가 계약 enum 과 정확히 같고, 값은 한국어 사람 말(명령 이름·밑줄 없음)", () => {
    expect(Object.keys(COMMAND_LABEL).sort()).toEqual([...enumFromOpenapi()].sort());
    for (const [k, v] of Object.entries(COMMAND_LABEL)) {
      expect(v, k).toMatch(/[가-힣]/);
      expect(v, k).not.toMatch(/_|\blane\b|\btask\b|hitl/i);
    }
    // 같은 말이 둘에 붙지 않는다.
    expect(new Set(Object.values(COMMAND_LABEL)).size).toBe(16);
  });

  it("표 그대로 — 위임 · 아티팩트 제출 · 검토 승인/반려 · 완료 승인 요청 · 사람에게 질문/정보 요청", () => {
    expect(COMMAND_LABEL).toEqual({
      room_get: "방 읽기", room_messages: "메시지 읽기", artifact_get: "아티팩트 읽기", message_post: "메시지 게시", status_set: "상태 알리기",
      decision_record: "결정 기록", lane_delegate: "위임", artifact_submit: "아티팩트 제출", review_approve: "검토 승인", review_reject: "검토 반려",
      hitl_ask: "사람에게 질문", hitl_approve_request: "완료 승인 요청", hitl_request_info: "사람에게 정보 요청",
      room_list: "다른 방 목록", room_read: "다른 방 읽기", work_propose: "미션 제안",
    });
  });
});

describe("summarizeCommands — 역할 6종의 요약 문장", () => {
  it("lead · custom 은 '전부' + 이유 한 마디, 못 하는 것 없음", () => {
    const lead = summarizeCommands("lead", commandsForRole("lead"));
    expect(lead.can).toBe("전부");
    expect(lead.allNote).toBe(ROLE_COMMANDS.all_lead);
    expect(lead.cannot).toBeNull();
    expect(lead.denied).toEqual([]);
    const custom = summarizeCommands("custom", commandsForRole("custom"));
    expect(custom.can).toBe("전부");
    expect(custom.allNote).toBe(ROLE_COMMANDS.all_custom);
    expect(custom.cannot).toBeNull();
  });

  it.each(["researcher", "writer", "engineer"] as const)("%s — 할 수 있는 일 11개를 ' · ' 로, 못 하는 것 한 줄은 Lead 의 일", (role) => {
    const s = summarizeCommands(role, commandsForRole(role));
    expect(s.can).toBe("방 읽기 · 메시지 읽기 · 아티팩트 읽기 · 메시지 게시 · 상태 알리기 · 결정 기록 · 아티팩트 제출 · 사람에게 질문 · 사람에게 정보 요청 · 다른 방 목록 · 다른 방 읽기");
    expect(s.cannot).toBe("위임 · 검토 승인 · 검토 반려 · 완료 승인 요청 · 미션 제안은 못 합니다 — 위임·검토 승인·완료 승인 요청·미션 제안은 Lead 의 일");
    expect(s.allNote).toBeNull();
    expect(s.denied).toEqual(["lane_delegate", "review_approve", "review_reject", "hitl_approve_request", "work_propose"]);
  });

  it("reviewer — 아티팩트 제출이 빠지고 검토 승인/반려가 들어간다, 이유는 반려 사유", () => {
    const s = summarizeCommands("reviewer", commandsForRole("reviewer"));
    expect(s.can).toContain("검토 승인 · 검토 반려");
    expect(s.can).not.toContain("아티팩트 제출");
    expect(s.cannot).toBe("위임 · 아티팩트 제출 · 완료 승인 요청 · 미션 제안은 못 합니다 — 위임·완료 승인 요청·미션 제안은 Lead 의 일, 아티팩트 대신 검토 반려 사유를 남깁니다");
  });

  it("목록이 비면(옛 서버·daemon-protocol '비면 전부') 전부로 본다 · 서버 값이 표와 달라도 서버 값을 그린다", () => {
    expect(summarizeCommands("researcher", []).can).toBe("전부");
    expect(summarizeCommands("researcher", null).denied).toEqual([]);
    const s = summarizeCommands("researcher", ["message_post"]);
    expect(s.can).toBe("메시지 게시");
    expect(s.denied).toHaveLength(15);
  });
});
