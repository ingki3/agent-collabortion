"use client";
/**
 * S14 「대시보드」 탭 — 지표 10개 표 **아래** 「관찰」 표(PRD §11 관찰 행 5개, openapi `getWorkspaceObservations`, v1.1 K-18).
 *
 * 지표 표와 **합치지 않는다**(Director 확정 2026-09-15): 목표치가 없는 행을 목표 열 옆에 두면 "미달" 로 읽힌다. 그래서 별도 표이고
 * 제목 아래 한 줄이 "목표치 없이 분포만 봅니다" 라고 먼저 말한다. 판정 열도 없다.
 *
 * 행마다: 이름(`label` 서버 문장 그대로) · 값(분포형 "중앙값 · p95", 비율형 "%") · 표본 n · 「세는 법」 접기(`note`) — 부품은 MetricsTable 과 같다
 * (`.metrics` CSS · `<details>`). `routing_concentration` 은 `breakdown[]`(규칙 번호별 비율)을 하위 행으로. **n 0 → "아직 잴 수 없음"**.
 * V-1(#247 리뷰): 머리에 「다시 세기」(지표 표와 같은 load, 두 op 함께) · `routing_concentration` 의 값 옆에 "규칙 6·7 폴백 비율" 한 줄
 * (값과 하위 행의 관계) · 모르는 `kind` 는 원시 값 + "(새 규칙)".
 */
import { formatMetricValue, formatObservation, NOT_MEASURABLE } from "@/lib/settings";
import { OBSERVATIONS, routingKindLabel } from "@/lib/wording";
import type { ObservationReport, ObservationRow } from "@/lib/api/types";
import "./settings.css";

export function ObservationsTableView({ report, onReload }: { report: ObservationReport; onReload?: () => void }) {
  return (
    <div className="metrics-wrap observations" data-testid="observations-wrap">
      <div className="observations__head">
        <h3 className="observations__title" data-testid="observations-title">{OBSERVATIONS.title}</h3>
        {/* 「다시 세기」 — 지표 표의 버튼과 같은 load(두 op 함께). 표가 화면 아래라 상단 버튼이 안 보일 때를 위해 여기도 둔다(V-1). */}
        {onReload && (
          <button type="button" className="btn btn--sm" onClick={onReload} data-testid="observations-reload">{OBSERVATIONS.reload}</button>
        )}
      </div>
      <p className="metrics-meta" data-testid="observations-subtitle">{OBSERVATIONS.subtitle}</p>
      <table className="metrics" data-testid="observations-table">
        <thead>
          <tr>
            <th>{OBSERVATIONS.col_name}</th>
            <th className="num">{OBSERVATIONS.col_value}</th>
            <th className="num">{OBSERVATIONS.col_n}</th>
          </tr>
        </thead>
        <tbody>
          {report.rows.map((r) => (
            <ObservationRows key={r.key} row={r} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ObservationRows({ row }: { row: ObservationRow }) {
  const value = formatObservation(row, { median: OBSERVATIONS.median, p95: OBSERVATIONS.p95 });
  return (
    <>
      <tr data-testid="observation-row" data-key={row.key} data-measurable={value === NOT_MEASURABLE ? "false" : "true"}>
        <td>
          <span className="metrics__label">{row.label}</span>
          <details className="metrics__note">
            <summary>{OBSERVATIONS.note_summary}</summary>
            {row.note}
          </details>
        </td>
        <td className="num" data-testid="observation-value">
          {value}
          {row.key === "routing_concentration" && value !== NOT_MEASURABLE && (
            <span className="observations__hint" data-testid="observation-value-hint">{OBSERVATIONS.routing_value_hint}</span>
          )}
        </td>
        <td className="num" data-testid="observation-n">{row.n}</td>
      </tr>
      {row.n > 0 && row.breakdown?.map((b) => (
        <tr key={b.kind} className="metrics__sub" data-testid="observation-breakdown" data-kind={b.kind}>
          <td>{routingKindLabel(b.kind)}</td>
          <td className="num">{formatMetricValue(b.share, "ratio")}</td>
          <td className="num">{b.n}</td>
        </tr>
      ))}
    </>
  );
}
