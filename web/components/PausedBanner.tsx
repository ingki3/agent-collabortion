"use client";
/**
 * 세션 `paused` 배너(SCREEN §4.5 O6 · COMPONENTS §2.7 Status Banner) — 우열 최상단.
 *
 * **사유 5종마다 할 일이 다르므로 버튼도 다르다.** 계약 `PausedDetail` 은 객체이고(`{reason, paused_at, loop{limit,count,agents},
 * budget, time, runtime}`), 배너는 그 안을 읽어 "무엇을 올려야 하는지"를 말한다.
 *
 * 루프 상한은 승인만 하면 같은 핑퐁이 반복되므로 **어느 상한**(chain_depth·hops_per_hour·pair_roundtrips)에
 * **몇 번**(count) 걸렸는지, pair_roundtrips 면 **어느 두 에이전트**인지를 함께 보여 사람이 원인을 판단하게 한다.
 *
 * 동작 가능 여부는 계약이 준 `resolve_actions` 로만 정한다(권한은 서버 판정). 비활성은 숨기지 않고 사유를 툴팁으로(원칙 4).
 * `can_resolve_from` 은 deputy 가 언제부터 할 수 있는지다 — "22:31부터 승인 가능".
 */
import { useState } from "react";
import "./paused-banner.css";
import { clockTime, humanDuration, relativeTime } from "@/lib/time";
import type { PausedDetail, PauseReason } from "@/lib/api/types";

type ResolveAction = "resume" | "rebind" | "cancel";

/** 루프 상한 이름 — 어느 상한에 걸렸는지가 Director 가 올려야 할 값을 정한다. */
export const LOOP_LIMIT_LABEL = {
  chain_depth: "연쇄 깊이(chain_depth)",
  hops_per_hour: "시간당 홉(hops_per_hour)",
  pair_roundtrips: "두 에이전트 왕복(pair_roundtrips)",
} as const;

export const PAUSE_TITLE: Record<PauseReason, string> = {
  budget: "예산 초과로 일시정지",
  time: "시간 상한 도달로 일시정지",
  loop: "루프 상한 도달로 일시정지",
  runtime_offline: "런타임 오프라인으로 일시정지",
  director: "Director 가 일시정지했습니다",
};

/** 배너 본문 한 줄 — 사유별로 "무엇이 얼마나" 를 말한다. */
export function pausedSummary(d: PausedDetail, agentName?: (id: string) => string): string {
  switch (d.reason) {
    case "budget": {
      const b = d.budget;
      if (!b || b.limit_usd == null) return "예산을 초과했습니다.";
      return `예산 $${b.limit_usd}을 초과했습니다 (현재 $${(b.spent_usd ?? 0).toFixed(2)})`;
    }
    case "time": {
      const t = d.time;
      if (!t?.limit) return "시간 상한에 도달했습니다.";
      return `시간 상한 ${humanDuration(t.limit)}에 도달했습니다${t.elapsed ? ` (경과 ${humanDuration(t.elapsed)})` : ""}`;
    }
    case "loop": {
      const l = d.loop;
      if (!l?.limit) return "에이전트 간 왕복이 상한에 도달했습니다.";
      const who = (l.agents ?? []).map((id) => `@${agentName?.(id) ?? id.slice(0, 8)}`).join(" ↔ ");
      const pair = l.limit === "pair_roundtrips" && who ? `${who} ` : "";
      return `${LOOP_LIMIT_LABEL[l.limit]} 상한에 도달했습니다 — ${pair}${l.count ?? 0}회`;
    }
    case "runtime_offline": {
      const r = d.runtime;
      return r?.offline_since
        ? `런타임이 ${relativeTime(r.offline_since)}부터 오프라인입니다`
        : "세션 런타임이 오프라인입니다";
    }
    case "director":
      return "Director 가 일시정지했습니다 — 진행 중이던 턴은 마치고 대기 중입니다.";
  }
}

export interface PausedBannerProps {
  detail: PausedDetail;
  /** 에이전트 id → 이름(루프 배너의 두 에이전트). */
  agentName?: (id: string) => string;
  onResume?: (body: { limits?: { budget_usd?: number; time_limit?: string }; reset_loop_counters?: boolean }) => Promise<void> | void;
  onRebind?: () => void;
  onCancel?: () => void;
  busy?: boolean;
}

