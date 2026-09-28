/**
 * 미디어 미리보기 · 파일 붙이기의 순수 규칙(PRD v0.19.10 FR-4.3.1 · FR-3.7 · SCREEN v0.19.12 §4.6 · COMPONENTS §9.12).
 *
 * **종류는 서버가 판정한 `content_type` 만 본다**(openapi v0.3.7 Artifact.content_type) — 파일 이름·확장자로 추측하지 않는다.
 * 미리보기 목록은 서버 `artifacts.Previewable` 과 같은 표다: 목록 밖(HTML 포함)은 파일 카드.
 */
import { API_BASE } from "@/lib/api/client";

export type MediaKind = "image" | "video" | "audio" | "file";

const IMAGE = new Set(["image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml"]);
const VIDEO = new Set(["video/mp4", "video/webm"]);
const AUDIO = new Set(["audio/mpeg", "audio/wav", "audio/ogg", "audio/webm", "audio/mp4"]);

/** 서버 판정 content_type → 카드 모양. 파라미터(`; charset=…`)·대소문자는 무시한다. null 이면(판정 전 옛 행) 파일 카드. */
export function mediaKind(contentType: string | null | undefined): MediaKind {
  const base = (contentType ?? "").split(";")[0].trim().toLowerCase();
  if (IMAGE.has(base)) return "image";
  if (VIDEO.has(base)) return "video";
  if (AUDIO.has(base)) return "audio";
  return "file";
}

/** 칩·파일 카드의 종류 글리프(SCREEN §4.6 「파일 붙이기」 표). */
export function kindGlyph(k: MediaKind): string {
  return k === "image" ? "🖼" : k === "video" ? "🎞" : k === "audio" ? "🎵" : "📄";
}

/** 미리보기 주소 — `?inline=true`(서버가 목록 밖이면 무시하고 attachment 로 준다). */
export function inlineUrl(artifactId: string): string {
  return `${API_BASE}/artifacts/${encodeURIComponent(artifactId)}/content?inline=true`;
}
/** 내려받기 주소. */
export function downloadUrl(artifactId: string): string {
  return `${API_BASE}/artifacts/${encodeURIComponent(artifactId)}/content`;
}

/** 사람이 읽는 크기 — 3.1MB · 820KB · 12B. */
export function formatBytes(n: number | null | undefined): string {
  const v = n ?? 0;
  if (v >= 1024 * 1024) return `${(v / (1024 * 1024)).toFixed(1)}MB`;
  if (v >= 1024) return `${Math.round(v / 1024)}KB`;
  return `${v}B`;
}

/** 한 메시지 첨부 한도(openapi MessageCreate.attachment_ids maxItems) · 파일당 상한(submitArtifact 413). */
export const MAX_ATTACHMENTS = 10;
export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024;

/** 미리보기가 그리는 아티팩트의 최소 모양 — Artifact 와 AttachmentRef 가 둘 다 맞는다. */
export interface MediaItem {
  id: string;
  name: string;
  version: number;
  content_type?: string | null;
  size_bytes?: number | null;
}

/** AttachmentRef(artifact_id) → MediaItem. */
export function fromAttachmentRef(a: { artifact_id: string; name: string; version: number; content_type?: string | null; size_bytes: number }): MediaItem {
  return { id: a.artifact_id, name: a.name, version: a.version, content_type: a.content_type ?? null, size_bytes: a.size_bytes };
}

/** 첨부만 보낼 때 본문 자리(SCREEN §4.6 「첨부만」). */
export function attachmentsOnlyContent(n: number): string {
  return `(파일 ${n}개)`;
}

export interface UploadResult {
  id: string;
  name: string;
  version: number;
  content_type: string | null;
  size_bytes: number;
}

/**
 * 파일 하나를 사람 아티팩트(type `attachment`)로 올린다 — `submitArtifact` multipart(name · type · file). 진행률이 필요해서
 * `fetch` 가 아니라 XHR 이다(fetch 는 올리기 진행을 주지 않는다). 실패는 서버 Problem.detail 을 담은 Error.
 */
export function uploadAttachment(roomId: string, file: File, onProgress: (ratio: number) => void, signal?: AbortSignal): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append("name", file.name || "pasted.png");
    fd.append("type", "attachment");
    fd.append("file", file, file.name || "pasted.png");
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_BASE}/rooms/${encodeURIComponent(roomId)}/artifacts`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json, application/problem+json");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(Math.min(1, e.loaded / e.total));
    };
    xhr.onload = () => {
      let j: { artifact?: { id: string; name: string; version: number; content_type?: string | null; size_bytes?: number }; detail?: string; title?: string } = {};
      try {
        j = JSON.parse(xhr.responseText || "{}");
      } catch {
        /* 본문이 JSON 이 아니면 아래 문장 */
      }
      if (xhr.status >= 200 && xhr.status < 300 && j.artifact) {
        onProgress(1);
        resolve({ id: j.artifact.id, name: j.artifact.name, version: j.artifact.version, content_type: j.artifact.content_type ?? null, size_bytes: j.artifact.size_bytes ?? file.size });
      } else reject(new Error(j.detail ?? j.title ?? `올리지 못했습니다 (${xhr.status})`));
    };
    xhr.onerror = () => reject(new Error("올리지 못했습니다 — 연결을 확인하세요"));
    xhr.onabort = () => reject(new Error("올리기를 취소했습니다"));
    signal?.addEventListener("abort", () => xhr.abort());
    xhr.send(fd);
  });
}
