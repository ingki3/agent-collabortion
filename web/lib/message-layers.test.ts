/**
 * 에이전트 메시지 세 층(PRD FR-3.1.2 · SCREEN §4.6) — 순수 함수 유닛. 폴백 절단 규칙을 촘촘히 못박는다.
 */
import { describe, expect, it } from "vitest";
import {
  AUTO_FOLD_CHARS, AUTO_FOLD_LINES, AUTO_HEAD_CHARS, charCount, countTables, cutHead, exceedsAutoFold, firstLinePreview, formatChars,
  formatDuration, isLayered, messageLayers, splitAuto, summarizeProcess, timelineViewKey,
} from "./message-layers";
import { slotText } from "@/components/Slot";
import type { TaskEvent } from "@/lib/api/types";

const para = (n: number, word = "가") => word.repeat(n);
const TABLE = ["| 사업자 | 수수료 |", "|---|---|", "| A | 2.1% |", "| B | 2.4% |"].join("\n");
const CODE = ["```ts", "const a = 1;", "", "const b = 2;", "```"].join("\n");
const fmt = (n: number) => { const f = formatChars(n); return slotText(f.text, f.n); };

describe("isLayered — 접는 메시지는 에이전트의 text 만", () => {
  it("에이전트 text 는 세 층, 답글(answer)은 아니다", () => {
    expect(isLayered({ author_type: "agent", kind: "text" })).toBe(true);
    expect(isLayered({ author_type: "agent", kind: "text" }, { asAnswer: true })).toBe(false);
  });
  it("사람·시스템 메시지, 요약·HITL·질문 카드는 접지 않는다", () => {
    expect(isLayered({ author_type: "user", kind: "text" })).toBe(false);
    expect(isLayered({ author_type: "system", kind: "system" })).toBe(false);
    for (const kind of ["summary", "hitl", "blocked_q", "system"] as const) expect(isLayered({ author_type: "agent", kind }), kind).toBe(false);
  });
});

describe("폴백 임계 — 1,200자 또는 25줄 초과", () => {
  it("정확히 임계면 접지 않고, 하나 넘으면 접는다(글자)", () => {
    expect(exceedsAutoFold(para(AUTO_FOLD_CHARS))).toBe(false);
    expect(exceedsAutoFold(para(AUTO_FOLD_CHARS + 1))).toBe(true);
  });
  it("줄 수만으로도 접는다 — 짧은 줄 26개", () => {
    const lines = Array.from({ length: AUTO_FOLD_LINES + 1 }, (_, i) => `- 항목 ${i}`).join("\n");
    expect(charCount(lines)).toBeLessThan(AUTO_FOLD_CHARS);
    expect(exceedsAutoFold(lines)).toBe(true);
    expect(exceedsAutoFold(lines.split("\n").slice(0, AUTO_FOLD_LINES).join("\n"))).toBe(false);
  });
  it("글자 수는 코드 포인트 — 이모지 하나가 한 글자", () => {
    expect(charCount("📄가")).toBe(2);
  });
  it("짧은 본문은 나누지 않는다(null) — 작업 내용 줄이 없다", () => {
    expect(splitAuto("짧은 답입니다.")).toBeNull();
    expect(messageLayers({ content: "짧은 답입니다.", detail: null })).toEqual({ conversation: "짧은 답입니다.", work: null });
  });
});

