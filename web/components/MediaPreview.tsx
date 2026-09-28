"use client";
/**
 * Media Preview(COMPONENTS §9.12 · SCREEN v0.19.12 §4.6 「미디어 미리보기」 · PRD FR-4.3.1) — 아티팩트 카드의 본문 자리.
 * 타임라인 아티팩트 카드 · 우열 아티팩트 목록 · 메시지 첨부 카드가 **같은 컴포넌트**를 쓴다.
 *
 *  - 종류는 **서버가 판정한 `content_type`** 만 본다(`lib/media.ts mediaKind`) — 이름으로 추측하지 않는다.
 *  - 이미지 = `<img>` 썸네일(누르면 라이트박스). SVG 도 `<img>` 로만 그린다(문서로 열면 스크립트가 돌 수 있다).
 *  - 영상 = `<video controls preload="metadata">`(자동 재생 없음), 소리 = `<audio controls preload="metadata">` + 이름·길이.
 *  - 못 불러오면(권한·삭제·형식 불일치) 「미리보기를 불러올 수 없습니다 — 내려받아 여세요」 + 파일 카드로 떨어진다.
 *  - 카드 머리줄(글리프 · 이름 · vN · 크기 · 열기/내려받기)은 종류와 무관하게 위에 그대로.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import "./media-preview.css";
import { MEDIA } from "@/lib/wording";
import { downloadUrl, formatBytes, inlineUrl, kindGlyph, mediaKind, type MediaItem } from "@/lib/media";

function durationText(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "";
  const s = Math.round(sec);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** 카드 머리줄 — 그 밖 종류는 「열기」(새 창, 서버가 attachment·CSP sandbox 로 준다), 미디어는 「내려받기」. */
function CardHead({ item, kind }: { item: MediaItem; kind: ReturnType<typeof mediaKind> }) {
  return (
    <div className="media__head" data-testid="media-head">
      <span className="media__glyph" aria-hidden="true">{kindGlyph(kind)}</span>
      <span className="media__name" title={item.name}>{item.name}</span>
      <span className="media__meta">
        v{item.version}
        <span aria-hidden="true">{" · "}</span>
        {formatBytes(item.size_bytes)}
      </span>
      <a className="media__open" href={downloadUrl(item.id)} target="_blank" rel="noopener noreferrer" data-testid="media-open" download={kind === "file" ? undefined : item.name}>
        {kind === "file" ? MEDIA.open : MEDIA.download}
      </a>
    </div>
  );
}

export interface MediaPreviewProps {
  item: MediaItem;
  /** 이미지를 누르면 — 부른 쪽이 라이트박스를 연다(같은 메시지의 다른 이미지로 ←→ 하려면 목록이 부른 쪽에 있다). */
  onOpenImage?: (item: MediaItem) => void;
  /** 머리줄 없이 본문만(우열 목록처럼 이름 줄이 이미 있는 곳). */
  bare?: boolean;
}

export function MediaPreview({ item, onOpenImage, bare }: MediaPreviewProps) {
  const kind = mediaKind(item.content_type);
  const [failed, setFailed] = useState(false);
  const [duration, setDuration] = useState("");
  const src = inlineUrl(item.id);
  let body: React.ReactNode = null;
  if (failed) {
    body = <p className="media__fail" role="note" data-testid="media-failed">{MEDIA.preview_failed}</p>;
  } else if (kind === "image") {
    body = (
      <button type="button" className="media__thumb" onClick={() => onOpenImage?.(item)} aria-label={MEDIA.enlarge(item.name)} data-testid="media-image-open">
        {/* eslint-disable-next-line @next/next/no-img-element -- 인증 쿠키가 드는 같은 오리진 본문; next/image 최적화 경로를 타면 안 된다 */}
        <img src={src} alt={item.name} loading="lazy" onError={() => setFailed(true)} data-testid="media-image" />
      </button>
    );
  } else if (kind === "video") {
    body = (
      <video className="media__video" controls preload="metadata" src={src} aria-label={MEDIA.play(item.name)} onError={() => setFailed(true)} data-testid="media-video" />
    );
  } else if (kind === "audio") {
    body = (
      <div className="media__audio">
        <audio controls preload="metadata" src={src} aria-label={MEDIA.play(item.name)} onError={() => setFailed(true)} onLoadedMetadata={(e) => setDuration(durationText((e.target as HTMLAudioElement).duration))} data-testid="media-audio" />
        <span className="media__audio-meta" data-testid="media-audio-meta">
          {item.name}
          {duration && <><span aria-hidden="true">{" · "}</span>{duration}</>}
        </span>
      </div>
    );
  }
  return (
    <div className="media" data-testid="media-preview" data-kind={failed ? "failed" : kind} data-artifact-id={item.id}>
      {!bare && <CardHead item={item} kind={kind} />}
      {body}
    </div>
  );
}

