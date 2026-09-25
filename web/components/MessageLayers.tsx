"use client";
/**
 * 에이전트 메시지 카드의 접힌 줄 둘(COMPONENTS §9.6 Fold Row) · 아티팩트 참조 줄 · 타임라인 머리 보기 전환(§9.7) — SCREEN §4.6 v0.19.3.
 *
 * **펼침 상태는 여기 두지 않는다.** 이 컴포넌트들은 `open`·`onToggle` 만 받는다 — 상태는 방 화면이 메시지 id 키 맵으로 들고 있어서
 * SSE 로 목록이 바뀌거나 카드가 다시 마운트돼도 같은 메시지의 펼침이 남는다(SCREEN §4.6 「펼침 상태는 스크롤·실시간 갱신에도 유지된다」).
 */
import type { ReactNode } from "react";
import "./message-layers.css";
import { Markdown } from "@/lib/markdown";
import { Slot } from "@/components/Slot";
import { DETAIL_WINDOW_CHARS, charCount, countTables, firstLinePreview, formatChars, type ProcessSummary } from "@/lib/message-layers";
import { MESSAGE_LAYERS as L } from "@/lib/wording";
import type { Artifact } from "@/lib/api/types";

/** 접힌 줄 한 개 — 글리프 · 굵은 라벨 · 요약(한 줄 말줄임) · 꼬리. 펼친 영역은 `regionId` 로 잇는다(`aria-controls`). */
function FoldRow({ label, open, onToggle, regionId, testId, children, tail }: {
  label: string; open: boolean; onToggle: () => void; regionId: string; testId: string; children?: ReactNode; tail?: ReactNode;
}) {
  return (
    <button type="button" className="fold" aria-expanded={open} aria-controls={regionId} onClick={onToggle} data-testid={testId} data-open={open ? "true" : "false"}>
      <span className="fold__glyph" aria-hidden="true">{open ? "▾" : "▸"}</span>
      <span className="fold__label">{label}</span>
      <span className="fold__sum">{children}</span>
      {tail}
    </button>
  );
}

/** 요약 조각을 가운뎃점으로 잇는다. */
function Dots({ parts }: { parts: ReactNode[] }) {
  return (
    <>
      {parts.map((p, i) => (
        <span key={i} className="fold__part">
          {i > 0 && <span aria-hidden="true">{" · "}</span>}
          {p}
        </span>
      ))}
    </>
  );
}

/** 긴 작업 내용을 새 창에 — 원문 마크다운을 글자 그대로(Blob URL). 라우트를 따로 두지 않는다. */
export function openDetailWindow(text: string): void {
  try {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    window.open(url, "_blank", "noopener");
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  } catch {
    /* 새 창이 막혀도 카드 안 스크롤로 다 읽을 수 있다 */
  }
}

