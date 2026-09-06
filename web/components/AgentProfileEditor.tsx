"use client";
/**
 * S10 실행 구역 — 런타임 프로파일 목록(COMPONENTS §2.6 Profile Row · SCREEN §4.7).
 * 각 프로파일은 런타임 종류 → 모델 → 옵션 → 폴백. 추가·삭제·기본 지정.
 *
 * **모델과 옵션은 모두 데몬 probe 결과**다(§8.2·§8.2.6): 모델은 `RuntimeCapability.models`,
 * 옵션은 `supported_options`. 옵션 키가 광고되면 **그 값만** 고를 수 있고, 광고가 없으면 비활성 + 사유다 —
 * `runtime_kind` 로 추측하지 않는다. 데몬이 옵션을 채우는 것은 후속이라 **"광고 없음"이 당분간 첫 화면**이고,
 * 그래서 그 화면은 막다른 길이 아니라고 말한다: 옵션 없이도 프로파일은 런타임 기본값으로 동작한다.
 *
 * 폴백은 **같은 머신 안에서만** 일어난다(FR-1.6) — 그래서 후보는 같은 에이전트의 다른 프로파일뿐이다.
 */
import { useState } from "react";
import "./profile-row.css";
import type { KindCapability } from "@/lib/runtime-options";
import type { AgentProfile, RuntimeKind } from "@/lib/api/types";

const NO_ADVERT = "이 런타임은 이 옵션의 지원 범위를 광고하지 않습니다";

export interface AgentProfileEditorProps {
  profiles: AgentProfile[];
  /** 데몬 probe 로 감지된 kind → {models, options}. 비어 있으면 "먼저 컴퓨터를 연결하세요". */
  caps: Map<RuntimeKind, KindCapability>;
  canEdit: boolean;
  disabledReason?: string;
  onCreate: (p: { name: string; runtime_kind: RuntimeKind; model: string; options: Record<string, unknown> }) => Promise<void>;
  onUpdate: (id: string, patch: Partial<AgentProfile>) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  error?: string | null;
}

/** 광고된 옵션 키 하나 — 값 목록이 그대로 선택지가 된다. */
function OptionSelect({
  keyName, values, value, disabled, disabledTitle, label, onChange,
}: {
  keyName: string;
  values: string[];
  value: string;
  disabled: boolean;
  disabledTitle?: string;
  label: string;
  onChange: (v: string) => void;
}) {
  return (
    <select
      className="select prof__sel"
      value={value}
      disabled={disabled}
      title={disabled ? disabledTitle : undefined}
      onChange={(e) => onChange(e.target.value)}
      aria-label={label}
      data-testid="profile-option"
      data-option={keyName}
    >
      <option value="">{keyName} 기본값</option>
      {values.map((v) => <option key={v} value={v}>{`${keyName}: ${v}`}</option>)}
    </select>
  );
}

function OptionRow({
  kind, cap, options, canEdit, onChange, idPrefix,
}: {
  kind: RuntimeKind;
  cap: KindCapability | undefined;
  options: Record<string, unknown>;
  canEdit: boolean;
  onChange: (next: Record<string, unknown>) => void;
  idPrefix: string;
}) {
  const advertised = Object.entries(cap?.options ?? {});
  if (advertised.length === 0) {
    return (
      <span className="prof__quiet" data-testid="profile-options-unadvertised" data-kind={kind}>
        옵션 — {NO_ADVERT}. 프로파일은 런타임 기본값으로 동작하고, 데몬이 범위를 보고하면 여기서 고를 수 있습니다.
      </span>
    );
  }
  return (
    <>
      {advertised.map(([k, vs]) => (
        <OptionSelect
          key={k}
          keyName={k}
          values={vs}
          value={String(options[k] ?? "")}
          disabled={!canEdit}
          label={`${idPrefix} ${k}`}
          onChange={(v) => {
            const next = { ...options };
            if (v) next[k] = v;
            else delete next[k];
            onChange(next);
          }}
        />
      ))}
    </>
  );
}

