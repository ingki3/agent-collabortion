/**
 * 에이전트 메시지 세 층 — 대화 · 작업 내용 · 작업 과정(PRD FR-3.1.2 · SCREEN §4.6 「에이전트 메시지 카드」 · COMPONENTS §9.6, v0.19.3).
 *
 * 전부 순수 함수다 — 화면(`components/MessageLayers.tsx`)은 여기서 낸 값을 그리기만 한다.
 *
 * - **어느 메시지를 나누나** `isLayered` — 에이전트가 쓴 `text` 메시지만. 사람·시스템·요약·HITL·질문 카드(`blocked_q`)·답글(`answer`)은
 *   접지 않는다(사람의 말은 쓴 그대로, 나머지는 이미 짧거나 카드 모양이 따로 있다).
 * - **대화와 작업 내용** `messageLayers` — `detail` 이 있으면 에이전트가 나눈 것 그대로. 없고 본문이 1,200자 또는 25줄을 넘으면
 *   화면이 나눈다(폴백, `splitAuto`) — 저장은 바꾸지 않는다.
 * - **접힌 줄의 요약** `formatChars` · `countTables` · `firstLinePreview` · `summarizeProcess`.
 */
import { FENCE_RE, HEADING_RE, isTableStart, parseBlocks, type Block } from "@/lib/markdown";
import { foldEvents } from "@/components/ActivityRail";
import { isFailure, payloadOf } from "@/lib/feed";
import { MESSAGE_LAYERS as L, PROCESS_ACTION, type Slotted } from "@/lib/wording";
import type { Message, TaskEvent } from "@/lib/api/types";

/** 폴백 임계 — 본문이 이 글자 수 **또는** 줄 수를 넘으면 화면이 나눈다(FR-3.1.2). */
export const AUTO_FOLD_CHARS = 1200;
export const AUTO_FOLD_LINES = 25;
/** 폴백 때 대화로 남기는 첫 문단의 상한 — 400자(FR-3.1.2) · 5줄(대화 층 「5줄 안팎」). */
export const AUTO_HEAD_CHARS = 400;
export const AUTO_HEAD_LINES = 5;
/** 이보다 긴 작업 내용은 카드 안 스크롤에 더해 「새 창으로 보기」를 단다(SCREEN §4.6 표). */
export const DETAIL_WINDOW_CHARS = 10_000;
/** 접힌 줄의 첫 줄 미리보기 길이. */
export const PREVIEW_CHARS = 40;
/** 문장 경계로 자를 때 대화가 너무 짧아지지 않게 — 이보다 앞의 경계는 쓰지 않는다. */
const MIN_HEAD_CHARS = 80;

/** 타임라인 보기(대화만 / 작업 내용 펼침, COMPONENTS §9.7)의 저장 키 — 방마다 보는 사람의 브라우저에만. 서버에 두지 않는다. */
export const timelineViewKey = (roomId: string) => `colab.timelineView.${roomId}`;

/** 글자 수 — 코드 포인트 단위(한글·이모지 하나가 한 글자). */
export function charCount(s: string): number {
  return Array.from(s).length;
}

// ── 어느 메시지를 나누나 ─────────────────────────────────────────────────

/** 세 층으로 그리는 메시지인가 — 에이전트의 `text` 만(답글 `answer` 제외). */
export function isLayered(m: Pick<Message, "author_type" | "kind">, opts: { asAnswer?: boolean } = {}): boolean {
  return m.author_type === "agent" && m.kind === "text" && !opts.asAnswer;
}

// ── 폴백 — 화면이 나눈다 ─────────────────────────────────────────────────

export function lineCount(s: string): number {
  return s.replace(/\r\n?/g, "\n").split("\n").length;
}

/** 폴백 임계를 넘는가 — 1,200자 **또는** 25줄 초과. */
export function exceedsAutoFold(content: string): boolean {
  return charCount(content) > AUTO_FOLD_CHARS || lineCount(content) > AUTO_FOLD_LINES;
}

const isBlank = (l: string) => l.trim() === "";

