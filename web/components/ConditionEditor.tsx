"use client";
/**
 * 종료 조건 편집기(T-W15, S-84 · SCREEN §4.4 6단계). S21 미션 열기·설정 편집과 「조건 고치기」 다이얼로그가 **같은 것**을 그린다(S6 마법사 6단계가 S21 로 왔다, T-R2-W4b) —
 * 두 자리가 다른 편집기를 가지면 한쪽에서만 리뷰어를 잊는다.
 *
 * 막는 것은 막고 근처에서 말한다(§8.5): 조건 0개 · `agent_approval` 인데 리뷰어 없음 → `conditionGate` 가 사유를 주고 호출부가
 * 다음/저장 버튼을 비활성으로 둔다. 담당 에이전트를 리뷰어로 고르는 것은 **막지 않고 안내만** 한다(자기 것을 자기가 검토하지 않게).
 */
import { ConditionRow } from "./ConditionRow";
import { COND_ORDER, type CondType, type ConditionDraft, hasHumanGate } from "@/lib/completion";
import { CONDITION_EDITOR } from "@/lib/wording";

export interface ConditionEditorParticipant {
  id: string;
  name: string;
}

export interface ConditionEditorProps {
  value: ConditionDraft;
  onChange: (next: ConditionDraft) => void;
  /** 참여자 — 제출자·리뷰어 후보. */
  participants: ConditionEditorParticipant[];
  /** 담당 에이전트 — 리뷰어로 고르면 안내 한 줄. */
  assigneeId?: string | null;
  /** 리뷰어 필수 문장을 그 자리의 말로(S21 미션 열기는 「세션」이 아니라 방의 말을 쓴다). 비우면 표의 문장. */
  reviewerRequiredText?: string;
}

export function ConditionEditor({ value, onChange, participants, assigneeId, reviewerRequiredText }: ConditionEditorProps) {
  const nameOf = (id: string) => participants.find((p) => p.id === id)?.name ?? id;
  const submitterLabel = value.submitter ? `@${nameOf(value.submitter)}` : CONDITION_EDITOR.submitter_default_short;
  const toggle = (t: CondType, next: boolean) =>
    onChange({ ...value, conds: next ? [...value.conds, t] : value.conds.filter((x) => x !== t) });

  return (
    <div data-testid="condition-editor">
      <div className="row" style={{ marginBottom: 8 }}>
        <span className="small muted">{CONDITION_EDITOR.op_label}</span>
        <select className="select" style={{ width: "auto" }} value={value.op} onChange={(e) => onChange({ ...value, op: e.target.value as "and" | "or" })} data-testid="cond-op">
          <option value="and">{CONDITION_EDITOR.op_and}</option>
          <option value="or">{CONDITION_EDITOR.op_or}</option>
        </select>
      </div>
      <div className="stack" style={{ gap: 6 }}>
        {COND_ORDER.map((t) => (
          <div key={t} className="stack" style={{ gap: 4 }}>
            <ConditionRow
              type={t}
              met={null}
              variant="wizard"
              selected={value.conds.includes(t)}
              who={t === "artifact_submitted" ? submitterLabel : undefined}
              agentName={t === "agent_approval" && value.reviewer ? nameOf(value.reviewer) : undefined}
              onToggle={(next) => toggle(t, next)}
            />
            {/* 제출자를 고를 수 없으면 시나리오 A 3단계(Writer 가 보고서를 제출)를 화면으로 만들 수 없다.
                E6-02 는 "지정 에이전트가 아니면 미충족" 이므로 그 지정이 여기서 나온다. 기본값은 담당 에이전트다. */}
            {t === "artifact_submitted" && value.conds.includes(t) && (
              <label className="row small" style={{ paddingLeft: 26 }}>
                <span className="muted">{CONDITION_EDITOR.submitter}</span>
                <select
                  className="select"
                  style={{ width: "auto" }}
                  value={value.submitter}
                  onChange={(e) => onChange({ ...value, submitter: e.target.value })}
                  data-testid="submitter-select"
                  aria-label={CONDITION_EDITOR.submitter}
                >
                  <option value="">{CONDITION_EDITOR.submitter_default}</option>
                  {participants.map((p) => (
                    <option key={p.id} value={p.id}>@{p.name}</option>
                  ))}
                </select>
              </label>
            )}
            {/* 리뷰어는 **필수**(계약 v0.1.4 — `agent_id` 없는 `agent_approval` 은 422). 없으면 아무도 승인할 수 없어 세션이 영영 안 닫힌다(S-84). */}
            {t === "agent_approval" && value.conds.includes(t) && (
              <div className="stack" style={{ gap: 2, paddingLeft: 26 }}>
                <label className="row small">
                  <span className="muted">{CONDITION_EDITOR.reviewer}</span>
                  <select
                    className="select"
                    style={{ width: "auto" }}
                    value={value.reviewer}
                    onChange={(e) => onChange({ ...value, reviewer: e.target.value })}
                    data-testid="reviewer-select"
                    aria-label={CONDITION_EDITOR.reviewer}
                    aria-invalid={!value.reviewer}
                  >
                    <option value="">{CONDITION_EDITOR.reviewer_placeholder}</option>
                    {participants.map((p) => (
                      <option key={p.id} value={p.id}>@{p.name}{p.id === assigneeId ? ` (${CONDITION_EDITOR.submitter_default_short})` : ""}</option>
                    ))}
                  </select>
                </label>
                {!value.reviewer && <span className="small" style={{ color: "var(--s-wait-text)" }} data-testid="reviewer-required">{reviewerRequiredText ?? CONDITION_EDITOR.reviewer_required}</span>}
                {value.reviewer && value.reviewer === assigneeId && (
                  <span className="small muted" data-testid="reviewer-is-assignee">{CONDITION_EDITOR.reviewer_is_assignee}</span>
                )}
              </div>
            )}
          </div>
        ))}
        <ConditionRow type="criteria_met" met={null} variant="wizard" disabled disabledNote={CONDITION_EDITOR.criteria_met_note} />
      </div>
      {!hasHumanGate(value) && value.conds.length > 0 && (
        <p className="notice" style={{ marginTop: 8 }} data-testid="no-human-gate-warning">⚠ {CONDITION_EDITOR.no_human_gate}</p>
      )}
    </div>
  );
}

export default ConditionEditor;
