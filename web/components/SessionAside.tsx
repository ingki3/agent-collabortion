"use client";
/**
 * S7 우열 — 진행 상황(SCREEN §4.5 "우열"). goal·성공 기준 · **종료 조건 진행률** · 아티팩트(버전) · 결정 기록 ·
 * **비용**(누적/예산, 추정이면 배지) · 런타임과 격리 · 세션 설정 요약.
 * 세션이 `paused` 면 최상단에 `PausedBanner`(O6) — 사유마다 할 일이 다르므로 버튼도 다르다.
 */
import "./session-aside.css";
import { ConditionRow } from "./ConditionRow";
import { PausedBanner, type PausedBannerProps } from "./PausedBanner";
import { progressSummary, topOp } from "@/lib/completion";
import { humanDuration, relativeTime } from "@/lib/time";
import { FIX_CONDITION, PROGRESS } from "@/lib/wording";
import type { Artifact, Decision } from "@/lib/api/types";
import type { Session } from "@/lib/legacy-session";

const ISOLATION_LABEL = { none: "격리 없음", worktree: "워크트리", container: "컨테이너" } as const;
const AUTONOMY_LABEL = { guided: "기다림 — 기한이 지나도 계속 답을 기다립니다", autonomous: "알아서 진행 — 기한이 지나면 제안값으로 진행(승인은 예외)", supervised: "매번 확인 (다음 버전)" } as const;

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
  /** 진행률 행 「받은 요청에서 승인하세요」 — 타임라인의 그 확인 카드로. */
  onOpenHitl?: (hitlRequestId: string) => void;
  /** 「조건 고치기」 — Director 에게만 넘긴다(updateSession 권한). 없으면 버튼 대신 "Director 가 고쳐야" 한 줄. */
  onFixCondition?: () => void;
}

/**
 * 충족시킨 주체의 **이름** — `met_by` 는 에이전트 id · 사용자 id · "platform" 이라 그대로 보이면 안 된다(§8.4).
 * 에이전트 조건은 지정 에이전트 이름(`agent_name`)이 정답이고, 사람 조건(`user_approval`·`manual`)은 Director 다.
 */
export function metByName(type: string, metBy: string | null | undefined, agentName: string | null | undefined, resolve?: (id: string) => string): string | null {
  if (type === "user_approval" || type === "manual") return "Director";
  if (agentName) return agentName;
  if (!metBy || metBy === "platform") return null;
  const r = resolve?.(metBy);
  return r && r !== metBy && r !== metBy.slice(0, 8) ? r : null;
}