/**
 * 400자 넘는 첫 문단의 자를 자리 — 앞 400자 안에서 **마지막 문장 끝**(`. ` `? ` `! ` `。` · 줄바꿈) 뒤. 그런 경계가 80자 앞에만 있으면
 * 마지막 공백에서 자르고 말줄임을 단다(없으면 400자에서). 마크다운 링크·멘션 `[…](…)` 한가운데는 자르지 않는다(그 링크 앞으로 당긴다).
 */
export function cutHead(text: string, max = AUTO_HEAD_CHARS): { head: string; rest: string; ellipsis: boolean } {
  if (text.length <= max) return { head: text, rest: "", ellipsis: false };
  const win = text.slice(0, max);
  let cut = -1;
  let ellipsis = false;
  for (const m of win.matchAll(/[.?!。](?=\s)|[.?!。]$|\n/g)) {
    const end = m.index! + m[0].length;
    if (end >= MIN_HEAD_CHARS) cut = end;
  }
  if (cut < 0) {
    const sp = win.lastIndexOf(" ");
    cut = sp >= MIN_HEAD_CHARS ? sp : max;
    ellipsis = true;
  }
  // 링크 한가운데면 링크 앞으로.
  for (const m of text.matchAll(/\[[^\]\n]*\]\([^)\n]*\)/g)) {
    const a = m.index!;
    const b = a + m[0].length;
    if (a < cut && cut < b) {
      cut = a;
      ellipsis = true;
    }
  }
  if (cut <= 0) return { head: "", rest: text, ellipsis: false };
  return { head: text.slice(0, cut).trimEnd(), rest: text.slice(cut).replace(/^[ \t]+/, ""), ellipsis };
}

/** 이 줄에서 표나 코드 블록이 시작하나 — 첫 문단은 여기서 멈춘다(표·코드 도중에서 자르지 않는다). */
function startsStructured(lines: string[], i: number): boolean {
  return FENCE_RE.test(lines[i]) || isTableStart(lines, i);
}

/**
 * 폴백 나누기 — `detail` 이 없는 긴 본문을 대화(첫 문단)와 나머지로. 임계를 넘지 않으면 `null`.
 *
 * 규칙(FR-3.1.2 「첫 문단(최대 400자)을 대화로 · 표·코드 블록 도중에서 자르지 않는다(그 앞 문단 경계로)」의 해석):
 * 1. 첫 문단 = 첫 비어 있지 않은 줄부터 빈 줄 전까지. **표·코드 펜스가 시작되는 줄 앞에서 멈춘다** — 빈 줄 없이 붙은 표도 대화에 들지 않는다.
 * 2. 첫 문단이 **제목 한 줄**뿐이면 그 다음 문단까지 대화로 본다(제목만 남은 대화는 아무것도 말하지 않는다).
 * 3. 첫 블록이 **표나 코드 블록**이면 대화로 보일 문단이 없다 — 대화 층은 비우고 본문 전부를 작업 내용으로 접는다
 *    (표를 잘라 앞머리만 보이면 틀린 표를 보이는 것이고, 통째로 보이면 폴백이 아무것도 접지 못한다).
 * 4. 첫 문단은 5줄에서 줄 경계로 자르고, 400자가 넘으면 `cutHead` 의 문장 경계로 자른다.
 * 5. 나머지가 비면(첫 문단이 곧 본문 전부) 나누지 않는다 — 접을 것이 없다.
 */
