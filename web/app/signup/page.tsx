"use client";
/** S2 회원가입(SCREEN §4.1) — 이름·이메일·비밀번호. 가입 직후 S4 온보딩(초대 토큰이 있으면 자동 수락 → S5). */
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, isApiError, errorMessage } from "@/lib/api/client";

function SignupForm() {
  const router = useRouter();
  const params = useSearchParams();
  const inviteToken = params.get("invite") ?? undefined;
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await api.post("/auth/signup", {
        body: { display_name: name, email, password, invite_token: inviteToken },
      });
      router.replace(r.accepted_invite ? "/sessions" : "/onboarding");
    } catch (err) {
      if (isApiError(err) && err.status === 409) setError("이미 가입된 이메일입니다. 로그인하세요.");
      else if (isApiError(err) && err.problem.errors?.length) setError(err.problem.errors.map((x) => x.message).join(" "));
      else setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="auth__card" onSubmit={submit} data-testid="signup-form">
      <div className="auth__brand">COLAB</div>
      <h1 className="auth__title">회원가입</h1>
      <p className="auth__sub">가입 후 바로 워크스페이스를 만들고 컴퓨터를 연결합니다(15분).</p>
      {error && (
        <p className="problem" role="alert" data-testid="signup-error">
          {error}
        </p>
      )}
      <label className="field">
        <span className="field__label">이름</span>
        <input className="input" name="display_name" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="field">
        <span className="field__label">이메일</span>
        <input className="input" type="email" name="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      <label className="field">
        <span className="field__label">비밀번호</span>
        <input className="input" type="password" name="password" autoComplete="new-password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} />
        <span className="field__hint">8자 이상</span>
      </label>
      <button className="btn btn--primary btn--block" type="submit" disabled={busy}>
        {busy ? "가입 중…" : "가입"}
      </button>
      <p className="auth__foot">
        이미 계정이 있나요? <Link href={inviteToken ? `/login?invite=${encodeURIComponent(inviteToken)}` : "/login"}>로그인</Link>
      </p>
    </form>
  );
}

export default function SignupPage() {
  return (
    <main className="auth">
      <Suspense fallback={null}>
        <SignupForm />
      </Suspense>
    </main>
  );
}
