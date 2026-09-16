/**
 * 마크다운 렌더러 유닛(PRD FR-3.1, W-12). AST(`parseBlocks`·`parseInline`)와 DOM 둘 다 잰다 —
 * XSS 케이스는 **DOM 에 태그가 생기지 않는 것**이 판정이라 AST 만으로는 부족하다.
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { Markdown, parseBlocks, parseInline } from "./markdown";

afterEach(cleanup);

const html = (src: string, inline = false) => render(<Markdown content={src} inline={inline} />).container.innerHTML;
const dom = (src: string) => render(<Markdown content={src} />).container;

describe("블록", () => {
  it("문단 — 빈 줄로 나뉘고, 문단 안 줄바꿈은 <br>", () => {
    expect(parseBlocks("첫 줄\n둘째 줄\n\n둘째 문단")).toEqual([
      { type: "p", text: "첫 줄\n둘째 줄" },
      { type: "p", text: "둘째 문단" },
    ]);
    const c = dom("첫 줄\n둘째 줄");
    expect(c.querySelectorAll("p.md-p")).toHaveLength(1);
    expect(c.querySelectorAll("br")).toHaveLength(1);
  });

  it("제목 # ~ ### — #### 이상은 ### 로 접는다(메시지 안 제목은 --fs-card 이하)", () => {
    expect(parseBlocks("# 하나\n## 둘\n### 셋\n#### 넷")).toEqual([
      { type: "h", level: 1, text: "하나" },
      { type: "h", level: 2, text: "둘" },
      { type: "h", level: 3, text: "셋" },
      { type: "h", level: 3, text: "넷" },
    ]);
    const c = dom("## 계획");
    expect(c.querySelector("h2.md-h.md-h2")!.textContent).toBe("계획");
  });

  it("#### · ##### · ###### 전부 ### 로 접힌다 — h4 이하가 DOM 에 생기지 않는다(W-17, 규칙은 markdown.tsx HEADING_RE 주석)", () => {
    expect(parseBlocks("#### 넷\n##### 다섯\n###### 여섯").map((b) => (b.type === "h" ? b.level : b.type))).toEqual([3, 3, 3]);
    const c = dom("#### 넷\n##### 다섯\n###### 여섯");
    expect(c.querySelectorAll("h3.md-h.md-h3")).toHaveLength(3);
    expect(c.querySelectorAll("h4, h5, h6")).toHaveLength(0);
    // 일곱 개 이상은 제목이 아니다(CommonMark 와 같다).
    expect(parseBlocks("####### 일곱")).toEqual([{ type: "p", text: "####### 일곱" }]);
  });

  it("#태그 처럼 공백이 없으면 제목이 아니다", () => {
    expect(parseBlocks("#123 이슈")).toEqual([{ type: "p", text: "#123 이슈" }]);
  });

  it("코드 블록 — 언어는 버리고 본문은 그대로(마크다운 문법도 문자)", () => {
    const b = parseBlocks("```ts\nconst a = **1**;\n# not heading\n```\n뒤");
    expect(b).toEqual([
      { type: "code", text: "const a = **1**;\n# not heading", open: false },
      { type: "p", text: "뒤" },
    ]);
    const c = dom("```ts\nconst a = 1;\n```");
    expect(c.querySelector("pre.md-pre code")!.textContent).toBe("const a = 1;");
    expect(c.querySelector("pre.md-pre")!.getAttribute("data-open")).toBeNull();
  });

  it("닫히지 않은 펜스는 열린 채로 렌더된다(「작성 중…」 델타) — 뒤의 줄은 전부 코드", () => {
    expect(parseBlocks("설명\n```\nfoo\nbar")).toEqual([
      { type: "p", text: "설명" },
      { type: "code", text: "foo\nbar", open: true },
    ]);
    const c = dom("```\nfoo");
    expect(c.querySelector("pre.md-pre")!.getAttribute("data-open")).toBe("true");
    expect(c.querySelector("pre.md-pre code")!.textContent).toBe("foo");
  });

  it("목록 -·*·1. 과 중첩 1단", () => {
    expect(parseBlocks("- 하나\n- 둘\n  - 둘의 자식\n  - 또\n- 셋")).toEqual([
      {
        type: "list", ordered: false,
        items: [{ text: "하나" }, { text: "둘", children: { ordered: false, items: [{ text: "둘의 자식" }, { text: "또" }] } }, { text: "셋" }],
      },
    ]);
    expect(parseBlocks("1. 범위 확정\n2. 조사\n3. 검토")).toEqual([
      { type: "list", ordered: true, items: [{ text: "범위 확정" }, { text: "조사" }, { text: "검토" }] },
    ]);
    expect(parseBlocks("* a\n* b")).toMatchObject([{ type: "list", ordered: false }]);
    const c = dom("- 하나\n- 둘\n  1. 자식");
    expect(c.querySelectorAll("ul.md-ul > li.md-li")).toHaveLength(2);
    expect(c.querySelectorAll("ul.md-ul > li > ol.md-ol > li")).toHaveLength(1);
  });

  it("느슨한 목록(항목 사이 빈 줄)도 한 목록이고, 종류가 바뀌면 새 목록", () => {
    expect(parseBlocks("1. a\n\n2. b\n\n- c")).toEqual([
      { type: "list", ordered: true, items: [{ text: "a" }, { text: "b" }] },
      { type: "list", ordered: false, items: [{ text: "c" }] },
    ]);
  });

  it("목록 항목 아래 들여쓴 줄은 항목의 글에 붙는다", () => {
    expect(parseBlocks("- 하나\n  이어서\n- 둘")).toEqual([
      { type: "list", ordered: false, items: [{ text: "하나\n이어서" }, { text: "둘" }] },
    ]);
  });

  it("> 인용 — 안은 다시 블록(문단·목록)", () => {
    expect(parseBlocks("> 인용 한 줄\n> - 항목")).toEqual([
      { type: "quote", blocks: [{ type: "p", text: "인용 한 줄" }, { type: "list", ordered: false, items: [{ text: "항목" }] }] },
    ]);
    expect(dom("> 말").querySelector("blockquote.md-quote > p.md-p")!.textContent).toBe("말");
  });

  it("표 — 구분줄이 있는 것만, 셀 수는 머리행에 맞춘다", () => {
    expect(parseBlocks("| 항목 | 값 |\n|---|---:|\n| a | 1 |\n| b |")).toEqual([
      { type: "table", head: ["항목", "값"], rows: [["a", "1"], ["b", ""]] },
    ]);
    const c = dom("| h1 | h2 |\n|---|---|\n| **a** | `b` |");
    expect(c.querySelectorAll("table.md-table th")).toHaveLength(2);
    expect(c.querySelector("table.md-table td strong")!.textContent).toBe("a");
    expect(c.querySelector("table.md-table td code")!.textContent).toBe("b");
    expect(c.querySelector(".md-table-wrap")).not.toBeNull();
  });

  it("구분줄 없는 | 는 그냥 글이다", () => {
    expect(parseBlocks("a | b")).toEqual([{ type: "p", text: "a | b" }]);
  });

  it("표는 구분줄이 있어야 표다(GFM) — 머리행 뒤가 구분줄이 아니면 여러 줄이어도 문단, 구분줄 뒤에만 행이 붙는다(W-17, 규칙은 TABLE_SEP_RE 주석)", () => {
    // 구분줄 없이 파이프 줄 셋 — 표가 아니라 문단 하나(줄바꿈은 <br>).
    expect(parseBlocks("| a | b |\n| c | d |\n| e | f |")).toEqual([{ type: "p", text: "| a | b |\n| c | d |\n| e | f |" }]);
    expect(dom("| a | b |\n| c | d |").querySelector("table")).toBeNull();
    // 구분줄이 있으면 표 — 정렬 표시(:---:)도 구분줄이다.
    expect(parseBlocks("| a | b |\n|:---:|---:|\n| c | d |")).toEqual([{ type: "table", head: ["a", "b"], rows: [["c", "d"]] }]);
    // 구분줄만으로는 표가 되지 않는다(머리행이 먼저).
    expect(parseBlocks("|---|---|\n| c | d |").some((b) => b.type === "table")).toBe(false);
  });

  it("--- 가로줄은 목록이 아니다", () => {
    expect(parseBlocks("위\n\n---\n\n아래")).toEqual([{ type: "p", text: "위" }, { type: "hr" }, { type: "p", text: "아래" }]);
    expect(parseBlocks("* * *")).toEqual([{ type: "hr" }]);
  });

  it("CRLF 도 같은 결과", () => {
    expect(parseBlocks("a\r\n\r\nb")).toEqual([{ type: "p", text: "a" }, { type: "p", text: "b" }]);
  });

  it("빈 본문은 아무 블록도 없다", () => {
    expect(parseBlocks("")).toEqual([]);
    expect(html("")).toBe('<div class="md"></div>');
  });
});

describe("인라인", () => {
  it("**굵게** · *기울임* · `코드` · ~~취소~~", () => {
    expect(parseInline("**굵게** 와 *기울임* 과 `코드` 와 ~~취소~~")).toEqual([
      { type: "strong", children: [{ type: "text", text: "굵게" }] },
      { type: "text", text: " 와 " },
      { type: "em", children: [{ type: "text", text: "기울임" }] },
      { type: "text", text: " 과 " },
      { type: "code", text: "코드" },
      { type: "text", text: " 와 " },
      { type: "del", children: [{ type: "text", text: "취소" }] },
    ]);
    const c = dom("**굵게** *기울임* `코드`");
    expect(c.querySelector("strong")!.textContent).toBe("굵게");
    expect(c.querySelector("em")!.textContent).toBe("기울임");
    expect(c.querySelector("code.md-code")!.textContent).toBe("코드");
  });

  it("인라인 코드 안은 문자 그대로(강조·링크 문법이 살아남지 않는다)", () => {
    expect(parseInline("`**a** [b](https://x)`")).toEqual([{ type: "code", text: "**a** [b](https://x)" }]);
  });

  it("__굵게__ 와 _기울임_ 은 단어 경계에서만 — snake_case 는 그대로", () => {
    expect(parseInline("__굵게__ _기울임_ some_var_name")).toEqual([
      { type: "strong", children: [{ type: "text", text: "굵게" }] },
      { type: "text", text: " " },
      { type: "em", children: [{ type: "text", text: "기울임" }] },
      { type: "text", text: " some_var_name" },
    ]);
  });

  it("짝이 없는 표시는 문자 그대로 — 2 * 3 * 4 · 별 하나", () => {
    expect(parseInline("2 * 3 * 4")).toEqual([{ type: "text", text: "2 * 3 * 4" }]);
    expect(parseInline("a*b")).toEqual([{ type: "text", text: "a*b" }]);
    expect(parseInline("**열림")).toEqual([{ type: "text", text: "**열림" }]);
    expect(parseInline("`열린 백틱")).toEqual([{ type: "text", text: "`열린 백틱" }]);
  });

  it("강조는 중첩된다", () => {
    expect(parseInline("**굵고 *기울고* `코드`**")).toEqual([
      { type: "strong", children: [{ type: "text", text: "굵고 " }, { type: "em", children: [{ type: "text", text: "기울고" }] }, { type: "text", text: " " }, { type: "code", text: "코드" }] },
    ]);
  });

  it("역슬래시 이스케이프 — \\* 는 별", () => {
    expect(parseInline("\\*별\\* 그리고 \\`백틱\\`")).toEqual([{ type: "text", text: "*별* 그리고 `백틱`" }]);
  });

  it("[텍스트](https://…) 링크 — target=_blank · rel=noopener", () => {
    expect(parseInline("[문서](https://example.com/a?b=1)")).toEqual([
      { type: "link", href: "https://example.com/a?b=1", children: [{ type: "text", text: "문서" }] },
    ]);
    const a = dom("[문서](http://example.com)").querySelector("a.md-link")!;
    expect(a.getAttribute("href")).toBe("http://example.com");
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toContain("noopener");
    expect(a.textContent).toBe("문서");
  });

  it("http/https 가 아닌 스킴은 링크가 되지 않고 문자 그대로 — javascript: · data: · ftp:", () => {
    for (const src of ["[x](javascript:alert(1))", "[x](data:text/html,hi)", "[x](ftp://h/f)", "[x](//evil)", "[x](JAVASCRIPT:alert(1))"]) {
      expect(parseInline(src)).toEqual([{ type: "text", text: src }]);
      expect(dom(src).querySelector("a")).toBeNull();
    }
  });

  it("멘션 링크는 기존 칩 그대로(.msg__mention · data-mention) — lib/mentions 의 문법", () => {
    expect(parseInline("[@Lead](mention://agent/a1) 봐줘 [@all](mention://all/all)")).toEqual([
      { type: "mention", kind: "agent", id: "a1", name: "Lead" },
      { type: "text", text: " 봐줘 " },
      { type: "mention", kind: "all", id: "all", name: "all" },
    ]);
    const c = dom("**[@Lead](mention://agent/a1)** 인사");
    const m = c.querySelector("strong > .msg__mention")!;
    expect(m.textContent).toBe("@Lead");
    expect(m.getAttribute("data-mention")).toBe("agent:a1");
    expect(c.querySelector("a")).toBeNull();
  });

  it("mention:// 가 멘션 모양이 아니면(이름에 @ 없음) 문자 그대로", () => {
    expect(parseInline("[Lead](mention://agent/a1)")).toEqual([{ type: "text", text: "[Lead](mention://agent/a1)" }]);
  });
});

describe("XSS — HTML 은 문자 그대로(태그가 DOM 에 생기지 않는다)", () => {
  it.each([
    "<img src=x onerror=alert(1)>",
    "<script>alert(1)</script>",
    "**<b onmouseover=alert(1)>굵게</b>**",
    "[<img src=x onerror=alert(1)>](https://x.io)",
    "```\n<script>alert(1)</script>\n```",
    "| <script>x</script> |\n|---|\n| <img onerror=alert(1) src=x> |",
    "> <iframe src=javascript:alert(1)>",
    "- <svg onload=alert(1)>",
    "# <a href=javascript:alert(1)>x</a>",
  ])("%s", (src) => {
    const c = dom(src);
    expect(c.querySelectorAll("img, script, iframe, svg, b, a:not(.md-link)")).toHaveLength(0);
    // 원문이 글자로 남아 있다(이스케이프됐지 지워지지 않았다).
    expect(c.textContent).toContain("alert(1)".slice(0, 5));
    expect(c.innerHTML).not.toMatch(/<(img|script|iframe|svg|b)\b/);
  });

  it("dangerouslySetInnerHTML 을 쓰지 않는다", async () => {
    const { readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const src = readFileSync(join(__dirname, "markdown.tsx"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
    expect(src).not.toContain("dangerouslySetInnerHTML");
  });
});

describe("<Markdown inline> — 짧은 본문(HITL 질문)", () => {
  it("인라인 문법만 살고 블록 문법은 문자 그대로", () => {
    const out = html("# 제목? **네** `x`", true);
    expect(out).toContain("<strong>네</strong>");
    expect(out).toContain("# 제목?");
    expect(out).not.toContain("<h1");
    expect(out.startsWith("<span")).toBe(true);
  });
});

describe("에이전트 답변 한 덩어리 — 통째로", () => {
  it("문단·제목·목록·코드·인용·표·링크·멘션이 한 본문에서 함께 그려진다", () => {
    const src = [
      "## 조사 결과",
      "상위 **5개** 사업자를 비교했습니다. 자세한 건 [보고서](https://example.com/r)를 보세요.",
      "",
      "- 토스페이먼츠 — `PG`",
      "- 나이스페이",
      "  - 하위 브랜드 2",
      "",
      "1. 범위 확정",
      "2. 초안",
      "",
      "> 주의: 수수료는 *협상* 가능",
      "",
      "| 사업자 | 수수료 |",
      "|---|---|",
      "| 토스 | 2.9% |",
      "",
      "```json",
      '{ "ok": true }',
      "```",
      "",
      "[@Lead](mention://agent/a1) 검토 부탁드립니다.",
    ].join("\n");
    const c = dom(src);
    expect(c.querySelector("h2")!.textContent).toBe("조사 결과");
    expect(c.querySelector("p strong")!.textContent).toBe("5개");
    expect(c.querySelector("a.md-link")!.getAttribute("href")).toBe("https://example.com/r");
    expect(c.querySelector(".md > ul.md-ul")!.children).toHaveLength(2);
    expect(c.querySelectorAll("ul.md-ul ul.md-ul > li")).toHaveLength(1);
    expect(c.querySelector(".md > ol.md-ol")!.children).toHaveLength(2);
    expect(c.querySelector("blockquote em")!.textContent).toBe("협상");
    expect(c.querySelectorAll("table td")).toHaveLength(2);
    expect(c.querySelector("pre code")!.textContent).toBe('{ "ok": true }');
    expect(c.querySelector(".msg__mention")!.getAttribute("data-mention")).toBe("agent:a1");
  });
});
