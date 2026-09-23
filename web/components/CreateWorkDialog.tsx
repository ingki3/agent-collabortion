"use client";
/**
 * S21 미션 열기(`/rooms/:id?work=new` · `?work=from&message=:mid`, 다이얼로그) — SCREEN v0.19.2 §4.7, T-R2-W3.
 *
 * 마법사 1·2·6·7단계가 여기로 왔다. **다이얼로그 하나이고 필수 칸은 goal 하나**다(FR-2A.1). 나머지는 접혀 있고 기본값이 **문장으로** 보인다
 * — 그 문장이 없으면 W-3·W-4 사고(마법사가 강제로 읽히던 것을 아무도 안 읽는다)가 그대로 난다.
 *   - 제목 바로 아래 회색 한 줄: 미션의 정의(SCR-C H — 세 길 모두 같은 문장).
 *   - 담당 에이전트(비어 있음이 기본) → **종료 조건 문장이 바뀐다**: 있으면 「보고서 제출(@담당) 그리고 Director 승인」, 없으면 「Director 승인」
 *     단독 + 이유 한 줄. 메시지에서 열 때 그 메시지가 멘션한 에이전트가 하나뿐이면 **제시하되 사람이 확인한다**.
 *   - 종료 조건을 바꾸면 S6·S7 과 **같은 편집기**(ConditionEditor) — 「에이전트 검토 승인」은 리뷰어 필수(S-84, 서버 422), 사람 관문이 없으면 경고.
 *   - Director(방 기본값 → 연 사람) · deputy(취소 즉시 · 승인 기한 절반 후) · 예산(「방 한도 $50 중 이 미션에 $20」 + 작은 쪽이 이긴다) ·
 *     자율성(§4.7 문장 표, supervised 는 v1.1 비활성).
 *   - 동시 미션 상한 — **열기 전에** 알린다(열린 미션 목록 + 「방 설정」 링크). 서버 `409 max_concurrent_works` 가 오면 `open_works[]` 를 그린다.
 *   - 보관된 방 · 참여자 아님 · 멈춘 방은 전체 비활성 + 사유(층을 적는다).
 * 세 길(새 미션 · 메시지에서 · 에이전트 제안)이 같은 다이얼로그다 — 제안 길은 `resolveWorkProposal accept` 로 연다(연 사람이 Director).
 * 「열기」 → `onOpened(work)` — 호출부(S7)가 그 미션 칩을 고른 채 방 화면으로 돌아간다.
 *
 * **편집 모드**(`mode: "edit"`, T-R2-W4b — §4.6 미션 동작 「설정 편집」)도 이 폼이다: 지금 값으로 채워 열고, 바꾼 칸만 `updateWork` 로 보낸다.
 * 종료 조건 기본값 규칙(담당이 바뀌면 조건이 따라 바뀜)은 새 미션에만 — 편집에서는 사람이 편집기를 연 경우에만 조건을 보낸다.
 * Director 는 여기서 바꾸지 않는다(교체는 별도 op `changeWorkDirector` — 미션 칸의 「Director 교체」). 동시 상한·방 멈춤은 여는 일의 관문이라
 * 편집에는 걸지 않고, 권한(그 미션의 director)·끝난 미션만 막는다(`workEditBlocked`).
 */
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ConditionEditor } from "./ConditionEditor";
import { DisabledHint } from "./PageHead";
import { RoomDialogShell, useRoomEvents } from "./RoomDialogShell";
import { Slot } from "./Slot";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { draftNames, fromCompletionCondition, toCompletionCondition, type ConditionDraft } from "@/lib/completion";
import { conditionSentence } from "@/lib/wording";
import {
  AUTONOMY_TEXT, COMMON, CREATE_WORK, defaultConditionTypes, firstLine, maxConcurrentWorks, openWorkCount, openWorkGate, personName, RUNNING_WORK,
  suggestedAssignee, type RoomParticipant, type Work, type WorkCreate, type WorkProposal,
} from "@/lib/room-dialogs";
import { pageItems, type AutonomyLevel, type Member, type Message, type Room, type WorkListItem } from "@/lib/api/types";
import { WORK_EDIT, workEditBlocked } from "@/lib/work-edit";

export type CreateWorkMode = "new" | "from" | "proposal" | "edit";