/**
 * 라이트박스(§9.12) — 덮개 rgba(0,0,0,.8), 이미지 90vw×90vh, 오른쪽 위 ✕(44px), ← → (같은 묶음의 이미지 여럿), 아래 이름·버전·「내려받기」.
 * Esc·바깥 누르면 닫힘. 열리면 ✕ 에 초점, 닫히면 연 버튼으로 돌아간다(부른 쪽이 초점 복귀를 맡는다 — 여기서는 `onClose`).
 */
export function Lightbox({ items, index, onIndex, onClose }: { items: MediaItem[]; index: number; onIndex: (i: number) => void; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null);
  const item = items[index];
  const many = items.length > 1;
  const go = useCallback((d: number) => onIndex((index + d + items.length) % items.length), [index, items.length, onIndex]);
  useEffect(() => {
    closeRef.current?.focus();
  }, []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (many && e.key === "ArrowRight") {
        e.preventDefault();
        go(1);
      } else if (many && e.key === "ArrowLeft") {
        e.preventDefault();
        go(-1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go, many, onClose]);
  if (!item) return null;
  return (
    <div className="lbox" role="dialog" aria-modal="true" aria-label={`${MEDIA.lightbox} — ${item.name}`} data-testid="lightbox" onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <button ref={closeRef} type="button" className="lbox__close" aria-label={MEDIA.lightbox_close} onClick={onClose} data-testid="lightbox-close">✕</button>
      {many && <button type="button" className="lbox__nav lbox__nav--prev" aria-label={MEDIA.lightbox_prev} onClick={() => go(-1)} data-testid="lightbox-prev">←</button>}
      {/* eslint-disable-next-line @next/next/no-img-element -- 같은 오리진 인증 본문 */}
      <img className="lbox__img" src={inlineUrl(item.id)} alt={item.name} data-testid="lightbox-image" />
      {many && <button type="button" className="lbox__nav lbox__nav--next" aria-label={MEDIA.lightbox_next} onClick={() => go(1)} data-testid="lightbox-next">→</button>}
      <div className="lbox__foot" data-testid="lightbox-foot">
        <span className="lbox__name">{item.name}</span>
        <span className="lbox__meta">v{item.version}{many ? ` · ${index + 1}/${items.length}` : ""}</span>
        <a className="lbox__dl" href={downloadUrl(item.id)} download={item.name} data-testid="lightbox-download">{MEDIA.download}</a>
      </div>
    </div>
  );
}

/**
 * 한 묶음(메시지 하나의 첨부 · 한 턴의 아티팩트)의 미리보기 목록 + 그 묶음 이미지끼리 ←→ 하는 라이트박스.
 * 라이트박스를 닫으면 연 썸네일로 초점을 돌려준다.
 */
export function MediaGroup({ items, label, testId = "media-group", bare }: { items: MediaItem[]; label?: string; testId?: string; bare?: boolean }) {
  const images = items.filter((i) => mediaKind(i.content_type) === "image");
  const [open, setOpen] = useState<number | null>(null);
  const opener = useRef<HTMLElement | null>(null);
  if (items.length === 0) return null;
  const close = () => {
    setOpen(null);
    const el = opener.current;
    if (el) setTimeout(() => el.focus(), 0);
  };
  return (
    <div className="media-group" role="group" aria-label={label} data-testid={testId}>
      {items.map((it) => (
        <MediaPreview
          key={it.id}
          item={it}
          bare={bare}
          onOpenImage={(x) => {
            opener.current = document.activeElement as HTMLElement | null;
            setOpen(images.findIndex((i) => i.id === x.id));
          }}
        />
      ))}
      {open !== null && open >= 0 && <Lightbox items={images} index={open} onIndex={setOpen} onClose={close} />}
    </div>
  );
}
