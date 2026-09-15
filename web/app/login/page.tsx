"use client";
/** S1 로그인(SCREEN §4.1) — 실패 사유 구분(account_not_found · password_mismatch · account_locked), return_to 복귀. */
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, isApiError, errorMessage } from "@/lib/api/client";

const REASON: Record<string, string> = {
  account_not_found: "이 이메일로 만든 계정이 없습니다.",
  password_mismatch: "비밀번호가 맞지 않습니다.",
  account_locked: "계정이 잠겨 있습니다. 잠시 후 다시 시도하세요.",
};

function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const returnTo = params.get("return_to");
  const inviteToken = params.get("invite") ?? undefined;
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await api.post("/auth/login", { body: { email, password, invite_token: inviteToken } });
      const me = await api.get("/me");
      const dest = returnTo && returnTo.startsWith("/") ? returnTo : me.workspaces.length === 0 ? "/onboarding" : "/sessions";
      router.replace(r.accepted_invite ? "/sessions" : dest);
    } catch (err) {
      setError(isApiError(err) && err.code && REASON[err.code] ? REASON[err.code] : errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="auth__card" onSubmit={submit} data-testid="login-form">
      <div className="auth__brand">COLAB</div>
      <h1 className="auth__title">로그인</h1>
      <p className="auth__sub">{inviteToken ? "로그인하면 초대가 자동으로 수락됩니다." : "이메일과 비밀번호로 로그인합니다."}</p>
      {error && (
        <p className="problem" role="alert" data-testid="login-error">
          {error}
        </p>
      )}
      <label className="field">
        <span className="field__label">이메일</span>
        <input className="input" type="email" name="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      <label className="field">
        <span className="field__label">비밀번호</span>
        <input className="input" type="password" name="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      <button className="btn btn--primary btn--block" type="submit" disabled={busy}>
        {busy ? "로그인 중…" : "로그인"}
      </button>
      <p className="auth__foot">
        계정이 없나요?{" "}
        <Link href={inviteToken ? `/signup?invite=${encodeURIComponent(inviteToken)}` : "/signup"}>회원가입</Link>
      </p>
    </form>
  );
}

export default function LoginPage() {
  return (
    <main className="auth">
      <Suspense fallback={null}>
        <LoginForm />
      </Suspense>
    </main>
  );
}
