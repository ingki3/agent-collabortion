"use client";
/**
 * 우열 (가) 미션 칸 = S22 미션 패널(SCREEN §4.6 우열 · §4.8) — `SessionAside` 에서 미션 쪽 절을 떼어 낸 것(§5 「둘로 쪼갠다」).
 *
 * **칩 선택과 연동한다**: 사람이 고른 미션이면 동작이 켜지고(권한은 따로), `(전체)` 에서는 최근 활동한 미션을 **보여 주기만** 하며,
 * `(미션 없음)` 에서는 칸을 남기고 안을 비운다(감추지 않는다 — 우열 폭이 흔들리면 방 전체 칸의 자리가 매번 달라진다).
 * `(전체)`·`(미션 없음)` 에서 미션 동작은 **통째로 비활성**이고 사유는 버튼 아래 글자다(`DisabledHint`, Lead 판정 4).
 * 끝난 미션(S22)은 읽기 전용 + 「끝난 미션입니다」.
 * 미션 `paused` 해소 배너는 **미션 사유만**(예산·시간·수동) — 방 사유는 전폭 배너로 올라간다(§4.6). 배너는 조용하다(`role="status"`).
 * 편집 동작(T-R2-W4b): 「설정 편집」(S21 폼 편집 모드) · 「Director 교체」 · 막힌 조건의 「조건 고치기」 — 권한은 `lib/work-edit.ts` 한 곳.
 * Director 교체만 층이 다르다(그 미션의 director **또는 ws owner·admin**) — 사유도 따로 선다.
 */
import { useState } from "react";
import "./session-aside.css";
import "./paused-banner.css";
import { Badge } from "./Badge";
import { ConditionRow } from "./ConditionRow";
import { Slot } from "./Slot";
import { DisabledHint } from "./PageHead";
import { metByName } from "./SessionAside";
import { progressSummary, topOp } from "@/lib/completion";
import { humanDuration } from "@/lib/time";
import { panelActionsEnabled, type PanelMode } from "@/lib/room-view";
import { ROOM_HEAD, WORK_CHIPS, WORK_PANEL } from "@/lib/wording";
import { WORK_EDIT, changeDirectorBlocked } from "@/lib/work-edit";
import type { Work } from "@/lib/api/types";

export interface WorkPanelProps {
  mode: PanelMode;
  /** 우열에 실린 미션(모드가 picked·recent 일 때). 읽는 중이면 null. */
  work: Work | null;
  /** 고른 미션을 이 방에서 못 찾았다(다른 방의 id · 지워짐). */
  notFound?: boolean;
  busy?: boolean;
  agentName?: (id: string) => string;
  onPause?: () => void;
  onResume?: (body?: { limits?: { budget_usd?: number; time_limit?: string } }) => void;
  onComplete?: () => void;
  onCancel?: () => void;
  onOpenHitl?: (hitlRequestId: string) => void;
  onNewWork?: () => void;
  /** 「+ 새 미션」 비활성 사유(보관된 방). */
  newWorkDisabled?: string | null;
  onOpenSummary?: (messageId: string) => void;
  onBackToRoom?: () => void;
  /** 「설정 편집」 — S21 폼 편집 모드. */
  onEdit?: () => void;
  /** 「Director 교체」 — changeWorkDirector. */
  onChangeDirector?: () => void;
  /** 「조건 고치기」 — 막힌 조건이 있을 때 Director 에게. */
  onFixCondition?: () => void;
  /** 워크스페이스 owner·admin — Director 교체 권한(계약 changeWorkDirector). */
  canManage?: boolean;
}

const CLOSED = new Set(["completed", "cancelled"]);

