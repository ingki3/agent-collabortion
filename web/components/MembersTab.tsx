"use client";
/**
 * S14 멤버 탭(SCREEN §4.10 · U13) — 목록 · 역할 변경 · 제거 · 초대 링크 · 초대 목록(대기·만료·취소).
 *
 * 권한은 계약이 정한다: 목록은 멤버 누구나(listMembers), 나머지는 owner·admin. **owner 강등은 owner 만**(§2.3) —
 * 그래서 admin 이 보는 owner 행의 역할 선택은 잠기고 사유가 옆에 선다. 마지막 owner 강등·제거와 "그 멤버가 Director 인
 * 진행 중 세션" 은 서버가 409 로 막고, 화면은 그 `detail` 을 그대로 보인다(문장을 지어내지 않는다).
 * 서버가 아직 안 만든 op(역할 변경·제거는 T-S12 뒤)은 501 이 오면 그 문장을 보인다.
 */
import { useCallback, useEffect, useState } from "react";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { DisabledHint } from "./PageHead";
import { relativeTime } from "@/lib/time";
import type { Invite, Member, MemberRole } from "@/lib/api/types";
import "./settings.css";

export const ROLE_LABEL: Record<MemberRole, string> = { owner: "소유자", admin: "관리자", member: "멤버" };
const INVITE_STATUS: Record<Invite["status"], string> = { pending: "대기 중", accepted: "수락됨", expired: "만료됨", revoked: "취소됨" };

/**
 * 이 행의 역할을 내가 바꿀 수 있는가(SCREEN §2.3 권한 매트릭스). 사유는 화면 문장 그대로.
 * - member 는 아무것도 못 한다.
 * - owner 행은 owner 만 만진다(강등이든 유지든).
 */
export function roleChangeRight(me: MemberRole | null | undefined, target: Member, meUserId: string | null): { ok: true } | { ok: false; reason: string } {
  if (me !== "owner" && me !== "admin") return { ok: false, reason: "소유자·관리자만 바꿀 수 있습니다" };
  if (target.role === "owner" && me !== "owner") return { ok: false, reason: "소유자의 역할은 소유자만 바꿀 수 있습니다" };
  if (target.user.id === meUserId && target.role === "owner") return { ok: false, reason: "내 소유자 역할은 다른 소유자가 바꿔야 합니다" };
  return { ok: true };
}

export interface MembersTabProps {
  workspaceId: string;
  myRole: MemberRole | null;
  meUserId: string | null;
}

