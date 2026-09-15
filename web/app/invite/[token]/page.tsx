"use client";
/**
 * S3 초대 수락(SCREEN §4.1, U13) — 토큰으로 워크스페이스·초대자·역할을 미리 본다.
 * 미로그인이면 로그인/가입으로(토큰을 넘겨 자동 수락). 로그인 상태면 수락 → S4 건너뛰고 S5.
 * 만료·취소는 사유(410 invite_expired / invite_revoked)를 명시하고 재요청 안내를 준다.
 */
import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { api, isApiError, errorMessage } from "@/lib/api/client";
import type { InvitePreview } from "@/lib/api/types";

const ROLE_LABEL = { owner: "owner", admin: "admin", member: "member" } as const;

export default function InvitePage() {
  const { token } = useParams<{ token: string }>();
  const router = useRouter();
  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [loggedIn, setLoggedIn] = useState<boolean | null>(null);
  const [gone, setGone] = useState<{ code?: string; message: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const p = await api.get("/invites/{inviteToken}", { path: { inviteToken: token } });
        if (alive) setPreview(p);
      } catch (e) {
        if (!alive) return;
        if (isApiError(e) && (e.status === 410 || e.status === 404)) setGone({ code: e.code, message: errorMessage(e) });
        else setError(errorMessage(e));
      }
      try {
        await api.get("/me");
        if (alive) setLoggedIn(true);
      } catch {
        if (alive) setLoggedIn(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [token]);

  async function accept() {
    setBusy(true);
    setError(null);
    try {
      await api.post("/invites/{inviteToken}/accept", { path: { inviteToken: token } });
      router.replace("/sessions");
    } catch (e) {
      if (isApiError(e) && e.status === 401) {
        setLoggedIn(false);
        return;
      }
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth">
      <div className="auth__card" data-testid="invite-card">
        <div className="auth__brand">COLAB</div>
        {gone ? (
          <>
            <h1 className="auth__title">초대를 열 수 없습니다</h1>
            <p className="auth__sub" data-testid="invite-gone">
              {gone.code === "invite_expired"
                ? "초대 링크가 만료되었습니다."
                : gone.code === "invite_revoked"
                  ? "초대가 취소되었습니다."
                  : "초대를 찾을 수 없습니다."}{" "}
              {gone.message}
            </p>
            <a className="btn btn--block" href={`mailto:?subject=${encodeURIComponent("Colab 초대 재요청")}`}>
              초대자에게 다시 요청
            </a>
          </>
        ) : preview ? (
          <>
            <h1 className="auth__title">{preview.workspace.name}에 초대되었습니다</h1>
            <p className="auth__sub">
              <b>{preview.invited_by.display_name}</b> 님이 <b>{ROLE_LABEL[preview.role]}</b> 역할로 초대했습니다.
              <br />
              <span className="small muted-3">만료: {new Date(preview.expires_at).toLocaleString("ko-KR")}</span>
            </p>
            {error && <p className="problem">{error}</p>}
            {loggedIn === null ? (
              <p className="muted">확인 중…</p>
            ) : loggedIn ? (
              <button className="btn btn--primary btn--block" onClick={() => void accept()} disabled={busy} data-testid="invite-accept">
                {busy ? "수락 중…" : "초대 수락"}
              </button>
            ) : (
              <div className="stack">
                <Link className="btn btn--primary btn--block" href={`/signup?invite=${encodeURIComponent(token)}`}>
                  가입하고 참여
                </Link>
                <Link className="btn btn--block" href={`/login?invite=${encodeURIComponent(token)}`}>
                  로그인하고 참여
                </Link>
              </div>
            )}
          </>
        ) : error ? (
          <p className="problem">{error}</p>
        ) : (
          <p className="muted">초대를 확인하는 중…</p>
        )}
      </div>
    </main>
  );
}
