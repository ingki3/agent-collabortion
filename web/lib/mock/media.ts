/**
 * 목 — 미디어 미리보기 · 파일 붙이기(PRD v0.19.10 FR-4.3.1 · FR-3.7 · openapi v0.3.7). 서버와 같은 규칙을 흉내 낸다:
 *
 *  - `POST /rooms/{id}/artifacts`(submitArtifact, multipart name·type·file) — **종류는 목이 판정한다**(첫 바이트 + 확장자,
 *    서버 `artifacts.DetectContentType` 의 축약). 올리는 쪽 Content-Type 은 믿지 않는다. 50MB 넘으면 413.
 *  - `GET /artifacts/{id}/content` — `?inline=true` 이고 미리보기 목록이면 inline, 늘 nosniff · CSP sandbox · 단일 Range(206/416).
 *    본문이 없는 옛 시드 아티팩트는 이름·버전 글(message-layers 가 하던 것).
 *  - 메시지 게시의 `attachment_ids` — 같은 방만(422 attachment_not_in_room), 중복 한 번, 최대 10 → `Message.attachments`(그 버전 그대로).
 *  - `POST /__mock/rooms/{id}/seed-media`(계약 밖) — 에이전트 턴 하나가 아티팩트(본문 base64)를 내고 말한다. 스크린샷용.
 *
 * handlers.ts 에는 등록 한 줄과 게시 핸들러의 `resolveAttachments` 호출만 둔다.
 */
import type { Artifact, Message } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";
import { store, uuid, type MockTask, type Store } from "./store";
import type { Req, Res } from "./handlers";

type Handler = (req: Req, params: Record<string, string>) => Res | Promise<Res>;
type ProblemCtor = new (status: number, code?: string, detail?: string, extra?: Record<string, unknown>) => Error;

export interface MediaCtx {
  on: (method: string, pattern: string, h: Handler) => void;
  Problem: ProblemCtor;
  sessionOf: (s: Store, req: Req, id: string) => Session;
  requireMember: (s: Store, req: Req, workspaceId: string) => { user: { id: string; display_name: string } };
  addMessage: (s: Store, sess: Session, m: Partial<Message> & Pick<Message, "author_type" | "author_id" | "kind" | "content" | "mentions">) => Message;
  createTask: (s: Store, sess: Session, agentId: string, triggerId: string | null, opts?: { brief?: string | null }) => MockTask;
  setLaneStatus: (s: Store, sess: Session, laneId: string, patch: Record<string, unknown>) => void;
  emit: (s: Store, workspaceId: string, type: "artifact.created", payload: unknown, sessionId?: string) => void;
  notFound: () => Error;
}

/** 목 저장소마다 본문 — resetStore 가 저장소를 새로 만들면 본문도 새로 시작한다. */
const bodies = new WeakMap<Store, Map<string, Uint8Array>>();
function bytesOf(s: Store): Map<string, Uint8Array> {
  let m = bodies.get(s);
  if (!m) bodies.set(s, (m = new Map()));
  return m;
}

