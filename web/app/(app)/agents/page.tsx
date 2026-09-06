"use client";
/**
 * S9 Agents 목록(SCREEN §4.7) — 카드마다 이름·역할·역할 설명·상태·기본 프로파일·소유자·`respond_to`.
 * 필터(역할·런타임·상태·소유자), 새 에이전트, **팀 템플릿 3종**(FR-1.4).
 *
 * 템플릿은 G5 실측 대상이다 — **템플릿에서 팀 생성까지 3분**. 그래서 고르는 순간 프로파일 자동 매핑 결과를 보여주고,
 * 적용은 클릭 한 번이며, 매핑 실패도 등록은 하되 사유를 남긴다.
 * `.agent.md` 가져오기·Build with AI 는 v1.1 이라 여기 없다.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Badge } from "@/components/Badge";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import type { Agent, AgentRole, AgentTemplate, RespondTo } from "@/lib/api/types";

const RESPOND_TO_LABEL: Record<RespondTo, string> = {
  owner: "소유자만",
  allowlist: "허용 목록",
  workspace: "워크스페이스 전체",
  nobody: "정지됨(킬 스위치)",
};

export default function AgentsPage() {
  const { workspace, me } = useAuth();
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [templates, setTemplates] = useState<AgentTemplate[] | null>(null);
  const [openTemplates, setOpenTemplates] = useState(false);
  const [applying, setApplying] = useState<string | null>(null);
  const [applied, setApplied] = useState<{ names: string[]; unmapped: string[] } | null>(null);
  const [role, setRole] = useState<string>("");
  const [status, setStatus] = useState<string>("");
  const [mine, setMine] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const r = await api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } });
      setAgents(r.items);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspace]);
  useEffect(() => { void load(); }, [load]);

  const loadTemplates = useCallback(async () => {
    if (!workspace || templates) return;
    try {
      setTemplates(await api.get("/workspaces/{workspaceId}/agent-templates", { path: { workspaceId: workspace.id } }));
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspace, templates]);

  async function apply(key: AgentTemplate["key"]) {
    if (!workspace) return;
    setApplying(key);
    setError(null);
    try {
      const r = await api.post("/workspaces/{workspaceId}/agent-templates/{templateKey}/apply", {
        path: { workspaceId: workspace.id, templateKey: key },
        body: {},
      });
      setApplied({ names: r.agents.map((a) => a.name), unmapped: r.unmapped.map((u) => u.reason) });
      setOpenTemplates(false);
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setApplying(null);
    }
  }

  const shown = useMemo(() => {
    let list = (agents ?? []).filter((a) => !a.archived_at);
    if (role) list = list.filter((a) => a.role === role);
    if (status) list = list.filter((a) => a.status === status);
    if (mine && me) list = list.filter((a) => a.owner_id === me.user.id);
    return list;
  }, [agents, role, status, mine, me]);

  return (
    <div>
      <div className="page-head">
        <h1>Agents</h1>
        <div className="row">
          <button type="button" className="btn" onClick={() => { setOpenTemplates((v) => !v); void loadTemplates(); }} data-testid="open-templates">
            팀 템플릿
          </button>
          <Link href="/agents/new" className="btn btn--primary" data-testid="new-agent">새 에이전트</Link>
        </div>
      </div>
      {error && <p className="problem" role="alert">{error}</p>}

      {applied && (
        <div className="notice notice--info" data-testid="template-applied">
          <b>{applied.names.length}명 생성됨</b> — {applied.names.map((n) => `@${n}`).join(", ")}
          {applied.unmapped.length > 0 && <div className="small">프로파일 미매핑 {applied.unmapped.length}건: {applied.unmapped.join(" · ")}</div>}
        </div>
      )}

      {openTemplates && (
        <section className="card" style={{ marginBottom: 16 }} data-testid="team-templates">
          <h2 style={{ fontSize: 14, margin: "0 0 4px" }}>팀 템플릿</h2>
          <p className="small muted-3" style={{ marginTop: 0 }}>
            역할과 instruction 만 담깁니다. 프로파일은 이 워크스페이스에서 감지된 런타임에 맞춰 자동 매핑됩니다(FR-1.4).
          </p>
          {templates === null ? <p className="muted small">불러오는 중…</p> : (
            <div className="story__grid">
              {templates.map((t) => (
                <div key={t.key} className="card" data-testid="template-card" data-key={t.key}>
                  <b>{t.name}</b>
                  <p className="small muted" style={{ margin: "4px 0" }}>{t.description}</p>
                  <ul className="small muted-3" style={{ margin: "0 0 8px", paddingLeft: 16 }}>
                    {t.agents.map((a) => (
                      <li key={a.key} data-testid="template-agent" data-mapping={a.mapping.status}>
                        @{a.name} <span className="muted-3">({a.role})</span>{" "}
                        {a.mapping.status === "mapped"
                          ? <span style={{ color: "var(--s-done-text)" }}>{a.mapping.runtime_kind} · {a.mapping.model}</span>
                          : <span style={{ color: "var(--s-wait-text)" }}>미매핑 — {a.mapping.reason}</span>}
                        {a.mapping.status === "mapped" && a.mapping.reason && <span className="muted-3"> · {a.mapping.reason}</span>}
                      </li>
                    ))}
                  </ul>
                  <button type="button" className="btn btn--primary btn--sm" disabled={applying !== null} onClick={() => void apply(t.key)} data-testid={`apply-${t.key}`}>
                    {applying === t.key ? "만드는 중…" : "이 팀 만들기"}
                  </button>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      <div className="row" style={{ marginBottom: 12 }} data-testid="agent-filters">
        <select className="select" style={{ width: "auto" }} value={role} onChange={(e) => setRole(e.target.value)} aria-label="역할 필터">
          <option value="">역할 전체</option>
          {(["lead", "researcher", "writer", "engineer", "reviewer", "custom"] as AgentRole[]).map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
        <select className="select" style={{ width: "auto" }} value={status} onChange={(e) => setStatus(e.target.value)} aria-label="상태 필터">
          <option value="">상태 전체</option>
          {["idle", "working", "waiting_human", "error", "offline", "disabled"].map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <label className="small muted">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} /> 내가 만든 것만
        </label>
      </div>

      {agents === null ? (
        <p className="muted">불러오는 중…</p>
      ) : shown.length === 0 ? (
        <div className="empty" data-testid="empty-agents">
          <div className="empty__title">{agents.length === 0 ? "아직 에이전트가 없습니다" : "조건에 맞는 에이전트가 없습니다"}</div>
          <div className="empty__body">팀 템플릿으로 세 명을 한 번에 만들거나, 하나씩 등록하세요.</div>
          <button type="button" className="btn btn--primary" onClick={() => { setOpenTemplates(true); void loadTemplates(); }}>팀 템플릿 보기</button>
        </div>
      ) : (
        <div className="story__grid">
          {shown.map((a) => {
            const prof = a.profiles.find((p) => p.is_default) ?? a.profiles[0];
            return (
              <Link key={a.id} href={`/agents/${a.id}`} className="card" data-testid="agent-card" data-agent-id={a.id} style={{ textDecoration: "none", display: "block" }}>
                <div className="row" style={{ justifyContent: "space-between" }}>
                  <b>@{a.name}</b>
                  <Badge kind="agent" value={a.status} size="sm" />
                </div>
                <div className="small muted-3">{a.role} · {a.role_description}</div>
                <div className="small muted" style={{ marginTop: 6 }}>
                  {prof ? `${prof.runtime_kind} · ${prof.model}` : "프로파일 없음"}
                </div>
                <div className="small muted-3" style={{ marginTop: 4 }} data-testid="agent-respond-to">
                  응답 대상: {RESPOND_TO_LABEL[a.respond_to]}
                  {a.owner_id === me?.user.id ? " · 내 에이전트" : ""}
                </div>
                {a.definition_source && <div className="small muted-3">템플릿 {a.definition_source} v{a.definition_version}</div>}
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}
