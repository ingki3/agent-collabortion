"use client";
/**
 * S19 참여자 초대·퇴장(`/rooms/:id/participants`, 다이얼로그) — SCREEN v0.19.2 §4.10, T-R2-W3.
 *
 * **사람과 에이전트를 한 다이얼로그에서** 다룬다(`room_participant` 한 표). 두 구역:
 *   - 지금 있는 사람·에이전트 — 각 행 「내보내기」, **본인 행에는 「이 방에서 나가기」**(FR-2.2). 사람 행의 「…」에 부방장 지정·해제.
 *     거부 규칙(서버 409 `is_owner`·`is_director`)은 **누르기 전에** 말한다 — 방장이면 「먼저 방장을 넘기세요」 + 방 설정 링크,
 *     열린 미션의 Director 면 그 미션 이름(목록 `listWorks` 로 안다). 서버가 그래도 거절하면 서버 문장 그대로.
 *   - 초대하기 — 검색 한 칸 + 두 탭(사람 / 에이전트). 에이전트 탭 머리 한 줄(지난 대화 전부를 읽는다), `respond_to` 로 못 부르는 행은
 *     비활성 + 사유, 방 컴퓨터에 없는 종류의 프로파일이면 경고(컴퓨터가 아직 없으면 안내).
 * 권한 밖이면 **읽기 전용**으로 열린다 — 누가 있는지는 참여자 전원이 봐야 한다. 읽기 전용이어도 본인의 「이 방에서 나가기」는 활성이다.
 * 비활성은 숨기지 않고 `DisabledHint` + `aria-describedby`(툴팁 아님, §5).
 *
 * 실시간: `participant.joined`·`participant.left`(다른 사람이 동시에 초대·퇴장) · `room.updated`(보관·방장 바뀜).
 */
import { useCallback, useEffect, useId, useMemo, useState } from "react";
import Link from "next/link";
import { ConfirmDialog } from "./ConfirmDialog";
import { DisabledHint } from "./PageHead";
import { RoomDialogShell, useRoomEvents } from "./RoomDialogShell";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { relativeTime } from "@/lib/time";
import {
  agentInviteGate, COMMON, deputyGate, inviteGate, kindName, PARTICIPANTS, personName, removeGate, ROOM_ROLE_LABEL, runtimeKindNote,
  type RoomParticipant,
} from "@/lib/room-dialogs";
import { pageItems, type Agent, type Member, type Room, type WorkListItem } from "@/lib/api/types";

export interface RoomParticipantsDialogProps {
  roomId: string;
  onClose: () => void;
  /** 이 방에서 나간 뒤 — 호출부가 방 목록으로 보낸다(나간 방은 `invited` 면 더는 안 보인다). */
  onLeft?: () => void;
}

type Tab = "people" | "agents";
type Confirm = { kind: "leave"; row: RoomParticipant } | { kind: "remove"; row: RoomParticipant };

const PARTICIPANT_EVENTS = ["participant.joined", "participant.left", "participant.updated", "room.updated"] as const;

