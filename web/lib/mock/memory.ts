/**
 * 목 — 미션 상태 원장(PRD FR-4.6 · openapi v0.3.12 태그 `memory`).
 *
 * op 넷: `listMemory`(GET /works/{id}/memory?kind=&status=) · `noteMemory`(POST /works/{id}/memory) · `supersedeMemory`(POST /memory/{id}/supersede) ·
 * `retireMemory`(POST /memory/{id}/retire). 저장은 이 모듈이 쥔다(`Store` 옆 WeakMap — `cards.ts` 와 같은 모양).
 * 서버(migration memory_item)가 하는 대로:
 *  · 쓰기는 새 행 추가뿐 — 옛 행은 상태 칸 셋(status · superseded_by · invalidated_at)만 바뀐다. 철회 사유는 content 와 따로 retire_reason 에(응답 칸, v0.3.13).
 *  · content 는 앞뒤 공백을 떼고 1..300자, 아니면 422. certainty 는 fact 만, outcome 은 lesson 만 남긴다(다른 kind 면 버린다).
 *  · lesson — 같은 미션의 active lesson 과 (뗀) 내용이 같으면 새 행 없이 200 으로 그 행 — 아직 기여하지 않은 작성자일 때만
 *    support_count +1 · last_reinforced_at 갱신(openapi v0.3.13).
 *  · plan — 미션당 active 1개: 새 plan 이 이전 active plan 을 대체한다(superseded_by = 새 항목, 새 항목의 supersedes = 이전).
 *  · supersede — 대상이 active 가 아니면 409 memory_not_active, 본문에 kind·work_id 가 있으면 422 kind_immutable.
 *    새 항목은 kind 와(lesson 이면) outcome·support_count 를 물려받는다.
 *  · retire — 사유 필수(떼고 1..300, 아니면 422) · status retired + invalidated_at. `status=all` 로는 계속 보인다.
 *  · promoted — lesson 은 support_count ≥2, 그 밖은 항상 true(읽을 때 계산).
 * 권한: 목은 사람 쿠키로 부른다(사람은 plan·progress 도 쓴다 — 403 memory_kind_forbidden 은 에이전트 역할 검사라 목에 없다). 방을 볼 수 있으면 읽고 쓴다.
 *
 * 시드 `POST /__mock/works/{id}/seed-memory` — 마리오 카트 미션의 원장(계획 2판 · 진행 · 사실 셋(하나는 대체됨) · 담당 · 열린 질문 ·
 * 교훈 둘(2번 겪음 · 아직 한 번) · 철회된 항목). `seed-cards` 도 끝에 같은 시드를 부른다. 응답 `{ work_id, items: {plan: id, …} }`.
 */
import type { MemoryCertainty, MemoryItem, MemoryKind, MemoryOutcome, User } from "@/lib/api/types";
import { store, uuid, type Store } from "./store";
import type { Req, Res } from "./handlers";
import { MEMORY_MOCK, MEMORY_SEED, VALIDATION_DETAIL, notFound } from "./wording";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface MemoryCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  requireUser: (s: Store, req: Req) => User;
  /** 미션이 있고 방을 볼 수 있는가(없으면 404 · 권한 없으면 403 을 던진다). */
  workGate: (s: Store, req: Req, workId: string) => void;
}

type Author = MemoryItem["created_by"];
type Stored = Omit<MemoryItem, "promoted"> & { seq: number; retire_reason: string | null; supporters: string[] };

const KINDS: readonly MemoryKind[] = ["fact", "assignment", "open_question", "lesson", "plan", "progress"];
const CERTAINTIES: readonly MemoryCertainty[] = ["given", "to_verify", "derived", "guess"];
const OUTCOMES: readonly MemoryOutcome[] = ["dead_end", "corrected", "useful"];
const STATUSES = new Set(["active", "superseded", "retired"]);
const MAX = 300;

const STATE = new WeakMap<Store, { items: Map<string, Stored>; seq: number }>();
const stateOf = (s: Store) => {
  let m = STATE.get(s);
  if (!m) STATE.set(s, (m = { items: new Map(), seq: 0 }));
  return m;
};

