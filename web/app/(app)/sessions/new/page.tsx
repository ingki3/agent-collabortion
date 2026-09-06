"use client";
/**
 * S6 새 세션 마법사 — 7단계(SCREEN §4.4). PRD 순서 그대로:
 * **goal → Director → 격리 → 런타임 → 참여자·프로파일 → 종료 조건 → 한도·autonomy** → 요약.
 *
 * 순서가 의존 순서다(디자인 리뷰 #02 C4′): 격리가 런타임 후보를 제한하고(worktree → 같은 remote URL 의 저장소가 있는 머신만),
 * 런타임이 참여자 프로파일 후보를 제한한다. 되돌아갈 수 있고 마지막에 요약을 보여준다.
 *
 * 막는 것은 막는다(U15): 미커밋 변경이 있는 저장소는 다음 버튼 비활성(E13-01), 초대 권한 없는 에이전트는
 * 비활성 + 사유(E10-11), `agent_approval` 단독이면 "사람 승인 없이 완료됩니다" 경고(m-m), `container`·`supervised`·
 * `criteria_met` 은 v1.1 배지로 비활성.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ConditionRow } from "@/components/ConditionRow";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import type { Agent, IsolationKind, Member, RepoCheck, Runtime, RuntimeCandidate, SessionListItem } from "@/lib/api/types";

const STEPS = ["goal", "Director", "격리", "런타임", "참여자", "종료 조건", "한도·autonomy"] as const;

type CondType = "artifact_submitted" | "agent_approval" | "user_approval" | "manual";
const COND_ORDER: CondType[] = ["artifact_submitted", "agent_approval", "user_approval", "manual"];

const AUTONOMY = [
  { value: "guided", label: "guided (기본)", note: "질문 기한이 지나면 계속 기다립니다", enabled: true },
  { value: "autonomous", label: "autonomous", note: "질문 기한이 지나면 에이전트가 제안한 기본값으로 진행합니다. 승인 요청은 예외로 항상 기다립니다", enabled: true },
  { value: "supervised", label: "supervised", note: "Lead 의 모든 위임을 Director 가 먼저 승인합니다", enabled: false },
] as const;

export default function NewSessionPage() {
  const router = useRouter();
  const { me, workspace } = useAuth();
  const [step, setStep] = useState(0);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [pastSessions, setPastSessions] = useState<SessionListItem[]>([]);

  // 1 goal
  const [title, setTitle] = useState("");
  const [goal, setGoal] = useState("");
  const [criteria, setCriteria] = useState<string[]>([]);
  const [criterion, setCriterion] = useState("");
  const [contextSessionId, setContextSessionId] = useState<string>("");
  // 2 Director
  const [directorId, setDirectorId] = useState<string>("");
  const [deputyId, setDeputyId] = useState<string>("");
  // 3 격리
  const [isolation, setIsolation] = useState<IsolationKind>("none");
  const [repoPath, setRepoPath] = useState<string>("");
  const [repoCheck, setRepoCheck] = useState<RepoCheck | null>(null);
  /** 검증 자체가 실패한 경우(409 runtime_offline 등). `ok: false` 와 다르다 — 그건 200 이고 결과다. */
  const [repoCheckError, setRepoCheckError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  // 4 런타임
  const [candidates, setCandidates] = useState<{ auto_select_allowed: boolean; candidates: RuntimeCandidate[] } | null>(null);
  const [runtimeId, setRuntimeId] = useState<string | null>(null); // null = 자동 선택
  // 5 참여자
  const [picked, setPicked] = useState<Record<string, string | null>>({}); // agent id → profile id
  const [assignee, setAssignee] = useState<string>("");
  // 6 종료 조건
  const [conds, setConds] = useState<CondType[]>(["artifact_submitted", "user_approval"]);
  const [op, setOp] = useState<"and" | "or">("and");
  /** `artifact_submitted` 의 제출자. 빈 문자열이면 `who: "assignee"`(기본값, SCREEN §4.4 6단계). */
  const [submitter, setSubmitter] = useState<string>("");
  // 7 한도
  const [budget, setBudget] = useState("20");
  const [timeLimit, setTimeLimit] = useState("PT4H");
  const [maxTasks, setMaxTasks] = useState("");
  const [maxLanes, setMaxLanes] = useState("5");
  const [autonomy, setAutonomy] = useState<"guided" | "autonomous" | "supervised">("guided");

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!workspace) return;
    (async () => {
      try {
        const [rts, ags, mems, sess] = await Promise.all([
          api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
          api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }),
          api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id } }),
          api.get("/workspaces/{workspaceId}/sessions", { path: { workspaceId: workspace.id }, query: { status: ["completed"] } }).catch(() => ({ items: [] as SessionListItem[] })),
        ]);
        setRuntimes(rts);
        setAgents(ags.items);
        setMembers(mems.items);
        setPastSessions(sess.items.filter((x) => x.status === "completed"));
      } catch (e) {
        setError(errorMessage(e));
      }
    })();
  }, [workspace]);

  useEffect(() => {
    if (me && !directorId) setDirectorId(me.user.id);
  }, [me, directorId]);

  const online = useMemo(() => (runtimes ?? []).filter((r) => r.status === "online"), [runtimes]);
  const invitable = useMemo(() => (agents ?? []).filter((a) => !a.archived_at), [agents]);
  const noRuntime = runtimes !== null && online.length === 0;
  /** 온라인 런타임이 광고한 저장소 — worktree 후보의 출발점(경로가 아니라 remote URL 이 키다). */
  const repos = useMemo(() => {
    const m = new Map<string, { path: string; remote_url: string | null; runtimeId: string; runtimeName: string }>();
    for (const r of online) for (const repo of r.repos) if (!m.has(repo.path)) m.set(repo.path, { path: repo.path, remote_url: repo.remote_url ?? null, runtimeId: r.id, runtimeName: r.name });
    return [...m.values()];
  }, [online]);
  const selectedRepo = repos.find((r) => r.path === repoPath);

  /**
   * 저장소 검증(E13-01) — 실패하면 폼이 다음 단계를 막는다.
   *
   * 계약이 못 박은 두 가지를 화면이 그대로 따른다:
   *   · `ok: false` 는 **오류가 아니라 답**이다(200). 그래서 화면은 사유를 그리고 오류 배너를 띄우지 않는다.
   *   · 이 검증은 **마지막 probe `repos[]`** 를 읽고 최신값을 위해 probe 를 큐에 넣을 뿐, 데몬 왕복을 하지
   *     않는다. 마지막 probe 뒤에 만든 저장소는 다음 probe 까지 "없음"으로 읽히므로 **다시 확인**이 필요하다.
   * 데몬이 오프라인이면 `409 runtime_offline` 이고, 그것도 폼 안의 문장으로 말한다.
   */
  const checkRepo = useCallback(async (path: string) => {
    const repo = repos.find((r) => r.path === path);
    if (!repo) return;
    setChecking(true);
    setRepoCheck(null);
    setRepoCheckError(null);
    try {
      setRepoCheck(await api.post("/runtimes/{runtimeId}/repo-checks", { path: { runtimeId: repo.runtimeId }, body: { repo_path: path } }));
    } catch (e) {
      setRepoCheckError(
        isApiError(e) && e.code === "runtime_offline"
          ? `${repo.runtimeName} 이 오프라인이라 저장소를 확인할 수 없습니다 — 그 컴퓨터를 켜거나 다른 저장소를 고르세요.`
          : errorMessage(e),
      );
    } finally {
      setChecking(false);
    }
  }, [repos]);

  // 격리가 정해지면 런타임 후보를 다시 묻는다 — 격리가 후보를 제한한다
  useEffect(() => {
    if (!workspace || step < 3) return;
    (async () => {
      try {
        const r = await api.get("/workspaces/{workspaceId}/runtime-candidates", {
          path: { workspaceId: workspace.id },
          query: { isolation, ...(isolation === "worktree" && selectedRepo?.remote_url ? { remote_url: selectedRepo.remote_url } : {}) },
        });
        setCandidates(r);
        if (isolation !== "none") {
          const first = r.candidates.find((c) => c.eligible);
          setRuntimeId((cur) => cur ?? first?.runtime.id ?? null);
        }
      } catch (e) {
        setError(errorMessage(e));
      }
    })();
  }, [workspace, step, isolation, selectedRepo?.remote_url]);

  const pickedIds = Object.keys(picked);
  useEffect(() => {
    if (assignee && pickedIds.includes(assignee)) return;
    const lead = pickedIds.find((id) => invitable.find((a) => a.id === id)?.role === "lead");
    setAssignee(lead ?? pickedIds[0] ?? "");
  }, [pickedIds.join(","), invitable, assignee]);

  // 참여자에서 빠진 에이전트가 제출자로 남으면 아무도 못 채우는 종료 조건이 된다 — assignee 로 되돌린다.
  useEffect(() => {
    if (submitter && !pickedIds.includes(submitter)) setSubmitter("");
  }, [pickedIds.join(","), submitter]);

  /** 선택한 런타임에 없는 `runtime_kind` 의 프로파일은 경고(거부 아님). */
  const runtimeKinds = useMemo(() => {
    const rts = runtimeId ? online.filter((r) => r.id === runtimeId) : online;
    return new Set(rts.flatMap((r) => r.capabilities.filter((c) => c.logged_in).map((c) => c.kind)));
  }, [online, runtimeId]);
  const profileWarnings = useMemo(
    () =>
      pickedIds.flatMap((id) => {
        const a = invitable.find((x) => x.id === id);
        const prof = a?.profiles.find((p) => p.id === picked[id]) ?? a?.profiles.find((p) => p.is_default) ?? a?.profiles[0];
        return prof && !runtimeKinds.has(prof.runtime_kind) ? [`@${a!.name} 의 프로파일(${prof.runtime_kind})은 선택한 런타임에 없습니다`] : [];
      }),
    [pickedIds.join(","), picked, invitable, runtimeKinds],
  );

  const humanGate = conds.includes("user_approval") || conds.includes("manual");
  /** 화면에 쓰는 제출자 이름 — 지정이 없으면 역할(`assignee`) 그대로 말한다. */
  const submitterLabel = submitter ? `@${invitable.find((a) => a.id === submitter)?.name ?? submitter}` : "assignee";
  const stepBlocked = ((): string | null => {
    if (step === 0) return title.trim() && goal.trim() ? null : "제목과 goal 을 입력하세요";
    if (step === 2 && isolation === "worktree") {
      if (!repoPath) return "저장소를 고르세요";
      if (checking) return "저장소 확인 중…";
      if (repoCheckError) return repoCheckError;
      if (!repoCheck?.ok) return repoCheck?.problems?.[0] ?? "저장소 검증이 필요합니다";
    }
    if (step === 3 && isolation !== "none" && !runtimeId) return "런타임을 고르세요";
    if (step === 4 && pickedIds.length === 0) return "참여자를 1명 이상 고르세요";
    if (step === 5 && conds.length === 0) return "종료 조건을 하나 이상 고르세요";
    return null;
  })();

  async function submit() {
    if (!workspace || !me) return;
    setBusy(true);
    setError(null);
    try {
      const s = await api.post("/workspaces/{workspaceId}/sessions", {
        path: { workspaceId: workspace.id },
        body: {
          title: title.trim(),
          goal: goal.trim(),
          acceptance_criteria: criteria,
          director_user_id: directorId || me.user.id,
          deputy_director_user_id: deputyId || null,
          isolation: isolation === "worktree" ? { kind: "worktree", repo_path: repoPath, remote_url: selectedRepo?.remote_url ?? null } : { kind: "none" },
          runtime_id: runtimeId,
          participants: pickedIds.map((id) => ({ agent_id: id, profile_id: picked[id] ?? null })),
          assignee_agent_id: assignee || undefined,
          context: contextSessionId ? [{ type: "session" as const, ref: contextSessionId }] : [],
          completion_condition: {
            op,
            // CompletionAtom: `who` 는 역할(`assignee`), `agent_id` 는 **`who` 대신** 쓰는 지정 에이전트다.
            // 둘을 함께 보내지 않는다 — 계약이 배타로 적었다. E6-02 는 이 지정으로 판정된다.
            conditions: conds.map((t) =>
              t !== "artifact_submitted" ? { type: t } : submitter ? { type: t, agent_id: submitter } : { type: t, who: "assignee" },
            ),
          },
          limits: {
            budget_usd: budget ? Number(budget) : null,
            budget_tokens: null,
            time_limit: timeLimit || null,
            max_tasks: maxTasks ? Number(maxTasks) : null,
            max_parallel_lanes: Number(maxLanes) || 5,
          },
          autonomy,
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

  if (noRuntime) {
    return (
      <div className="content--narrow">
        <div className="page-head"><h1>새 세션</h1></div>
        <div className="empty" data-testid="new-session-no-runtime">
          <div className="empty__title">먼저 컴퓨터를 연결하세요</div>
          <div className="empty__body">세션은 런타임에 묶입니다(FR-2.1). 실행되지 않을 세션을 만들면 원인을 모른 채 기다리게 됩니다.</div>
          <Link href="/runtimes/new" className="btn btn--primary">Add a computer</Link>
        </div>
      </div>
    );
  }

  const last = step === STEPS.length - 1;

  return (
    <div className="content--narrow" data-testid="session-wizard" data-step={step}>
      <div className="page-head">
        <h1>새 세션</h1>
        <Link href="/sessions" className="btn btn--ghost btn--sm">취소</Link>
      </div>
      <div className="steps" data-testid="wizard-steps">
        {STEPS.map((s, i) => (
          <span key={s} className={`steps__item${i === step ? " steps__item--current" : i < step ? " steps__item--done" : ""}`}>
            {i + 1}. {s}
          </span>
        ))}
      </div>
      {error && <p className="problem" role="alert" data-testid="new-session-error">{error}</p>}

      {step === 0 && (
        <section data-testid="wizard-goal">
          <label className="field">
            <span className="field__label">제목</span>
            <input className="input" required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="결제 시장 조사" data-testid="session-title" />
          </label>
          <label className="field">
            <span className="field__label">goal (필수)</span>
            <textarea className="textarea" required value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="국내 B2B SaaS 결제 시장 조사 보고서 10페이지" data-testid="session-goal" />
          </label>
          <div className="field">
            <span className="field__label">성공 기준 (선택)</span>
            <div className="row">
              <input className="input" value={criterion} onChange={(e) => setCriterion(e.target.value)} placeholder="출처가 모두 링크로 남아 있다" data-testid="criterion-input" />
              <button type="button" className="btn btn--sm" onClick={() => { if (criterion.trim()) { setCriteria((c) => [...c, criterion.trim()]); setCriterion(""); } }}>추가</button>
            </div>
            <ul className="small muted" data-testid="criteria-list">
              {criteria.map((c, i) => (
                <li key={i}>{c} <button type="button" className="chip__x" aria-label={`${c} 삭제`} onClick={() => setCriteria((x) => x.filter((_, j) => j !== i))}>✕</button></li>
              ))}
            </ul>
          </div>
          <label className="field">
            <span className="field__label">이전 세션 첨부 (선택)</span>
            <select className="select" value={contextSessionId} onChange={(e) => setContextSessionId(e.target.value)} data-testid="context-session">
              <option value="">첨부 없음</option>
              {pastSessions.map((s) => <option key={s.id} value={s.id}>{s.title}</option>)}
            </select>
            {contextSessionId && <span className="field__hint" data-testid="context-token-note">요약 약 1,400 토큰 (상한 2,000) · 아티팩트는 링크만 실립니다(FR-4.4)</span>}
          </label>
        </section>
      )}

      {step === 1 && (
        <section data-testid="wizard-director">
          <label className="field">
            <span className="field__label">Director</span>
            <select className="select" value={directorId} onChange={(e) => setDirectorId(e.target.value)} data-testid="director-select">
              {members.map((m) => <option key={m.user.id} value={m.user.id}>{m.user.display_name}{m.user.id === me?.user.id ? " (본인)" : ""}</option>)}
            </select>
          </label>
          <label className="field">
            <span className="field__label">deputy (선택)</span>
            <select className="select" value={deputyId} onChange={(e) => setDeputyId(e.target.value)} data-testid="deputy-select">
              <option value="">없음</option>
              {members.filter((m) => m.user.id !== directorId).map((m) => <option key={m.user.id} value={m.user.id}>{m.user.display_name}</option>)}
            </select>
            <span className="field__hint">deputy 는 <b>취소는 즉시</b>, <b>승인은 기한 절반이 지난 뒤</b> 할 수 있습니다(t-3).</span>
          </label>
        </section>
      )}

      {step === 2 && (
        <section data-testid="wizard-isolation">
          <p className="small muted">격리 방식이 다음 단계의 런타임 후보를 결정합니다.</p>
          {(["none", "worktree", "container"] as IsolationKind[]).map((k) => (
            <label key={k} className={`card${isolation === k ? " card--surface" : ""}`} style={{ display: "block", marginBottom: 8, opacity: k === "container" ? 0.45 : 1 }} data-testid={`isolation-${k}`}>
              <input type="radio" name="isolation" value={k} checked={isolation === k} disabled={k === "container"} onChange={() => { setIsolation(k); setRuntimeId(null); }} />
              {" "}<b>{k}</b>
              {k === "container" && <span className="chip" style={{ marginLeft: 6 }}>v1.1</span>}
              <div className="small muted-3" style={{ marginTop: 4 }}>
                {k === "none" ? "격리 없이 런타임의 작업 디렉터리에서 실행합니다. 런타임을 자동 선택할 수 있습니다(첫 실행 시 고정)."
                  : k === "worktree" ? "에이전트마다 git worktree 를 하나씩 만듭니다. 같은 remote URL 의 저장소를 가진 머신만 후보가 됩니다."
                    : "컨테이너 격리는 v1.1 입니다."}
              </div>
            </label>
          ))}
          {isolation === "worktree" && (
            <div className="field" data-testid="repo-picker">
              <span className="field__label">저장소</span>
              {repos.length === 0 && <p className="small muted-3">온라인 런타임이 보고한 저장소가 없습니다.</p>}
              <select className="select" value={repoPath} onChange={(e) => { setRepoPath(e.target.value); void checkRepo(e.target.value); }} data-testid="repo-select">
                <option value="">고르세요</option>
                {repos.map((r) => <option key={r.path} value={r.path}>{r.path} — {r.runtimeName}</option>)}
              </select>
              <div className="row" style={{ marginTop: 4 }}>
                {checking && <span className="field__hint">확인 중…</span>}
                {repoPath && !checking && (
                  // 계약: 마지막 probe 뒤에 만든 저장소는 다음 probe 까지 "없음"으로 읽힌다 — 그래서 다시 확인이 필요하다.
                  <button type="button" className="btn btn--sm" onClick={() => void checkRepo(repoPath)} data-testid="repo-recheck">다시 확인</button>
                )}
              </div>
              {repoCheckError && <p className="problem" data-testid="repo-check-error">{repoCheckError}</p>}
              {repoCheck && (
                <div className={repoCheck.ok ? "notice notice--info" : "problem"} data-testid="repo-check" data-ok={String(repoCheck.ok)}>
                  {/* 데몬 probe 가 본 것을 그대로 — 경로·git 여부·클린·브랜치·remote URL(FR-9, U6 1단계). */}
                  <div data-testid="repo-check-line">
                    {repoCheck.exists ? "경로 있음" : "경로가 없습니다"}
                    {" · "}{repoCheck.is_git ? "git 저장소" : "git 저장소가 아닙니다"}
                    {" · "}{repoCheck.clean === true ? "클린" : repoCheck.clean === false ? "클린 아님" : "클린 여부 미상"}
                  </div>
                  <div className="small" data-testid="repo-check-git">
                    브랜치 {repoCheck.current_branch ?? "?"}{repoCheck.default_branch ? ` (기본 ${repoCheck.default_branch})` : ""}
                    {" · remote "}{repoCheck.remote_url ?? "없음"}
                  </div>
                  {!repoCheck.ok && (
                    <div className="small" data-testid="repo-check-problems">
                      {repoCheck.problems?.join(" · ") || "검증 실패"} — 커밋하거나 stash 후 다시 확인하세요.
                    </div>
                  )}
                  {repoCheck.tracks_brief_file && (
                    // §8.4 우회 B — 지시 파일은 읽지도 쓰지도 않는다. 사람이 놀라지 않게 미리 말한다.
                    <div className="small" data-testid="repo-check-brief">
                      이 저장소는 지시 파일(CLAUDE.md·AGENTS.md)을 추적 중입니다 — 브리프는 그 파일이 아니라 워크트리 안의
                      미추적 <code>COLAB_BRIEF.md</code> 로 전달되어 <code>git status</code> 를 더럽히지 않습니다.
                    </div>
                  )}
                  {repoCheck.checked_at && <div className="small muted-3">확인 {new Date(repoCheck.checked_at).toLocaleString("ko-KR")}</div>}
                </div>
              )}
              {/* 바인딩 규칙(U6 1단계) — 이 선택이 되돌릴 수 없는 이유를 함께 말한다. */}
              <span className="field__hint" data-testid="worktree-binding-note">
                에이전트당 워크트리 1개 — 같은 에이전트의 lane 은 순차 실행됩니다. 브랜치는 <code>colab/&lt;세션&gt;/&lt;에이전트&gt;</code>,
                워크트리는 세션이 끝난 뒤 보존 기한까지 남습니다. 이 바인딩은 나중에 바꿀 수 없습니다.
              </span>
            </div>
          )}
        </section>
      )}

      {step === 3 && (
        <section data-testid="wizard-runtime">
          {candidates === null ? <p className="muted">후보를 확인하는 중…</p> : (
            <>
              {/* 숨기지 않고 **비활성 + 사유**로 남긴다(SCREEN §7 · U6 2단계 "자동 선택 비활성"). 사라진 선택지는 이유를 말하지 못한다. */}
              <label
                className={`card${runtimeId === null && candidates.auto_select_allowed ? " card--surface" : ""}`}
                style={{ display: "block", marginBottom: 8, opacity: candidates.auto_select_allowed ? 1 : 0.45 }}
                data-testid="runtime-auto"
                data-allowed={String(candidates.auto_select_allowed)}
              >
                <input type="radio" name="runtime" disabled={!candidates.auto_select_allowed} checked={runtimeId === null && candidates.auto_select_allowed} onChange={() => setRuntimeId(null)} />
                {" "}<b>자동 선택 (첫 실행 시 고정)</b>
                <div className="small muted-3">
                  {candidates.auto_select_allowed
                    ? "첫 task 를 보낼 때 온라인 런타임 하나로 고정됩니다(FR-2.1 M10)."
                    : "worktree 격리에서는 고를 수 없습니다 — 워크트리는 저장소가 있는 그 머신에서만 만들 수 있습니다."}
                </div>
              </label>
              {candidates.candidates.map((c) => (
                <label key={c.runtime.id} className={`card${runtimeId === c.runtime.id ? " card--surface" : ""}`} style={{ display: "block", marginBottom: 8, opacity: c.eligible ? 1 : 0.45 }} data-testid="runtime-candidate" data-eligible={String(c.eligible)}>
                  <input type="radio" name="runtime" disabled={!c.eligible} checked={runtimeId === c.runtime.id} onChange={() => setRuntimeId(c.runtime.id)} />
                  {" "}<b>{c.runtime.name}</b> <span className="small muted-3">{c.runtime.status === "online" ? "온라인" : "오프라인"}</span>
                  <div className="small muted-3">
                    {c.eligible
                      ? `${c.runtime.capabilities.map((x) => `${x.kind}${x.logged_in ? "" : "(로그인 필요)"}`).join(" · ")}${c.matched_repo ? ` · ${c.matched_repo.path}` : ""}`
                      : c.reason}
                  </div>
                  {c.runtime.colab_cli?.present === false && (
                    <div className="small" style={{ color: "var(--s-fail-text)" }} data-testid="candidate-no-cli">colab CLI 미설치 — 세션이 조용히 아무 말도 못 합니다</div>
                  )}
                </label>
              ))}
              {candidates.candidates.every((c) => !c.eligible) && (
                <p className="problem" data-testid="no-candidate">조건에 맞는 런타임이 없습니다 — 격리 방식을 바꾸거나 저장소를 가진 컴퓨터를 연결하세요.</p>
              )}
            </>
          )}
        </section>
      )}

      {step === 4 && (
        <section data-testid="wizard-participants">
          <p className="small muted">에이전트를 고르고 프로파일과 assignee 를 정합니다.</p>
          {invitable.map((a) => {
            const on = a.id in picked;
            const allowed = a.invitable.allowed;
            return (
              <div key={a.id} className={`card${on ? " card--surface" : ""}`} style={{ marginBottom: 8, opacity: allowed ? 1 : 0.45 }} data-testid="participant-option" data-agent-id={a.id} data-allowed={String(allowed)}>
                <label>
                  <input
                    type="checkbox"
                    checked={on}
                    disabled={!allowed}
                    onChange={(e) => setPicked((p) => { const n = { ...p }; if (e.target.checked) n[a.id] = a.profiles.find((x) => x.is_default)?.id ?? a.profiles[0]?.id ?? null; else delete n[a.id]; return n; })}
                  />{" "}
                  <b>@{a.name}</b> <span className="small muted-3">{a.role} · {a.role_description}</span>
                </label>
                {!allowed && <div className="small" style={{ color: "var(--s-wait-text)" }} data-testid="not-invitable">{a.invitable.reason ?? "초대 권한 없음"}</div>}
                {on && (
                  <div className="row small" style={{ marginTop: 6 }}>
                    <select className="select" style={{ width: "auto" }} value={picked[a.id] ?? ""} onChange={(e) => setPicked((p) => ({ ...p, [a.id]: e.target.value }))} data-testid="profile-select" aria-label={`${a.name} 프로파일`}>
                      {a.profiles.map((pr) => <option key={pr.id} value={pr.id}>{pr.name} — {pr.runtime_kind} · {pr.model}</option>)}
                    </select>
                    <label>
                      <input type="radio" name="assignee" checked={assignee === a.id} onChange={() => setAssignee(a.id)} data-testid="assignee-radio" /> assignee
                    </label>
                  </div>
                )}
              </div>
            );
          })}
          {profileWarnings.map((w, i) => <p key={i} className="notice" data-testid="profile-warning">⚠ {w}</p>)}
        </section>
      )}

      {step === 5 && (
        <section data-testid="wizard-conditions">
          <div className="row" style={{ marginBottom: 8 }}>
            <span className="small muted">조건 결합</span>
            <select className="select" style={{ width: "auto" }} value={op} onChange={(e) => setOp(e.target.value as "and" | "or")} data-testid="cond-op">
              <option value="and">모두 충족 (AND)</option>
              <option value="or">하나만 충족 (OR)</option>
            </select>
          </div>
          <div className="stack" style={{ gap: 6 }}>
            {COND_ORDER.map((t) => (
              <div key={t} className="stack" style={{ gap: 4 }}>
                <ConditionRow
                  type={t}
                  met={null}
                  variant="wizard"
                  selected={conds.includes(t)}
                  who={t === "artifact_submitted" ? submitterLabel : undefined}
                  onToggle={(next) => setConds((c) => (next ? [...c, t] : c.filter((x) => x !== t)))}
                />
                {/* 제출자를 고를 수 없으면 시나리오 A 3단계("**Writer 가** 아티팩트 제출")를 화면으로 만들 수 없다.
                    E6-02 는 "지정 에이전트가 아니면 미충족" 이므로 그 지정이 여기서 나온다. 기본값은 assignee 다. */}
                {t === "artifact_submitted" && conds.includes(t) && (
                  <label className="row small" style={{ paddingLeft: 26 }}>
                    <span className="muted">제출자</span>
                    <select
                      className="select"
                      style={{ width: "auto" }}
                      value={submitter}
                      onChange={(e) => setSubmitter(e.target.value)}
                      data-testid="submitter-select"
                      aria-label="아티팩트 제출자"
                    >
                      <option value="">assignee (기본) — 담당이 바뀌면 따라갑니다</option>
                      {pickedIds.map((id) => (
                        <option key={id} value={id}>@{invitable.find((a) => a.id === id)?.name ?? id}</option>
                      ))}
                    </select>
                  </label>
                )}
              </div>
            ))}
            <ConditionRow type="criteria_met" met={null} variant="wizard" disabled disabledNote="v1.1 — 성공 기준 자동 판정은 아직 없습니다" />
          </div>
          {!humanGate && conds.length > 0 && (
            <p className="notice" data-testid="no-human-gate-warning">⚠ 사람 승인 없이 완료됩니다 — 종료 조건에 Director 승인이나 수동 종료가 없습니다.</p>
          )}
        </section>
      )}

      {step === 6 && (
        <section data-testid="wizard-limits">
          <p className="small muted">초과는 완료가 아니라 <b>일시정지</b>입니다 — Director 가 승인하면 같은 자리에서 이어집니다(FR-7.3).</p>
          <div className="row">
            <label className="field" style={{ flex: 1 }}>
              <span className="field__label">예산 (USD)</span>
              <input className="input" type="number" min={0} value={budget} onChange={(e) => setBudget(e.target.value)} data-testid="limit-budget" />
            </label>
            <label className="field" style={{ flex: 1 }}>
              <span className="field__label">시간 상한</span>
              <select className="select" value={timeLimit} onChange={(e) => setTimeLimit(e.target.value)} data-testid="limit-time">
                <option value="">없음</option>
                <option value="PT1H">1시간</option>
                <option value="PT4H">4시간</option>
                <option value="P1D">1일</option>
              </select>
            </label>
          </div>
          <div className="row">
            <label className="field" style={{ flex: 1 }}>
              <span className="field__label">최대 task (비우면 없음)</span>
              <input className="input" type="number" min={1} value={maxTasks} onChange={(e) => setMaxTasks(e.target.value)} data-testid="limit-tasks" />
            </label>
            <label className="field" style={{ flex: 1 }}>
              <span className="field__label">최대 병렬 lane</span>
              <input className="input" type="number" min={1} value={maxLanes} onChange={(e) => setMaxLanes(e.target.value)} data-testid="limit-lanes" />
            </label>
          </div>
          {online.some((r) => r.capabilities.some((c) => c.usage === false)) && (
            <p className="notice notice--info" data-testid="estimate-note">사용량을 보고하지 않는 런타임이 있습니다 — 그 실행의 비용은 추정치이고 하드 컷을 하지 않습니다.</p>
          )}
          <div className="field">
            <span className="field__label">autonomy</span>
            {AUTONOMY.map((a) => (
              <label key={a.value} className="card" style={{ display: "block", marginBottom: 6, opacity: a.enabled ? 1 : 0.45 }} data-testid={`autonomy-${a.value}`}>
                <input type="radio" name="autonomy" disabled={!a.enabled} checked={autonomy === a.value} onChange={() => setAutonomy(a.value)} />
                {" "}<b>{a.label}</b>{!a.enabled && <span className="chip" style={{ marginLeft: 6 }}>v1.1</span>}
                <div className="small muted-3">{a.note}</div>
              </label>
            ))}
            <span className="field__hint">승인(approval)은 autonomy 와 무관하게 절대 자동 진행되지 않습니다(FR-5.4).</span>
          </div>

          <h2 style={{ fontSize: 14, margin: "16px 0 8px" }}>요약</h2>
          <div className="card card--surface small" data-testid="wizard-summary">
            <ul style={{ margin: 0, paddingLeft: 18, lineHeight: 1.8 }}>
              <li>제목 <b>{title || "—"}</b> · goal {goal.slice(0, 60) || "—"}</li>
              <li>Director <b>{members.find((m) => m.user.id === directorId)?.user.display_name ?? "—"}</b>{deputyId ? ` · deputy ${members.find((m) => m.user.id === deputyId)?.user.display_name}` : " · deputy 없음"}</li>
              <li>격리 <b>{isolation}</b>{isolation === "worktree" ? ` · ${repoPath}` : ""}</li>
              <li>런타임 <b>{runtimeId ? online.find((r) => r.id === runtimeId)?.name ?? runtimeId : "자동 선택(첫 실행 시 고정)"}</b></li>
              <li>참여자 {pickedIds.map((id) => `@${invitable.find((a) => a.id === id)?.name}`).join(", ") || "—"} · assignee <b>@{invitable.find((a) => a.id === assignee)?.name ?? "—"}</b></li>
              <li>종료 조건 <b>{conds.join(op === "and" ? " AND " : " OR ")}</b>{conds.includes("artifact_submitted") ? ` · 제출자 ${submitterLabel}` : ""}{humanGate ? "" : " — 사람 승인 없음"}</li>
              <li>한도 {budget ? `$${budget}` : "예산 없음"} · {timeLimit || "시간 제한 없음"} · autonomy <b>{autonomy}</b></li>
            </ul>
          </div>
        </section>
      )}

      <div className="row" style={{ marginTop: 16, justifyContent: "space-between" }}>
        <button type="button" className="btn" disabled={step === 0} onClick={() => setStep((x) => x - 1)} data-testid="wizard-back">이전</button>
        <div className="row">
          {stepBlocked && <span className="small" style={{ color: "var(--s-wait-text)" }} data-testid="wizard-blocked">{stepBlocked}</span>}
          {last ? (
            <button type="button" className="btn btn--primary" disabled={busy || !!stepBlocked} onClick={() => void submit()} data-testid="session-start">
              {busy ? "시작 중…" : "시작"}
            </button>
          ) : (
            <button type="button" className="btn btn--primary" disabled={!!stepBlocked} onClick={() => setStep((x) => x + 1)} data-testid="wizard-next">다음</button>
          )}
        </div>
      </div>
    </div>
  );
}
