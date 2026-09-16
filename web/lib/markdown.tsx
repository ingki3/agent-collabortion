/**
 * 메시지 본문 마크다운 렌더러(PRD FR-3.1 "마크다운 지원", Director 지적 2026-09-15 · W-12).
 *
 * **의존성 없이, React 요소로만** 그린다 — `dangerouslySetInnerHTML` 은 쓰지 않는다. 그래서 본문 안의 HTML 태그
 * (`<img onerror=…>`, `<script>`)는 React 가 텍스트로 이스케이프해 **문자 그대로** 보이고(XSS 0), 링크는 `http`·`https`
 * 만 `<a>` 가 된다(`javascript:` 같은 다른 스킴은 원문 그대로 텍스트).
 *
 * 지원 문법(에이전트 답변에 실제로 나오는 만큼만):
 *   블록 — 문단(줄바꿈은 `<br>` — 예전 `white-space: pre-wrap` 시절과 같은 줄 모양), `#`~`###` 제목(`####` 이상은 `###` 로),
 *          ``` 코드 블록(언어 표시는 생략 · **닫히지 않은 펜스는 열린 채로** — 「작성 중…」 델타에 필요하다), `-`/`*`/`1.` 목록
 *          (중첩 1단 — 2칸 이상 들여쓰기), `>` 인용(안은 다시 블록), `|` 표(구분줄이 있는 것만 · 셀은 인라인), `---` 가로줄.
 *   인라인 — `**굵게**`·`__굵게__`, `*기울임*`·`_기울임_`(단어 경계에서만 — `snake_case` 를 건드리지 않는다), `` `코드` ``,
 *          `~~취소~~`, `[텍스트](http/https URL)`(target=_blank · rel=noopener), **멘션 링크는 기존 칩 그대로**
 *          (`[@이름](mention://kind/id)` → `.msg__mention`, `lib/mentions.ts` 의 문법 그대로), `\*` 같은 역슬래시 이스케이프.
 *
 * 파서는 두 층이다: `parseBlocks()` 가 줄 단위로 블록을 나누고, `parseInline()` 이 문자 단위로 인라인을 나눈다.
 * 둘 다 순수 함수라 유닛이 AST 를 그대로 재고, 화면은 `<Markdown>` 하나만 쓴다.
 */
import type { ReactNode } from "react";
import type { MentionKind } from "@/lib/mentions";

// ── 블록 ─────────────────────────────────────────────────────────────────

export type Block =
  | { type: "p"; text: string }
  | { type: "h"; level: 1 | 2 | 3; text: string }
  | { type: "code"; text: string; open: boolean }
  | { type: "list"; ordered: boolean; items: ListItem[] }
  | { type: "quote"; blocks: Block[] }
  | { type: "table"; head: string[]; rows: string[][] }
  | { type: "hr" };

export interface ListItem {
  text: string;
  /** 중첩 1단. 그 아래는 부모 항목의 글로 편입된다. */
  children?: { ordered: boolean; items: ListItem[] };
}

const FENCE_RE = /^ {0,3}(`{3,}|~{3,})\s*([^`\s]*)\s*$/;
/**
 * 제목 — `#`~`######` 를 인식하되 **`####` 이상은 `###` 로 접는다**(W-17 · PR #229 NN3): 메시지 안 제목은 글자 계단 §8.2 의
 * `--fs-card` 이하여야 하고(`.md-h1`~`.md-h3`), 4단계 이상의 위계는 카드 안에서 구분이 안 된다. 접는 자리는 `parseBlocks()` 의
 * `Math.min(3, …)` 하나뿐이고 `Block.level` 타입이 1|2|3 이라 렌더러에 h4 가 생길 수 없다.
 */
const HEADING_RE = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const HR_RE = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE_RE = /^ {0,3}>\s?(.*)$/;
const ITEM_RE = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
/**
 * 표 — GFM 처럼 **구분줄(`|---|---|`)이 있어야 표다**(W-17 · PR #229 NN4): `|` 가 든 줄 다음 줄이 구분줄일 때만 `isTableStart()`
 * 가 참이다. 구분줄 없는 `a | b` 는 문단(파이프는 글자)이고, 머리행 뒤 줄이 구분줄이 아니면 그 두 줄도 문단이다.
 * 셀 수는 머리행에 맞춘다(모자라면 빈 셀, 넘치면 버린다). 정렬 표시(`:---:`)는 인식만 하고 그리지 않는다.
 */
const TABLE_SEP_RE = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

const isBlank = (l: string) => l.trim() === "";

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\" && s[i + 1] === "|") { cur += "|"; i++; continue; }
    if (c === "|") { cells.push(cur.trim()); cur = ""; continue; }
    cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