export function splitAuto(content: string): { conversation: string; rest: string } | null {
  if (!exceedsAutoFold(content)) return null;
  const lines = content.replace(/\r\n?/g, "\n").split("\n");
  let i = 0;
  while (i < lines.length && isBlank(lines[i])) i++;
  if (i >= lines.length) return null;
  if (startsStructured(lines, i)) return { conversation: "", rest: content.trim() };

  const para = (from: number): number => {
    let j = from;
    while (j < lines.length && !isBlank(lines[j]) && !(j > from && startsStructured(lines, j))) j++;
    return j;
  };
  let end = para(i);
  // 제목 한 줄뿐이면 다음 문단까지.
  if (end - i === 1 && HEADING_RE.test(lines[i])) {
    let k = end;
    while (k < lines.length && isBlank(lines[k])) k++;
    if (k < lines.length && !startsStructured(lines, k)) end = para(k);
  }
  let headLines = lines.slice(i, end);
  let restLines = lines.slice(end);
  if (headLines.filter((l) => !isBlank(l)).length > AUTO_HEAD_LINES) {
    // 줄 경계 — 비어 있지 않은 줄 5개까지(제목과 문단 사이 빈 줄은 센다에 넣지 않는다).
    let seen = 0;
    let k = 0;
    for (; k < headLines.length; k++) {
      if (!isBlank(headLines[k])) seen++;
      if (seen > AUTO_HEAD_LINES) break;
    }
    restLines = [...headLines.slice(k), ...restLines];
    headLines = headLines.slice(0, k);
  }
  const headText = headLines.join("\n");
  const cut = cutHead(headText);
  const conversation = (cut.head + (cut.ellipsis ? "…" : "")).trim();
  const rest = [cut.rest, ...restLines].join("\n").replace(/^\s*\n/, "").trim();
  if (!rest) return null;
  return { conversation, rest };
}

// ── 대화 / 작업 내용 ─────────────────────────────────────────────────────

export interface MessageLayerView {
  /** 대화 층 — 늘 보인다. 폴백이 첫 블록부터 표·코드면 빈 문자열. */
  conversation: string;
  /** 작업 내용 층 — 없으면 null(줄을 그리지 않는다). `auto` = 화면이 나눴다(「자동으로 접음」). */
  work: { text: string; auto: boolean } | null;
}

const cache = new WeakMap<object, MessageLayerView>();

/** 메시지 → 두 층. 같은 메시지 객체면 다시 계산하지 않는다(타임라인은 30초마다·SSE 마다 다시 그린다). */
export function messageLayers(m: Pick<Message, "content" | "detail">): MessageLayerView {
  const hit = cache.get(m);
  if (hit) return hit;
  let v: MessageLayerView;
  if (m.detail && m.detail.trim()) v = { conversation: m.content, work: { text: m.detail, auto: false } };
  else {
    const s = splitAuto(m.content);
    v = s ? { conversation: s.conversation, work: { text: s.rest, auto: true } } : { conversation: m.content, work: null };
  }
  cache.set(m, v);
  return v;
}

// ── 접힌 줄의 요약 ──────────────────────────────────────────────────────

/**
 * 글자 수 표기(SCREEN §4.6 「1,000 이상이면 1.5만 자처럼」의 해석): 1,000 미만은 「850자」, 1,000 이상 1만 미만은 천 단위 쉼표 「2,400자」,
 * 1만 이상은 만 단위 소수 한 자리 「1.5만 자」(.0 은 떼어 「2만 자」). 한국어는 만 단위로 끊어 읽으므로 「0.2만 자」는 쓰지 않는다 —
 * 「1.5만 자처럼」은 만 자리에 닿은 수의 모양을 든 예로 읽었다.
 */
export function formatChars(n: number): { text: Slotted; n: string } {
  if (n < 10_000) return { text: L.chars, n: n.toLocaleString("en-US") };
  const man = Math.round(n / 1000) / 10;
  return { text: L.chars_man, n: Number.isInteger(man) ? String(man) : man.toFixed(1) };
}

/** 마크다운 표 개수(인용 안의 표까지). 구분줄이 있는 것만 표다(렌더러와 같은 규칙). */
export function countTables(md: string): number {
  const walk = (bs: Block[]): number => bs.reduce((n, b) => n + (b.type === "table" ? 1 : b.type === "quote" ? walk(b.blocks) : 0), 0);
  return walk(parseBlocks(md));
}

