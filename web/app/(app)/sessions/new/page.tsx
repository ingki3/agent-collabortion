"use client";
/**
 * S6 새 세션 마법사 — P1 최소(SCREEN §4.4, U1 7~13): 제목·goal 만 입력, 나머지는 기본값으로 통과.
 * 기본값: Director=본인, 격리 none, 런타임 자동 선택(null), 참여자=워크스페이스 에이전트 전부(assignee=첫 lead),
 * 종료 조건 artifact_submitted(assignee) AND user_approval(스키마 v0 기본), autonomy guided.
 * 런타임 0개면 "먼저 컴퓨터를 연결하세요"(SCREEN §2.1 · §7).
 */
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import type { Agent, Runtime } from "@/lib/api/types";

export default function NewSessionPage() {
  const router = useRouter();
  const { me, workspace } = useAuth();
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [title, setTitle] = useState("");
  const [goal, setGoal] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!workspace) return;
    (async () => {
      try {
        const [rts, ags] = await Promise.all([
          api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
          api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }),
        ]);
        setRuntimes(rts);
        setAgents(ags.items);
      } catch (e) {
        setError(errorMessage(e));
      }
    })();
  }, [workspace]);

  const online = useMemo(() => (runtimes ?? []).filter((r) => r.status === "online"), [runtimes]);
  const invitable = useMemo(() => (agents ?? []).filter((a) => a.invitable.allowed && !a.archived_at), [agents]);
  const assignee = useMemo(() => invitable.find((a) => a.role === "lead") ?? invitable[0], [invitable]);
  const noRuntime = runtimes !== null && online.length === 0;
  const noAgent = agents !== null && invitable.length === 0;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!workspace || !me || !assignee) return;
    setBusy(true);
    setError(null);
    try {
      const s = await api.post("/workspaces/{workspaceId}/sessions", {
        path: { workspaceId: workspace.id },
        body: {
          title: title.trim(),
          goal: goal.trim(),
          director_user_id: me.user.id,
          isolation: { kind: "none" },
          runtime_id: null,
          participants: invitable.map((a) => ({ agent_id: a.id })),
          assignee_agent_id: assignee.id,
          autonomy: "guided",
        },
      });
      router.replace(`/sessions/${s.id}`);
    } catch (err) {
      if (isApiError(err) && err.code === "no_runtime") setError("먼저 컴퓨터를 연결하세요 — 런타임이 없으면 세션을 만들 수 없습니다.");
      else if (isApiError(err) && err.problem.errors?.length) setError(err.problem.errors.map((x) => `${x.field}: ${x.message}`).join(" · "));
      else setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="content--narrow">
      <div className="page-head">
        <h1>새 세션</h1>
        <Link href="/sessions" className="btn btn--ghost btn--sm">
          취소
        </Link>
      </div>
      {error && (
        <p className="problem" role="alert" data-testid="new-session-error">
          {error}
        </p>
      )}
      {noRuntime && (
        <div className="empty" data-testid="new-session-no-runtime">
          <div className="empty__title">먼저 컴퓨터를 연결하세요</div>
          <div className="empty__body">세션은 런타임에 묶입니다(FR-2.1). 연결 후 다시 오세요.</div>
          <Link href="/runtimes/new" className="btn btn--primary">
            Add a computer
          </Link>
        </div>
      )}
      {!noRuntime && noAgent && (
        <div className="empty" data-testid="new-session-no-agent">
          <div className="empty__title">참여할 에이전트가 없습니다</div>
          <div className="empty__body">온보딩 3단계에서 Lead 를 만들거나(P1), Agents 화면(P2)에서 등록하세요.</div>
          <Link href="/onboarding" className="btn">
            온보딩으로
          </Link>
        </div>
      )}
      <form onSubmit={submit} data-testid="new-session-form" style={{ opacity: noRuntime || noAgent ? 0.5 : 1 }}>
        <fieldset disabled={noRuntime || noAgent || runtimes === null} style={{ border: 0, padding: 0, margin: 0 }}>
          <h2 style={{ fontSize: 14, margin: "0 0 10px" }}>1. goal</h2>
          <label className="field">
            <span className="field__label">제목</span>
            <input className="input" required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="결제 시장 조사" data-testid="session-title" />
          </label>
          <label className="field">
            <span className="field__label">goal (필수)</span>
            <textarea className="textarea" required value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="국내 B2B SaaS 결제 시장 조사 보고서 10페이지" data-testid="session-goal" />
          </label>

          <h2 style={{ fontSize: 14, margin: "16px 0 8px" }}>2~7. 기본값으로 진행</h2>
          <div className="card card--surface small" data-testid="session-defaults">
            <ul style={{ margin: 0, paddingLeft: 18, lineHeight: 1.8 }}>
              <li>Director: <b>{me?.user.display_name}</b> (본인) · deputy 없음</li>
              <li>격리: <b>none</b> — worktree 는 저장소가 필요합니다(P4)</li>
              <li>런타임: <b>자동 선택(첫 실행 시 고정)</b> · 온라인 {online.length}대{online[0] ? ` — ${online.map((r) => r.name).join(", ")}` : ""}</li>
              <li>
                참여자: {invitable.length ? invitable.map((a) => `@${a.name}`).join(", ") : "—"} · assignee <b>{assignee ? `@${assignee.name}` : "—"}</b>
              </li>
              <li>종료 조건: <b>assignee 가 artifact 제출</b> AND <b>Director 승인</b></li>
              <li>한도: 기본값 · autonomy <b>guided</b> — "질문 기한이 지나면 계속 기다립니다"</li>
            </ul>
          </div>
          <div className="row" style={{ marginTop: 16, justifyContent: "flex-end" }}>
            <button type="submit" className="btn btn--primary" disabled={busy} data-testid="session-start">
              {busy ? "시작 중…" : "시작"}
            </button>
          </div>
        </fieldset>
      </form>
    </div>
  );
}