export function PausedBanner({ detail, agentName, onResume, onRebind, onCancel, busy }: PausedBannerProps) {
  const actions = new Set<ResolveAction>((detail.resolve_actions ?? []) as ResolveAction[]);
  const [budget, setBudget] = useState<string>(String(detail.budget?.limit_usd ?? ""));
  const [extend, setExtend] = useState<string>("PT2H");

  const gate = detail.can_resolve_from && Date.parse(detail.can_resolve_from) > Date.now();
  const gateNote = gate ? `${clockTime(detail.can_resolve_from)}부터 승인 가능` : null;
  const cannot = gateNote ?? "Director 만 할 수 있습니다";

  async function resume() {
    if (detail.reason === "budget") {
      const n = Number(budget);
      await onResume?.({ limits: Number.isFinite(n) && n > 0 ? { budget_usd: n } : undefined });
    } else if (detail.reason === "time") {
      await onResume?.({ limits: { time_limit: extend } });
    } else if (detail.reason === "loop") {
      await onResume?.({ reset_loop_counters: true });
    } else {
      await onResume?.({});
    }
  }

  const canResume = actions.has("resume") && !gate && !!onResume;

  return (
    <section className="pbanner" data-testid="paused-banner" data-reason={detail.reason}>
      <div className="pbanner__title" data-testid="paused-title">
        <span aria-hidden="true">⏸︎</span> {PAUSE_TITLE[detail.reason]}
      </div>
      <p className="pbanner__body" data-testid="paused-summary">{pausedSummary(detail, agentName)}</p>
      {detail.reason === "loop" && detail.loop?.limit && (
        <p className="pbanner__hint" data-testid="paused-loop-limit" data-limit={detail.loop.limit} data-count={detail.loop.count ?? 0}>
          올려야 할 상한: <b>{LOOP_LIMIT_LABEL[detail.loop.limit]}</b> · 현재 {detail.loop.count ?? 0}회.
          승인만 하면 같은 핑퐁이 반복됩니다 — lane 을 중단하거나 다시 지시해 방향을 바꿀 수도 있습니다.
        </p>
      )}
      {detail.reason === "runtime_offline" && (
        <p className="pbanner__hint">이 사유는 재개할 수 없습니다 — 다른 런타임으로 재바인딩하거나 세션을 종료하세요(FR-9.2).</p>
      )}
      <p className="pbanner__meta">일시정지 {relativeTime(detail.paused_at)}{gateNote ? ` · ${gateNote}` : ""}</p>

      {detail.reason === "budget" && (
        <label className="pbanner__field">
          <span>새 상한 (USD)</span>
          <input className="input" type="number" min={0} step="1" value={budget} onChange={(e) => setBudget(e.target.value)} data-testid="paused-budget-input" />
        </label>
      )}
      {detail.reason === "time" && (
        <label className="pbanner__field">
          <span>연장 시간</span>
          <select className="select" value={extend} onChange={(e) => setExtend(e.target.value)} data-testid="paused-time-input">
            <option value="PT1H">1시간</option>
            <option value="PT2H">2시간</option>
            <option value="PT4H">4시간</option>
            <option value="P1D">1일</option>
          </select>
        </label>
      )}

      <div className="pbanner__actions">
        <button
          type="button"
          className="btn btn--primary btn--sm"
          disabled={!canResume || busy}
          title={!actions.has("resume") || gate ? cannot : undefined}
          onClick={() => void resume()}
          data-testid="paused-resume"
        >
          {detail.reason === "director" ? "재개" : "계속 진행 승인"}
        </button>
        {actions.has("rebind") && (
          <button type="button" className="btn btn--sm" disabled={busy || !onRebind} onClick={onRebind} data-testid="paused-rebind">
            다른 런타임으로 재바인딩
          </button>
        )}
        {actions.has("cancel") && (
          <button type="button" className="btn btn--sm" disabled={busy || !onCancel} onClick={onCancel} data-testid="paused-cancel">
            세션 종료
          </button>
        )}
      </div>
    </section>
  );
}

export default PausedBanner;