/** 표의 시작인가 — 첫 줄에 `|` 가 있고 다음 줄이 구분줄이다. */
function isTableStart(lines: string[], i: number): boolean {
  return lines[i].includes("|") && i + 1 < lines.length && TABLE_SEP_RE.test(lines[i + 1]) && lines[i + 1].includes("-");
}

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (isBlank(line)) { i++; continue; }

    // 코드 블록 — 닫는 펜스가 없으면 끝까지 코드이고 `open: true` 로 남긴다(델타).
    const fence = line.match(FENCE_RE);
    if (fence) {
      const mark = fence[1];
      const body: string[] = [];
      let j = i + 1;
      let closed = false;
      for (; j < lines.length; j++) {
        const m = lines[j].match(/^ {0,3}(`{3,}|~{3,})\s*$/);
        if (m && m[1][0] === mark[0] && m[1].length >= mark.length) { closed = true; break; }
        body.push(lines[j]);
      }
      out.push({ type: "code", text: body.join("\n"), open: !closed });
      i = closed ? j + 1 : j;
      continue;
    }

    const h = line.match(HEADING_RE);
    if (h) {
      out.push({ type: "h", level: Math.min(3, h[1].length) as 1 | 2 | 3, text: h[2] });
      i++;
      continue;
    }

    if (HR_RE.test(line)) { out.push({ type: "hr" }); i++; continue; }

    if (QUOTE_RE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length) {
        const q = lines[i].match(QUOTE_RE);
        if (q) inner.push(q[1]);
        else if (!isBlank(lines[i]) && inner.length > 0 && !ITEM_RE.test(lines[i]) && !FENCE_RE.test(lines[i]) && !HEADING_RE.test(lines[i])) inner.push(lines[i]); // 게으른 이어짐
        else break;
        i++;
      }
      out.push({ type: "quote", blocks: parseBlocks(inner.join("\n")) });
      continue;
    }

    const item = line.match(ITEM_RE);
    if (item) {
      const r = parseList(lines, i);
      out.push(r.block);
      i = r.next;
      continue;
    }

    if (isTableStart(lines, i)) {
      const head = splitRow(lines[i]);
      const rows: string[][] = [];
      let j = i + 2;
      for (; j < lines.length && !isBlank(lines[j]) && lines[j].includes("|"); j++) {
        const cells = splitRow(lines[j]);
        while (cells.length < head.length) cells.push("");
        rows.push(cells.slice(0, head.length));
      }
      out.push({ type: "table", head, rows });
      i = j;
      continue;
    }

    // 문단 — 다른 블록이 시작되거나 빈 줄까지.
    const para: string[] = [line];
    let j = i + 1;
    for (; j < lines.length; j++) {
      const l = lines[j];
      if (isBlank(l) || FENCE_RE.test(l) || HEADING_RE.test(l) || HR_RE.test(l) || QUOTE_RE.test(l) || ITEM_RE.test(l) || isTableStart(lines, j)) break;
      para.push(l);
    }
    out.push({ type: "p", text: para.join("\n") });
    i = j;
  }
  return out;
}

/**
 * 목록 — 같은 들여쓰기의 항목이 이어지는 동안. 2칸 이상 들여쓴 항목은 바로 앞 항목의 자식(1단)이고, 그보다
 * 깊은 것은 자식 목록의 항목으로 편입된다. 항목 아래에 들여쓴 일반 줄은 그 항목의 글에 이어 붙는다.
 * 빈 줄 하나는 넘기되, 그 다음이 같은 종류의 항목이 아니면 목록이 끝난다(느슨한 목록도 한 목록).
 */
function parseList(lines: string[], start: number): { block: Block; next: number } {
  const first = lines[start].match(ITEM_RE)!;
  const baseIndent = first[1].length;
  const ordered = /\d/.test(first[2]);
  const items: ListItem[] = [];
  let i = start;
  while (i < lines.length) {
    const line = lines[i];
    if (isBlank(line)) {
      // 빈 줄 뒤에 같은 목록의 항목이 이어지면 계속, 아니면 끝.
      const nx = lines[i + 1]?.match(ITEM_RE);
      if (nx && nx[1].length >= baseIndent && (nx[1].length > baseIndent || /\d/.test(nx[2]) === ordered)) { i++; continue; }
      break;
    }
    const m = line.match(ITEM_RE);
    if (m && m[1].length <= baseIndent) {
      if (m[1].length < baseIndent || /\d/.test(m[2]) !== ordered) break; // 부모 목록 또는 다른 종류의 목록
      items.push({ text: m[3] });
      i++;
      continue;
    }
    const cur = items[items.length - 1];
    if (!cur) break;
    if (m) {
      // 들여쓴 항목 — 자식 목록(1단). 그 안에서 더 깊은 것은 자식의 글로.
      const childOrdered = /\d/.test(m[2]);
      if (!cur.children) cur.children = { ordered: childOrdered, items: [] };
      cur.children.items.push({ text: m[3] });
      i++;
      continue;
    }
    if (/^\s+/.test(line)) {
      // 들여쓴 이어지는 줄 — 마지막 항목(자식이 있으면 자식의 마지막 항목)의 글에 붙인다.
      const target = cur.children?.items[cur.children.items.length - 1] ?? cur;
      target.text += "\n" + line.trim();
      i++;
      continue;
    }
    if (FENCE_RE.test(line) || HEADING_RE.test(line) || HR_RE.test(line) || QUOTE_RE.test(line) || isTableStart(lines, i)) break;
    // 들여쓰지 않은 일반 줄 — 게으른 이어짐(마크다운 관례).
    cur.text += "\n" + line.trim();
    i++;
  }
  return { block: { type: "list", ordered, items }, next: i };
}

// ── 인라인 ────────────────────────────────────────────────────────────────

export type Inline =
  | { type: "text"; text: string }
  | { type: "br" }
  | { type: "code"; text: string }
  | { type: "strong"; children: Inline[] }
  | { type: "em"; children: Inline[] }
  | { type: "del"; children: Inline[] }
  | { type: "link"; href: string; children: Inline[] }
  | { type: "mention"; kind: MentionKind; id: string; name: string };

const LINK_HEAD_RE = /^\[([^\]\n]*)\]\(([^)\s]+)\)/;
/** `lib/mentions.ts` LINK_RE 와 같은 모양 — 문법을 둘로 갈라 두지 않는다. */
const MENTION_RE = /^\[@([^\]]+)\]\(mention:\/\/(agent|user|all)\/([0-9a-zA-Z-]+)\)/;
const SAFE_HREF_RE = /^https?:\/\/[^\s<>"']+$/i;
const WORD = /[\p{L}\p{N}_]/u;

function findClose(s: string, from: number, mark: string): number {
  let i = s.indexOf(mark, from);
  while (i >= 0) {
    // 여는 표시 바로 뒤·닫는 표시 바로 앞이 공백이면 강조가 아니다(`a * b * c`).
    if (i > from && !/\s/.test(s[i - 1])) return i;
    i = s.indexOf(mark, i + 1);
  }
  return -1;
}

export function parseInline(src: string): Inline[] {
  const out: Inline[] = [];
  let buf = "";
  const flush = () => { if (buf) { out.push({ type: "text", text: buf }); buf = ""; } };
  const s = src;
  let i = 0;
  while (i < s.length) {
    const c = s[i];

    if (c === "\\" && i + 1 < s.length && /[\\`*_{}\[\]()#+\-.!>~|]/.test(s[i + 1])) { buf += s[i + 1]; i += 2; continue; }

    if (c === "\n") { flush(); out.push({ type: "br" }); i++; continue; }

    if (c === "`") {
      let n = 1;
      while (s[i + n] === "`") n++;
      const mark = "`".repeat(n);
      const close = s.indexOf(mark, i + n);
      if (close >= 0) {
        flush();
        out.push({ type: "code", text: s.slice(i + n, close).replace(/\n/g, " ").trim() });
        i = close + n;
        continue;
      }
      buf += mark; i += n; continue;
    }

    if (c === "[") {
      const rest = s.slice(i);
      const mm = rest.match(MENTION_RE);
      if (mm) { flush(); out.push({ type: "mention", kind: mm[2] as MentionKind, id: mm[3], name: mm[1] }); i += mm[0].length; continue; }
      const lm = rest.match(LINK_HEAD_RE);
      if (lm && SAFE_HREF_RE.test(lm[2])) {
        flush();
        out.push({ type: "link", href: lm[2], children: parseInline(lm[1]) });
        i += lm[0].length;
        continue;
      }
      // 그 밖의 스킴(javascript: · mention 이 아닌 것)은 원문 그대로 — 아래에서 문자로 흘러간다.
    }

    if ((c === "*" || c === "_") && s[i + 1] === c && s[i + 2] && !/\s/.test(s[i + 2])) {
      const mark = c + c;
      const close = findClose(s, i + 2, mark);
      if (close >= 0 && (c === "*" || !WORD.test(s[close + 2] ?? ""))) {
        flush();
        out.push({ type: "strong", children: parseInline(s.slice(i + 2, close)) });
        i = close + 2;
        continue;
      }
    }

    if ((c === "*" || c === "_") && s[i + 1] && !/\s/.test(s[i + 1]) && s[i + 1] !== c) {
      // `_` 는 단어 경계에서만(snake_case 보호), `*` 는 어디서든.
      const wordBefore = i > 0 && WORD.test(s[i - 1]);
      if (c === "*" || !wordBefore) {
        const close = findClose(s, i + 1, c);
        if (close >= 0 && (c === "*" || !WORD.test(s[close + 1] ?? ""))) {
          flush();
          out.push({ type: "em", children: parseInline(s.slice(i + 1, close)) });
          i = close + 1;
          continue;
        }
      }
    }

    if (c === "~" && s[i + 1] === "~" && s[i + 2] && !/\s/.test(s[i + 2])) {
      const close = findClose(s, i + 2, "~~");
      if (close >= 0) {
        flush();
        out.push({ type: "del", children: parseInline(s.slice(i + 2, close)) });
        i = close + 2;
        continue;
      }
    }

    buf += c;
    i++;
  }
  flush();
  return out;
}

// ── React ────────────────────────────────────────────────────────────────

function renderInlines(nodes: Inline[]): ReactNode[] {
  return nodes.map((n, i) => {
    switch (n.type) {
      case "text": return n.text;
      case "br": return <br key={i} />;
      case "code": return <code key={i} className="md-code">{n.text}</code>;
      case "strong": return <strong key={i}>{renderInlines(n.children)}</strong>;
      case "em": return <em key={i}>{renderInlines(n.children)}</em>;
      case "del": return <del key={i}>{renderInlines(n.children)}</del>;
      case "link":
        return (
          <a key={i} className="md-link" href={n.href} target="_blank" rel="noopener noreferrer">
            {renderInlines(n.children)}
          </a>
        );
      case "mention":
        return (
          <span key={i} className="msg__mention" data-mention={`${n.kind}:${n.id}`}>
            @{n.name}
          </span>
        );
    }
  });
}

function renderList(l: { ordered: boolean; items: ListItem[] }, key: number | string): ReactNode {
  const Tag = l.ordered ? "ol" : "ul";
  return (
    <Tag key={key} className={l.ordered ? "md-ol" : "md-ul"}>
      {l.items.map((it, i) => (
        <li key={i} className="md-li">
          {renderInlines(parseInline(it.text))}
          {it.children && renderList(it.children, "c")}
        </li>
      ))}
    </Tag>
  );
}

function renderBlocks(blocks: Block[]): ReactNode[] {
  return blocks.map((b, i) => {
    switch (b.type) {
      case "p": return <p key={i} className="md-p">{renderInlines(parseInline(b.text))}</p>;
      case "h": {
        const Tag = `h${b.level}` as "h1" | "h2" | "h3";
        return <Tag key={i} className={`md-h md-h${b.level}`}>{renderInlines(parseInline(b.text))}</Tag>;
      }
      case "code":
        return (
          <pre key={i} className="md-pre" data-open={b.open ? "true" : undefined}><code>{b.text}</code></pre>
        );
      case "list": return renderList(b, i);
      case "quote": return <blockquote key={i} className="md-quote">{renderBlocks(b.blocks)}</blockquote>;
      case "table":
        return (
          <div key={i} className="md-table-wrap">
            <table className="md-table">
              <thead><tr>{b.head.map((c, j) => <th key={j}>{renderInlines(parseInline(c))}</th>)}</tr></thead>
              <tbody>{b.rows.map((r, j) => <tr key={j}>{r.map((c, k) => <td key={k}>{renderInlines(parseInline(c))}</td>)}</tr>)}</tbody>
            </table>
          </div>
        );
      case "hr": return <hr key={i} className="md-hr" />;
    }
  });
}

/** 인라인만 — 짧은 본문(HITL 질문 한 줄 등). 블록 문법은 문자 그대로 남는다. */
export function renderInline(src: string): ReactNode[] {
  return renderInlines(parseInline(src));
}

/**
 * 본문 렌더. `inline` 이면 블록 없이 한 줄(`<span>`), 아니면 블록 컨테이너(`<div class="md">`).
 * 빈 본문은 아무것도 그리지 않는다.
 */
export function Markdown({ content, inline = false, className }: { content: string; inline?: boolean; className?: string }) {
  if (inline) return <span className={className}>{renderInline(content)}</span>;
  return <div className={className ? `md ${className}` : "md"}>{renderBlocks(parseBlocks(content))}</div>;
}

export default Markdown;