/** 계약 `MemoryItem` 모양 — 목 내부 칸(seq · supporters)을 떼고 promoted 를 계산한다. retire_reason·last_reinforced_at 은 v0.3.13 부터 응답 칸. */
export function toMemoryItem(it: Stored): MemoryItem {
  const { seq: _seq, supporters: _s, ...row } = it;
  return { ...row, promoted: it.kind === "lesson" ? it.support_count >= 2 : true };
}

/** 미션의 항목 — 오래된 것부터(같은 시각이면 넣은 순서). */
export function memoryOf(s: Store, workId: string): Stored[] {
  return [...stateOf(s).items.values()].filter((it) => it.work_id === workId).sort((a, b) => a.created_at.localeCompare(b.created_at) || a.seq - b.seq);
}

interface NoteInput {
  kind: MemoryKind;
  content: string;
  certainty?: MemoryCertainty | null;
  outcome?: MemoryOutcome | null;
  source_message_ids?: string[];
}

/** 쓰기 규칙 하나 — op 과 시드가 같이 탄다. `deduped` 면 새 행 없이 기존 lesson 의 support_count 를 올렸다. */
export function noteMemory(s: Store, workId: string, by: Author, input: NoteInput, at = new Date().toISOString()): { item: Stored; deduped: boolean } {
  const st = stateOf(s);
  const content = input.content.trim();
  if (input.kind === "lesson") {
    const same = memoryOf(s, workId).find((it) => it.kind === "lesson" && it.status === "active" && it.content === content);
    if (same) {
      // v0.3.13: 아직 기여하지 않은 작성자일 때만 +1 하고 last_reinforced_at 을 지금으로 — 같은 작성자의 재확인은 세지 않는다.
      if (!same.supporters.includes(by.id)) {
        same.supporters.push(by.id);
        same.support_count += 1;
        same.last_reinforced_at = at;
      }
      return { item: same, deduped: true };
    }
  }
  const item: Stored = {
    id: uuid(), work_id: workId, kind: input.kind, content,
    certainty: input.kind === "fact" ? input.certainty ?? null : null,
    outcome: input.kind === "lesson" ? input.outcome ?? null : null,
    support_count: input.kind === "lesson" ? 1 : 0,
    status: "active", supersedes: null, superseded_by: null, invalidated_at: null,
    source_message_ids: input.source_message_ids ?? [], created_by: by, created_at: at,
    seq: ++st.seq, retire_reason: null,
    supporters: input.kind === "lesson" ? [by.id] : [],
    last_reinforced_at: input.kind === "lesson" ? at : null,
  };
  if (input.kind === "plan") {
    // plan 은 미션당 active 1개 — 이전 active plan 을 이 항목이 대체한다.
    const prev = memoryOf(s, workId).find((it) => it.kind === "plan" && it.status === "active");
    if (prev) {
      prev.status = "superseded";
      prev.superseded_by = item.id;
      prev.invalidated_at = at;
      item.supersedes = prev.id;
    }
  }
  st.items.set(item.id, item);
  return { item, deduped: false };
}

/** 대체 — 대상은 active 여야 한다(호출자가 검사). 새 항목은 kind 와 lesson 의 outcome·support_count 를 물려받는다. */
export function supersedeMemory(s: Store, old: Stored, by: Author, body: { content: string; certainty?: MemoryCertainty | null; source_message_ids?: string[] }, at = new Date().toISOString()): Stored {
  const st = stateOf(s);
  const item: Stored = {
    id: uuid(), work_id: old.work_id, kind: old.kind, content: body.content.trim(),
    certainty: old.kind === "fact" ? (body.certainty !== undefined ? body.certainty : old.certainty ?? null) : null,
    outcome: old.kind === "lesson" ? old.outcome ?? null : null,
    support_count: old.kind === "lesson" ? old.support_count : 0,
    status: "active", supersedes: old.id, superseded_by: null, invalidated_at: null,
    source_message_ids: body.source_message_ids ?? [], created_by: by, created_at: at,
    seq: ++st.seq, retire_reason: null,
    supporters: old.kind === "lesson" ? [...old.supporters] : [],
    last_reinforced_at: old.kind === "lesson" ? at : null,
  };
  old.status = "superseded";
  old.superseded_by = item.id;
  old.invalidated_at = at;
  st.items.set(item.id, item);
  return item;
}

export function retireMemory(old: Stored, reason: string, at = new Date().toISOString()): Stored {
  old.status = "retired";
  old.invalidated_at = at;
  old.retire_reason = reason;
  return old;
}