export function MembersTab({ workspaceId, myRole, meUserId }: MembersTabProps) {
  const [members, setMembers] = useState<Member[] | null>(null);
  const [invites, setInvites] = useState<Invite[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Exclude<MemberRole, "owner">>("member");
  const [created, setCreated] = useState<Invite | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const canManage = myRole === "owner" || myRole === "admin";

  const load = useCallback(async () => {
    try {
      const page = await api.get("/workspaces/{workspaceId}/members", { path: { workspaceId }, query: { limit: 100 } });
      setMembers(page.items ?? []);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
    if (canManage) {
      try {
        setInvites(await api.get("/workspaces/{workspaceId}/invites", { path: { workspaceId } }));
      } catch (e) {
        setInviteError(errorMessage(e));
      }
    }
  }, [workspaceId, canManage]);
  useEffect(() => { void load(); }, [load]);

  async function changeRole(m: Member, next: MemberRole) {
    setBusy(true);
    setError(null);
    try {
      const updated = await api.patch("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId, memberId: m.id }, body: { role: next } });
      setMembers((ms) => ms?.map((x) => (x.id === m.id ? updated : x)) ?? null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function remove(m: Member) {
    setBusy(true);
    setError(null);
    try {
      await api.delete("/workspaces/{workspaceId}/members/{memberId}", { path: { workspaceId, memberId: m.id } });
      setMembers((ms) => ms?.filter((x) => x.id !== m.id) ?? null);
      setConfirmRemove(null);
    } catch (e) {
      // 409 member_is_director 는 `sessions[]` 를 싣는다 — 어느 세션이 막는지 이름으로.
      const blocking = isApiError(e) ? ((e.problem as { sessions?: { id: string; title: string }[] }).sessions ?? []) : [];
      setError(blocking.length ? `${errorMessage(e)} — ${blocking.map((s) => s.title).join(", ")}` : errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function invite() {
    setBusy(true);
    setInviteError(null);
    try {
      const inv = await api.post("/workspaces/{workspaceId}/invites", {
        path: { workspaceId }, idempotencyKey: newIdempotencyKey(),
        body: { email: email.trim() ? email.trim() : null, role, expires_in_hours: 168 },
      });
      setCreated(inv);
      setEmail("");
      setInvites((is) => [inv, ...(is ?? [])]);
    } catch (e) {
      const fields = isApiError(e) ? e.problem.errors?.map((x) => x.message).join(" · ") : undefined;
      setInviteError(fields || errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function revoke(inv: Invite) {
    setBusy(true);
    try {
      await api.delete("/workspaces/{workspaceId}/invites/{inviteId}", { path: { workspaceId, inviteId: inv.id } });
      setInvites((is) => is?.map((x) => (x.id === inv.id ? { ...x, status: "revoked" as const } : x)) ?? null);
      if (created?.id === inv.id) setCreated(null);
    } catch (e) {
      setInviteError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card" data-testid="settings-tab-members">
      <div className="tab-head">
        <div>
          <h2>멤버</h2>
          <p className="tab-head__desc">누가 이 워크스페이스에 있고 무엇을 할 수 있는지</p>
        </div>
        {!canManage && (
          <div className="tab-head__actions">
            <DisabledHint id="members-manage-hint">소유자·관리자만 초대하고 역할을 바꿀 수 있습니다 — 멤버는 목록만 봅니다</DisabledHint>
          </div>
        )}
      </div>
      {error && <p className="problem" role="alert" data-testid="members-error">{error}</p>}
      {!members && !error && <p className="muted">불러오는 중…</p>}
      {members && (
        <div className="members" data-testid="member-list">
          {members.map((m) => {
            const right = roleChangeRight(myRole, m, meUserId);
            const isMe = m.user.id === meUserId;
            return (
              <div key={m.id} className="member" data-testid="member-row" data-role={m.role}>
                <div style={{ minWidth: 0 }}>
                  <div className="member__name">
                    {m.user.display_name}
                    {isMe && <span className="member__me">(나)</span>}
                  </div>
                  <div className="member__sub">{m.user.email} · {relativeTime(m.created_at)} 합류</div>
                </div>
                <div className="row" style={{ gap: 6 }}>
                  <select
                    className="select"
                    value={m.role}
                    disabled={!right.ok || busy}
                    title={right.ok ? undefined : right.reason}
                    aria-label={`${m.user.display_name} 역할`}
                    onChange={(e) => void changeRole(m, e.target.value as MemberRole)}
                    data-testid="member-role"
                  >
                    {(Object.keys(ROLE_LABEL) as MemberRole[]).map((r) => <option key={r} value={r}>{ROLE_LABEL[r]}</option>)}
                  </select>
                  {!right.ok && canManage && <span className="small muted" data-testid="member-role-why">{right.reason}</span>}
                </div>
                {confirmRemove === m.id ? (
                  <div className="row" style={{ gap: 6 }} role="dialog" aria-label="멤버 제거 확인">
                    <span className="small">{m.user.display_name} 을(를) 내보냅니다. 되돌릴 수 없습니다.</span>
                    <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void remove(m)} data-testid="member-remove-yes">내보내기</button>
                    <button type="button" className="btn btn--sm btn--ghost" onClick={() => setConfirmRemove(null)}>취소</button>
                  </div>
                ) : (
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!canManage || busy || isMe}
                    title={!canManage ? "소유자·관리자만 내보낼 수 있습니다" : isMe ? "자기 자신은 내보낼 수 없습니다" : undefined}
                    onClick={() => setConfirmRemove(m.id)}
                    data-testid="member-remove"
                  >
                    내보내기
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {canManage && (
        <div style={{ marginTop: 18 }} data-testid="invite-section">
          <h3 style={{ margin: "0 0 8px", fontSize: "var(--fs-body)" }}>초대</h3>
          <div className="row" style={{ alignItems: "flex-end" }}>
            <label className="field" style={{ flex: 1, minWidth: 200, marginBottom: 0 }}>
              <span className="field__label">이메일 (비우면 링크를 아는 누구나)</span>
              <input className="input" type="email" value={email} disabled={busy} onChange={(e) => setEmail(e.target.value)} placeholder="name@example.com" data-testid="invite-email" />
            </label>
            <label className="field" style={{ marginBottom: 0 }}>
              <span className="field__label">역할</span>
              <select className="select" value={role} disabled={busy} onChange={(e) => setRole(e.target.value as Exclude<MemberRole, "owner">)} data-testid="invite-role">
                <option value="member">{ROLE_LABEL.member}</option>
                <option value="admin">{ROLE_LABEL.admin}</option>
              </select>
            </label>
            <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void invite()} data-testid="invite-create">초대 링크 만들기</button>
          </div>
          <p className="field__hint" style={{ margin: "6px 0 0" }}>링크는 7일 뒤 만료됩니다. 소유자 역할은 초대로 줄 수 없습니다.</p>
          {inviteError && <p className="problem" role="alert" style={{ marginTop: 8 }} data-testid="invite-error">{inviteError}</p>}
          {created && (
            <div className="notice notice--info" style={{ marginTop: 10 }} data-testid="invite-created">
              <div className="small" style={{ marginBottom: 4 }}>초대 링크를 만들었습니다 — 복사해 전달하세요.</div>
              <div className="invite-url">
                <code data-testid="invite-url">{created.url}</code>
                <button type="button" className="btn btn--sm" onClick={() => { void navigator.clipboard?.writeText(created.url); }}>복사</button>
              </div>
            </div>
          )}
          {invites && invites.length > 0 && (
            <div className="list" style={{ marginTop: 12 }} data-testid="invite-list">
              {invites.map((inv) => (
                <div key={inv.id} className="member" data-testid="invite-row" data-status={inv.status}>
                  <div style={{ minWidth: 0 }}>
                    <div className="member__name">{inv.email ?? "링크를 아는 누구나"}</div>
                    <div className="member__sub">{ROLE_LABEL[inv.role]} · {INVITE_STATUS[inv.status]} · {new Date(inv.expires_at).toLocaleDateString("ko-KR")} 만료</div>
                  </div>
                  <span />
                  <button type="button" className="btn btn--sm" disabled={busy || inv.status !== "pending"} title={inv.status !== "pending" ? "대기 중인 초대만 취소할 수 있습니다" : undefined} onClick={() => void revoke(inv)} data-testid="invite-revoke">취소</button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