export function WorkPanel(props: WorkPanelProps) {
  const { mode, work } = props;
  const [budget, setBudget] = useState("");

  if (mode.kind === "no_works") {
    return (
      <section className="aside__sec" data-testid="work-panel" data-need="work-panel" data-mode="no_works">
        <h2 className="aside__h">{WORK_PANEL.title}</h2>
        <p className="aside__goal" data-testid="work-panel-empty"><b>{WORK_PANEL.no_works_title}</b> {WORK_PANEL.no_works_body}</p>
        {props.onNewWork && (
          <span className="work-panel__act">
            <button type="button" className="btn btn--sm btn--primary" onClick={props.onNewWork} disabled={!!props.newWorkDisabled} aria-describedby={props.newWorkDisabled ? "work-panel-new-hint" : undefined} data-testid="work-panel-new">
              {WORK_CHIPS.new_work}
            </button>
            {props.newWorkDisabled && <DisabledHint id="work-panel-new-hint">{props.newWorkDisabled}</DisabledHint>}
          </span>
        )}
        <p className="aside__quiet">{WORK_PANEL.no_works_alt}</p>
      </section>
    );
  }

  if (props.notFound) {
    return (
      <section className="aside__sec" data-testid="work-panel" data-need="work-panel" data-mode="not_found">
        <h2 className="aside__h">{WORK_PANEL.title}</h2>
        <p className="aside__warn" data-testid="work-not-found">{WORK_PANEL.not_found}</p>
        {props.onBackToRoom && <button type="button" className="aside__link" onClick={props.onBackToRoom}>{WORK_PANEL.back_to_room}</button>}
      </section>
    );
  }

  // (미션 없음) — 칸은 남기고 안을 비운다. 종료 조건·비용·제출자 행은 「미션이 없어 해당 없음」 한 줄로 접히고 동작 묶음은 비활성.
  const noneView = mode.kind === "none_view";
  const actionsOn = panelActionsEnabled(mode);
  const closed = !!work && CLOSED.has(work.status);
  const isDirector = work?.my_work_role === "director";
  const disabledWhy = !actionsOn
    ? noneView ? WORK_PANEL.no_end : WORK_PANEL.pick_first
    : closed ? null
    : !isDirector && work ? WORK_PANEL.not_director(work.director?.display_name ?? "") : null;
  const actDisabled = !actionsOn || closed || !isDirector || props.busy;
  // Director 교체 — 그 미션의 director 또는 ws owner·admin. 칩을 고르지 않았으면 다른 동작과 같은 사유(pick_first)로 막힌다.
  const directorBlocked = actionsOn && work && !closed ? changeDirectorBlocked(work, !!props.canManage) : null;
  const directorDisabled = !actionsOn || closed || !!directorBlocked || !!props.busy;
  // 교체는 층이 하나 더 있다(owner·admin) — 다른 동작의 사유 아래 한 줄 더 적어 그 길을 말한다.
  const directorWhy = directorBlocked;

  const actions = (
    <div className="work-panel__actions" data-testid="work-actions" data-enabled={actDisabled ? "false" : "true"}>
      <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
        {work?.status === "paused" ? (
          <button type="button" className="btn btn--sm" disabled={actDisabled} aria-describedby={disabledWhy ? "work-actions-why" : undefined} onClick={() => props.onResume?.()} data-testid="work-action-resume">
            {WORK_PANEL.resume}
          </button>
        ) : (
          <button type="button" className="btn btn--sm" disabled={actDisabled || work?.status !== "active"} aria-describedby={disabledWhy ? "work-actions-why" : undefined} onClick={props.onPause} data-testid="work-action-pause">
            {WORK_PANEL.pause}
          </button>
        )}
        <button type="button" className="btn btn--sm" disabled={actDisabled} aria-describedby={disabledWhy ? "work-actions-why" : undefined} onClick={props.onComplete} data-testid="work-action-complete">
          {WORK_PANEL.complete}
        </button>
        <button type="button" className="btn btn--sm" disabled={actDisabled} aria-describedby={disabledWhy ? "work-actions-why" : undefined} onClick={props.onCancel} data-testid="work-action-cancel">
          {WORK_PANEL.cancel}
        </button>
        {props.onEdit && (
          <button type="button" className="btn btn--sm" disabled={actDisabled} aria-describedby={disabledWhy ? "work-actions-why" : undefined} onClick={props.onEdit} data-testid="work-action-edit">
            {WORK_EDIT.edit}
          </button>
        )}
        {props.onChangeDirector && (
          <button type="button" className="btn btn--sm" disabled={directorDisabled} aria-describedby={directorWhy ? "work-director-why" : disabledWhy ? "work-actions-why" : undefined} onClick={props.onChangeDirector} data-testid="work-action-director">
            {WORK_EDIT.change_director}
          </button>
        )}
      </div>
      {disabledWhy && <DisabledHint id="work-actions-why">{disabledWhy}</DisabledHint>}
      {directorWhy && <DisabledHint id="work-director-why">{directorWhy}</DisabledHint>}
    </div>
  );

  if (noneView) {
    return (
      <section className="aside__sec" data-testid="work-panel" data-need="work-panel" data-mode="none_view">
        <h2 className="aside__h">{WORK_PANEL.title}</h2>
        <p className="aside__goal" data-testid="work-panel-none">{WORK_PANEL.none_view}</p>
        <p className="aside__quiet" data-testid="work-panel-na">{WORK_PANEL.not_applicable}</p>
        {actions}
      </section>
    );
  }

  if (!work) {
    return (
      <section className="aside__sec" data-testid="work-panel" data-mode={mode.kind}>
        <h2 className="aside__h">{WORK_PANEL.title}</h2>
        <p className="aside__quiet">{ROOM_HEAD.loading}</p>
      </section>
    );
  }

  const prog = work.completion_progress;
  const limit = work.limits?.budget_usd ?? null;
  const pct = limit ? Math.round((work.cost_usd / limit) * 100) : null;
  const blockedCount = prog.conditions.filter((c) => !c.met && !!c.blocked_reason).length;
  const pausedReason = work.status === "paused" ? work.paused_reason : null;
  const pd = work.paused_detail;
  const mayResolve = isDirector && actionsOn;

  return (
    <section className="aside__sec work-panel" data-testid="work-panel" data-need="work-panel" data-mode={mode.kind} data-work-id={work.id}>
      <h2 className="aside__h">{WORK_PANEL.title}</h2>
      {mode.kind === "recent" && <p className="aside__quiet" data-testid="work-panel-recent">{WORK_PANEL.recent(work.title)}</p>}
      {closed && (
        <p className="aside__warn" data-testid="work-ended" data-status={work.status}>
          {WORK_PANEL.ended}
          {work.finished_at && ` (${work.finished_at.slice(0, 10)} ${work.status === "completed" ? WORK_PANEL.ended_completed : WORK_PANEL.ended_cancelled})`}
          {work.summary_message_id && props.onOpenSummary && (
            <>
              {" · "}
              <button type="button" className="aside__link" onClick={() => props.onOpenSummary!(work.summary_message_id!)} data-testid="work-summary-link">{WORK_PANEL.summary_link}</button>
            </>
          )}
        </p>
      )}
      {pausedReason && (pausedReason === "budget" || pausedReason === "time" || pausedReason === "director") && (
        <div className="pbanner" role="status" data-testid="work-paused-banner" data-reason={pausedReason}>
          <p className="pbanner__body">
            {pausedReason === "budget" && (
              pd?.budget?.limit_usd != null ? (
                <>
                  <Slot text={WORK_PANEL.paused_budget} n={pd.budget.limit_usd} />
                  {pd.budget.spent_usd != null && <> (<Slot text={WORK_PANEL.paused_budget_now} n={pd.budget.spent_usd.toFixed(2)} />)</>}
                </>
              ) : WORK_PANEL.paused_budget[0].trim()
            )}
            {pausedReason === "time" && <>{WORK_PANEL.paused_time}{pd?.time?.limit ? ` (${humanDuration(pd.time.limit)})` : ""}</>}
            {pausedReason === "director" && WORK_PANEL.paused_director}
          </p>
          {mayResolve && pausedReason === "budget" && (
            <div className="pbanner__actions">
              <input className="input" type="number" min={0} step="1" value={budget} onChange={(e) => setBudget(e.target.value)} aria-label={WORK_PANEL.cost} style={{ width: 100 }} data-testid="work-budget-input" />
              <button type="button" className="btn btn--sm btn--primary" disabled={props.busy || !budget} onClick={() => props.onResume?.({ limits: { budget_usd: Number(budget) } })} data-testid="work-approve">
                {WORK_PANEL.approve}
              </button>
            </div>
          )}
          {mayResolve && pausedReason !== "budget" && (
            <div className="pbanner__actions">
              <button type="button" className="btn btn--sm btn--primary" disabled={props.busy} onClick={() => props.onResume?.()} data-testid="work-approve">
                {pausedReason === "time" ? WORK_PANEL.approve : WORK_PANEL.resume}
              </button>
            </div>
          )}
        </div>
      )}
      <div className="work-panel__title">
        <b data-testid="work-title">{work.title}</b>
        <Badge kind="work" value={work.status} size="sm" data-testid="work-status" />
      </div>
      <dl className="aside__dl">
        <dt>{WORK_PANEL.director}</dt>
        <dd data-testid="work-director">
          {work.director?.display_name ?? "—"}
          {work.deputy ? ` · ${WORK_PANEL.deputy} ${work.deputy.display_name}` : ""}
        </dd>
      </dl>
      <h3 className="aside__h">{WORK_PANEL.goal}</h3>
      <p className="aside__goal" data-testid="work-goal">{work.goal}</p>
      {work.acceptance_criteria.length > 0 && (
        <ul className="aside__list">
          {work.acceptance_criteria.map((c, i) => <li key={i}>{c}</li>)}
        </ul>
      )}
      <h3 className="aside__h">
        {WORK_PANEL.progress} <span className="aside__count" data-testid="progress-count">{prog.met}/{prog.total}</span>
      </h3>
      <p className="aside__summary" data-testid="progress-summary">{progressSummary(prog, topOp(work.completion_condition), closed)}</p>
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
          <span className="aside__warn">{mayResolve && props.onFixCondition ? WORK_EDIT.blocked_director : WORK_EDIT.blocked_member}</span>
          {mayResolve && props.onFixCondition && (
            <button type="button" className="btn btn--sm" onClick={props.onFixCondition} disabled={props.busy} data-testid="work-fix-condition">
              {WORK_EDIT.fix_condition}
            </button>
          )}
        </div>
      )}
      <h3 className="aside__h">{WORK_PANEL.cost}</h3>
      <p className="aside__cost" data-testid="work-cost">
        {WORK_PANEL.cost_this}${work.cost_usd.toFixed(2)}
        {limit != null ? ` / $${limit}` : ""}
        {pct != null ? ` (${pct}%)` : ""}
        {work.cost_estimated && <span className="aside__badge">{WORK_PANEL.estimated}</span>}
      </p>
      <p className="aside__quiet" data-testid="work-assignee">
        {work.assignee_agent_id ? <>{WORK_PANEL.assignee}@{props.agentName?.(work.assignee_agent_id) ?? "agent"}</> : WORK_PANEL.no_assignee}
      </p>
      {!closed && actions}
    </section>
  );
}

export default WorkPanel;