/**
 * 시드 — 마리오 카트 미션의 원장. `authors` 는 [Lead, Researcher, Designer] 순(없으면 사람 하나로 채운다). 시각은 지금에서 거꾸로 분 단위.
 * 항목: 계획 2판(첫 판 대체됨) · 진행 · 사실 셋(16px → 32px 대체, 추측 하나) · 담당 · 열린 질문 · 교훈 둘(2번 겪음 · 아직 한 번) · 철회된 사실 하나.
 */
export function seedMemory(s: Store, workId: string, authors: Author[]): Record<string, string> {
  const [lead, researcher, designer] = [authors[0], authors[1] ?? authors[0], authors[2] ?? authors[0]];
  const base = Date.now() - 40 * 60000;
  let k = 0;
  const at = () => new Date(base + k++ * 3 * 60000).toISOString();
  const M = MEMORY_SEED;
  const ids: Record<string, string> = {};
  ids.plan_old = noteMemory(s, workId, lead, { kind: "plan", content: M.plan_old }, at()).item.id;
  ids.fact_old = noteMemory(s, workId, researcher, { kind: "fact", content: M.fact_old, certainty: "to_verify" }, at()).item.id;
  ids.fact_guess = noteMemory(s, workId, researcher, { kind: "fact", content: M.fact_guess, certainty: "guess" }, at()).item.id;
  ids.assignment = noteMemory(s, workId, lead, { kind: "assignment", content: M.assignment }, at()).item.id;
  ids.lesson_promoted = noteMemory(s, workId, designer, { kind: "lesson", content: M.lesson_promoted, outcome: "dead_end" }, at()).item.id;
  ids.retired = noteMemory(s, workId, designer, { kind: "fact", content: M.retired, certainty: "given" }, at()).item.id;
  ids.plan = noteMemory(s, workId, lead, { kind: "plan", content: M.plan }, at()).item.id;
  const st = stateOf(s).items;
  ids.fact_new = supersedeMemory(s, st.get(ids.fact_old)!, lead, { content: M.fact_new, certainty: "given" }, at()).id;
  // 같은 교훈 두 번째 — 다른 작성자여야 support_count 2(v0.3.13). Designer 가 없어 Researcher 로 채워졌으면 Lead 가 적는다.
  const second = [researcher, lead].find((x) => x.id !== designer.id) ?? researcher;
  noteMemory(s, workId, second, { kind: "lesson", content: M.lesson_promoted, outcome: "dead_end" }, at());
  ids.lesson_once = noteMemory(s, workId, designer, { kind: "lesson", content: M.lesson_once, outcome: "useful" }, at()).item.id;
  ids.open_question = noteMemory(s, workId, researcher, { kind: "open_question", content: M.open_question }, at()).item.id;
  retireMemory(st.get(ids.retired)!, M.retire_reason, at());
  ids.progress = noteMemory(s, workId, lead, { kind: "progress", content: M.progress }, at()).item.id;
  return ids;
}