describe("splitAuto — 첫 문단(최대 400자)을 대화로, 나머지를 작업 내용으로", () => {
  it("첫 문단 = 빈 줄 전까지, 나머지는 그 뒤 전부", () => {
    const content = ["@Lead 조사 끝났습니다. 핵심은 둘입니다.", "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe("@Lead 조사 끝났습니다. 핵심은 둘입니다.");
    expect(s.rest).toBe(para(1300));
  });

  it("표 도중에서 자르지 않는다 — 빈 줄 없이 붙은 표는 첫 문단에 들지 않는다", () => {
    const content = ["요약 한 줄입니다.", TABLE, "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe("요약 한 줄입니다.");
    expect(s.rest.startsWith("| 사업자 | 수수료 |\n|---|---|")).toBe(true);
    expect(countTables(s.rest)).toBe(1);
  });

  it("코드 블록 도중에서 자르지 않는다 — 코드 안의 빈 줄은 문단 경계가 아니다", () => {
    const content = ["구현 설명입니다.", CODE, "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe("구현 설명입니다.");
    expect(s.rest.startsWith("```ts\nconst a = 1;\n\nconst b = 2;\n```")).toBe(true);
  });

  it("첫 블록이 표면 대화 층은 비우고 본문 전부를 접는다(표를 반만 보이지 않는다)", () => {
    const content = [TABLE, "", para(1300)].join("\n");
    expect(splitAuto(content)).toEqual({ conversation: "", rest: content });
  });

  it("첫 블록이 코드 블록이어도 같다 — 앞의 빈 줄은 무시", () => {
    const content = ["", "", CODE, "", para(1300)].join("\n");
    expect(splitAuto(content)).toEqual({ conversation: "", rest: content.trim() });
  });

  it("제목 한 줄뿐인 첫 문단은 다음 문단까지 대화로", () => {
    const content = ["## 조사 결과", "", "법은 통과했고 시행은 내년입니다.", "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe("## 조사 결과\n\n법은 통과했고 시행은 내년입니다.");
    expect(s.rest).toBe(para(1300));
  });

  it("제목 다음이 표면 제목만 대화로 두고 표는 접는다", () => {
    const content = ["## 비교표", "", TABLE, "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe("## 비교표");
    expect(s.rest.startsWith("| 사업자")).toBe(true);
  });

  it("400자 넘는 첫 문단은 400자 안의 마지막 문장 끝에서 자른다(말줄임 없음)", () => {
    const s1 = "가".repeat(150) + "다. ";
    const s2 = "나".repeat(150) + "다. ";
    const s3 = "라".repeat(300) + "다.";
    const content = [s1 + s2 + s3, "", para(1300)].join("\n");
    const s = splitAuto(content)!;
    expect(s.conversation).toBe((s1 + s2).trimEnd());
    expect(charCount(s.conversation)).toBeLessThanOrEqual(AUTO_HEAD_CHARS);
    expect(s.rest.startsWith(s3)).toBe(true);
    // 저장은 바꾸지 않는다 — 대화 + 나머지가 원문의 글자를 잃지 않는다(공백만 다듬는다).
    expect((s.conversation + s.rest).replace(/\s/g, "")).toBe(content.replace(/\s/g, ""));
  });

  it("문장 경계가 없으면 마지막 공백에서 자르고 말줄임을 단다", () => {
    const words = Array.from({ length: 120 }, () => "낱말임").join(" "); // 문장 부호 없음
    const s = splitAuto([words, "", para(1300)].join("\n"))!;
    expect(s.conversation.endsWith("…")).toBe(true);
    expect(charCount(s.conversation)).toBeLessThanOrEqual(AUTO_HEAD_CHARS + 1);
    expect(s.rest.startsWith("낱말임")).toBe(true);
  });

  it("공백도 없으면 400자에서 끊는다", () => {
    const s = splitAuto(para(1500))!;
    expect(s.conversation).toBe(para(AUTO_HEAD_CHARS) + "…");
    expect(s.rest).toBe(para(1100));
  });

  it("80자 앞의 문장 끝은 쓰지 않는다 — 대화가 한 토막이 되지 않게", () => {
    const text = "네. " + Array.from({ length: 120 }, () => "낱말임").join(" ");
    const c = cutHead(text);
    expect(c.head.length).toBeGreaterThan(80);
    expect(c.ellipsis).toBe(true);
  });

  it("멘션 링크 한가운데는 자르지 않는다 — 링크 앞으로 당긴다", () => {
    const pad = "가".repeat(390);
    const text = pad + " [@Researcher](mention://agent/6f1a) 이어서";
    const c = cutHead(text);
    expect(c.head).toBe(pad);
    expect(c.rest.startsWith("[@Researcher](mention://agent/6f1a)")).toBe(true);
  });

  it("첫 문단은 5줄까지 — 25줄 넘는 목록은 줄 경계로 나눈다", () => {
    const lines = Array.from({ length: 30 }, (_, i) => `- 항목 ${i}`);
    const s = splitAuto(lines.join("\n"))!;
    expect(s.conversation).toBe(lines.slice(0, 5).join("\n"));
    expect(s.rest).toBe(lines.slice(5).join("\n"));
  });

  it("나머지가 비면 나누지 않는다", () => {
    // 한 줄짜리 1,300자 문단 — 400자에서 잘리므로 나머지가 있다. 반면 26줄이지만 첫 문단 5줄 안에 전부 들어가는 경우는 없다 —
    // 여기서는 임계를 넘는데 첫 문단 뒤가 공백뿐인 경우(끝의 빈 줄 30개)를 잰다.
    const content = "짧은 문단입니다." + "\n".repeat(30);
    expect(exceedsAutoFold(content)).toBe(true);
    expect(splitAuto(content)).toBeNull();
  });

  it("detail 이 있으면 폴백을 쓰지 않는다 — 에이전트가 나눈 그대로(auto=false)", () => {
    const v = messageLayers({ content: para(2000), detail: "## A\n조사 전문" });
    expect(v).toEqual({ conversation: para(2000), work: { text: "## A\n조사 전문", auto: false } });
  });

  it("detail 이 공백뿐이면 없는 것으로 본다", () => {
    expect(messageLayers({ content: "짧다", detail: "  \n " }).work).toBeNull();
  });

  it("폴백은 auto=true", () => {
    const v = messageLayers({ content: ["첫 문단.", "", para(1300)].join("\n"), detail: null });
    expect(v.work).toEqual({ text: para(1300), auto: true });
    expect(v.conversation).toBe("첫 문단.");
  });
});

describe("접힌 줄의 요약 — 글자 수 · 표 N개 · 첫 줄 미리보기", () => {
  it("글자 수: 1,000 미만은 「850자」, 1만 미만은 쉼표 「2,400자」, 1만 이상은 「1.5만 자」", () => {
    expect(fmt(0)).toBe("0자");
    expect(fmt(850)).toBe("850자");
    expect(fmt(999)).toBe("999자");
    expect(fmt(1000)).toBe("1,000자");
    expect(fmt(2400)).toBe("2,400자");
    expect(fmt(9999)).toBe("9,999자");
    expect(fmt(10_000)).toBe("1만 자");
    expect(fmt(12_000)).toBe("1.2만 자");
    expect(fmt(15_480)).toBe("1.5만 자");
    expect(fmt(28_000)).toBe("2.8만 자");
    expect(fmt(19_960)).toBe("2만 자");
  });

  it("표 개수 — 구분줄 있는 것만, 인용 안의 표도 센다, 코드 안의 표 모양은 세지 않는다", () => {
    expect(countTables("")).toBe(0);
    expect(countTables([TABLE, "", "글", "", TABLE].join("\n"))).toBe(2);
    expect(countTables("a | b\nc | d")).toBe(0);
    expect(countTables("> | a | b |\n> |---|---|\n> | 1 | 2 |")).toBe(1);
    expect(countTables("```\n| a | b |\n|---|---|\n```")).toBe(0);
  });

  it("첫 줄 미리보기 — 제목 표시를 걷고 40자에서 말줄임", () => {
    expect(firstLinePreview("## A. 시장 규모·성장 전망 — 3년 연속 두 자릿수 성장이 이어졌고 내년에도 이어질 전망")).toBe(
      "A. 시장 규모·성장 전망 — 3년 연속 두 자릿수 성장이 이어졌고 내년…",
    );
    expect(Array.from(firstLinePreview("가".repeat(100))).length).toBe(41);
  });

  it("미리보기 — 빈 줄·코드 펜스는 건너뛰고, 굵게·코드·링크 표시는 글자만, 멘션은 이름만", () => {
    expect(firstLinePreview("\n\n```ts\nconst a = 1\n```")).toBe("const a = 1");
    expect(firstLinePreview("**중요** `REST` 는 [공시](https://x.y) 참고 [@Lead](mention://agent/1)")).toBe("중요 REST 는 공시 참고 @Lead");
    expect(firstLinePreview("- 첫 항목")).toBe("첫 항목");
    expect(firstLinePreview("> 인용")).toBe("인용");
  });

  it("미리보기 — 표 머리행이면 칸을 가운뎃점으로", () => {
    expect(firstLinePreview(TABLE)).toBe("사업자 · 수수료");
  });
});

let seq = 0;
const ev = (cls: TaskEvent["class"], verb: TaskEvent["verb"], over: Partial<TaskEvent> = {}): TaskEvent => ({
  id: `e${++seq}`, task_id: "t1", seq, class: cls, verb, object_ref: null, outcome: "ok", tool: null, input: null, output: null, usage: null,
  superseded_by: null, masked: false, sentence: null, created_at: "2026-09-25T10:00:00Z", ...over,
} as TaskEvent);

describe("summarizeProcess — 많은 동작 2개와 수 · 걸린 시간 · 실패", () => {
  const parts = (s: ReturnType<typeof summarizeProcess>) => (s.state === "ready" ? s.top.map((p) => slotText(p.text, p.n)) : []);

  it("상태 셋 — 읽는 중 · 구조화 미지원 · 이벤트 0(대기 중)", () => {
    expect(summarizeProcess(undefined)).toEqual({ state: "loading" });
    expect(summarizeProcess({ events: [], structured: true, loading: true })).toEqual({ state: "loading" });
    expect(summarizeProcess({ events: [], structured: false, loading: false })).toEqual({ state: "unstructured" });
    expect(summarizeProcess({ events: [], structured: true, loading: false })).toEqual({ state: "waiting" });
  });

  it("가장 많은 동작 2개 — 편집은 파일 수, 나머지는 횟수, 발화·턴 생명주기는 세지 않는다", () => {
    const events = [
      ev("runtime", "start"),
      ev("tool", "search"), ev("tool", "search"), ev("tool", "search"),
      ev("tool", "edit_file", { payload: { path: "a.md" } }), ev("tool", "edit_file", { payload: { path: "a.md" } }), ev("tool", "edit_file", { payload: { path: "b.md" } }),
      ev("tool", "run_shell"),
      ev("message", "say"), ev("message", "say"), ev("message", "say"), ev("message", "say"),
    ];
    const s = summarizeProcess({ events, structured: true, loading: false });
    expect(parts(s)).toEqual(["검색 3회", "파일 2개 편집"]);
  });

  it("같은 툴 호출(started → ok)은 한 번으로 센다", () => {
    const events = [
      ev("tool", "search", { outcome: "started", payload: { tool_call_id: "c1" } }),
      ev("tool", "search", { outcome: "ok", payload: { tool_call_id: "c1" } }),
    ];
    expect(parts(summarizeProcess({ events, structured: true, loading: false }))).toEqual(["검색 1회"]);
  });

  it("동률이면 먼저 나온 동작이 앞", () => {
    const events = [ev("status", "submit_artifact"), ev("tool", "run_shell")];
    expect(parts(summarizeProcess({ events, structured: true, loading: false }))).toEqual(["아티팩트 제출 1건", "셸 명령 1회"]);
  });

  it("실패 수 — 접힌 줄 꼬리에 쓴다(started 뒤 failed 로 끝난 호출도 하나)", () => {
    const events = [
      ev("tool", "run_shell", { outcome: "started", payload: { tool_call_id: "x" } }),
      ev("tool", "run_shell", { outcome: "failed", payload: { tool_call_id: "x" } }),
      ev("runtime", "error", { outcome: "failed" }),
      ev("tool", "search"),
    ];
    const s = summarizeProcess({ events, structured: true, loading: false });
    expect(s.state === "ready" && s.failures).toBe(2);
  });

  it("걸린 시간 — 첫 이벤트에서 마지막 이벤트까지", () => {
    const events = [ev("tool", "search", { created_at: "2026-09-25T10:00:00Z" }), ev("tool", "search", { created_at: "2026-09-25T10:04:10Z" })];
    const s = summarizeProcess({ events, structured: true, loading: false });
    expect(s.state === "ready" && s.duration.map((d) => slotText(d.text, d.n))).toEqual(["4분"]);
  });

  it("formatDuration — 초 · 분 · 시간 분", () => {
    const f = (ms: number) => formatDuration(ms).map((d) => slotText(d.text, d.n)).join(" ");
    expect(f(40_000)).toBe("40초");
    expect(f(7 * 60_000)).toBe("7분");
    expect(f(65 * 60_000)).toBe("1시간 5분");
    expect(f(120 * 60_000)).toBe("2시간");
  });

  it("셀 동작이 하나도 없으면 top 이 비어 있다(화면은 「도구·파일·플랫폼 조작 없음」)", () => {
    const s = summarizeProcess({ events: [ev("message", "say")], structured: true, loading: false });
    expect(s).toMatchObject({ state: "ready", top: [], failures: 0, duration: [] });
  });
});

describe("보기 전환 저장 키", () => {
  it("방 id 를 키에 넣는다", () => expect(timelineViewKey("r1")).toBe("colab.timelineView.r1"));
});
