"use client";
/**
 * 「조건 고치기」 다이얼로그(T-W15, S-84 · SCREEN §4.5 "종료 조건 진행률"). 리뷰어 없는 `agent_approval` 같은 **구조상 충족될 수 없는**
 * 조건에 걸린 세션을 Director 가 구하는 길 — updateSession `completion_condition` 은 v0.1.4 부터 `active`·`paused` 에서도 된다.
 *
 * 편집기는 마법사 6단계와 같은 `ConditionEditor` 다. 저장하면 서버가 진행률을 다시 계산해 `session.completion_progress` 를 보내고,
 * 응답 본문(Session)으로 호출부가 즉시 갱신한다. 이미 충족된 원자는 그대로 유지된다(계약).
 * 서버가 거절하면(422 `reviewer_required`·`reviewer_not_participant` · 403) 다이얼로그 **안에서** 서버 문장 그대로 말한다.
 */
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { conditionGate, fromCompletionCondition, toCompletionCondition, type ConditionDraft } from "@/lib/completion";
import { FIX_CONDITION } from "@/lib/wording";
import type { Session } from "@/lib/api/types";
import { ConditionEditor, type ConditionEditorParticipant } from "./ConditionEditor";
import { DisabledHint } from "./PageHead";
import "./fix-condition-dialog.css";

export interface FixConditionDialogProps {
  session: Pick<Session, "id" | "completion_condition" | "assignee_agent_id" | "participants">;
  onSaved: (session: Session) => void;
  onClose: () => void;
}

export function FixConditionDialog({ session, onSaved, onClose }: FixConditionDialogProps) {
  const participants: ConditionEditorParticipant[] = useMemo(
    () => (session.participants ?? []).map((p) => ({ id: p.agent_id, name: p.agent.name })),
    [session.participants],
  );
  const [draft, setDraft] = useState<ConditionDraft>(() => fromCompletionCondition(session.completion_condition).draft);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const titleId = `${useId()}-title`;
  const hintId = `${useId()}-hint`;
  const gate = conditionGate(draft, participants.map((p) => p.id));

  useEffect(() => {
    cancelRef.current?.focus();
  }, []);

  async function save() {
    if (!gate.ok) return;
    setBusy(true);
    setError(null);
    try {
      const s = await api.patch("/sessions/{sessionId}", {
        path: { sessionId: session.id },
        body: { completion_condition: toCompletionCondition(draft) },
      });
      onSaved(s);
      onClose();
    } catch (e) {
      // 422 errors[] 는 칸 이름 없이 문장만 — 어느 조건인지는 편집기가 이미 보이고 있다.
      if (isApiError(e) && e.problem.errors?.length) setError(e.problem.errors.map((x) => x.message).join(" · "));
      else setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fix-cond__scrim"
      role="presentation"
      onClick={(e) => e.target === e.currentTarget && !busy && onClose()}
      onKeyDown={(e) => e.key === "Escape" && !busy && onClose()}
    >
      <div className="fix-cond" role="dialog" aria-modal="true" aria-labelledby={titleId} data-testid="fix-condition-dialog">
        <h2 className="fix-cond__title" id={titleId}>{FIX_CONDITION.title}</h2>
        <p className="fix-cond__note">{FIX_CONDITION.note}</p>
        <ConditionEditor value={draft} onChange={setDraft} participants={participants} assigneeId={session.assignee_agent_id} />
        {error && <p className="problem fix-cond__problem" role="alert" data-testid="fix-condition-error">{error}</p>}
        <div className="fix-cond__actions">
          {!gate.ok && <DisabledHint id={hintId}>{gate.reason}</DisabledHint>}
          <button ref={cancelRef} type="button" className="btn" disabled={busy} onClick={onClose} data-testid="fix-condition-cancel">
            {FIX_CONDITION.cancel}
          </button>
          <button
            type="button"
            className="btn btn--primary"
            disabled={busy || !gate.ok}
            aria-describedby={!gate.ok ? hintId : undefined}
            onClick={() => void save()}
            data-testid="fix-condition-save"
          >
            {busy ? FIX_CONDITION.busy : FIX_CONDITION.save}
          </button>
        </div>
      </div>
    </div>
  );
}

export default FixConditionDialog;