export function registerMemory(ctx: MemoryCtx): void {
  const { on, Problem, requireUser, workGate } = ctx;
  const ok = (b: unknown, status = 200): Res => ({ status, body: b });
  const invalid = (field: string, code: string, message: string) => new Problem(422, "validation_failed", VALIDATION_DETAIL, { errors: [{ field, code, message }] });
  const authorOf = (u: User): Author => ({ kind: "user", id: u.id, name: u.display_name });
  /** content — 떼고 1..300자(글자 수는 코드 포인트로 — 한글이 바이트로 잘리지 않게). */
  const contentOf = (raw: unknown): string => {
    const c = typeof raw === "string" ? raw.trim() : "";
    if (!c) throw invalid("content", "required", MEMORY_MOCK.content_required);
    if ([...c].length > MAX) throw invalid("content", "too_long", MEMORY_MOCK.content_too_long);
    return c;
  };
  const itemOr404 = (s: Store, req: Req, id: string): Stored => {
    const it = stateOf(s).items.get(id);
    if (!it) throw new Problem(404, "not_found", notFound("memory"));
    workGate(s, req, it.work_id);
    return it;
  };

  on("GET", "/works/{id}/memory", (req, p) => {
    const s = store();
    workGate(s, req, p.id);
    const kind = req.query.get("kind");
    const status = req.query.get("status") ?? "active";
    if (kind && !KINDS.includes(kind as MemoryKind)) throw invalid("kind", "invalid", MEMORY_MOCK.kind_invalid);
    if (status !== "all" && !STATUSES.has(status)) throw invalid("status", "invalid", MEMORY_MOCK.status_invalid);
    const rows = memoryOf(s, p.id).filter((it) => (!kind || it.kind === kind) && (status === "all" || it.status === status));
    return ok(rows.map(toMemoryItem));
  });

  on("POST", "/works/{id}/memory", (req, p) => {
    const s = store();
    workGate(s, req, p.id);
    const u = requireUser(s, req);
    const b = (req.body ?? {}) as Partial<NoteInput>;
    if (!b.kind || !KINDS.includes(b.kind)) throw invalid("kind", "invalid", MEMORY_MOCK.kind_invalid);
    const content = contentOf(b.content);
    if (b.kind === "fact" && b.certainty != null && !CERTAINTIES.includes(b.certainty)) throw invalid("certainty", "invalid", MEMORY_MOCK.certainty_invalid);
    if (b.kind === "lesson" && b.outcome != null && !OUTCOMES.includes(b.outcome)) throw invalid("outcome", "invalid", MEMORY_MOCK.outcome_invalid);
    const r = noteMemory(s, p.id, authorOf(u), { kind: b.kind, content, certainty: b.certainty, outcome: b.outcome, source_message_ids: b.source_message_ids });
    return ok(toMemoryItem(r.item), r.deduped ? 200 : 201);
  });

  on("POST", "/memory/{id}/supersede", (req, p) => {
    const s = store();
    const u = requireUser(s, req);
    const b = (req.body ?? {}) as Record<string, unknown>;
    // 서버 순서(handlers_memory.go → memory.Supersede): kind·work_id(422 kind_immutable, 필드 오류도) → 대상(404) → 내용(422) → 확실도 → active(409).
    for (const k of ["kind", "work_id"]) {
      if (k in b) throw new Problem(422, "kind_immutable", MEMORY_MOCK.kind_immutable, { errors: [{ field: k, code: "kind_immutable", message: MEMORY_MOCK.kind_immutable }] });
    }
    const old = itemOr404(s, req, p.id);
    const content = contentOf(b.content);
    const certainty = b.certainty as MemoryCertainty | null | undefined;
    if (old.kind === "fact" && certainty != null && !CERTAINTIES.includes(certainty)) throw invalid("certainty", "invalid", MEMORY_MOCK.certainty_invalid);
    if (old.status !== "active") throw new Problem(409, "memory_not_active", MEMORY_MOCK.not_active);
    const item = supersedeMemory(s, old, authorOf(u), { content, certainty, source_message_ids: b.source_message_ids as string[] | undefined });
    return ok({ item: toMemoryItem(item), superseded: toMemoryItem(old) }, 201);
  });

  on("POST", "/memory/{id}/retire", (req, p) => {
    const s = store();
    requireUser(s, req);
    const old = itemOr404(s, req, p.id);
    // 서버 memory.Retire: 사유(떼고 1..300, 422) → active(409). 사유는 행 옆에 두고 응답에 싣지 않는다.
    const reason = String(((req.body ?? {}) as { reason?: unknown }).reason ?? "").trim();
    if (!reason) throw invalid("reason", "required", MEMORY_MOCK.reason_required);
    if ([...reason].length > MAX) throw invalid("reason", "too_long", MEMORY_MOCK.reason_too_long);
    if (old.status !== "active") throw new Problem(409, "memory_not_active", MEMORY_MOCK.not_active);
    return ok(toMemoryItem(retireMemory(old, reason)));
  });

  // ── 시드 ──
  on("POST", "/__mock/works/{id}/seed-memory", (req, p) => {
    const s = store();
    workGate(s, req, p.id);
    const u = requireUser(s, req);
    const agents = [...s.agents.values()];
    const by = (n: string): Author | null => {
      const a = agents.find((x) => x.name === n);
      return a ? { kind: "agent", id: a.id, name: a.name } : null;
    };
    const authors = [by("Lead") ?? authorOf(u), by("Researcher") ?? authorOf(u), by("Designer") ?? by("Researcher") ?? authorOf(u)];
    return ok({ work_id: p.id, items: seedMemory(s, p.id, authors) }, 201);
  });
}
