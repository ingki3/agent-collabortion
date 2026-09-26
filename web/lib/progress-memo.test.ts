/**
 * 진행 메모(SCREEN §4.6 v0.19.10 「작업 중」 말풍선) — `message.delta` 누적 전문을 조각·마지막 문장으로 바꾸는 규칙.
 *  - 정본 경계: 데몬이 도구 호출 경계마다 넣는 빈 줄(`\n\n`, runner.go `appendSay`).
 *  - 옛 데몬 폴백: 두 델타 사이에 같은 task 의 도구 이벤트가 오면 앞 델타 길이가 경계.
 *  - 추측 분리 금지: 공백 없이 붙은 「…합니다.BGM」은 나누지 않는다.
 */
import { describe, expect, it } from "vitest";
import { ELIDED_MARK, applyDelta, dropMemo, lastSentence, memoEffect, memoSegments, noteToolEvent, notePosted, type ProgressMemos } from "./progress-memo";

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

describe("잘린 스냅숏(데몬 preview 상한, v0.10.2)", () => {
  const long = (n: number, ch: string) => ch.repeat(n);
  it("잘린 꼬리를 앞 전문에 이어 붙인다 — 게시 기준점·조각 경계가 그대로", () => {
    const p1 = "밸런스 하네스를 돌려 봤는데 1등 쏠림이 남아 있습니다. 원인을 찾겠습니다.";
    const p2 = "하네스 자체가 편향돼 있었습니다 — 시작 위치가 고정이라 1번 자리가 늘 유리했습니다.";
    const p3 = "시작 위치를 섞고 다시 돌렸더니 테스트 28항목이 모두 통과합니다.";
    let m: ProgressMemos = applyDelta({}, d(p1));
    m = notePosted(m, "a1"); // 게시 — 여기까지는 말풍선 밖
    m = applyDelta(m, d(`${p1}\n\n${p2}`));
    expect(memoSegments(m.a1)).toEqual([p2]);
    // 데몬이 자른 스냅숏: 앞부분 생략 + 꼬리(둘째 문단부터).
    m = applyDelta(m, d(`${ELIDED_MARK}\n\n${p2}\n\n${p3}`));
    expect(m.a1.text).toBe(`${p1}\n\n${p2}\n\n${p3}`);
    expect(memoSegments(m.a1)).toEqual([p2, p3]);
    expect(lastSentence(m.a1)).toBe(p3);
  });

  it("겹침을 못 찾으면(사이가 통째로 잘렸다) 꼬리부터 새로 센다 — 생략 표시는 본문에 남지 않는다", () => {
    let m: ProgressMemos = applyDelta({}, d(long(300, "가")));
    m = applyDelta(m, d(`${ELIDED_MARK}\n\n${long(300, "나")} 이어서 씁니다.`));
    expect(m.a1.text.startsWith(ELIDED_MARK)).toBe(false);
    expect(m.a1.text).toBe(`${long(300, "나")} 이어서 씁니다.`);
    expect(memoSegments(m.a1)).toEqual([`${long(300, "나")} 이어서 씁니다.`]);
  });

  it("잘린 스냅숏이 같은 전문을 다시 보내도 텍스트가 자라지 않는다(중복 이어 붙이기 없음)", () => {
    const p1 = "첫 문단입니다 — 하네스를 다시 짰습니다.";
    const p2 = "둘째 문단입니다 — 테스트 28항목을 돌리는 중입니다.";
    let m: ProgressMemos = applyDelta({}, d(`${p1}\n\n${p2}`));
    const before = m.a1.text;
    m = applyDelta(m, d(`${ELIDED_MARK}\n\n${p2}`));
    expect(m.a1.text).toBe(before);
    expect(memoSegments(m.a1)).toEqual([p1, p2]);
  });
});