export interface CreateWorkDialogProps {
  roomId: string;
  mode: CreateWorkMode;
  /** 「이걸 미션으로」의 원 메시지 id(`?work=from&message=`). */
  messageId?: string | null;
  /** 호출부가 이미 가진 원 메시지 — 있으면 다시 부르지 않는다. */
  message?: Message | null;
  /** 제안 길(S26) — goal 이 채워진 채로 연다. */
  proposal?: WorkProposal | null;
  /** 「이대로 열기」 — goal 을 고칠 수 없다(「고쳐서 열기」면 false). */
  goalLocked?: boolean;
  /** 편집 모드(`mode: "edit"`)의 미션 — 지금 값으로 칸을 채운다. */
  work?: Work | null;
  onOpened: (work: Work) => void;
  onClose: () => void;
}

const WORK_EVENTS = ["work.created", "work.updated", "work.closed", "work.deleted", "participant.joined", "participant.left", "room.updated"] as const;

export function CreateWorkDialog({ roomId, mode, messageId, message: given, proposal, goalLocked = false, work: editing = null, onOpened, onClose }: CreateWorkDialogProps) {
  const edit = mode === "edit" && !!editing;
  const { me, workspace } = useAuth();
  const [room, setRoom] = useState<Room | null>(null);
  const [agents, setAgents] = useState<RoomParticipant[]>([]);
  const [works, setWorks] = useState<WorkListItem[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [message, setMessage] = useState<Message | null>(given ?? null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [goal, setGoal] = useState(editing?.goal ?? proposal?.goal ?? given?.content ?? "");
  const [title, setTitle] = useState(editing?.title ?? "");
  const [criteria, setCriteria] = useState<string[]>(editing?.acceptance_criteria ?? []);
  const [assignee, setAssignee] = useState(editing?.assignee_agent_id ?? "");
  const [suggested, setSuggested] = useState<string | null>(null);
  const [draft, setDraft] = useState<ConditionDraft>(() =>
    editing ? fromCompletionCondition(editing.completion_condition).draft : { op: "and", conds: defaultConditionTypes(false), submitter: "", reviewer: "" });
  const [condTouched, setCondTouched] = useState(false);
  const [editCond, setEditCond] = useState(false);
  const [director, setDirector] = useState("");
  const [deputy, setDeputy] = useState(editing?.deputy_user_id ?? "");
  const [budget, setBudget] = useState(editing?.limits?.budget_usd != null ? String(editing.limits.budget_usd) : "");
  const [time, setTime] = useState(editing?.limits?.time_limit ?? "");
  const [autonomy, setAutonomy] = useState<AutonomyLevel | "">(editing?.autonomy ?? "");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [serverOpen, setServerOpen] = useState<{ id: string; title: string }[] | null>(null);
  const idem = useRef(newIdempotencyKey());
  const goalRef = useRef<HTMLTextAreaElement>(null);
  const base = useId();

  const load = useCallback(async () => {
    try {
      const [r, parts, ws] = await Promise.all([
        api.get("/rooms/{roomId}", { path: { roomId } }),
        api.get("/rooms/{roomId}/participants", { path: { roomId } }),
        api.get("/rooms/{roomId}/works", { path: { roomId } }).catch(() => ({ items: [] as WorkListItem[] })),
      ]);
      setRoom(r);
      setAgents(parts.items.filter((p) => p.kind === "agent" && p.agent));
      setWorks(pageItems<WorkListItem>(ws));
      setLoadError(null);
    } catch (e) {
      setLoadError(errorMessage(e));
    }
  }, [roomId]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!workspace) return;
    api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id }, query: { limit: 100 } }).then((p) => setMembers(p.items ?? []), () => setMembers([]));
  }, [workspace]);
  // 원 메시지 — goal 기본값과 인용. 호출부(S7)가 넘기는 것이 정본이다: 서버 `getMessage` 는 아직 501(unimplemented.go)이라
  // 주소로만 들어온 경우(`?work=from&message=`)에만 불러 보고, 못 받으면 인용 없이 연다(원 메시지 귀속은 서버가 `from_message_id` 로 한다).
  useEffect(() => {
    if (mode !== "from" || given || !messageId) return;
    api.get("/messages/{messageId}", { path: { messageId } }).then(
      (m) => {
        setMessage(m);
        setGoal((g) => g || m.content);
      },
      () => undefined,
    );
  }, [mode, given, messageId]);
  // S7 이 메시지를 늦게 받아 넘기는 경우(타임라인 로딩 뒤) — 비어 있을 때만 채운다.
  useEffect(() => {
    if (!given) return;
    setMessage((m) => m ?? given);
    setGoal((g) => g || given.content);
  }, [given]);
  // 메시지가 멘션한 에이전트가 하나뿐이면 제시한다 — 한 번만(사람이 비우면 다시 채우지 않는다).
  const suggestedOnce = useRef(false);
  useEffect(() => {
    if (suggestedOnce.current || mode !== "from" || !message || agents.length === 0) return;
    suggestedOnce.current = true;
    const s = suggestedAssignee(message.mentions, agents.map((a) => a.agent!.id));
    if (s) {
      setSuggested(s);
      setAssignee(s);
    }
  }, [mode, message, agents]);
  useEffect(() => {
    if (!goalLocked) goalRef.current?.focus();
  }, [goalLocked]);
  useRoomEvents(workspace?.id, roomId, WORK_EVENTS, () => void load());

  // 담당이 바뀌면 종료 조건 기본값이 바뀐다(FR-2A.1) — 사람이 조건을 직접 고친 뒤에는 건드리지 않는다.
  useEffect(() => {
    if (!condTouched && !edit) setDraft((d) => ({ ...d, conds: defaultConditionTypes(!!assignee) }));
  }, [assignee, condTouched, edit]);

  const nameOf = useCallback((id: string) => agents.find((a) => a.agent!.id === id)?.agent?.name ?? id, [agents]);
  const condNames = useMemo(() => draftNames({ ...draft, submitter: draft.submitter || assignee }, (id) => `${nameOf(id)}`), [draft, assignee, nameOf]);
  const sentence = conditionSentence(condNames, draft.op);

  const editWhy = edit ? workEditBlocked(editing!) : null;
  const gate = edit ? (editWhy ? { ok: false as const, reason: editWhy } : { ok: true as const }) : room ? openWorkGate(room) : { ok: true as const };
  const open = openWorkCount(works);
  const cap = room ? maxConcurrentWorks(room) : 3;
  const atCap = !edit && !!room && open >= cap;
  const openList = serverOpen ?? works.filter((w) => RUNNING_WORK.includes(w.status)).map((w) => ({ id: w.id, title: w.title }));
  const condReason = draft.conds.length === 0 ? CREATE_WORK.need_condition : draft.conds.includes("agent_approval") && !draft.reviewer ? CREATE_WORK.reviewer_required : null;
  const goalEmpty = !goal.trim();
  const blockedReason = !gate.ok ? gate.reason : goalEmpty ? CREATE_WORK.goal_required : condReason;
  const canOpen = !blockedReason && !atCap;
  const gateHint = `${base}-gate`;
  const openHint = gate.ok ? `${base}-open-hint` : gateHint;
  const capId = `${base}-cap`;
  const defaultDirectorName = room?.default_director_user_id ? personName(members.find((m) => m.user.id === room.default_director_user_id)?.user) : null;

  /** 편집 — 바꾼 칸만(서버는 보낸 칸만 고친다). 한도는 두 칸을 함께 보내고 비운 칸은 null(방 한도를 따른다). */
  function editBody(w: Work) {
    const b: Record<string, unknown> = {};
    if (goal.trim() !== w.goal) b.goal = goal.trim();
    if (title.trim() && title.trim() !== w.title) b.title = title.trim();
    const crit = criteria.map((c) => c.trim()).filter(Boolean);
    if (JSON.stringify(crit) !== JSON.stringify(w.acceptance_criteria)) b.acceptance_criteria = crit;
    if ((assignee || null) !== (w.assignee_agent_id ?? null)) b.assignee_agent_id = assignee || null;
    if (condTouched) b.completion_condition = toCompletionCondition(draft);
    if ((deputy || null) !== (w.deputy_user_id ?? null)) b.deputy_user_id = deputy || null;
    const budgetNow = budget.trim() ? Number(budget) : null;
    const timeNow = time.trim() || null;
    if (budgetNow !== (w.limits?.budget_usd ?? null) || timeNow !== (w.limits?.time_limit ?? null)) b.limits = { budget_usd: budgetNow, time_limit: timeNow };
    if (autonomy && autonomy !== w.autonomy) b.autonomy = autonomy;
    return b;
  }

  async function submit() {
    if (!canOpen || busy) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    if (edit) {
      try {
        const b = editBody(editing!);
        onOpened(Object.keys(b).length ? await api.patch("/works/{workId}", { path: { workId: editing!.id }, body: b }) : editing!);
      } catch (e) {
        if (isApiError(e) && e.problem.errors?.length) setFieldErrors(Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message])));
        setError(errorMessage(e));
      } finally {
        setBusy(false);
      }
      return;
    }
    const body: WorkCreate = {
      goal: goal.trim(),
      ...(title.trim() ? { title: title.trim() } : {}),
      acceptance_criteria: criteria.map((c) => c.trim()).filter(Boolean),
      assignee_agent_id: assignee || null,
      ...(condTouched ? { completion_condition: toCompletionCondition(draft) } : {}),
      ...(director ? { director_user_id: director } : {}),
      ...(deputy ? { deputy_user_id: deputy } : {}),
      ...(budget.trim() || time.trim() ? { limits: { ...(budget.trim() ? { budget_usd: Number(budget) } : {}), ...(time.trim() ? { time_limit: time.trim() } : {}) } } : {}),
      ...(autonomy ? { autonomy } : {}),
      ...(mode === "from" && (messageId ?? message?.id) ? { from_message_id: (messageId ?? message?.id)! } : {}),
    };
    try {
      if (mode === "proposal" && proposal) {
        const res = await api.post("/work-proposals/{workProposalId}/resolution", { path: { workProposalId: proposal.id }, body: { action: "accept", work: body }, idempotencyKey: idem.current });
        if (res.work) onOpened(res.work);
      } else {
        onOpened(await api.post("/rooms/{roomId}/works", { path: { roomId }, body, idempotencyKey: idem.current }));
      }
    } catch (e) {
      if (isApiError(e)) {
        if (e.problem.errors?.length) setFieldErrors(Object.fromEntries(e.problem.errors.map((x) => [x.field, x.message])));
        const ow = (e.problem as { open_works?: { id: string; title: string }[] }).open_works;
        if (e.code === "max_concurrent_works" && Array.isArray(ow)) setServerOpen(ow);
      }
      setError(errorMessage(e));
      idem.current = newIdempotencyKey();
    } finally {
      setBusy(false);
    }
  }

  const titleText = edit ? WORK_EDIT.title : mode === "from" ? CREATE_WORK.title_from : mode === "proposal" ? CREATE_WORK.title_proposal : CREATE_WORK.title_new;
  const fieldErr = (k: string) => fieldErrors[k];
  const condErr = Object.entries(fieldErrors).find(([k]) => k.startsWith("completion_condition"))?.[1];

  return (
    <RoomDialogShell title={titleText} sub={edit ? WORK_EDIT.sub : CREATE_WORK.definition} testId={edit ? "rd-edit-work" : "rd-create-work"} onClose={onClose} busy={busy}>
      {loadError && <p className="problem" role="alert">{loadError}</p>}
      {!gate.ok && <DisabledHint id={gateHint}>{gate.reason}</DisabledHint>}
      <form
        className="rd-section"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={!gate.ok || busy} style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }} className="rd-section">
          {mode === "from" && message && (
            <div className="rd-section" data-testid="rd-create-work-quote">
              <span className="rd-field__label">{CREATE_WORK.quoted}</span>
              <blockquote className="rd-quote">{message.content}</blockquote>
              <p className="rd-hint">{CREATE_WORK.quoted_note}</p>
            </div>
          )}
          <label className="rd-field">
            <span className="rd-field__label">{CREATE_WORK.goal}</span>
            <textarea
              ref={goalRef}
              className="textarea"
              rows={3}
              value={goal}
              readOnly={goalLocked}
              placeholder={CREATE_WORK.goal_placeholder}
              aria-invalid={!!fieldErr("goal") || undefined}
              onChange={(e) => setGoal(e.target.value)}
              data-testid="rd-create-work-goal"
            />
            {fieldErr("goal") && <span className="rd-err">{fieldErr("goal")}</span>}
          </label>

          <div className="rd-section">
            <label className="rd-field">
              <span className="rd-field__label">{CREATE_WORK.assignee}</span>
              {agents.length === 0 ? (
                <span className="rd-hint" data-testid="rd-create-work-no-agents">
                  {CREATE_WORK.no_agents} · <Link href={`/rooms/${roomId}/participants`}>{CREATE_WORK.no_agents_link}</Link>
                </span>
              ) : (
                <select className="select" value={assignee} onChange={(e) => (setAssignee(e.target.value), setSuggested(null))} data-testid="rd-create-work-assignee">
                  <option value="">{CREATE_WORK.assignee_none}</option>
                  {agents.map((a) => (
                    <option key={a.agent!.id} value={a.agent!.id}>@{a.agent!.name}</option>
                  ))}
                </select>
              )}
            </label>
            {suggested && suggested === assignee && <p className="rd-warn" data-testid="rd-create-work-suggested">{CREATE_WORK.assignee_suggested}</p>}
            <p className="rd-hint">{CREATE_WORK.assignee_hint}</p>
            {fieldErr("assignee_agent_id") && <span className="rd-err">{fieldErr("assignee_agent_id")}</span>}
          </div>

          <div className="rd-section" data-testid="rd-create-work-condition">
            <span className="rd-field__label">{CREATE_WORK.condition}</span>
            <p className="rd-cond" data-testid="rd-create-work-condition-sentence">{sentence}</p>
            {!assignee && !condTouched && !edit && <p className="rd-hint" data-testid="rd-create-work-condition-why">{CREATE_WORK.condition_default_hint}</p>}
            {editCond ? (
              <ConditionEditor
                value={draft}
                onChange={(d) => (setDraft(d), setCondTouched(true))}
                participants={agents.map((a) => ({ id: a.agent!.id, name: a.agent!.name }))}
                assigneeId={assignee || null}
                reviewerRequiredText={CREATE_WORK.reviewer_required}
              />
            ) : (
              <div>
                <button type="button" className="btn btn--sm btn--ghost" onClick={() => setEditCond(true)} data-testid="rd-create-work-condition-edit">
                  {CREATE_WORK.condition_edit}
                </button>
              </div>
            )}
            {condErr && <span className="rd-err">{condErr}</span>}
          </div>

          <details className="rd-fold" open={edit || undefined} data-testid="rd-create-work-more">
            <summary>{CREATE_WORK.defaults}</summary>
            <div className="rd-fold__body">
              <label className="rd-field">
                <span className="rd-field__label">{CREATE_WORK.title}</span>
                <input className="input" value={title} maxLength={200} placeholder={firstLine(goal) || CREATE_WORK.title_placeholder} onChange={(e) => setTitle(e.target.value)} data-testid="rd-create-work-name" />
                {fieldErr("title") && <span className="rd-err">{fieldErr("title")}</span>}
              </label>
              <div className="rd-criteria">
                <span className="rd-field__label">{CREATE_WORK.criteria}</span>
                {criteria.map((c, i) => (
                  <div key={i} className="rd-criteria__row">
                    <input className="input" value={c} placeholder={CREATE_WORK.criteria_placeholder} onChange={(e) => setCriteria((xs) => xs.map((x, j) => (j === i ? e.target.value : x)))} data-testid="rd-create-work-criterion" />
                    <button type="button" className="btn btn--sm btn--ghost" onClick={() => setCriteria((xs) => xs.filter((_, j) => j !== i))}>{CREATE_WORK.criteria_remove}</button>
                  </div>
                ))}
                <div>
                  <button type="button" className="btn btn--sm" onClick={() => setCriteria((xs) => [...xs, ""])} data-testid="rd-create-work-criteria-add">{CREATE_WORK.criteria_add}</button>
                </div>
              </div>
              <div className="rd-fields">
                {edit ? (
                  <div className="rd-field" data-testid="rd-edit-work-director">
                    <span className="rd-field__label">{CREATE_WORK.director}</span>
                    <span>{personName(editing!.director)}</span>
                    <span className="rd-hint">{WORK_EDIT.director_elsewhere}</span>
                  </div>
                ) : (
                <label className="rd-field">
                  <span className="rd-field__label">{CREATE_WORK.director}</span>
                  <select className="select" value={director} onChange={(e) => setDirector(e.target.value)} data-testid="rd-create-work-director">
                    <option value="">{defaultDirectorName ? `${CREATE_WORK.director_room_default} · ${defaultDirectorName}` : `${CREATE_WORK.director_me} · ${personName(me?.user)}`}</option>
                    {members.map((m) => (
                      <option key={m.user.id} value={m.user.id}>{personName(m.user)}</option>
                    ))}
                  </select>
                  <span className="rd-hint">{CREATE_WORK.director_note}</span>
                  {fieldErr("director_user_id") && <span className="rd-err">{fieldErr("director_user_id")}</span>}
                </label>
                )}
                <label className="rd-field">
                  <span className="rd-field__label">{CREATE_WORK.deputy}</span>
                  <select className="select" value={deputy} onChange={(e) => setDeputy(e.target.value)} data-testid="rd-create-work-deputy">
                    <option value="">{CREATE_WORK.deputy_none}</option>
                    {members.map((m) => (
                      <option key={m.user.id} value={m.user.id}>{personName(m.user)}</option>
                    ))}
                  </select>
                  <span className="rd-hint">{CREATE_WORK.deputy_note}</span>
                </label>
                <label className="rd-field">
                  <span className="rd-field__label">{CREATE_WORK.budget}</span>
                  <input className="input" inputMode="decimal" value={budget} placeholder={CREATE_WORK.budget_placeholder} onChange={(e) => setBudget(e.target.value)} data-testid="rd-create-work-budget" />
                  {fieldErr("limits/budget_usd") && <span className="rd-err">{fieldErr("limits/budget_usd")}</span>}
                </label>
                <label className="rd-field">
                  <span className="rd-field__label">{CREATE_WORK.time}</span>
                  <input className="input" value={time} placeholder="PT4H" onChange={(e) => setTime(e.target.value)} data-testid="rd-create-work-time" />
                  {fieldErr("limits/time_limit") && <span className="rd-err">{fieldErr("limits/time_limit")}</span>}
                </label>
              </div>
              <p className="rd-hint" data-testid="rd-create-work-budget-line">
                {room?.limits?.budget_usd != null ? <Slot text={CREATE_WORK.budget_room} n={`$${room.limits.budget_usd}`} /> : CREATE_WORK.budget_room_none}
                {budget.trim() && <Slot text={CREATE_WORK.budget_work} n={`$${budget.trim()}`} />}
                {" — "}
                {CREATE_WORK.smaller_wins}
              </p>
              <div className="rd-section">
                <span className="rd-field__label">{CREATE_WORK.autonomy}</span>
                {(["guided", "autonomous", "supervised"] as const).map((a) => {
                  const v11 = a === "supervised";
                  const current = autonomy || room?.autonomy || "guided";
                  return (
                    <label key={a} className="rd-choice" aria-disabled={v11 || undefined}>
                      <input type="radio" name={`${base}-aut`} checked={current === a} disabled={v11} onChange={() => setAutonomy(a)} data-testid={`rd-create-work-autonomy-${a}`} />
                      <span className="rd-choice__text">
                        <span className="rd-choice__label">
                          {AUTONOMY_TEXT[a].label}
                          {v11 && <span className="rd-v11">{AUTONOMY_TEXT.next_version}</span>}
                        </span>
                        <span className="rd-choice__note">{AUTONOMY_TEXT[a].note}</span>
                      </span>
                    </label>
                  );
                })}
              </div>
            </div>
          </details>

          {(atCap || serverOpen) && (
            <div className="notice" role="status" id={capId} data-testid="rd-create-work-cap">
              <p className="confirm-dlg__p">
                <Slot text={CREATE_WORK.limit_reached} n={serverOpen?.length ?? open} />
                <Slot text={CREATE_WORK.limit_cap} n={cap} /> · <Link href={`/rooms/${roomId}/settings`}>{CREATE_WORK.limit_link}</Link>
              </p>
              {openList.length > 0 && (
                <>
                  <p className="rd-hint">{CREATE_WORK.open_works}</p>
                  <ul className="rd-list" data-testid="rd-create-work-open-works">
                    {openList.map((w) => (
                      <li key={w.id} className="rd-row">
                        <Link href={`/rooms/${roomId}?work=${w.id}`}>{w.title}</Link>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </div>
          )}
        </fieldset>
        {error && !Object.keys(fieldErrors).length && !serverOpen && <p className="problem confirm-dlg__problem" role="alert" data-testid="rd-create-work-error">{error}</p>}
        <div className="confirm-dlg__actions">
          <button type="button" className="btn" disabled={busy} onClick={onClose} data-testid="rd-create-work-cancel">
            {COMMON.cancel}
          </button>
          <button
            type="submit"
            className="btn btn--primary"
            aria-disabled={!canOpen || undefined}
            aria-describedby={!canOpen ? (blockedReason ? openHint : capId) : undefined}
            disabled={busy}
            data-testid="rd-create-work-open"
          >
            {edit ? (busy ? WORK_EDIT.saving : WORK_EDIT.save) : busy ? CREATE_WORK.opening : CREATE_WORK.open}
          </button>
        </div>
        {blockedReason && gate.ok && <DisabledHint id={openHint}>{blockedReason}</DisabledHint>}
      </form>
    </RoomDialogShell>
  );
}

export default CreateWorkDialog;