export function RoomParticipantsDialog({ roomId, onClose, onLeft }: RoomParticipantsDialogProps) {
  const { me, workspace } = useAuth();
  const meId = me?.user.id ?? "";
  const [room, setRoom] = useState<Room | null>(null);
  const [rows, setRows] = useState<RoomParticipant[] | null>(null);
  const [works, setWorks] = useState<WorkListItem[]>([]);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("people");
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [rowError, setRowError] = useState<{ id: string; text: string } | null>(null);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const [profilePick, setProfilePick] = useState<Record<string, string>>({});
  const [warnings, setWarnings] = useState<Record<string, string>>({});
  const base = useId();

  const load = useCallback(async () => {
    try {
      const [r, parts, ws] = await Promise.all([
        api.get("/rooms/{roomId}", { path: { roomId } }),
        api.get("/rooms/{roomId}/participants", { path: { roomId } }),
        api.get("/rooms/{roomId}/works", { path: { roomId } }).catch(() => ({ items: [] as WorkListItem[] })),
      ]);
      setRoom(r);
      setRows(parts.items);
      setWorks(pageItems<WorkListItem>(ws));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!workspace) return;
    const w = workspace.id;
    api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: w }, query: { limit: 100 } }).then((p) => setMembers(p.items ?? []), () => setMembers([]));
    api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: w } }).then((p) => setAgents(p.items ?? []), () => setAgents([]));
  }, [workspace]);
  useRoomEvents(workspace?.id, roomId, PARTICIPANT_EVENTS, () => void load());

  const invite = room ? inviteGate(room) : { ok: false as const, reason: "" };
  const deputy = room ? deputyGate(room) : { ok: false as const, reason: "" };
  const here = useMemo(() => new Set((rows ?? []).map((p) => (p.kind === "user" ? p.user?.id : p.agent?.id)).filter(Boolean) as string[]), [rows]);
  const needle = q.trim().toLowerCase();
  const match = (...xs: (string | null | undefined)[]) => !needle || xs.some((x) => x?.toLowerCase().includes(needle));
  const candidatesPeople = (members ?? []).filter((m) => !here.has(m.user.id) && match(m.user.display_name, m.user.email));
  const candidatesAgents = (agents ?? []).filter((a) => !here.has(a.id) && match(a.name, a.role_description));

  async function addPerson(userId: string) {
    setBusy(`invite:${userId}`);
    setRowError(null);
    try {
      await api.post("/rooms/{roomId}/participants", { path: { roomId }, body: { user_id: userId }, idempotencyKey: newIdempotencyKey() });
      await load();
    } catch (e) {
      setRowError({ id: userId, text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }
  async function addAgent(a: Agent) {
    setBusy(`invite:${a.id}`);
    setRowError(null);
    try {
      const profileId = profilePick[a.id] ?? (a.profiles.find((p) => p.is_default) ?? a.profiles[0])?.id;
      const res = (await api.post("/rooms/{roomId}/participants", { path: { roomId }, body: { agent_id: a.id, ...(profileId ? { profile_id: profileId } : {}) }, idempotencyKey: newIdempotencyKey() })) as RoomParticipant & { warnings?: string[] };
      // 서버 경고(`runtime_kind_missing`)는 거부가 아니다 — 초대한 행에 문장으로 남긴다.
      if (res.warnings?.includes("runtime_kind_missing") && room) {
        const kind = a.profiles.find((p) => p.id === profileId)?.runtime_kind;
        const note = runtimeKindNote(room, kind);
        if (note) setWarnings((w) => ({ ...w, [a.id]: note.text }));
      }
      await load();
    } catch (e) {
      setRowError({ id: a.id, text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }
  async function setDeputy(userId: string | null) {
    setBusy(`deputy:${userId ?? "none"}`);
    setRowError(null);
    try {
      await api.put("/rooms/{roomId}/deputy", { path: { roomId }, body: { user_id: userId } });
      await load();
    } catch (e) {
      setRowError({ id: userId ?? "deputy", text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }
  async function changeProfile(row: RoomParticipant, profileId: string) {
    setBusy(`profile:${row.id}`);
    setRowError(null);
    try {
      await api.patch("/rooms/{roomId}/participants/{participantId}", { path: { roomId, participantId: row.id }, body: { profile_id: profileId } });
      await load();
    } catch (e) {
      setRowError({ id: row.id, text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  }
  async function doRemove(c: Confirm) {
    setBusy(`remove:${c.row.id}`);
    setConfirmError(null);
    try {
      await api.delete("/rooms/{roomId}/participants/{participantId}", { path: { roomId, participantId: c.row.id } });
      setConfirm(null);
      if (c.kind === "leave") {
        onLeft?.();
        return;
      }
      await load();
    } catch (e) {
      setConfirmError(errorMessage(e));
    } finally {
      setBusy(null);
    }
  }

  const title = PARTICIPANTS.title;
  if (error && !room) {
    return (
      <RoomDialogShell title={title} testId="rd-participants" onClose={onClose}>
        <p className="problem" role="alert" data-testid="rd-participants-error">{error}</p>
      </RoomDialogShell>
    );
  }
  return (
    <RoomDialogShell title={title} sub={room?.name} testId="rd-participants" onClose={onClose} busy={!!confirm && !!busy}>
      <section className="rd-section" aria-labelledby={`${base}-now`}>
        <h3 className="rd-section__title" id={`${base}-now`}>{PARTICIPANTS.section_now}</h3>
        {!rows ? (
          <p className="rd-hint">{COMMON.loading}</p>
        ) : (
          <ul className="rd-list" data-testid="rd-part-list">
            {rows.map((p) => {
              const isAgent = p.kind === "agent";
              const label = isAgent ? p.agent?.name ?? "" : personName(p.user);
              const gate = room ? removeGate(room, p, meId, works) : { ok: false as const, reason: "", self: false };
              const hint = `${base}-rm-${p.id}`;
              const isSelf = gate.self;
              const personId = p.user?.id;
              return (
                <li key={p.id} className="rd-row" data-testid="rd-part-row" data-kind={p.kind} data-self={isSelf || undefined}>
                  <span className={isAgent ? "rd-row__icon rd-row__icon--agent" : "rd-row__icon"} role="img" aria-label={isAgent ? PARTICIPANTS.kind_agent : PARTICIPANTS.kind_person}>
                    {label.slice(0, 1).toUpperCase()}
                  </span>
                  <div className="rd-row__main">
                    <span className="rd-row__name">
                      {label}
                      {isSelf && <span className="muted"> ({PARTICIPANTS.me})</span>}
                      {!isAgent && <span className="rd-role" data-testid="rd-part-role">{ROOM_ROLE_LABEL[p.room_role]}</span>}
                      {isAgent && p.agent?.role && <span className="rd-role">{p.agent.role}</span>}
                    </span>
                    <span className="rd-row__meta">
                      {PARTICIPANTS.joined} {relativeTime(p.joined_at)}
                      {isAgent && p.profile && ` · ${PARTICIPANTS.profile} ${p.profile.name} (${kindName(p.profile.runtime_kind)})`}
                      {isAgent && p.status === "working" && ` · ${PARTICIPANTS.status_working}`}
                    </span>
                    {isAgent && invite.ok && p.agent && (p.agent.profiles?.length ?? 0) > 1 && (
                      <label className="row small">
                        <span className="muted">{PARTICIPANTS.change_profile}</span>
                        <select
                          className="select"
                          style={{ width: "auto" }}
                          value={p.profile?.id ?? ""}
                          disabled={busy === `profile:${p.id}`}
                          onChange={(e) => void changeProfile(p, e.target.value)}
                          data-testid="rd-part-profile"
                        >
                          {p.agent.profiles.map((pr) => (
                            <option key={pr.id} value={pr.id}>{pr.name} ({kindName(pr.runtime_kind)})</option>
                          ))}
                        </select>
                      </label>
                    )}
                    {rowError && (rowError.id === p.id || rowError.id === personId) && <p className="rd-err" role="alert">{rowError.text}</p>}
                  </div>
                  <div className="rd-row__side">
                    {!isAgent && deputy.ok && p.room_role !== "owner" && personId && (
                      <details className="rd-more" data-testid="rd-part-more">
                        <summary className="btn btn--sm btn--ghost" aria-label={PARTICIPANTS.more}>…</summary>
                        <div className="rd-more__menu">
                          {p.room_role === "deputy" ? (
                            <button type="button" className="btn btn--sm btn--ghost" disabled={!!busy} onClick={() => void setDeputy(null)} data-testid="rd-part-clear-deputy">
                              {PARTICIPANTS.clear_deputy}
                            </button>
                          ) : (
                            <button type="button" className="btn btn--sm btn--ghost" disabled={!!busy} onClick={() => void setDeputy(personId)} data-testid="rd-part-make-deputy">
                              {PARTICIPANTS.make_deputy}
                            </button>
                          )}
                        </div>
                      </details>
                    )}
                    <button
                      type="button"
                      className="btn btn--sm"
                      aria-disabled={!gate.ok || undefined}
                      aria-describedby={!gate.ok ? hint : undefined}
                      onClick={() => gate.ok && (setConfirmError(null), setConfirm({ kind: isSelf ? "leave" : "remove", row: p }))}
                      data-testid={isSelf ? "rd-part-leave" : "rd-part-remove"}
                    >
                      {isSelf ? PARTICIPANTS.leave : PARTICIPANTS.remove}
                    </button>
                    {!gate.ok && (
                      <DisabledHint id={hint}>
                        {gate.reason}
                        {"ownerLink" in gate && gate.ownerLink && (
                          <>
                            {" "}
                            <Link href={`/rooms/${roomId}/settings`} data-testid="rd-part-owner-link">{PARTICIPANTS.owner_leave_link}</Link>
                          </>
                        )}
                      </DisabledHint>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="rd-section" aria-labelledby={`${base}-invite`} data-testid="rd-invite">
        <h3 className="rd-section__title" id={`${base}-invite`}>{PARTICIPANTS.section_invite}</h3>
        {!invite.ok && <DisabledHint id={`${base}-invite-hint`}>{invite.reason}</DisabledHint>}
        <label className="rd-field">
          <span className="rd-field__label">{PARTICIPANTS.search_label}</span>
          <input className="input" value={q} placeholder={PARTICIPANTS.search_placeholder} onChange={(e) => setQ(e.target.value)} data-testid="rd-invite-search" />
        </label>
        <div className="rd-tabs" role="tablist">
          {(["people", "agents"] as const).map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              className="rd-tab"
              aria-selected={tab === t}
              aria-controls={`${base}-panel-${t}`}
              onClick={() => setTab(t)}
              data-testid={`rd-invite-tab-${t}`}
            >
              {t === "people" ? PARTICIPANTS.tab_people : PARTICIPANTS.tab_agents}
            </button>
          ))}
        </div>
        {tab === "people" ? (
          <div role="tabpanel" id={`${base}-panel-people`} data-testid="rd-invite-people">
            {members && members.length <= 1 ? (
              <p className="rd-hint" data-testid="rd-invite-no-people">
                {PARTICIPANTS.no_people} · <Link href="/settings?tab=members">{PARTICIPANTS.no_people_link}</Link>
              </p>
            ) : candidatesPeople.length === 0 ? (
              <p className="rd-hint">{needle ? PARTICIPANTS.no_match : PARTICIPANTS.all_people_here}</p>
            ) : (
              <ul className="rd-list">
                {candidatesPeople.map((m) => (
                  <li key={m.id} className="rd-row" data-testid="rd-invite-person">
                    <span className="rd-row__icon" role="img" aria-label={PARTICIPANTS.kind_person}>{personName(m.user).slice(0, 1).toUpperCase()}</span>
                    <div className="rd-row__main">
                      <span className="rd-row__name">{personName(m.user)}</span>
                      <span className="rd-row__meta">{m.user.email}</span>
                      {rowError?.id === m.user.id && <p className="rd-err" role="alert">{rowError.text}</p>}
                    </div>
                    <div className="rd-row__side">
                      <button
                        type="button"
                        className="btn btn--sm btn--primary"
                        aria-disabled={!invite.ok || undefined}
                        aria-describedby={!invite.ok ? `${base}-invite-hint` : undefined}
                        disabled={busy === `invite:${m.user.id}`}
                        onClick={() => invite.ok && void addPerson(m.user.id)}
                        data-testid="rd-invite-person-btn"
                      >
                        {busy === `invite:${m.user.id}` ? PARTICIPANTS.inviting : PARTICIPANTS.invite}
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : (
          <div role="tabpanel" id={`${base}-panel-agents`} data-testid="rd-invite-agents" className="rd-section">
            <p className="rd-warn" data-testid="rd-invite-agents-head">{PARTICIPANTS.agents_head}</p>
            {agents && agents.length === 0 ? (
              <p className="rd-hint" data-testid="rd-invite-no-agents">
                {PARTICIPANTS.no_agents} · <Link href="/agents">{PARTICIPANTS.no_agents_link}</Link>
              </p>
            ) : candidatesAgents.length === 0 ? (
              <p className="rd-hint">{needle ? PARTICIPANTS.no_match : PARTICIPANTS.all_agents_here}</p>
            ) : (
              <ul className="rd-list">
                {candidatesAgents.map((a) => {
                  const g = agentInviteGate(a);
                  const allowed = invite.ok && g.ok;
                  const hintId = `${base}-ag-${a.id}`;
                  const profileId = profilePick[a.id] ?? (a.profiles.find((p) => p.is_default) ?? a.profiles[0])?.id;
                  const kind = a.profiles.find((p) => p.id === profileId)?.runtime_kind;
                  const note = room ? runtimeKindNote(room, kind) : null;
                  return (
                    <li key={a.id} className="rd-row" data-testid="rd-invite-agent" data-agent-id={a.id}>
                      <span className="rd-row__icon rd-row__icon--agent" role="img" aria-label={PARTICIPANTS.kind_agent}>{a.name.slice(0, 1).toUpperCase()}</span>
                      <div className="rd-row__main">
                        <span className="rd-row__name">
                          {a.name}
                          <span className="rd-role">{a.role}</span>
                        </span>
                        {a.role_description && <span className="rd-row__meta">{a.role_description}</span>}
                        {a.profiles.length > 0 && (
                          <label className="row small">
                            <span className="muted">{PARTICIPANTS.profile}</span>
                            <select
                              className="select"
                              style={{ width: "auto" }}
                              value={profileId}
                              disabled={!allowed}
                              onChange={(e) => setProfilePick((x) => ({ ...x, [a.id]: e.target.value }))}
                              data-testid="rd-invite-agent-profile"
                            >
                              {a.profiles.map((pr) => (
                                <option key={pr.id} value={pr.id}>{pr.name} ({kindName(pr.runtime_kind)})</option>
                              ))}
                            </select>
                          </label>
                        )}
                        {g.ok && note && (
                          <p className={note.tone === "warn" ? "rd-warn" : "rd-hint"} data-testid={note.tone === "warn" ? "rd-invite-agent-warn" : "rd-invite-agent-info"}>
                            {note.text}
                          </p>
                        )}
                        {warnings[a.id] && <p className="rd-warn">{warnings[a.id]}</p>}
                        {rowError?.id === a.id && <p className="rd-err" role="alert">{rowError.text}</p>}
                      </div>
                      <div className="rd-row__side">
                        <button
                          type="button"
                          className="btn btn--sm btn--primary"
                          aria-disabled={!allowed || undefined}
                          aria-describedby={!allowed ? (g.ok ? `${base}-invite-hint` : hintId) : undefined}
                          disabled={busy === `invite:${a.id}`}
                          onClick={() => allowed && void addAgent(a)}
                          data-testid="rd-invite-agent-btn"
                        >
                          {busy === `invite:${a.id}` ? PARTICIPANTS.inviting : PARTICIPANTS.invite}
                        </button>
                        {!g.ok && <DisabledHint id={hintId}>{g.reason}</DisabledHint>}
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        )}
      </section>

      {confirm && (
        <ConfirmDialog
          title={
            confirm.kind === "leave"
              ? PARTICIPANTS.leave_title
              : confirm.row.kind === "agent"
                ? PARTICIPANTS.remove_agent_title(confirm.row.agent?.name ?? "")
                : PARTICIPANTS.remove_title(personName(confirm.row.user))
          }
          confirmLabel={confirm.kind === "leave" ? PARTICIPANTS.leave_confirm : PARTICIPANTS.remove_confirm}
          busyLabel={confirm.kind === "leave" ? PARTICIPANTS.leave_busy : PARTICIPANTS.remove_busy}
          cancelLabel={COMMON.cancel}
          busy={!!busy}
          danger
          error={confirmError}
          onConfirm={() => void doRemove(confirm)}
          onClose={() => setConfirm(null)}
          testId={confirm.kind === "leave" ? "rd-leave-dialog" : "rd-remove-dialog"}
        >
          {confirm.kind === "leave" ? (
            <p className="confirm-dlg__p">{PARTICIPANTS.leave_body}</p>
          ) : confirm.row.kind === "agent" ? (
            <>
              <p className="confirm-dlg__p">{PARTICIPANTS.remove_agent_body}</p>
              <p className="confirm-dlg__p">{PARTICIPANTS.remove_agent_running}</p>
            </>
          ) : (
            <p className="confirm-dlg__p">{PARTICIPANTS.remove_person_body}</p>
          )}
        </ConfirmDialog>
      )}
    </RoomDialogShell>
  );
}

export default RoomParticipantsDialog;