/** 인라인 마크다운 표시를 걷어 낸 글 — 미리보기용. 멘션·링크는 글자만. */
function plain(s: string): string {
  return s
    .replace(/\[([^\]\n]*)\]\([^)\n]*\)/g, "$1")
    .replace(/(\*\*|__|~~|`)/g, "")
    .replace(/(^|\s)[*_](\S)/g, "$1$2")
    .replace(/(\S)[*_](?=\s|$)/g, "$1")
    .trim();
}

/** 첫 줄 미리보기 — 첫 비어 있지 않은 줄(코드 펜스·표 구분줄은 건너뛴다), 블록 표시를 걷고 40자에서 말줄임. */
export function firstLinePreview(md: string, max = PREVIEW_CHARS): string {
  const lines = md.replace(/\r\n?/g, "\n").split("\n");
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    if (isBlank(raw) || FENCE_RE.test(raw) || /^\s*\|?\s*:?-{2,}/.test(raw)) continue;
    let t = raw.replace(/^\s*(#{1,6}\s+|>\s?|[-*+]\s+|\d{1,9}[.)]\s+)/, "");
    if (t.includes("|") && isTableStart(lines, i)) t = t.split("|").map((c) => c.trim()).filter(Boolean).join(" · ");
    t = plain(t);
    if (!t) continue;
    const chars = Array.from(t);
    return chars.length > max ? chars.slice(0, max).join("") + "…" : t;
  }
  return "";
}

// ── 작업 과정 요약 ───────────────────────────────────────────────────────

export interface ProcessPart {
  text: Slotted;
  n: number | string;
}
export type ProcessSummary =
  | { state: "loading" }
  | { state: "unstructured" }
  | { state: "waiting" }
  | { state: "ready"; top: ProcessPart[]; duration: ProcessPart[]; failures: number };

/** 걸린 시간 — 1분 미만 「40초」, 1시간 미만 「4분」, 그 이상 「1시간 5분」(0분은 뗀다). */
export function formatDuration(ms: number): ProcessPart[] {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return [{ text: L.duration_sec, n: s }];
  const min = Math.round(s / 60);
  if (min < 60) return [{ text: L.duration_min, n: min }];
  const h = Math.floor(min / 60);
  const rest = min % 60;
  return rest ? [{ text: L.duration_hour, n: h }, { text: L.duration_min, n: rest }] : [{ text: L.duration_hour, n: h }];
}

/**
 * 작업 과정 한 줄 — 가장 많은 동작 2개와 수 · 걸린 시간 · 실패 수(SCREEN §4.6 표). 같은 툴 호출(started → ok/failed)은 한 번으로 센다
 * (`foldEvents` — 피드가 한 행으로 접는 것과 같다). 동률이면 먼저 나온 동작이 앞이다. 걸린 시간은 첫 이벤트 → 마지막 이벤트.
 */
export function summarizeProcess(ev: { events: TaskEvent[]; structured: boolean; loading: boolean } | undefined): ProcessSummary {
  if (!ev || (ev.loading && ev.events.length === 0)) return { state: "loading" };
  if (!ev.structured) return { state: "unstructured" };
  if (ev.events.length === 0) return { state: "waiting" };
  const rows = foldEvents(ev.events);
  const counts = new Map<string, { n: number; order: number; paths?: Set<string> }>();
  let failures = 0;
  rows.forEach(({ latest: e }, order) => {
    if (isFailure(e)) failures++;
    const key = `${e.class}/${e.verb ?? ""}`;
    if (!PROCESS_ACTION[key]) return;
    const c = counts.get(key) ?? { n: 0, order };
    if (key === "tool/edit_file") {
      const path = payloadOf(e).path ?? e.object_ref ?? e.id;
      c.paths = c.paths ?? new Set();
      c.paths.add(path);
      c.n = c.paths.size;
    } else c.n++;
    counts.set(key, c);
  });
  const top = [...counts.entries()]
    .sort((a, b) => b[1].n - a[1].n || a[1].order - b[1].order)
    .slice(0, 2)
    .map(([k, c]) => ({ text: PROCESS_ACTION[k], n: c.n }));
  const times = ev.events.map((e) => Date.parse(e.created_at)).filter((t) => !Number.isNaN(t));
  const span = times.length > 1 ? Math.max(...times) - Math.min(...times) : 0;
  return { state: "ready", top, duration: span > 0 ? formatDuration(span) : [], failures };
}
