"use client";
/**
 * 미션 칸의 두 다이얼로그(SCREEN v0.19.2 §4.6 우열 (가) 「미션 동작」 · §2.3 미션 층, T-R2-W4b).
 *
 *  - `FixWorkConditionDialog` — 「조건 고치기」. 리뷰어 없는 `agent_approval` 처럼 **구조상 충족될 수 없는** 조건(`blocked_reason`)에 걸린 미션을
 *    Director 가 구하는 길. 편집기는 S21 미션 열기·설정 편집과 **같은** `ConditionEditor` 다(두 자리가 다른 편집기를 가지면 한쪽에서만
 *    리뷰어를 잊는다). `updateWork` 에 `completion_condition` 만 보낸다 — `active`·`paused` 에서도 된다(계약).
 *  - `ChangeDirectorDialog` — 「Director 교체」(`changeWorkDirector`). 그 미션의 director · ws owner·admin. deputy 는 「그대로 둡니다」가 기본이라
 *    칸을 건드리지 않으면 본문에 싣지 않는다.
 * 서버가 거절하면(422 errors[] · 403 · 409) 다이얼로그 **안에서** 서버 문장 그대로 말한다.
 */
import { useEffect, useId, useMemo, useState } from "react";
import { ConditionEditor, type ConditionEditorParticipant } from "./ConditionEditor";
import { DisabledHint } from "./PageHead";
import { RoomDialogShell } from "./RoomDialogShell";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { conditionGate, fromCompletionCondition, toCompletionCondition, type ConditionDraft } from "@/lib/completion";
import { CREATE_WORK, personName } from "@/lib/room-dialogs";
import { FIX_CONDITION } from "@/lib/wording";
import { CHANGE_DIRECTOR } from "@/lib/work-edit";
import type { Member, Work } from "@/lib/api/types";

const problemText = (e: unknown) => (isApiError(e) && e.problem.errors?.length ? e.problem.errors.map((x) => x.message).join(" · ") : errorMessage(e));

export interface FixWorkConditionDialogProps {
  work: Pick<Work, "id" | "completion_condition" | "assignee_agent_id">;
  /** 이 방의 참여 에이전트 — 제출자·리뷰어 후보. */
  agents: ConditionEditorParticipant[];
  onSaved: (work: Work) => void;
  onClose: () => void;
}

export function FixWorkConditionDialog({ work, agents, onSaved, onClose }: FixWorkConditionDialogProps) {
  const [draft, setDraft] = useState<ConditionDraft>(() => fromCompletionCondition(work.completion_condition).draft);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const hintId = `${useId()}-hint`;
  const gate = conditionGate(draft, agents.map((a) => a.id));

  async function save() {
    if (!gate.ok || busy) return;
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.patch("/works/{workId}", { path: { workId: work.id }, body: { completion_condition: toCompletionCondition(draft) } }));
      onClose();
    } catch (e) {
      setError(problemText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RoomDialogShell title={FIX_CONDITION.title} sub={FIX_CONDITION.note} testId="fix-work-condition" busy={busy} onClose={onClose}>
      <ConditionEditor value={draft} onChange={setDraft} participants={agents} assigneeId={work.assignee_agent_id} reviewerRequiredText={CREATE_WORK.reviewer_required} />
      {error && <p className="problem confirm-dlg__problem" role="alert" data-testid="fix-work-condition-error">{error}</p>}
      <div className="confirm-dlg__actions">
        <button type="button" className="btn" disabled={busy} onClick={onClose} data-testid="fix-work-condition-cancel">{FIX_CONDITION.cancel}</button>
        <button type="button" className="btn btn--primary" disabled={busy || !gate.ok} aria-describedby={!gate.ok ? hintId : undefined} onClick={() => void save()} data-testid="fix-work-condition-save">
          {busy ? FIX_CONDITION.busy : FIX_CONDITION.save}
        </button>
      </div>
      {!gate.ok && <DisabledHint id={hintId}>{gate.reason}</DisabledHint>}
    </RoomDialogShell>
  );
}

