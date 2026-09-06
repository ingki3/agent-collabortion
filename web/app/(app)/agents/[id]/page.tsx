"use client";
/**
 * S10 Agent 편집(SCREEN §4.7) — 3구역 + 위험 영역.
 *
 * 정체성: 이름(멘션 라벨, 워크스페이스 내 유일) · 역할 태그 · **역할 설명**(다른 에이전트의 로스터에 노출되므로
 *   위임 판단의 근거가 된다) · 아바타.
 * instruction: 시스템 프롬프트. `lead` 를 고르면 "코디네이션 프로토콜이 자동 추가됩니다"를 알린다.
 * 실행: 프로파일 목록(추가·삭제·기본·폴백) · 툴 허용 목록 · `max_concurrent_tasks` · `budget_per_task` · `respond_to`.
 *
 * 위험 영역 — **`respond_to: nobody` 는 킬 스위치**(FR-1.9). 켜기 전에 무슨 일이 일어나는지 그대로 확인시킨다:
 * "실행 중인 턴이 취소되고 대기 중 task 가 취소됩니다. 열린 HITL 은 남습니다."
 * 테스트 채팅(FR-1.8.1)은 P3 라 자리와 사유만 둔다.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { Badge } from "@/components/Badge";
import { AgentProfileEditor } from "@/components/AgentProfileEditor";
import { capabilityIndex } from "@/lib/runtime-options";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import type { Agent, AgentProfile, AgentRole, RespondTo, Runtime, RuntimeKind } from "@/lib/api/types";

const ROLES: AgentRole[] = ["lead", "researcher", "writer", "engineer", "reviewer", "custom"];
const RESPOND_TO: { value: RespondTo; label: string; note: string }[] = [
  { value: "owner", label: "소유자만", note: "내가 만든 세션에서만 응답합니다" },
  { value: "allowlist", label: "허용 목록", note: "지정한 멤버가 부를 때만 응답합니다" },
  { value: "workspace", label: "워크스페이스 전체", note: "워크스페이스의 누구든 부를 수 있습니다" },
  { value: "nobody", label: "정지(킬 스위치)", note: "아무에게도 응답하지 않습니다" },
];

export default function AgentEditPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const { workspace, me, canManage } = useAuth();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [runtimes, setRuntimes] = useState<Runtime[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [profileError, setProfileError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmKill, setConfirmKill] = useState(false);

  // 편집 중인 값
  const [name, setName] = useState("");
  const [role, setRole] = useState<AgentRole>("custom");
  const [roleDesc, setRoleDesc] = useState("");
  const [instructions, setInstructions] = useState("");
  const [tools, setTools] = useState("");
  const [maxConcurrent, setMaxConcurrent] = useState("3");
  const [budgetPerTask, setBudgetPerTask] = useState("");

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [a, rts] = await Promise.all([
        api.get("/agents/{agentId}", { path: { agentId: id } }),
        api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }),
      ]);
      setAgent(a);
      setRuntimes(rts);
      setName(a.name);
      setRole(a.role);
      setRoleDesc(a.role_description);
      setInstructions(a.instructions);
      setTools(a.tools.join(", "));
      setMaxConcurrent(String(a.max_concurrent_tasks ?? 3));
      setBudgetPerTask(a.budget_per_task == null ? "" : String(a.budget_per_task));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [id, workspace]);
  useEffect(() => { void load(); }, [load]);

  /** 모델·옵션 목록은 데몬 probe 결과로 채운다(§8.2) — 온라인·로그인된 능력만. */
  const caps = useMemo(() => capabilityIndex(runtimes), [runtimes]);

  const isOwner = !!agent && !!me && agent.owner_id === me.user.id;
  const canEdit = isOwner || canManage;
  const editReason = "소유자와 owner·admin 만 수정할 수 있습니다";

  async function patch(body: Record<string, unknown>) {
    setBusy(true);
    setError(null);
    try {
      const a = await api.patch("/agents/{agentId}", { path: { agentId: id }, body });
      setAgent(a);
      setSaved(true);
      setTimeout(() => setSaved(false), 1500);
      return a;
    } catch (e) {
      setError(errorMessage(e));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    await patch({
      name: name.trim(),
      role,
      role_description: roleDesc,
      instructions,
      tools: tools.split(",").map((t) => t.trim()).filter(Boolean),
      max_concurrent_tasks: Number(maxConcurrent) || 3,
      budget_per_task: budgetPerTask ? Number(budgetPerTask) : null,
    });
  }

  async function setRespondTo(v: RespondTo) {
    if (v === "nobody") { setConfirmKill(true); return; }
    await patch({ respond_to: v });
  }

  async function createProfile(p: { name: string; runtime_kind: RuntimeKind; model: string; options: Record<string, unknown> }) {
    setProfileError(null);
    try {
      await api.post("/agents/{agentId}/profiles", { path: { agentId: id }, body: p });
      await load();
    } catch (e) {
      setProfileError(errorMessage(e));
    }
  }
  async function updateProfile(pid: string, body: Partial<AgentProfile>) {
    setProfileError(null);
    try {
      await api.patch("/agents/{agentId}/profiles/{profileId}", { path: { agentId: id, profileId: pid }, body });
      await load();
    } catch (e) {
      setProfileError(errorMessage(e));
    }
  }
  async function deleteProfile(pid: string) {
    setProfileError(null);
    try {
      await api.delete("/agents/{agentId}/profiles/{profileId}", { path: { agentId: id, profileId: pid } });
      await load();
    } catch (e) {
      setProfileError(errorMessage(e));
    }
  }

  if (error && !agent) {
    return (
      <div>
        <p className="problem">{error}</p>
        <Link href="/agents" className="btn">Agents 로</Link>
      </div>
    );
  }
  if (!agent) return <p className="muted">불러오는 중…</p>;

  return (
    <div className="content--narrow" data-testid="agent-editor" data-agent-id={agent.id}>
      <div className="page-head">
        <div className="row">
          <Link href="/agents" className="small muted-3">← Agents</Link>
          <h1>@{agent.name}</h1>
          <Badge kind="agent" value={agent.status} size="sm" />
        </div>
        <div className="row">
          {saved && <span className="small" style={{ color: "var(--s-done-text)" }} data-testid="agent-saved">저장됨</span>}
          <button type="button" className="btn btn--primary" disabled={!canEdit || busy} title={canEdit ? undefined : editReason} onClick={() => void save()} data-testid="agent-save">저장</button>
        </div>
      </div>
      {error && <p className="problem" role="alert">{error}</p>}
      {!canEdit && <p className="notice notice--info" data-testid="agent-readonly">{editReason} — 소유자는 {agent.owner?.display_name ?? "다른 멤버"} 입니다.</p>}

      <section className="card" style={{ marginBottom: 14 }} data-testid="agent-identity">
        <h2 style={{ fontSize: 14, margin: "0 0 8px" }}>정체성</h2>
        <label className="field">
          <span className="field__label">이름 (멘션 라벨 · 워크스페이스 내 유일)</span>
          <input className="input" value={name} maxLength={40} disabled={!canEdit} onChange={(e) => setName(e.target.value)} data-testid="agent-name" />
        </label>
        <label className="field">
          <span className="field__label">역할</span>
          <select className="select" value={role} disabled={!canEdit} onChange={(e) => setRole(e.target.value as AgentRole)} data-testid="agent-role">
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
          {role === "lead" && <span className="field__hint" data-testid="lead-protocol-note">코디네이션 프로토콜이 instruction 에 자동 추가됩니다.</span>}
        </label>
        <label className="field">
          <span className="field__label">역할 설명</span>
          <input className="input" value={roleDesc} disabled={!canEdit} onChange={(e) => setRoleDesc(e.target.value)} data-testid="agent-role-desc" />
          <span className="field__hint">다른 에이전트의 로스터에 노출됩니다 — 위임 판단의 근거가 됩니다.</span>
        </label>
      </section>

      <section className="card" style={{ marginBottom: 14 }} data-testid="agent-instructions">
        <h2 style={{ fontSize: 14, margin: "0 0 8px" }}>instruction</h2>
        <textarea className="textarea" style={{ minHeight: 160 }} value={instructions} disabled={!canEdit} onChange={(e) => setInstructions(e.target.value)} data-testid="agent-instructions-input" aria-label="시스템 프롬프트" />
      </section>

      <section className="card" style={{ marginBottom: 14 }} data-testid="agent-execution">
        <h2 style={{ fontSize: 14, margin: "0 0 8px" }}>실행</h2>
        <AgentProfileEditor
          profiles={agent.profiles}
          caps={caps}
          canEdit={canEdit}
          disabledReason={editReason}
          onCreate={createProfile}
          onUpdate={updateProfile}
          onDelete={deleteProfile}
          error={profileError}
        />
        <label className="field" style={{ marginTop: 12 }}>
          <span className="field__label">툴 · MCP 허용 목록 (쉼표로 구분)</span>
          <input className="input" value={tools} disabled={!canEdit} onChange={(e) => setTools(e.target.value)} placeholder="Read, Edit, Bash" data-testid="agent-tools" />
        </label>
        <div className="row">
          <label className="field" style={{ flex: 1 }}>
            <span className="field__label">동시 task 상한</span>
            <input className="input" type="number" min={1} value={maxConcurrent} disabled={!canEdit} onChange={(e) => setMaxConcurrent(e.target.value)} data-testid="agent-max-concurrent" />
          </label>
          <label className="field" style={{ flex: 1 }}>
            <span className="field__label">task 당 예산 (USD · 비우면 없음)</span>
            <input className="input" type="number" min={0} value={budgetPerTask} disabled={!canEdit} onChange={(e) => setBudgetPerTask(e.target.value)} data-testid="agent-budget" />
          </label>
        </div>
        <div className="field">
          <span className="field__label">응답 대상 (respond_to)</span>
          {RESPOND_TO.filter((r) => r.value !== "nobody").map((r) => (
            <label key={r.value} className="row small" style={{ gap: 6 }} data-testid={`respond-to-${r.value}`}>
              <input type="radio" name="respond_to" checked={agent.respond_to === r.value} disabled={!canEdit || busy} onChange={() => void setRespondTo(r.value)} />
              <b>{r.label}</b> <span className="muted-3">{r.note}</span>
            </label>
          ))}
        </div>
      </section>

      <section className="card" style={{ borderColor: "var(--s-fail)" }} data-testid="agent-danger">
        <h2 style={{ fontSize: 14, margin: "0 0 4px", color: "var(--s-fail-text)" }}>위험 영역</h2>
        <p className="small muted" style={{ marginTop: 0 }}>
          <b>킬 스위치 — `respond_to: nobody`.</b> 이 에이전트를 즉시 정지시킵니다.
        </p>
        {agent.respond_to === "nobody" ? (
          <div className="row">
            <span className="small" style={{ color: "var(--s-fail-text)" }} data-testid="kill-switch-on">현재 정지됨 — 아무에게도 응답하지 않습니다.</span>
            <button type="button" className="btn btn--sm" disabled={!canEdit || busy} onClick={() => void patch({ respond_to: "workspace" })} data-testid="kill-switch-off">
              다시 활성화 (워크스페이스 전체)
            </button>
          </div>
        ) : confirmKill ? (
          <div className="notice" role="dialog" aria-label="킬 스위치 확인" data-testid="kill-switch-confirm">
            <p style={{ margin: "0 0 6px" }}>
              <b>실행 중인 턴이 취소되고 대기 중 task 가 취소됩니다. 열린 HITL 은 남습니다.</b>
            </p>
            <p className="small" style={{ margin: "0 0 8px" }}>답이 오면 그때 재개됩니다 — 정지를 풀기 전에는 아무것도 실행되지 않습니다.</p>
            <div className="row">
              <button type="button" className="btn btn--sm btn--primary" disabled={busy} onClick={() => { setConfirmKill(false); void patch({ respond_to: "nobody" }); }} data-testid="kill-switch-confirm-yes">
                정지시킨다
              </button>
              <button type="button" className="btn btn--sm" onClick={() => setConfirmKill(false)}>취소</button>
            </div>
          </div>
        ) : (
          <button type="button" className="btn btn--sm" disabled={!canEdit || busy} title={canEdit ? undefined : editReason} onClick={() => setConfirmKill(true)} data-testid="kill-switch">
            이 에이전트 정지 (respond_to: nobody)
          </button>
        )}
        <hr style={{ border: 0, borderTop: "1px solid var(--line)", margin: "12px 0" }} />
        <button
          type="button"
          className="btn btn--sm"
          disabled={!canEdit || busy}
          onClick={async () => {
            // 물리 삭제가 아니라 `archived_at` 이다 — 세션 이력이 이 에이전트를 참조한다(DELETE /agents/{id}).
            try {
              await api.delete("/agents/{agentId}", { path: { agentId: id } });
              router.replace("/agents");
            } catch (e) {
              setError(errorMessage(e));
            }
          }}
          data-testid="agent-archive"
        >
          보관(archive)
        </button>
        <p className="small muted-3" style={{ marginBottom: 0 }}>테스트 채팅(FR-1.8.1)은 P3 입니다 — 실행 경로와 토큰을 함께 보여주는 1:1 시험 대화가 여기 붙습니다.</p>
      </section>
    </div>
  );
}