// PR #356 리뷰 NN3 — 끝나는 길 셋은 서로를 기다리지 않는다. 화면은 말풍선을 「도는 턴」으로 한 번 더 거르므로
// 메모가 남는 것이 화면에 안 보인다(그래서 여기가 이 규칙의 자리다).
describe("memoEffect — 끝나는 길 셋 · 조각 경계 · 게시", () => {
  const ev = (type: string, payload: unknown) => ({ type, payload });
  const withMemo = () => memoEffect({}, ev("message.delta", { agent_id: "a1", task_id: "t1", text: "일하는 중입니다." }));

  it("task.updated 단독(turn-close 줄도 lane.updated 도 없이) 끝난 상태면 그 task 의 메모를 버린다", () => {
    for (const status of ["completed", "failed", "cancelled", "paused"]) {
      const m = memoEffect(withMemo(), ev("task.updated", { id: "t1", status }));
      expect(Object.keys(m), status).toEqual([]);
    }
  });

  it("도는 상태의 task.updated 는 메모를 건드리지 않는다", () => {
    for (const status of ["dispatched", "preparing", "running", "waiting_human"]) {
      const m = memoEffect(withMemo(), ev("task.updated", { id: "t1", status }));
      expect(memoSegments(m.a1), status).toEqual(["일하는 중입니다."]);
    }
  });

  it("다른 task 가 끝나도 남는다", () => {
    const m = memoEffect(withMemo(), ev("task.updated", { id: "t9", status: "completed" }));
    expect(memoSegments(m.a1)).toEqual(["일하는 중입니다."]);
  });

  it("턴을 닫는 기록 줄(turn_end·error·cancel)도 각각 버린다 — superseded 된 줄은 아니다", () => {
    for (const verb of ["turn_end", "error", "cancel"]) {
      const m = memoEffect(withMemo(), ev("task_event.appended", { task_id: "t1", class: "runtime", verb }));
      expect(Object.keys(m), verb).toEqual([]);
    }
    const superseded = memoEffect(withMemo(), ev("task_event.appended", { task_id: "t1", class: "runtime", verb: "turn_end", superseded_by: "e9" }));
    expect(memoSegments(superseded.a1)).toEqual(["일하는 중입니다."]);
  });

  it("lane.updated 가 더는 안 돌면 그 에이전트의 메모를 버린다", () => {
    expect(Object.keys(memoEffect(withMemo(), ev("lane.updated", { agent_id: "a1", status: "done" })))).toEqual([]);
    expect(memoSegments(memoEffect(withMemo(), ev("lane.updated", { agent_id: "a1", status: "running" })).a1)).toEqual(["일하는 중입니다."]);
  });

  it("도구 줄은 경계이고 · 에이전트 게시는 기준점 · 사람 메시지는 아무것도 아니다", () => {
    let m = withMemo();
    m = memoEffect(m, ev("task_event.appended", { task_id: "t1", class: "tool", verb: "run_shell" }));
    m = memoEffect(m, ev("message.delta", { agent_id: "a1", task_id: "t1", text: "일하는 중입니다.다 됐습니다." }));
    expect(memoSegments(m.a1)).toEqual(["일하는 중입니다.", "다 됐습니다."]);
    expect(memoSegments(memoEffect(m, ev("message.created", { author_type: "user", author_id: "u1" })).a1)).toHaveLength(2);
    expect(memoSegments(memoEffect(m, ev("message.created", { author_type: "agent", author_id: "a1" })).a1)).toEqual([]);
  });
});

describe("잘린 스냅숏 — 우연한 짧은 겹침으로 이어 붙이지 않는다", () => {
  it("꼬리 앞이 앞 전문 끝과 몇 글자만 같으면 이어 붙이지 않고 꼬리부터 새로 센다", () => {
    // 앞 전문 끝과 꼬리 앞이 「다.」 두 글자만 같다 — 진짜 겹침이 아니다(데몬은 16,000자를 보낸다).
    const prev = "하네스를 다시 짰습니다.";
    const tail = "다. 이어지는 다른 문장입니다.";
    let m: ProgressMemos = applyDelta({}, d(prev));
    m = applyDelta(m, d(`${ELIDED_MARK}\n\n${tail}`));
    // 이어 붙였다면 「…짰습니다.」 + 「 이어지는…」 처럼 없던 문장이 생긴다.
    expect(m.a1.text).toBe(tail);
    expect(m.a1.text.startsWith(prev.slice(0, 5))).toBe(false);
  });

  it("겹침이 충분히 길면(24자 이상) 이어 붙인다", () => {
    const shared = "시작 위치를 섞고 다시 돌렸더니 테스트 28항목이 모두 통과합니다.";
    const prev = `처음 문단입니다.\n\n${shared}`;
    let m: ProgressMemos = applyDelta({}, d(prev));
    m = applyDelta(m, d(`${ELIDED_MARK}\n\n${shared} 이어서 문서를 고치겠습니다.`));
    expect(m.a1.text).toBe(`${prev} 이어서 문서를 고치겠습니다.`);
  });
});
