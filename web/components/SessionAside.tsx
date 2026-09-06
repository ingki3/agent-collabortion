"use client";
/**
 * S7 우열 — 진행 상황(SCREEN §4.5 "우열"). goal·성공 기준 · **종료 조건 진행률** · 아티팩트(버전) · 결정 기록 ·
 * **비용**(누적/예산, 추정이면 배지) · 런타임과 격리 · 세션 설정 요약.
 * 세션이 `paused` 면 최상단에 `PausedBanner`(O6) — 사유마다 할 일이 다르므로 버튼도 다르다.
 */
import "./session-aside.css";
import { ConditionRow } from "./ConditionRow";
import { PausedBanner, type PausedBannerProps } from "./PausedBanner";
import { humanDuration, relativeTime } from "@/lib/time";
import type { Artifact, Decision, Session } from "@/lib/api/types";

const ISOLATION_LABEL = { none: "격리 없음(none)", worktree: "worktree", container: "container" } as const;
const AUTONOMY_LABEL = { guided: "guided — 기한이 지나도 계속 기다립니다", autonomous: "autonomous — 기한이 지나면 제안 기본값으로 진행(승인은 예외)", supervised: "supervised (v1.1)" } as const;

export interface SessionAsideProps {
  session: Session;
  artifacts: Artifact[] | null;
  decisions: Decision[] | null;
  runtimeName?: string | null;
  agentName?: (id: string) => string;
  onResume?: PausedBannerProps["onResume"];
  onRebind?: () => void;
  onCancelSession?: () => void;
  busy?: boolean;
}

export function SessionAside(props: SessionAsideProps) {
  const s = props.session;
  const prog = s.completion_progress;
  const budget = s.limits.budget_usd ?? null;
  const pct = budget ? Math.round((s.cost_usd / budget) * 100) : null;

  return (
    <aside className="aside" data-testid="session-aside">
      {s.status === "paused" && s.paused_detail && (
        <PausedBanner
          detail={s.paused_detail}
          agentName={props.agentName}
          onResume={props.onResume}
          onRebind={props.onRebind}
          onCancel={props.onCancelSession}
          busy={props.busy}
        />
      )}
      {s.status === "paused" && !s.paused_detail && (
        <p className="aside__quiet" data-testid="paused-detail-missing">
          일시정지 상태이지만 사유 정보를 받지 못했습니다{s.paused_reason ? ` (${s.paused_reason})` : ""}.
        </p>
      )}

      <section className="aside__sec" data-testid="aside-goal">
        <h2 className="aside__h">Goal</h2>
        <p className="aside__goal">{s.goal}</p>
        {s.acceptance_criteria.length > 0 && (
          <ul className="aside__list">
            {s.acceptance_criteria.map((c, i) => (
              <li key={i}>{c}</li>
            ))}
          </ul>
        )}
      </section>

      <section className="aside__sec" data-testid="aside-progress">
        <h2 className="aside__h">
          종료 조건 진행률 <span className="aside__count" data-testid="progress-count">{prog.met}/{prog.total}</span>
        </h2>
        {prog.conditions.map((c) => (
          <ConditionRow key={c.path} type={c.type} met={c.met} nextActor={c.next_actor} metAt={c.met_at} />
        ))}
        {prog.human_gate === false && (
          <p className="aside__warn" data-testid="no-human-gate">사람 승인 없이 완료됩니다 — 종료 조건에 Director 승인이 없습니다.</p>
        )}
      </section>

      <section className="aside__sec" data-testid="aside-artifacts">
        <h2 className="aside__h">아티팩트</h2>
        {props.artifacts === null ? (
          <p className="aside__quiet">불러오는 중…</p>
        ) : props.artifacts.length === 0 ? (
          <p className="aside__quiet" data-testid="artifacts-empty">아직 제출된 산출물이 없습니다.</p>
        ) : (
          <ul className="aside__list">
            {props.artifacts.map((a) => (
              <li key={a.id} data-testid="artifact-row" data-artifact-id={a.id}>
                <span className="aside__name">{a.name}</span>
                <span className="aside__ver" data-testid="artifact-version">v{a.version}</span>
                {a.latest === false && <span className="aside__quiet"> 이전 버전</span>}
                {a.review && (
                  <span className={a.review.verdict === "approve" ? "aside__ok" : "aside__warn"} data-testid="artifact-review">
                    {a.review.verdict === "approve" ? " 승인됨" : " 반려됨"}
                  </span>
                )}
                <span className="aside__quiet"> · {a.submitted_by?.agent_name ?? "—"} · {relativeTime(a.created_at)}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="aside__sec" data-testid="aside-decisions">
        <h2 className="aside__h">결정 기록</h2>
        {props.decisions === null ? (
          <p className="aside__quiet">불러오는 중…</p>
        ) : props.decisions.length === 0 ? (
          <p className="aside__quiet" data-testid="decisions-empty">아직 기록된 결정이 없습니다.</p>
        ) : (
          <ul className="aside__list">
            {props.decisions.map((d) => (
              <li key={d.id} data-testid="decision-row">
                <span className="aside__name">{d.summary}</span>
                {d.auto && <span className="aside__auto" data-testid="decision-auto"> 자동</span>}
                <span className="aside__quiet"> · {d.source === "hitl" ? "HITL" : "에이전트"} · {relativeTime(d.created_at)}</span>
                {d.rationale && <div className="aside__quiet">{d.rationale}</div>}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="aside__sec" data-testid="aside-cost">
        <h2 className="aside__h">비용</h2>
        <p className="aside__cost" data-testid="cost-line">
          ${s.cost_usd.toFixed(2)}
          {budget != null ? ` / $${budget}` : ""}
          {pct != null ? ` (${pct}%)` : ""}
          {s.cost_estimated && <span className="aside__badge" data-testid="cost-estimated">추정</span>}
        </p>
        {budget != null && (
          <div className="aside__bar" aria-hidden="true">
            <span style={{ width: `${Math.min(100, pct ?? 0)}%` }} />
          </div>
        )}
        {s.cost_estimated && <p className="aside__quiet">런타임이 사용량을 보고하지 않아 추정치입니다 — 하드 컷을 하지 않습니다(FR-7.3).</p>}
      </section>

      <section className="aside__sec" data-testid="aside-settings">
        <h2 className="aside__h">세션 설정</h2>
        <dl className="aside__dl">
          <dt>런타임</dt>
          <dd data-testid="aside-runtime">{props.runtimeName ?? (s.runtime_id ? s.runtime_id.slice(0, 8) : "자동 선택 — 첫 실행 시 고정")}</dd>
          <dt>격리</dt>
          <dd>{ISOLATION_LABEL[s.isolation.kind]}{s.isolation.repo_path ? ` · ${s.isolation.repo_path}` : ""}</dd>
          <dt>autonomy</dt>
          <dd>{AUTONOMY_LABEL[s.autonomy]}</dd>
          <dt>한도</dt>
          <dd>
            {budget != null ? `$${budget}` : "예산 없음"} · {s.limits.time_limit ? humanDuration(s.limits.time_limit) : "시간 제한 없음"} ·
            병렬 lane {s.limits.max_parallel_lanes ?? 5}
          </dd>
          <dt>Director</dt>
          <dd>{s.director?.display_name ?? "—"}{s.deputy_director ? ` · deputy ${s.deputy_director.display_name}` : ""}</dd>
        </dl>
        <p className="aside__quiet">런타임과 격리는 변경할 수 없습니다 — workdir 이 묶여 있습니다(SCREEN §4.5 상단 액션).</p>
      </section>
    </aside>
  );
}

export default SessionAside;