/** 「작업 내용」 — 접힌 줄 + 펼친 마크다운(좌측 2px 세로줄, 60vh 안쪽 스크롤, 1만 자 넘으면 「새 창으로 보기」). */
export function DetailFold({ messageId, text, auto, open, onToggle }: { messageId: string; text: string; auto: boolean; open: boolean; onToggle: () => void }) {
  const regionId = `msg-detail-${messageId}`;
  const n = charCount(text);
  const chars = formatChars(n);
  const tables = countTables(text);
  const preview = firstLinePreview(text);
  const parts: ReactNode[] = [<Slot key="c" text={chars.text} n={chars.n} />];
  if (tables > 0) parts.push(<Slot key="t" text={L.tables} n={tables} />);
  if (preview) parts.push(<span key="p" data-testid="fold-preview">「{preview}」</span>);
  return (
    <div className="fold-wrap" data-testid="detail-layer" data-auto={auto ? "true" : "false"}>
      <FoldRow label={L.detail} open={open} onToggle={onToggle} regionId={regionId} testId="detail-fold">
        {auto && <span className="fold__auto" data-testid="fold-auto">{L.auto_folded}</span>}
        <Dots parts={parts} />
      </FoldRow>
      {open && (
        <div className="fold__region" id={regionId} data-testid="detail-body">
          {/* 본문만 안쪽 스크롤(60vh) — 「새 창으로 보기」는 스크롤 밖 아래에 둬 끝까지 내리지 않아도 보인다. */}
          <div className="fold__detail msg__body" data-testid="detail-scroll"><Markdown content={text} /></div>
          {n > DETAIL_WINDOW_CHARS && (
            <button type="button" className="msg__link fold__window" onClick={() => openDetailWindow(text)} data-testid="detail-window">
              {L.open_window}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/** 「작업 과정」 — 접힌 줄(많은 동작 2개 · 걸린 시간 · 실패는 접혀 있어도 빨강 꼬리) + 펼친 활동 피드. */
export function ProcessFold({ messageId, summary, open, onToggle, children }: {
  messageId: string; summary: ProcessSummary; open: boolean; onToggle: () => void; children?: ReactNode;
}) {
  const regionId = `msg-process-${messageId}`;
  let sum: ReactNode;
  let fails = 0;
  if (summary.state === "loading") sum = L.process_loading;
  else if (summary.state === "unstructured") sum = L.process_unstructured;
  else if (summary.state === "waiting") sum = L.process_waiting;
  else {
    fails = summary.failures;
    const parts: ReactNode[] = summary.top.map((p, i) => <Slot key={`a${i}`} text={p.text} n={p.n} />);
    if (parts.length === 0) parts.push(L.process_no_actions);
    if (summary.duration.length) parts.push(<span key="d">{summary.duration.map((d, i) => <span key={i}>{i > 0 ? " " : null}<Slot text={d.text} n={d.n} /></span>)}</span>);
    sum = <Dots parts={parts} />;
  }
  return (
    <div className="fold-wrap" data-testid="process-layer">
      <FoldRow
        label={L.process}
        open={open}
        onToggle={onToggle}
        regionId={regionId}
        testId="process-fold"
        tail={fails > 0 ? (
          <span className="fold__fail" data-testid="fold-fail">
            <span aria-hidden="true">{"· "}</span>
            <Slot text={L.failures} n={fails} />
          </span>
        ) : null}
      >
        {sum}
      </FoldRow>
      {open && <div className="fold__process" id={regionId} data-testid="process-body">{children}</div>}
    </div>
  );
}

/**
 * 「작업 중」 줄(T-FEED B · SCREEN §4.6 v0.19.6) — 턴이 도는 동안 **마지막 메시지 뒤**의 작업을 타임라인 맨 아래 한 줄로.
 * 「@Lead 작업 중 · 셸 명령 12회 · 파일 3개 편집 · 17분 ▸」, 펼치면 그 조각의 활동 피드. 에이전트마다 하나, 턴이 끝나면 부른 쪽이 그리지 않는다.
 * 요약 규칙은 「작업 과정」 접힌 줄과 같다(`summarizeProcess` 에 꼬리 조각).
 */
export function WorkingRow({ taskId, agentName, summary, open, onToggle, children }: {
  taskId: string; agentName: string; summary: ProcessSummary; open: boolean; onToggle: () => void; children?: ReactNode;
}) {
  const regionId = `working-${taskId}`;
  const parts: ReactNode[] = [];
  let fails = 0;
  if (summary.state === "ready") {
    fails = summary.failures;
    summary.top.forEach((p, i) => parts.push(<Slot key={`a${i}`} text={p.text} n={p.n} />));
    if (summary.duration.length) parts.push(<span key="d">{summary.duration.map((d, i) => <span key={i}>{i > 0 ? " " : null}<Slot text={d.text} n={d.n} /></span>)}</span>);
  }
  return (
    <div className="fold-wrap working" data-testid="working-row" data-task-id={taskId} aria-live="polite">
      <FoldRow
        label={`@${agentName} ${L.working}`}
        open={open}
        onToggle={onToggle}
        regionId={regionId}
        testId="working-fold"
        tail={fails > 0 ? (
          <span className="fold__fail" data-testid="fold-fail">
            <span aria-hidden="true">{"· "}</span>
            <Slot text={L.failures} n={fails} />
          </span>
        ) : null}
      >
        {parts.length > 0 ? <Dots parts={parts} /> : summary.state === "loading" ? L.process_loading : null}
      </FoldRow>
      {open && <div className="fold__process" id={regionId} data-testid="working-body">{children}</div>}
    </div>
  );
}

/** 아티팩트 참조 줄 — 📄 이름 · 아티팩트 · vN · 열기(§4.6 우열 아티팩트와 같은 칸). */
export function ArtifactRef({ artifact }: { artifact: Artifact }) {
  return (
    <div className="artref" data-testid="artifact-ref" data-artifact-id={artifact.id}>
      <span className="artref__icon" aria-hidden="true">📄</span>
      <span className="artref__name">{artifact.name}</span>
      <span className="artref__meta">
        {L.artifact}
        <span aria-hidden="true">{" · "}</span>
        <Slot text={L.artifact_version} n={artifact.version} />
      </span>
      <a className="artref__open" href={`/api/v1/artifacts/${artifact.id}/content`} target="_blank" rel="noopener noreferrer" data-testid="artifact-ref-open">
        {L.artifact_open}
      </a>
    </div>
  );
}

export type TimelineView = "conversation" | "detail";

/** 타임라인 머리 — 「보기 [대화만 | 작업 내용 펼침]」 두 칸 세그먼트(§9.7). 미션 칩 줄 아래 오른쪽. */
export function TimelineViewToggle({ value, onChange }: { value: TimelineView; onChange: (v: TimelineView) => void }) {
  const opt = (v: TimelineView, label: string) => (
    <button type="button" className={`seg__opt${value === v ? " seg__opt--on" : ""}`} aria-pressed={value === v} onClick={() => onChange(v)} data-testid={`view-${v}`}>
      {label}
    </button>
  );
  return (
    <div className="tlview" data-testid="timeline-view" data-view={value}>
      <span className="tlview__label" aria-hidden="true">{L.view_label}</span>
      <div className="seg" role="group" aria-label={L.view_group}>
        {opt("conversation", L.view_conversation)}
        {opt("detail", L.view_detail)}
      </div>
    </div>
  );
}
