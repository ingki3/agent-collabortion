"use client";
/**
 * S14 「대시보드」 탭 — PRD §11 성공 지표 10개를 **표 그대로**(getWorkspaceMetrics: "이 배열을 표로 그리기만 한다").
 * 열: 지표 · 현재값 · 목표 · 표본 n. `value: null` 이면 "아직 잴 수 없음"(0 을 실측처럼 보이지 않는다, G9 조건).
 * 판정은 글리프(✓ ✕ –)가 1차, 색은 보조(SCREEN §5 배지 규칙 — 색만으로 말하지 않는다).
 * `task_success_rate_by_runtime` 은 `breakdown[]` 을 종류별 하위 행으로. 세는 법(`note`)은 「자세히 보기」 안.
 * 그 **아래** 「관찰」 표(`ObservationsTable`, getWorkspaceObservations — 목표치 없음, v1.1 K-18)를 별도로 그린다. 두 표를 한 표로
 * 합치지 않는다(Director 확정 2026-09-15). 관찰 표가 실패해도(서버가 아직 없으면 501) 지표 표는 그대로 보인다 — 오류는 그 자리에.
 */
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage } from "@/lib/api/client";
import { formatMetricTarget, formatMetricValue, metricVerdict, RUNTIME_KIND_LABEL, SETTINGS_TABS, VERDICT_GLYPH, VERDICT_LABEL } from "@/lib/settings";
import { OBSERVATIONS } from "@/lib/wording";
import type { Metric, MetricsReport, ObservationReport } from "@/lib/api/types";
import { ObservationsTableView } from "./ObservationsTable";
import "./settings.css";

export function Verdict({ m }: { m: Pick<Metric, "value" | "target" | "target_op"> }) {
  const v = metricVerdict(m);
  return (
    <span className={`verdict verdict--${v}`} data-testid="metric-verdict" data-verdict={v} title={VERDICT_LABEL[v]}>
      <span aria-hidden="true">{VERDICT_GLYPH[v]}</span>
      <span className="small">{VERDICT_LABEL[v]}</span>
    </span>
  );
}

export function MetricsTableView({ report }: { report: MetricsReport }) {
  return (
    <div className="metrics-wrap">
      <p className="metrics-meta" data-testid="metrics-meta">
        최근 {report.window.replace(/^P(\d+)D$/, "$1일")} 기준 · {new Date(report.computed_at).toLocaleString("ko-KR")} 집계
      </p>
      <table className="metrics" data-testid="metrics-table">
        <thead>
          <tr>
            <th>지표</th>
            <th className="num">현재값</th>
            <th className="num">목표</th>
            <th className="num">표본</th>
            <th>판정</th>
          </tr>
        </thead>
        <tbody>
          {report.metrics.map((m) => (
            <MetricRows key={m.key} m={m} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function MetricRows({ m }: { m: Metric }) {
  return (
    <>
      <tr data-testid="metric-row" data-key={m.key} data-verdict={metricVerdict(m)}>
        <td>
          <span className="metrics__label">{m.label}</span>
          <details className="metrics__note">
            <summary>세는 법</summary>
            {m.note}
          </details>
        </td>
        <td className="num" data-testid="metric-value">{formatMetricValue(m.value, m.unit)}</td>
        <td className="num">{formatMetricTarget(m)}</td>
        <td className="num" data-testid="metric-n">{m.n}</td>
        <td><Verdict m={m} /></td>
      </tr>
      {m.breakdown?.map((b) => (
        <tr key={b.kind} className="metrics__sub" data-testid="metric-breakdown" data-kind={b.kind}>
          <td>{RUNTIME_KIND_LABEL[b.kind] ?? b.kind}</td>
          <td className="num">{formatMetricValue(b.value, m.unit)}</td>
          <td className="num">{formatMetricTarget({ target: b.target, target_op: m.target_op, unit: m.unit })}</td>
          <td className="num">{b.n}</td>
          <td><Verdict m={{ value: b.value, target: b.target, target_op: m.target_op }} /></td>
        </tr>
      ))}
    </>
  );
}

export function MetricsTab({ workspaceId }: { workspaceId: string }) {
  const [report, setReport] = useState<MetricsReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [observations, setObservations] = useState<ObservationReport | null>(null);
  const [obsError, setObsError] = useState<string | null>(null);
  const meta = SETTINGS_TABS.find((t) => t.key === "dashboard")!;
  const load = useCallback(async () => {
    // 두 op 을 따로 부른다 — 한쪽의 실패가 다른 표를 지우지 않게.
    const [m, o] = await Promise.allSettled([
      api.get("/workspaces/{workspaceId}/metrics", { path: { workspaceId } }),
      api.get("/workspaces/{workspaceId}/observations", { path: { workspaceId } }),
    ]);
    if (m.status === "fulfilled") { setReport(m.value); setError(null); } else setError(errorMessage(m.reason));
    if (o.status === "fulfilled") { setObservations(o.value); setObsError(null); } else setObsError(errorMessage(o.reason));
  }, [workspaceId]);
  useEffect(() => { void load(); }, [load]);
  return (
    <section className="card" data-testid="settings-tab-dashboard">
      <div className="tab-head">
        <div>
          <h2>{meta.label}</h2>
          <p className="tab-head__desc">{meta.desc}</p>
        </div>
        <div className="tab-head__actions">
          <button type="button" className="btn btn--sm" onClick={() => void load()} data-testid="metrics-reload">다시 세기</button>
        </div>
      </div>
      {error && <p className="problem" role="alert" data-testid="metrics-error">{error}</p>}
      {!report && !error && <p className="muted">세는 중…</p>}
      {report && <MetricsTableView report={report} />}
      {obsError && <p className="problem" role="alert" data-testid="observations-error">{obsError}</p>}
      {!observations && !obsError && <p className="muted">{OBSERVATIONS.counting}</p>}
      {observations && <ObservationsTableView report={observations} />}
    </section>
  );
}
