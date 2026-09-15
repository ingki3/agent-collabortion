import { describe, expect, it } from "vitest";
import { toDisplay, toWire, type MentionTarget } from "./mentions";

// W-15(2026-09-15, Director): 작성창에 `[@Writer](mention://agent/…)` 원문이 보였다.
// 화면은 `@이름`, 전송 직전에만 링크로.
const T: MentionTarget[] = [
  { kind: "agent", id: "a-lead", name: "Lead" },
  { kind: "agent", id: "a-lead2", name: "Lead2" },
  { kind: "agent", id: "a-w", name: "Writer" },
  { kind: "user", id: "u-1", name: "민지" },
];

describe("toWire — 화면 글 → 전송 본문", () => {
  it("아는 이름만 링크로 바꾼다", () => {
    expect(toWire("@Writer 요약해줘", T)).toBe("[@Writer](mention://agent/a-w) 요약해줘");
    expect(toWire("@Nobody 요약해줘", T)).toBe("@Nobody 요약해줘");
  });
  it("긴 이름을 먼저 맞춰 @Lead 가 @Lead2 를 잘라먹지 않는다", () => {
    expect(toWire("@Lead2 와 @Lead", T)).toBe("[@Lead2](mention://agent/a-lead2) 와 [@Lead](mention://agent/a-lead)");
  });
  it("이미 링크인 자리는 그대로 두고, 이메일 같은 중간 @ 는 건드리지 않는다", () => {
    const linked = "[@Writer](mention://agent/a-w) 그리고 @민지 · mail@Writer.com";
    expect(toWire(linked, T)).toBe("[@Writer](mention://agent/a-w) 그리고 [@민지](mention://user/u-1) · mail@Writer.com");
  });
  it("@all 은 항상 안다 · 문장 부호 뒤도 이름 끝으로 본다", () => {
    expect(toWire("@all 공지, @Writer!", T)).toBe("[@all](mention://all/all) 공지, [@Writer](mention://agent/a-w)!");
  });
  it("아는 이름이 없으면 그대로", () => {
    expect(toWire("@Writer", [])).toBe("@Writer");
  });
});

describe("toDisplay — 전송 본문 → 화면 글", () => {
  it("링크를 @이름 으로", () => {
    expect(toDisplay("[@Writer](mention://agent/a-w) 요약 [@all](mention://all/all)")).toBe("@Writer 요약 @all");
  });
  it("왕복", () => {
    const wire = "[@Writer](mention://agent/a-w) 요약";
    expect(toWire(toDisplay(wire), T)).toBe(wire);
  });
});