const MAX = 50 * 1024 * 1024;
const PREVIEW = new Set(["image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "video/mp4", "video/webm", "audio/mpeg", "audio/wav", "audio/ogg", "audio/webm", "audio/mp4"]);

const starts = (b: Uint8Array, sig: number[], at = 0) => sig.every((v, i) => b[at + i] === v);
const ascii = (b: Uint8Array, at: number, n: number) => String.fromCharCode(...b.slice(at, at + n));

/** 서버 DetectContentType 의 축약 — 서명이 이름보다 먼저, SVG 는 텍스트일 때만 `.svg` 로, 미디어 확장자는 서명이 없을 때만. */
export function judgeContentType(name: string, b: Uint8Array): string {
  const ext = (name.toLowerCase().match(/\.[a-z0-9]+$/) ?? [""])[0];
  if (starts(b, [0x89, 0x50, 0x4e, 0x47])) return "image/png";
  if (starts(b, [0xff, 0xd8, 0xff])) return "image/jpeg";
  if (ascii(b, 0, 4) === "GIF8") return "image/gif";
  if (ascii(b, 0, 4) === "RIFF" && ascii(b, 8, 4) === "WEBP") return "image/webp";
  if (ascii(b, 0, 4) === "RIFF" && ascii(b, 8, 4) === "WAVE") return "audio/wav";
  if (ascii(b, 0, 3) === "ID3" || (b[0] === 0xff && (b[1] & 0xe0) === 0xe0)) return "audio/mpeg";
  if (ascii(b, 0, 4) === "OggS") return "audio/ogg";
  if (starts(b, [0x1a, 0x45, 0xdf, 0xa3])) return ext === ".weba" ? "audio/webm" : "video/webm";
  if (ascii(b, 4, 4) === "ftyp") return ext === ".m4a" ? "audio/mp4" : "video/mp4";
  const head = new TextDecoder().decode(b.slice(0, 512)).trimStart().toLowerCase();
  if (/^<(!doctype html|html|head|script|body|iframe|h1|div|table|a |style|title|b>|font|br|p>|p )/.test(head)) return "text/html; charset=utf-8";
  const binary = b.slice(0, 512).some((c) => c < 9 || (c > 13 && c < 32));
  if (!binary) {
    if (ext === ".svg" && (head.startsWith("<?xml") || head.startsWith("<svg"))) return "image/svg+xml";
    if (head.startsWith("<?xml")) return "text/xml; charset=utf-8";
    return "text/plain; charset=utf-8";
  }
  return "application/octet-stream";
}

/** 게시의 `attachment_ids` → AttachmentRef[] (서버 router.NormalizeAttachments + attach). */
export function resolveAttachments(s: Store, sessionId: string, ids: string[] | undefined, Problem: ProblemCtor): NonNullable<Message["attachments"]> {
  if (!ids?.length) return [];
  const uniq = [...new Set(ids)];
  if (uniq.length > 10) throw new Problem(422, "validation_failed", "입력값을 확인해 주세요", { errors: [{ field: "attachment_ids", code: "too_many_attachments", message: `파일은 한 메시지에 10개까지 붙일 수 있습니다 — ${uniq.length}개를 보냈습니다` }] });
  return uniq.map((id) => {
    const a = s.artifacts.get(id);
    if (!a || a.session_id !== sessionId) throw new Problem(422, "validation_failed", "입력값을 확인해 주세요", { errors: [{ field: "attachment_ids", code: "attachment_not_in_room", message: `이 방의 파일이 아닙니다 (${id}) — 이 방에 올린 파일만 붙일 수 있습니다` }] });
    return { artifact_id: a.id, name: a.name, version: a.version, type: a.type, content_type: a.content_type ?? null, size_bytes: a.size_bytes ?? 0 };
  });
}

/** route.ts 가 multipart 를 이 모양으로 넘긴다. */
export interface MultipartBody {
  __multipart: true;
  fields: Record<string, string>;
  file?: { name: string; type: string; bytes: Uint8Array };
}

function b64(s: string): Uint8Array {
  const bin = typeof atob === "function" ? atob(s) : Buffer.from(s, "base64").toString("binary");
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/**
 * storeArtifact — 목 저장소에 아티팩트 하나(같은 이름이면 다음 버전, FR-4.3). 종류는 **바이트로 판정**한다
 * (judgeContentType) — 시드든 업로드든 같은 판정을 지나야 화면이 서버에서 안 되는 것을 그리지 않는다.
 */
export function storeArtifact(s: Store, sess: Session, name: string, type: string, bytes: Uint8Array, by: { agentId?: string; agentName?: string; taskId?: string | null }): Artifact {
  const prev = [...s.artifacts.values()].filter((a) => a.session_id === sess.id && a.name === name);
  prev.forEach((a) => (a.latest = false));
  const a: Artifact = {
    id: uuid(), session_id: sess.id, name, version: prev.length + 1, type, storage_ref: `mock://${type}/${name}`,
    size_bytes: bytes.length, content_type: judgeContentType(name, bytes), submitted_by_task_id: by.taskId ?? null,
    submitted_by: by.agentId ? { agent_id: by.agentId, agent_name: by.agentName ?? "agent" } : undefined,
    description: null, latest: true, created_at: new Date().toISOString(), work_id: null,
  };
  s.artifacts.set(a.id, a);
  bytesOf(s).set(a.id, bytes);
  return a;
}

/** b64 본문으로 아티팩트 하나 — 시드 전용(parts-seed 등). */
export function seedArtifactFromB64(s: Store, sess: Session, name: string, b64s: string): string {
  return storeArtifact(s, sess, name, "file", b64(b64s), {}).id;
}

export function registerMedia(ctx: MediaCtx): void {
  const { on, Problem, sessionOf, requireMember, addMessage, createTask, setLaneStatus, emit } = ctx;
  const ok = (b: unknown, status = 200): Res => ({ status, body: b });

  const store1 = (s: Store, sess: Session, name: string, type: string, bytes: Uint8Array, by: { agentId?: string; agentName?: string; taskId?: string | null }): Artifact => {
    const a = storeArtifact(s, sess, name, type, bytes, by);
    emit(s, sess.workspace_id, "artifact.created", a, sess.id);
    return a;
  };

  on("POST", "/rooms/{id}/artifacts", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const b = req.body as MultipartBody | undefined;
    if (!b || !b.__multipart) throw new Problem(422, "validation_failed", "입력값을 확인해 주세요", { errors: [{ field: "body", code: "unsupported_media_type", message: "아티팩트는 multipart/form-data 로 name · type · file · description 을 보내야 합니다" }] });
    if (!b.file) throw new Problem(422, "validation_failed", "입력값을 확인해 주세요", { errors: [{ field: "file", code: "required", message: "파일을 첨부해 주세요" }] });
    if (b.file.bytes.length > MAX) throw new Problem(413, "payload_too_large", "파일이 너무 큽니다 — 상한은 52428800바이트(50 MB)입니다");
    const name = (b.fields.name ?? b.file.name).trim();
    const type = (b.fields.type ?? "file").trim();
    const a = store1(s, sess, name, type, b.file.bytes, {});
    return ok({ artifact: a, completion_progress: sess.completion_progress ?? null }, 201);
  });

  on("GET", "/artifacts/{id}/content", (req, p) => {
    const s = store();
    const a = s.artifacts.get(p.id);
    if (!a) throw ctx.notFound();
    const sess = s.sessions.get(a.session_id);
    if (sess) requireMember(s, req, sess.workspace_id);
    const bytes = bytesOf(s).get(a.id) ?? new TextEncoder().encode(`${a.name} v${a.version}\n\n(목 본문 — ${a.storage_ref})\n`);
    const ct = a.content_type ?? "text/plain; charset=utf-8";
    const inline = req.query.get("inline") === "true" && PREVIEW.has(ct.split(";")[0].trim());
    const headers: Record<string, string> = {
      "Content-Type": ct, "Content-Disposition": `${inline ? "inline" : "attachment"}; filename="${encodeURIComponent(a.name)}"`,
      "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "sandbox", "Accept-Ranges": "bytes",
    };
    let slice = bytes;
    let status = 200;
    const range = req.headers.get("range");
    if (range) {
      const m = /^bytes=(\d*)-(\d*)$/.exec(range.trim());
      const size = bytes.length;
      let st = -1, en = size - 1;
      if (m && !range.includes(",")) {
        if (m[1] === "" && m[2] !== "") { const n = Math.min(size, Number(m[2])); st = size - n; }
        else if (m[1] !== "") { st = Number(m[1]); if (m[2] !== "") en = Math.min(en, Number(m[2])); }
      }
      if (st < 0 || st >= size || en < st) return { status: 416, headers: { ...headers, "Content-Range": `bytes */${size}` }, body: { type: "https://colab.dev/problems/range_not_satisfiable", title: "Range Not Satisfiable", status: 416, code: "range_not_satisfiable" } };
      slice = bytes.slice(st, en + 1);
      status = 206;
      headers["Content-Range"] = `bytes ${st}-${en}/${size}`;
    }
    headers["Content-Length"] = String(slice.length);
    const stream = new ReadableStream<Uint8Array>({ start(c) { c.enqueue(slice); c.close(); } });
    return { status, stream, headers };
  });

  /** 에이전트 턴 하나 — 파일(base64)을 아티팩트로 내고 그 턴의 메시지 한 줄. `attach: true` 면 메시지 첨부로도 건다(`--attach`). */
  on("POST", "/__mock/rooms/{id}/seed-media", (req, p) => {
    const s = store();
    const sess = sessionOf(s, req, p.id);
    const b = req.body as { agent?: string; content?: string; attach?: boolean; files: { name: string; b64: string; type?: string }[] };
    const agent = [...s.agents.values()].find((a) => a.name === b.agent) ?? s.agents.get(sess.participants?.[0]?.agent_id ?? "");
    if (!agent) throw new Problem(409, "no_agent", "참여 에이전트가 없습니다");
    const task = createTask(s, sess, agent.id, null, { brief: null });
    task.status = "completed";
    const made = b.files.map((f) => store1(s, sess, f.name, f.type ?? "file", b64(f.b64), { agentId: agent.id, agentName: agent.name, taskId: task.id }));
    setLaneStatus(s, sess, task.lane_id, { status: "done", current_activity: null, finished_at: new Date().toISOString(), brief: null });
    const msg = addMessage(s, sess, {
      author_type: "agent", author_id: agent.id, author: { name: agent.name, avatar_url: null, role: agent.role }, kind: "text",
      content: b.content ?? "시안 올렸습니다.", mentions: [], source_task_id: task.id, lane_id: task.lane_id,
      attachments: b.attach ? made.map((a) => ({ artifact_id: a.id, name: a.name, version: a.version, type: a.type, content_type: a.content_type ?? null, size_bytes: a.size_bytes ?? 0 })) : [],
    });
    return ok({ message_id: msg.id, artifacts: made }, 201);
  });
}