export function SessionAside(props: SessionAsideProps) {
  const s = props.session;
  const prog = s.completion_progress;
  const budget = s.limits.budget_usd ?? null;
  const pct = budget ? Math.round((s.cost_usd / budget) * 100) : null;
  const closed = s.status === "completed" || s.status === "cancelled";
  const blockedCount = prog.conditions.filter((c) => !c.met && !!c.blocked_reason).length;

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
        <h2 className="aside__h">목표</h2>
        <p className="aside__goal">{s.goal}</p>
        {s.acceptance_criteria.length > 0 && (
          <ul className="aside__list">
            {s.acceptance_criteria.map((c, i) => (
              <li key={i}>{c}</li>
            ))}
          </ul>
        )}
      </section>

      {/* 종료 조건 진행률(SCREEN §4.5, T-W15 · S-84) — 조건마다 사람 말 한 줄 + 다음 행동. `blocked_reason` 이 있으면 ✗ 대신 이유와
          Director 의 「조건 고치기」. 상단 한 줄이 남은 것과 막힌 개수를 센다 — 세션이 왜 안 닫히는지 이 칸만 보고 알 수 있어야 한다. */}
      <section className="aside__sec" data-testid="aside-progress">
        <h2 className="aside__h">
          종료 조건 진행률 <span className="aside__count" data-testid="progress-count">{prog.met}/{prog.total}</span>
        </h2>
        <p className="aside__summary" data-testid="progress-summary">{progressSummary(prog, topOp(s.completion_condition), closed)}</p>
        {prog.conditions.map((c) => (
          <ConditionRow
            key={c.path}
            type={c.type}
            met={c.met}
            agentName={c.agent_name ?? null}
            metBy={metByName(c.type, c.met_by, c.agent_name, props.agentName)}
            metAt={c.met_at}
            nextActor={c.next_actor}
            blockedReason={c.blocked_reason ?? null}
            heldReason={c.held_reason ?? null}
            hitlRequestId={c.hitl_request_id ?? null}
            onOpenHitl={props.onOpenHitl}
          />
        ))}
        {blockedCount > 0 && !closed && (
          <div className="aside__blocked" data-testid="progress-blocked">
            <span className="aside__warn">{props.onFixCondition ? PROGRESS.blocked_director : PROGRESS.blocked_member}</span>
            {props.onFixCondition && (
              <button type="button" className="btn btn--sm" onClick={props.onFixCondition} disabled={props.busy} data-testid="fix-condition-open">
                {FIX_CONDITION.button}
              </button>
            )}
          </div>
        )}
        {blockedCount === 0 && props.onFixCondition && !closed && (
          <button type="button" className="aside__link" onClick={props.onFixCondition} disabled={props.busy} data-testid="fix-condition-open">
            {FIX_CONDITION.button}
          </button>
        )}
        {prog.human_gate === false && (
          <p className="aside__warn" data-testid="no-human-gate">사람 승인 없이 완료됩니다 — 종료 조건에 Director 승인이 없습니다.</p>
        )}
      </section>

      <section className="aside__sec" data-testid="aside-artifacts">
        <h2 className="aside__h">아티팩트</h2>
        {props.artifacts === null ? (
          <p className="aside__quiet">불러오는 중…</p>
        ) : props.artifacts.length === 0 ? (
          <p className="aside__quiet" data-testid="artifacts-empty">아직 제출된 아티팩트가 없습니다.</p>
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
                <span className="aside__quiet"> · {d.source === "hitl" ? "사람 확인" : "에이전트"} · {relativeTime(d.created_at)}</span>
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
        {s.cost_estimated && <p className="aside__quiet">이 컴퓨터가 사용량을 보고하지 않아 추정치입니다 — 금액으로 자동 중단하지 않습니다.</p>}
      </section>

      <section className="aside__sec" data-testid="aside-settings">
        <h2 className="aside__h">방 설정</h2>
        <dl className="aside__dl">
          <dt>컴퓨터</dt>
          {/* W-10: id 앞 8자를 보이지 않는다 — 이름은 호출부가 `runtimeNameOf` 로 넘기고, 못 받았으면 자리 표시. */}
          <dd data-testid="aside-runtime">{props.runtimeName ?? (s.runtime_id ? "이름 확인 중…" : "자동 선택 — 첫 실행 시 고정")}</dd>
          <dt>격리</dt>
          <dd>{ISOLATION_LABEL[s.isolation.kind]}{s.isolation.repo_path ? ` · ${s.isolation.repo_path}` : ""}</dd>
          <dt>자율성</dt>
          <dd>{AUTONOMY_LABEL[s.autonomy]}</dd>
          <dt>한도</dt>
          <dd>
            {budget != null ? `$${budget}` : "예산 없음"} · {s.limits.time_limit ? humanDuration(s.limits.time_limit) : "시간 제한 없음"} ·
            동시에 {s.limits.max_parallel_lanes ?? 5}줄기까지
          </dd>
          <dt>Director</dt>
          <dd>{s.director?.display_name ?? "—"}{s.deputy_director ? ` · deputy ${s.deputy_director.display_name}` : ""}</dd>
        </dl>
        <p className="aside__quiet">컴퓨터와 격리 방식은 바꿀 수 없습니다 — 작업 폴더가 거기 묶여 있습니다.</p>
      </section>
    </aside>
  );
}

export default SessionAside;
