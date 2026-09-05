"use client";
/**
 * S4 온보딩(SCREEN §4.2) — P1 최소. 세 단계, 각 단계 건너뛰기 가능.
 * 1 워크스페이스 이름 → 2 컴퓨터 연결(S12 인라인) → 3 에이전트(P1: 템플릿은 P2 — Lead 하나를 기본값으로 만들거나 건너뛴다).
 * 워크스페이스가 이미 있으면(초대 수락 등) S5 로 보낸다.
 */
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { AuthProvider, useAuth } from "@/lib/auth/AuthContext";
import { api, errorMessage } from "@/lib/api/client";
import { PairingPanel } from "@/components/PairingPanel";
import type { Pairing, WorkspaceWithRole } from "@/lib/api/types";

type Step = 1 | 2 | 3;

function Steps({ current, done }: { current: Step; done: Step[] }) {
  const items: { n: Step; label: string }[] = [
    { n: 1, label: "워크스페이스" },
    { n: 2, label: "컴퓨터 연결" },
    { n: 3, label: "에이전트" },
  ];
  return (
    <div className="steps" data-testid="onboarding-steps" data-step={current}>
      {items.map((it) => (
        <span
          key={it.n}
          className={`steps__item${it.n === current ? " steps__item--current" : done.includes(it.n) ? " steps__item--done" : ""}`}
        >
          {it.n}. {it.label}
        </span>
      ))}
    </div>
  );
}

function Onboarding() {
  const router = useRouter();
  const { me, loading, workspace, refresh, selectWorkspace } = useAuth();
  const [step, setStep] = useState<Step>(1);
  const [done, setDone] = useState<Step[]>([]);
  const [ws, setWs] = useState<WorkspaceWithRole | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [agentName, setAgentName] = useState("Lead");

  // 이미 워크스페이스가 있으면(초대 수락 등) 온보딩을 건너뛴다 — 단, 이 화면에서 방금 만든 경우는 제외
  useEffect(() => {
    if (!loading && me && workspace && !ws && step === 1) router.replace("/sessions");
  }, [loading, me, workspace, ws, step, router]);

  async function createWorkspace(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const w = await api.post("/workspaces", { body: { name } });
      selectWorkspace(w.id);
      const wr: WorkspaceWithRole = { ...w, my_role: "owner" };
      setWs(wr);
      setDone([1]);
      setStep(2);
      void refresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function createLead() {
    if (!ws) return;
    setBusy(true);
    setError(null);
    try {
      const rt = pairing?.runtime;
      const cap = rt?.capabilities.find((c) => c.kind === "claude_code") ?? rt?.capabilities[0];
      await api.post("/workspaces/{workspaceId}/agents", {
        path: { workspaceId: ws.id },
        body: {
          name: agentName.trim() || "Lead",
          role: "lead",
          role_description: "팀을 이끌고 위임·종합한다",
          instructions: "You are the lead. Coordinate the team, delegate, and summarize results.",
          profiles: [
            {
              name: "default",
              runtime_kind: cap?.kind ?? "claude_code",
              model: cap?.models?.[0] ?? "default",
              is_default: true,
            },
          ],
        },
      });
      router.replace("/sessions/new");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p className="muted">불러오는 중…</p>;

  return (
    <div className="auth__card" style={{ maxWidth: 560 }} data-testid="onboarding">
      <div className="auth__brand">COLAB · 온보딩</div>
      <Steps current={step} done={done} />
      {error && <p className="problem">{error}</p>}

      {step === 1 && (
        <form onSubmit={createWorkspace}>
          <h1 className="auth__title">워크스페이스 이름</h1>
          <p className="auth__sub">팀원 초대는 나중에 Settings 에서 합니다.</p>
          <label className="field">
            <span className="field__label">이름</span>
            <input className="input" name="workspace_name" required maxLength={80} placeholder="마케팅팀" value={name} onChange={(e) => setName(e.target.value)} data-testid="workspace-name" />
          </label>
          <button className="btn btn--primary btn--block" type="submit" disabled={busy} data-testid="workspace-next">
            {busy ? "만드는 중…" : "다음"}
          </button>
        </form>
      )}

      {step === 2 && ws && (
        <div>
          <h1 className="auth__title">컴퓨터 연결</h1>
          <p className="auth__sub">에이전트가 실행될 컴퓨터에 데몬을 설치합니다. 건너뛰면 홈 체크리스트에 남습니다.</p>
          <PairingPanel workspaceId={ws.id} canManage onReady={setPairing} />
          <div className="row" style={{ marginTop: 16, justifyContent: "space-between" }}>
            <button type="button" className="btn btn--ghost" onClick={() => setStep(3)} data-testid="pairing-skip">
              건너뛰기
            </button>
            <button
              type="button"
              className="btn btn--primary"
              onClick={() => {
                setDone([1, 2]);
                setStep(3);
              }}
              disabled={!pairing}
              title={!pairing ? "데몬이 연결되면 활성화됩니다" : undefined}
              data-testid="pairing-next"
            >
              다음
            </button>
          </div>
        </div>
      )}

      {step === 3 && ws && (
        <div>
          <h1 className="auth__title">에이전트</h1>
          <p className="auth__sub">
            팀 템플릿(리서치 팀 / 개발 팀 / 콘텐츠 팀)은 P2 에서 열립니다. 지금은 Lead 하나를 기본값으로 만들어 첫 세션을 시작할 수 있습니다.
          </p>
          <div className="story__grid" style={{ marginBottom: 14 }}>
            {["리서치 팀", "개발 팀", "콘텐츠 팀"].map((t) => (
              <div key={t} className="card card--surface" aria-disabled="true" style={{ opacity: 0.5 }}>
                <b>{t}</b>
                <div className="small muted-3">템플릿 · P2</div>
              </div>
            ))}
          </div>
          <label className="field">
            <span className="field__label">Lead 이름(멘션 라벨)</span>
            <input className="input" value={agentName} maxLength={40} onChange={(e) => setAgentName(e.target.value)} data-testid="agent-name" />
            <span className="field__hint">
              프로파일: {pairing?.runtime?.capabilities[0]?.kind ?? "claude_code"} · {pairing?.runtime?.capabilities[0]?.models?.[0] ?? "default"}
              {!pairing && " (런타임 미연결 — 기본값)"}
            </span>
          </label>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <button type="button" className="btn btn--ghost" onClick={() => router.replace("/sessions")} data-testid="agent-skip">
              건너뛰기
            </button>
            <button type="button" className="btn btn--primary" onClick={() => void createLead()} disabled={busy} data-testid="agent-create">
              {busy ? "만드는 중…" : "Lead 만들고 첫 세션 만들기"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

export default function OnboardingPage() {
  return (
    <AuthProvider requireWorkspace={false}>
      <main className="auth">
        <Onboarding />
      </main>
    </AuthProvider>
  );
}