export function AgentProfileEditor(props: AgentProfileEditorProps) {
  const kinds = [...props.caps.keys()];
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState<RuntimeKind>(kinds[0] ?? "claude_code");
  const [model, setModel] = useState("");
  const [options, setOptions] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);

  const cap = props.caps.get(kind);
  const kindModels = cap?.models ?? [];

  async function create() {
    setBusy(true);
    try {
      await props.onCreate({ name: name.trim(), runtime_kind: kind, model: model || kindModels[0] || "", options });
      setAdding(false);
      setName("");
      setOptions({});
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="prof" data-testid="profile-editor">
      {props.caps.size === 0 && (
        <p className="notice" data-testid="no-probe-models">
          감지된 런타임이 없습니다 — 모델과 옵션 목록은 데몬 probe 결과로 채워집니다. 먼저 컴퓨터를 연결하세요.
        </p>
      )}
      {props.profiles.map((p) => {
        const pcap = props.caps.get(p.runtime_kind);
        const models = pcap?.models ?? [];
        const opts = (p.options ?? {}) as Record<string, unknown>;
        return (
          <div key={p.id} className={`prof__row${p.is_default ? " prof__row--default" : ""}`} data-testid="profile-row" data-profile-id={p.id}>
            <div className="prof__l1">
              <b>{p.name}</b>
              {p.is_default && <span className="prof__star" title="기본 프로파일" data-testid="profile-default">★ 기본</span>}
              <span className="prof__quiet">{p.runtime_kind}</span>
              <select
                className="select prof__sel"
                value={p.model}
                disabled={!props.canEdit || models.length === 0}
                title={!props.canEdit ? props.disabledReason : models.length === 0 ? "이 런타임 종류가 감지되지 않았습니다" : undefined}
                onChange={(e) => void props.onUpdate(p.id, { model: e.target.value, runtime_kind: p.runtime_kind })}
                aria-label={`${p.name} 모델`}
                data-testid="profile-model"
              >
                {(models.includes(p.model) ? models : [p.model, ...models]).map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </div>
            <div className="prof__l2">
              <OptionRow
                kind={p.runtime_kind}
                cap={pcap}
                options={opts}
                canEdit={props.canEdit}
                idPrefix={p.name}
                onChange={(next) => void props.onUpdate(p.id, { options: next })}
              />
            </div>
            <div className="prof__l2">
              <label className="prof__quiet">
                폴백{" "}
                <select
                  className="select prof__sel"
                  value={p.fallback_profile_id ?? ""}
                  disabled={!props.canEdit}
                  onChange={(e) => void props.onUpdate(p.id, { fallback_profile_id: e.target.value || null })}
                  aria-label={`${p.name} 폴백 프로파일`}
                  data-testid="profile-fallback"
                >
                  <option value="">없음</option>
                  {props.profiles.filter((x) => x.id !== p.id).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </select>
              </label>
              <span className="prof__quiet">폴백은 같은 머신 안에서만 일어납니다(FR-1.6)</span>
              <span className="prof__spacer" />
              {!p.is_default && (
                <button type="button" className="btn btn--sm" disabled={!props.canEdit} onClick={() => void props.onUpdate(p.id, { is_default: true })} data-testid="profile-make-default">
                  기본으로
                </button>
              )}
              <button
                type="button"
                className="btn btn--sm"
                disabled={!props.canEdit || p.is_default || props.profiles.length === 1}
                title={p.is_default ? "먼저 다른 프로파일을 기본으로 지정하세요" : props.profiles.length === 1 ? "마지막 프로파일은 삭제할 수 없습니다" : undefined}
                onClick={() => void props.onDelete(p.id)}
                data-testid="profile-delete"
              >
                삭제
              </button>
            </div>
          </div>
        );
      })}

      {props.error && <p className="problem" role="alert" data-testid="profile-error">{props.error}</p>}

      {adding ? (
        <div className="prof__row" data-testid="profile-new">
          <div className="prof__l1">
            <input className="input prof__sel" placeholder="이름 (예 fast)" value={name} onChange={(e) => setName(e.target.value)} aria-label="새 프로파일 이름" data-testid="new-profile-name" />
            <select className="select prof__sel" value={kind} onChange={(e) => { setKind(e.target.value as RuntimeKind); setModel(""); setOptions({}); }} aria-label="런타임 종류" data-testid="new-profile-kind">
              {(kinds.length ? kinds : (["claude_code"] as RuntimeKind[])).map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
            <select className="select prof__sel" value={model} onChange={(e) => setModel(e.target.value)} aria-label="모델" data-testid="new-profile-model">
              {kindModels.length === 0 && <option value="">감지된 모델 없음</option>}
              {kindModels.map((m) => <option key={m} value={m}>{m}</option>)}
            </select>
          </div>
          <div className="prof__l2">
            <OptionRow kind={kind} cap={cap} options={options} canEdit onChange={setOptions} idPrefix="새 프로파일" />
          </div>
          <div className="prof__l2">
            <button type="button" className="btn btn--sm btn--primary" disabled={busy || !name.trim() || kindModels.length === 0} onClick={() => void create()} data-testid="new-profile-save">추가</button>
            <button type="button" className="btn btn--sm" onClick={() => setAdding(false)}>취소</button>
          </div>
        </div>
      ) : (
        <button type="button" className="btn btn--sm" disabled={!props.canEdit} title={props.canEdit ? undefined : props.disabledReason} onClick={() => setAdding(true)} data-testid="profile-add">
          프로파일 추가
        </button>
      )}
    </div>
  );
}

export default AgentProfileEditor;
