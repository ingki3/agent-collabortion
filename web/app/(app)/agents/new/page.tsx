"use client";
/**
 * S10 새 에이전트(SCREEN §4.7) — 편집 화면과 같은 3구역이지만 만들 때 필요한 최소만 받는다.
 * 프로파일은 **하나 이상** 필요하고(계약 `AgentCreate.profiles` minItems 1) 모델은 데몬 probe 결과에서 고른다.
 * 만든 뒤 나머지(툴·폴백·respond_to·추가 프로파일)는 S10 편집에서 이어서 한다.
 */
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { capabilityIndex } from "@/lib/runtime-options";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import type { AgentRole, Runtime, RuntimeKind } from "@/lib/api/types";

const ROLES: AgentRole[] = ["lead", "researcher", "writer", "engineer", "reviewer", "custom"];

export default function NewAgentPage() {
  const router = useRouter();
  const { workspace } = useAuth();
  const [runtimes, setRuntimes] = useState<Runtime[]>([]);
  const [name, setName] = useState("");
  const [role, setRole] = useState<AgentRole>("custom");
  const [roleDesc, setRoleDesc] = useState("");
  const [instructions, setInstructions] = useState("");
  const [kind, setKind] = useState<RuntimeKind | "">("");
  const [model, setModel] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!workspace) return;
    api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }).then(setRuntimes).catch((e) => setError(errorMessage(e)));
  }, [workspace]);

  const caps = useMemo(() => capabilityIndex(runtimes), [runtimes]);
  const models = useMemo(() => new Map([...caps].map(([k, v]) => [k, v.models] as const)), [caps]);

  useEffect(() => {
    const first = [...models.keys()][0];
    if (first && !kind) setKind(first);
  }, [models, kind]);
  useEffect(() => {
    const list = kind ? models.get(kind) ?? [] : [];
    if (list.length && !list.includes(model)) setModel(list[0]);
  }, [kind, models, model]);

  const kindModels = kind ? models.get(kind) ?? [] : [];
  const blocked = !name.trim() || !roleDesc.trim() || !instructions.trim() || !kind || !model;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!workspace || blocked) return;
    setBusy(true);
    setError(null);
    try {
      const a = await api.post("/workspaces/{workspaceId}/agents", {
        path: { workspaceId: workspace.id },
        body: {
          name: name.trim(),
          role,
          role_description: roleDesc.trim(),
          instructions: instructions.trim(),
          profiles: [{ name: "default", runtime_kind: kind as RuntimeKind, model, is_default: true }],
        },
      });
      router.replace(`/agents/${a.id}`);
    } catch (err) {
      if (isApiError(err) && err.problem.errors?.length) setError(err.problem.errors.map((x) => `${x.field}: ${x.message}`).join(" · "));
      else setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="content--narrow" data-testid="new-agent-page">
      <div className="page-head">
        <h1>새 에이전트</h1>
        <Link href="/agents" className="btn btn--ghost btn--sm">취소</Link>
      </div>
      {error && <p className="problem" role="alert" data-testid="new-agent-error">{error}</p>}
      {models.size === 0 && (
        <div className="empty" data-testid="new-agent-no-runtime">
          <div className="empty__title">먼저 컴퓨터를 연결하세요</div>
          <div className="empty__body">프로파일의 모델 목록은 데몬 probe 결과로 채워집니다.</div>
          <Link href="/runtimes/new" className="btn btn--primary">Add a computer</Link>
        </div>
      )}
      <form onSubmit={submit}>
        <label className="field">
          <span className="field__label">이름 (멘션 라벨)</span>
          <input className="input" required maxLength={40} value={name} onChange={(e) => setName(e.target.value)} placeholder="Researcher" data-testid="agent-name" />
        </label>
        <label className="field">
          <span className="field__label">역할</span>
          <select className="select" value={role} onChange={(e) => setRole(e.target.value as AgentRole)} data-testid="agent-role">
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
          {role === "lead" && <span className="field__hint" data-testid="lead-protocol-note">코디네이션 프로토콜이 instruction 에 자동 추가됩니다.</span>}
        </label>
        <label className="field">
          <span className="field__label">역할 설명</span>
          <input className="input" required value={roleDesc} onChange={(e) => setRoleDesc(e.target.value)} placeholder="자료를 찾아 근거와 함께 정리한다" data-testid="agent-role-desc" />
          <span className="field__hint">다른 에이전트의 로스터에 노출됩니다 — 위임 판단의 근거가 됩니다.</span>
        </label>
        <label className="field">
          <span className="field__label">instruction</span>
          <textarea className="textarea" required style={{ minHeight: 140 }} value={instructions} onChange={(e) => setInstructions(e.target.value)} data-testid="agent-instructions-input" />
        </label>
        <div className="row">
          <label className="field" style={{ flex: 1 }}>
            <span className="field__label">런타임 종류</span>
            <select className="select" value={kind} onChange={(e) => setKind(e.target.value as RuntimeKind)} data-testid="agent-kind">
              <option value="">고르세요</option>
              {[...models.keys()].map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </label>
          <label className="field" style={{ flex: 1 }}>
            <span className="field__label">모델 (probe 결과)</span>
            <select className="select" value={model} onChange={(e) => setModel(e.target.value)} data-testid="agent-model">
              {kindModels.length === 0 && <option value="">감지된 모델 없음</option>}
              {kindModels.map((m) => <option key={m} value={m}>{m}</option>)}
            </select>
          </label>
        </div>
        <div className="row" style={{ justifyContent: "flex-end", marginTop: 12 }}>
          <button type="submit" className="btn btn--primary" disabled={busy || blocked} data-testid="agent-create">
            {busy ? "만드는 중…" : "만들기"}
          </button>
        </div>
      </form>
    </div>
  );
}
