"use client";
/**
 * 활동 피드(COMPONENTS §2.8 Activity Feed · SCREEN §4.5) — **가공 렌더러**.
 * 원본 레일(`ActivityRail`)이 task_event 를 그대로 흘린다면, 여기는 `x-render-class` 5클래스로 가공한다.
 *
 * - 항목마다 한 문장. 진행 중 항목은 **제자리 갱신**(같은 `tool_call_id` 를 한 행으로 접는다) — 상태 줄을 쌓지 않는다.
 * - 실패는 크게(`data-outcome="failed"`), 조회(read·search)는 조용하게.
 * - 침묵·유휴는 "대기 중…", 응답 없음도 렌더한다(FR-7.2 "침묵도 렌더한다").
 * - 원본 레일 토글로 가공 없는 출력을 본다.
 * - 런타임이 구조화 이벤트를 주지 않으면(`structured=false`) 그 사실을 명시하고 레일만 남긴다(SCREEN §1 원칙 5).
 */
import { useState } from "react";
import "./activity-feed.css";
import { ActivityRail, foldEvents } from "./ActivityRail";
import { feedSentence, isFailure, payloadOf, renderClassWithCut, type RenderClass } from "@/lib/feed";
import { clockTime } from "@/lib/time";
import type { TaskEvent } from "@/lib/api/types";

/** 클래스별 글리프 — ● 동작(플랫폼 조작·편집·셸), ○ 턴 생명주기(COMPONENTS §2.8 K6). 아이콘은 결과로 바뀌지 않는다. */
const GLYPH: Record<RenderClass, string> = {
  message: "○",
  platform: "●",
  file_edit: "●",
  shell: "●",
  error: "✕",
  raw: "○",
};

export interface ActivityFeedProps {
  events: TaskEvent[];
  /** 런타임이 구조화 이벤트를 주는가(FR-7.2 강등). */
  structured?: boolean;
  loading?: boolean;
  /** 컷 1 — file_edit·shell 렌더러를 raw 로 강등(x-render-class.cut_1). */
  cut1?: boolean;
  /** run 제목 줄(예 "run · Claude Code · resume"). */
  title?: string;
  limit?: number;
}

function Detail({ cls, e }: { cls: RenderClass; e: TaskEvent }) {
  const p = payloadOf(e);
  switch (cls) {
    case "file_edit": {
      // 한 줄 요약은 **경로 + diff 규모**다(U6 4단계 "파일 편집 카드(경로·+/-줄)"). 경로 없이 "+12 -3" 만
      // 남으면 무엇을 고쳤는지 모르고, 문장(`feedSentence`)이 `object_ref` 를 못 받은 이벤트에서는 그 칸도 빈다.
      const d = [p.lines_added ? `+${p.lines_added}` : null, p.lines_removed ? `-${p.lines_removed}` : null].filter(Boolean).join(" ");
      return (
        <>
          {p.path && <code className="feed__path" data-testid="feed-path">{p.path}</code>}
          {d && <span className="feed__diff" data-testid="feed-diff">{d}</span>}
        </>
      );
    }
    case "shell":
      // 셸은 **명령과 종료 코드가 한 쌍**이다(U6 4단계 "셸 카드(명령·종료 코드)"). 0 도 보여 준다 —
      // `p.exit_code && …` 로 쓰면 성공(0)이 통째로 사라져 "끝났는지"조차 알 수 없다.
      return (
        <>
          {p.command && <code className="feed__cmd" data-testid="feed-command">{p.command}</code>}
          {p.exit_code != null && (
            <span className="feed__exit" data-testid="feed-exit-code" data-exit-code={p.exit_code} data-ok={String(p.exit_code === 0)}>
              exit {p.exit_code}
            </span>
          )}
          {p.duration_ms != null && <span className="feed__why" data-testid="feed-duration">{(p.duration_ms / 1000).toFixed(1)}s</span>}
        </>
      );
    case "error":
      return (
        <span className="feed__why" data-testid="feed-error-detail">
          {[p.failure_kind, p.rejected_reason, p.detail].filter(Boolean).join(" · ") || "실패"}
          {p.not_before ? ` · ${clockTime(p.not_before)} 이후 재시도 가능` : ""}
        </span>
      );
    case "platform":
      return p.result_ref ? <span className="feed__why" data-testid="feed-result-ref">{p.result_ref}</span> : null;
    default:
      return null;
  }
}

