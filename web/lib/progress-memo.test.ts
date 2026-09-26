/**
 * 진행 메모(SCREEN §4.6 v0.19.10 「작업 중」 말풍선) — `message.delta` 누적 전문을 조각·마지막 문장으로 바꾸는 규칙.
 *  - 정본 경계: 데몬이 도구 호출 경계마다 넣는 빈 줄(`\n\n`, runner.go `appendSay`).
 *  - 옛 데몬 폴백: 두 델타 사이에 같은 task 의 도구 이벤트가 오면 앞 델타 길이가 경계.
 *  - 추측 분리 금지: 공백 없이 붙은 「…합니다.BGM」은 나누지 않는다.
 */
import { describe, expect, it } from "vitest";
import { applyDelta, dropMemo, lastSentence, memoSegments, noteToolEvent, notePosted, type ProgressMemos } from "./progress-memo";

const d = (text: string, task_id = "t1", agent_id = "a1") => ({ agent_id, task_id, text });

describe("조각 경계", () => {
  it("빈 줄(새 데몬) — 조각마다 한 문단", () => {
    const m = applyDelta({}, d("BGM v2 를 다시 짜고 있습니다.\n\n테스트 28항목을 돌려 보겠습니다."));
    expect(memoSegments(m.a1)).toEqual(["BGM v2 를 다시 짜고 있습니다.", "테스트 28항목을 돌려 보겠습니다."]);
  });

  it("옛 데몬 폴백 — 델타 사이에 도구 이벤트가 끼면 앞 델타 길이에서 나눈다", () => {
    let m: ProgressMemos = applyDelta({}, d("테스트가 통과합니다."));
    m = noteToolEvent(m, { task_id: "t1", class: "tool" });
    m = applyDelta(m, d("테스트가 통과합니다.BGM v2 가 준비됐습니다."));
    expect(memoSegments(m.a1)).toEqual(["테스트가 통과합니다.", "BGM v2 가 준비됐습니다."]);
  });

  it("도구 이벤트가 없으면 — 붙은 문장(「…합니다.BGM」)을 추측으로 나누지 않는다", () => {
    let m: ProgressMemos = applyDelta({}, d("테스트가 통과합니다."));
    m = applyDelta(m, d("테스트가 통과합니다.BGM v2 가 준비됐습니다."));
    expect(memoSegments(m.a1)).toEqual(["테스트가 통과합니다.BGM v2 가 준비됐습니다."]);
    expect(lastSentence(m.a1)).toBe("테스트가 통과합니다.BGM v2 가 준비됐습니다.");
  });

  it("다른 task 의 도구 이벤트 · 도구 아닌 이벤트는 경계가 아니다", () => {
    let m: ProgressMemos = applyDelta({}, d("하나."));
    m = noteToolEvent(m, { task_id: "t2", class: "tool" });
    m = noteToolEvent(m, { task_id: "t1", class: "message" });
    m = applyDelta(m, d("하나.둘."));
    expect(memoSegments(m.a1)).toEqual(["하나.둘."]);
  });

  it("같은 전문이 다시 와도(heartbeat 반복) 경계는 다음에 늘어난 델타에서 선다", () => {
    let m: ProgressMemos = applyDelta({}, d("하나."));
    m = noteToolEvent(m, { task_id: "t1", class: "tool" });
    m = applyDelta(m, d("하나."));
    expect(m.a1.pendingCut).toBe(true);
    m = applyDelta(m, d("하나.둘."));
    expect(memoSegments(m.a1)).toEqual(["하나.", "둘."]);
  });

  it("앞 전문의 연장이 아니면(재시도) 처음부터", () => {
    let m: ProgressMemos = applyDelta({}, d("하나.\n\n둘."));
    m = applyDelta(m, d("다시 시작."));
    expect(memoSegments(m.a1)).toEqual(["다시 시작."]);
  });
});

describe("마지막 문장 한 줄", () => {
  it("마지막 조각의 마지막 문장 — 문장 끝 뒤 공백에서만", () => {
    const m = applyDelta({}, d("원인을 찾았습니다.\n\n하네스가 편향돼 있었습니다. 4명 승률 25% 씩으로 맞췄습니다."));
    expect(lastSentence(m.a1)).toBe("4명 승률 25% 씩으로 맞췄습니다.");
  });

  it("쓰다 만 문장이면 그 문장 · 마크다운 기호는 걷는다", () => {
    const m = applyDelta({}, d("표를 **정리**했습니다. 이제 `go test` 를 돌리"));
    expect(lastSentence(m.a1)).toBe("이제 go test 를 돌리");
  });

  it("델타가 없으면 null", () => {
    expect(lastSentence(undefined)).toBeNull();
  });
});

describe("게시 · 턴 끝", () => {
  it("게시되면 그때까지의 메모는 메시지 앞의 것 — 말풍선은 그 뒤 메모만", () => {
    let m: ProgressMemos = applyDelta({}, d("밸런스를 맞췄습니다."));
    m = notePosted(m, "a1");
    expect(memoSegments(m.a1)).toEqual([]);
    expect(lastSentence(m.a1)).toBeNull();
    m = applyDelta(m, d("밸런스를 맞췄습니다.\n\n문서를 고치는 중입니다."));
    expect(memoSegments(m.a1)).toEqual(["문서를 고치는 중입니다."]);
  });

  it("턴이 끝나면 그 task 의 메모만 사라진다(다른 에이전트는 남는다)", () => {
    let m: ProgressMemos = applyDelta({}, d("하나.", "t1", "a1"));
    m = applyDelta(m, d("둘.", "t2", "a2"));
    m = dropMemo(m, { taskId: "t1" });
    expect(Object.keys(m)).toEqual(["a2"]);
    m = dropMemo(m, { agentId: "a2" });
    expect(m).toEqual({});
  });
});