export interface ChangeDirectorDialogProps {
  work: Pick<Work, "id" | "director_user_id" | "director" | "deputy_user_id">;
  /** 워크스페이스 멤버 — 새 Director·deputy 후보(서버: 워크스페이스 멤버여야 한다, 방에 없으면 서버가 참여자로 넣는다). */
  members: Member[];
  onSaved: (work: Work) => void;
  onClose: () => void;
}

const KEEP = "__keep__";

export function ChangeDirectorDialog({ work, members, onSaved, onClose }: ChangeDirectorDialogProps) {
  const [to, setTo] = useState("");
  const [deputy, setDeputy] = useState(KEEP);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const base = useId();
  const people = useMemo(() => members.filter((m) => m.user.id), [members]);
  // 새 Director 가 지금 deputy 면 deputy 칸은 「없음」이 자연스럽다 — 같은 사람이 둘을 겸하지 않게 한 번만 제안한다.
  useEffect(() => {
    if (to && to === work.deputy_user_id) setDeputy((d) => (d === KEEP ? "" : d));
  }, [to, work.deputy_user_id]);
  const why = !to ? CHANGE_DIRECTOR.pick : to === work.director_user_id ? CHANGE_DIRECTOR.same : null;

  async function save() {
    if (why || busy) return;
    setBusy(true);
    setError(null);
    try {
      const body = { director_user_id: to, ...(deputy !== KEEP ? { deputy_user_id: deputy || null } : {}) };
      onSaved(await api.put("/works/{workId}/director", { path: { workId: work.id }, body }));
      onClose();
    } catch (e) {
      setError(problemText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RoomDialogShell title={CHANGE_DIRECTOR.title} sub={CHANGE_DIRECTOR.note} testId="change-director" busy={busy} onClose={onClose}>
      <form className="rd-section" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        <p className="rd-hint" data-testid="change-director-current">{CHANGE_DIRECTOR.current} · {personName(work.director)}</p>
        <div className="rd-fields">
          <label className="rd-field">
            <span className="rd-field__label">{CHANGE_DIRECTOR.director}</span>
            <select className="select" value={to} onChange={(e) => setTo(e.target.value)} data-testid="change-director-to">
              <option value="">{CHANGE_DIRECTOR.choose}</option>
              {people.map((m) => (
                <option key={m.user.id} value={m.user.id}>{personName(m.user)}</option>
              ))}
            </select>
          </label>
          <label className="rd-field">
            <span className="rd-field__label">{CHANGE_DIRECTOR.deputy}</span>
            <select className="select" value={deputy} onChange={(e) => setDeputy(e.target.value)} data-testid="change-director-deputy">
              <option value={KEEP}>{CHANGE_DIRECTOR.deputy_keep}</option>
              <option value="">{CHANGE_DIRECTOR.deputy_none}</option>
              {people.filter((m) => m.user.id !== to).map((m) => (
                <option key={m.user.id} value={m.user.id}>{personName(m.user)}</option>
              ))}
            </select>
            <span className="rd-hint">{CHANGE_DIRECTOR.deputy_note}</span>
          </label>
        </div>
        {error && <p className="problem confirm-dlg__problem" role="alert" data-testid="change-director-error">{error}</p>}
        <div className="confirm-dlg__actions">
          <button type="button" className="btn" disabled={busy} onClick={onClose} data-testid="change-director-cancel">{CHANGE_DIRECTOR.cancel}</button>
          <button type="submit" className="btn btn--primary" disabled={busy || !!why} aria-describedby={why ? `${base}-why` : undefined} data-testid="change-director-save">
            {busy ? CHANGE_DIRECTOR.busy : CHANGE_DIRECTOR.confirm}
          </button>
        </div>
        {why && <DisabledHint id={`${base}-why`}>{why}</DisabledHint>}
      </form>
    </RoomDialogShell>
  );
}