export function ActivityFeed({ events, structured = true, loading, cut1 = false, title, limit = 200 }: ActivityFeedProps) {
  const [raw, setRaw] = useState(false);
  /** 펼친 카드 하나(행 id). 여러 개를 동시에 펼치면 피드가 로그가 된다. */
  const [expanded, setExpanded] = useState<string | null>(null);
  const rows = foldEvents(events).slice(-limit);

  if (!structured) {
    return (
      <div className="feed" data-testid="activity-feed" data-structured="false">
        <div className="feed__degraded" data-testid="feed-degraded">
          이 런타임은 툴 단위 로그를 제공하지 않습니다 — 메시지와 원본 출력만 표시합니다.
        </div>
        <ActivityRail events={events} structured loading={loading} />
      </div>
    );
  }

  return (
    <div className="feed" data-testid="activity-feed" data-structured="true">
      <div className="feed__head">
        <span className="feed__title" data-testid="feed-title">{title ?? "활동"}</span>
        <button type="button" className="msg__link" onClick={() => setRaw((v) => !v)} aria-expanded={raw} data-testid="feed-raw-toggle">
          {raw ? "원본 접기" : "원본 레일"}
        </button>
      </div>
      {loading && rows.length === 0 && <div className="feed__quiet" data-testid="feed-loading">불러오는 중…</div>}
      {!loading && rows.length === 0 && <div className="feed__quiet" data-testid="feed-empty">대기 중…</div>}
      <ol className="feed__list">
        {rows.map(({ first, latest: e }) => {
          const cls = renderClassWithCut(e, cut1);
          const pending = e.outcome === "started";
          const p = payloadOf(e);
          // 카드 상세 — 편집·셸의 결과 요약(`summary`)은 접어 둔다. 피드는 훑는 자리이고,
          // 마스킹이 켜진 워크스페이스에서는 이 요약이 diff·셸 출력을 대신하는 **유일한 내용**이다.
          const detailText = (cls === "file_edit" || cls === "shell") ? (p.summary ?? null) : null;
          const open = expanded === first.id;
          return (
            <li
              key={first.id}
              className="feed__row"
              data-testid={`feed-row-${cls}`}
              data-render-class={cls}
              data-outcome={e.outcome ?? ""}
              data-pending={pending ? "true" : "false"}
              data-event-id={e.id}
            >
              <span className="feed__glyph" aria-hidden="true">{GLYPH[cls]}</span>
              <span className="feed__body">
                <span className="feed__sentence">{feedSentence(e)}</span>
                <Detail cls={cls} e={e} />
                {pending && <span className="feed__pending" data-testid="feed-pending"> · 진행 중…</span>}
                {e.masked && <span className="feed__quiet"> · 마스킹됨</span>}
                {detailText && (
                  <button
                    type="button"
                    className="feed__more"
                    aria-expanded={open}
                    onClick={() => setExpanded(open ? null : first.id)}
                    data-testid="feed-detail-toggle"
                  >
                    {open ? "상세 접기" : "상세"}
                  </button>
                )}
                {detailText && open && (
                  <span className="feed__detail" data-testid="feed-detail">{detailText}</span>
                )}
              </span>
              <span className="feed__time">{clockTime(e.created_at)}</span>
            </li>
          );
        })}
      </ol>
      {rows.some((r) => isFailure(r.latest)) && (
        <div className="feed__failnote" data-testid="feed-has-failure">실패한 항목이 있습니다 — 자동 재시도 여부는 lane 카드가 말합니다.</div>
      )}
      {raw && <div className="feed__raw"><ActivityRail events={events} structured /></div>}
    </div>
  );
}

export default ActivityFeed;
