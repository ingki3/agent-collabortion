"use client";
/**
 * S14 워크스페이스 설정 탭 6개(컴퓨터 정책 · 예산 · 루프 상한 · 컨텍스트 · 작업 폴더 · 보안) + 알림 탭(개인).
 *
 * 한 탭 = 한 폼. 「저장」은 **바꾼 칸만** 보낸다(`diffSettings`, openapi "부분 갱신") — 안 바꾼 칸을 함께 보내면 서버가
 * 그 값을 "바꾼 것"으로 기록한다(S-26 `||` 합치기). 항목마다 기본값(PRD §7)과 「바꿨을 때의 영향」 한 줄을 값 아래에 둔다
 * (SCREEN §4.10 · U14 "사용자가 바꾸기 전에 읽음"). 권한이 없으면 입력을 잠그고 사유를 저장 버튼 아래에 쓴다(DisabledHint).
 */
import { useEffect, useMemo, useState } from "react";
import { DisabledHint } from "./PageHead";
import {
  daysIso, IMPACT, INCLUDE_ARTIFACTS_LABEL, ISOLATION_LABEL, isoDays, RUNTIME_KIND_LABEL, RUNTIME_KINDS, SETTINGS_DEFAULTS,
  SETTINGS_TABS, SUBSCRIPTION_LABEL, saveRight, type SettingsTab, diffSettings,
} from "@/lib/settings";
import type { IsolationKind, MemberRole, NotificationSettings, WorkspaceSettings, WorkspaceSettingsUpdate } from "@/lib/api/types";
import "./settings.css";

export type WorkspaceTab = Exclude<SettingsTab, "members" | "notifications" | "dashboard">;

/** 항목 행 — 라벨(+기본값) · 입력 · 영향 한 줄. */
export function SettingRow({ label, defaultValue, impact, children, error, testid }: {
  label: string;
  /** "기본값 8" 처럼 라벨 옆에 작게. 없으면 안 그린다. */
  defaultValue?: string | number | null;
  impact: string;
  children: React.ReactNode;
  error?: string | null;
  testid?: string;
}) {
  return (
    <div className="srow" data-testid={testid}>
      <div className="srow__label">
        {label}
        {defaultValue != null && <span className="srow__default">기본값 {defaultValue}</span>}
      </div>
      <div className="srow__ctl">{children}</div>
      <p className="srow__impact" data-testid={testid ? `${testid}-impact` : undefined}>{impact}</p>
      {error && <p className="srow__err" role="alert">{error}</p>}
    </div>
  );
}

/** 저장 버튼 + 비활성 사유(DisabledHint 패턴) — 탭 머리 오른쪽. */
export function SaveBar({ tab, role, dirty, busy, saved, onSave, hintId }: {
  tab: SettingsTab; role: MemberRole | null; dirty: boolean; busy: boolean; saved: boolean; onSave: () => void; hintId: string;
}) {
  const right = saveRight(tab, role);
  const meta = SETTINGS_TABS.find((t) => t.key === tab)!;
  return (
    <div className="tab-head">
      <div>
        <h2>{meta.label}</h2>
        <p className="tab-head__desc">{meta.desc}</p>
      </div>
      <div className="tab-head__actions">
        <div className="row">
          {saved && <span className="small" style={{ color: "var(--s-done-text)" }} data-testid="settings-saved">저장됨</span>}
          <button
            type="button"
            className="btn btn--primary btn--sm"
            disabled={!right.ok || !dirty || busy}
            aria-describedby={!right.ok ? hintId : undefined}
            title={!right.ok ? right.reason : undefined}
            onClick={onSave}
            data-testid="settings-save"
          >
            저장
          </button>
        </div>
        {!right.ok && <DisabledHint id={hintId}>{right.reason}</DisabledHint>}
      </div>
    </div>
  );
}

const num = (v: string): number | undefined => (v.trim() === "" ? undefined : Number(v));
const numOrNull = (v: string): number | null => (v.trim() === "" ? null : Number(v));
const str = (v: number | null | undefined): string => (v == null ? "" : String(v));

export interface WorkspaceSettingsTabProps {
  tab: WorkspaceTab;
  settings: WorkspaceSettings;
  role: MemberRole | null;
  /** 바꾼 칸만 담긴 payload 를 보낸다. 성공하면 새 설정을 돌려준다. */
  onSave: (patch: WorkspaceSettingsUpdate) => Promise<WorkspaceSettings | null>;
  /** 서버 422 의 `errors[]` — 필드 옆에 그린다. */
  fieldErrors?: Record<string, string>;
}

export function WorkspaceSettingsTab({ tab, settings, role, onSave, fieldErrors = {} }: WorkspaceSettingsTabProps) {
  const [draft, setDraft] = useState<WorkspaceSettings>(settings);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  // 탭을 옮기거나 서버가 새 값을 주면 초안을 버린다 — 다른 탭의 미저장 초안이 이 탭의 payload 에 섞이지 않게.
  useEffect(() => setDraft(settings), [settings, tab]);
  const patch = useMemo(() => diffSettings(settings, draft), [settings, draft]);
  const canSave = saveRight(tab, role).ok;
  const lock = !canSave || busy;

  async function save() {
    if (!patch) return;
    setBusy(true);
    try {
      const next = await onSave(patch);
      if (next) {
        setSaved(true);
        setTimeout(() => setSaved(false), 1500);
      }
    } finally {
      setBusy(false);
    }
  }
  const set = (fn: (d: WorkspaceSettings) => WorkspaceSettings) => setDraft((d) => fn(d));
  const err = (k: string) => fieldErrors[k] ?? null;

  return (
    <section className="card" data-testid={`settings-tab-${tab}`}>
      <SaveBar tab={tab} role={role} dirty={!!patch} busy={busy} saved={saved} onSave={() => void save()} hintId={`settings-${tab}-hint`} />
      {patch && <p className="notice notice--info small" data-testid="settings-dirty">바꾼 항목: {Object.keys(patch).length}개 — 저장해야 적용됩니다.</p>}

      {tab === "runtime" && (
        <>
          <SettingRow label="컴퓨터 한 대가 동시에 맡는 일" defaultValue={SETTINGS_DEFAULTS.runtime_policy.max_concurrent_tasks} impact={IMPACT.max_concurrent_tasks} error={err("runtime_policy.max_concurrent_tasks")} testid="row-max-concurrent">
            <input className="input input--num" type="number" min={1} disabled={lock} value={str(draft.runtime_policy.max_concurrent_tasks)} aria-label="컴퓨터 한 대가 동시에 맡는 일" onChange={(e) => set((d) => ({ ...d, runtime_policy: { ...d.runtime_policy, max_concurrent_tasks: num(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="종류별 상한" impact={IMPACT.per_kind} testid="row-per-kind">
            {RUNTIME_KINDS.map((k) => (
              <label key={k} className="row small" style={{ gap: 6 }}>
                <span>{RUNTIME_KIND_LABEL[k]}</span>
                <input className="input input--num" type="number" min={1} disabled={lock} value={str(draft.runtime_policy.per_kind?.[k])} aria-label={`${RUNTIME_KIND_LABEL[k]} 상한`} placeholder="전체 상한" onChange={(e) => set((d) => {
                  const per = { ...(d.runtime_policy.per_kind ?? {}) };
                  const v = num(e.target.value);
                  if (v == null) delete per[k]; else per[k] = v;
                  return { ...d, runtime_policy: { ...d.runtime_policy, per_kind: per } };
                })} />
              </label>
            ))}
          </SettingRow>
        </>
      )}

      {tab === "budget" && (
        <>
          <SettingRow label="세션 기본 상한 (USD)" defaultValue="없음" impact={IMPACT.default_session_budget_usd} error={err("budget_policy.default_session_budget_usd")} testid="row-session-budget">
            <input className="input input--num" type="number" min={0} step="0.5" disabled={lock} value={str(draft.budget_policy.default_session_budget_usd)} aria-label="세션 기본 상한" placeholder="없음" onChange={(e) => set((d) => ({ ...d, budget_policy: { ...d.budget_policy, default_session_budget_usd: numOrNull(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="할 일 하나의 기본 상한 (USD)" defaultValue="없음" impact={IMPACT.default_task_budget_usd} error={err("budget_policy.default_task_budget_usd")} testid="row-task-budget">
            <input className="input input--num" type="number" min={0} step="0.5" disabled={lock} value={str(draft.budget_policy.default_task_budget_usd)} aria-label="할 일 하나의 기본 상한" placeholder="없음" onChange={(e) => set((d) => ({ ...d, budget_policy: { ...d.budget_policy, default_task_budget_usd: numOrNull(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="워크스페이스 월 상한 (USD)" defaultValue="없음" impact={IMPACT.workspace_monthly_budget_usd} error={err("budget_policy.workspace_monthly_budget_usd")} testid="row-monthly-budget">
            <input className="input input--num" type="number" min={0} step="1" disabled={lock} value={str(draft.budget_policy.workspace_monthly_budget_usd)} aria-label="워크스페이스 월 상한" placeholder="없음" onChange={(e) => set((d) => ({ ...d, budget_policy: { ...d.budget_policy, workspace_monthly_budget_usd: numOrNull(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="모델별 단가 재정의" impact={IMPACT.pricing_overrides} testid="row-pricing">
            {Object.keys(draft.budget_policy.pricing_overrides ?? {}).length === 0 ? (
              <span className="small muted">재정의 없음 — 서버의 가격표를 씁니다</span>
            ) : (
              <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>
                {Object.entries(draft.budget_policy.pricing_overrides ?? {}).map(([model, p]) => (
                  <li key={model}><code>{model}</code> — 입력 {p.input ?? "–"} · 출력 {p.output ?? "–"} · 캐시 읽기 {p.cache_read ?? "–"} (USD / 1M 토큰)</li>
                ))}
              </ul>
            )}
          </SettingRow>
        </>
      )}

      {tab === "loop" && (
        <>
          <SettingRow label="위임 사슬 깊이" defaultValue={SETTINGS_DEFAULTS.loop_limits.max_chain_depth} impact={IMPACT.max_chain_depth} error={err("loop_limits.max_chain_depth")} testid="row-chain-depth">
            <input className="input input--num" type="number" min={1} max={100} disabled={lock} value={str(draft.loop_limits.max_chain_depth)} aria-label="위임 사슬 깊이" onChange={(e) => set((d) => ({ ...d, loop_limits: { ...d.loop_limits, max_chain_depth: num(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="한 시간에 주고받는 횟수" defaultValue={SETTINGS_DEFAULTS.loop_limits.max_hops_per_hour} impact={IMPACT.max_hops_per_hour} error={err("loop_limits.max_hops_per_hour")} testid="row-hops">
            <input className="input input--num" type="number" min={1} max={10000} disabled={lock} value={str(draft.loop_limits.max_hops_per_hour)} aria-label="한 시간에 주고받는 횟수" onChange={(e) => set((d) => ({ ...d, loop_limits: { ...d.loop_limits, max_hops_per_hour: num(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="둘이 연속으로 주고받는 횟수" defaultValue={SETTINGS_DEFAULTS.loop_limits.max_pair_roundtrips} impact={IMPACT.max_pair_roundtrips} error={err("loop_limits.max_pair_roundtrips")} testid="row-pair-roundtrips">
            <input className="input input--num" type="number" min={1} max={100} disabled={lock} value={str(draft.loop_limits.max_pair_roundtrips)} aria-label="둘이 연속으로 주고받는 횟수" onChange={(e) => set((d) => ({ ...d, loop_limits: { ...d.loop_limits, max_pair_roundtrips: num(e.target.value) } }))} />
          </SettingRow>
          <p className="srow__apply">{IMPACT.loop_limits_apply}</p>
        </>
      )}

      {tab === "context" && (
        <>
          <SettingRow label="이전 세션 요약의 최대 토큰" defaultValue={SETTINGS_DEFAULTS.context_reuse.max_summary_tokens} impact={IMPACT.max_summary_tokens} error={err("context_reuse.max_summary_tokens")} testid="row-summary-tokens">
            <input className="input input--num" type="number" min={0} step={100} disabled={lock} value={str(draft.context_reuse.max_summary_tokens)} aria-label="이전 세션 요약의 최대 토큰" onChange={(e) => set((d) => ({ ...d, context_reuse: { ...d.context_reuse, max_summary_tokens: num(e.target.value) } }))} />
          </SettingRow>
          <SettingRow label="산출물을 함께 넘길지" defaultValue={INCLUDE_ARTIFACTS_LABEL[SETTINGS_DEFAULTS.context_reuse.include_artifacts]} impact={IMPACT.include_artifacts} testid="row-include-artifacts">
            <select className="select" disabled={lock} value={draft.context_reuse.include_artifacts ?? "links"} aria-label="산출물을 함께 넘길지" onChange={(e) => set((d) => ({ ...d, context_reuse: { ...d.context_reuse, include_artifacts: e.target.value as "links" | "none" | "full" } }))}>
              {(Object.keys(INCLUDE_ARTIFACTS_LABEL) as (keyof typeof INCLUDE_ARTIFACTS_LABEL)[]).map((k) => <option key={k} value={k}>{INCLUDE_ARTIFACTS_LABEL[k]}</option>)}
            </select>
          </SettingRow>
        </>
      )}

      {tab === "workdir" && (
        <>
          <SettingRow label="기본 격리 방식" defaultValue={ISOLATION_LABEL[SETTINGS_DEFAULTS.default_isolation]} impact={IMPACT.default_isolation} error={err("default_isolation")} testid="row-isolation">
            <select className="select" disabled={lock} value={draft.default_isolation} aria-label="기본 격리 방식" onChange={(e) => set((d) => ({ ...d, default_isolation: e.target.value as IsolationKind }))}>
              {(Object.keys(ISOLATION_LABEL) as IsolationKind[]).map((k) => <option key={k} value={k}>{ISOLATION_LABEL[k]}</option>)}
            </select>
          </SettingRow>
          <SettingRow label="작업 폴더 보존 (일)" defaultValue={SETTINGS_DEFAULTS.workdir_retention_days} impact={IMPACT.workdir_retention_days(draft.workdir_retention_days)} error={err("workdir_retention_days")} testid="row-retention">
            <input className="input input--num" type="number" min={0} disabled={lock} value={str(draft.workdir_retention_days)} aria-label="작업 폴더 보존" onChange={(e) => set((d) => ({ ...d, workdir_retention_days: Number(e.target.value) || 0 }))} />
          </SettingRow>
          <SettingRow label="작업 폴더 용량 상한 (GB)" defaultValue="없음" impact={IMPACT.workdir_disk_quota_gb} error={err("workdir_disk_quota_gb")} testid="row-quota">
            <input className="input input--num" type="number" min={1} disabled={lock} value={str(draft.workdir_disk_quota_gb)} aria-label="작업 폴더 용량 상한" placeholder="없음" onChange={(e) => set((d) => ({ ...d, workdir_disk_quota_gb: numOrNull(e.target.value) }))} />
          </SettingRow>
          <SettingRow label="컴퓨터 연결 끊김 유예 (일)" defaultValue={isoDays(SETTINGS_DEFAULTS.runtime_offline_grace)} impact={IMPACT.runtime_offline_grace(isoDays(draft.runtime_offline_grace))} error={err("runtime_offline_grace")} testid="row-grace">
            {isoDays(draft.runtime_offline_grace) != null ? (
              <input className="input input--num" type="number" min={0} disabled={lock} value={isoDays(draft.runtime_offline_grace) ?? 0} aria-label="컴퓨터 연결 끊김 유예" onChange={(e) => set((d) => ({ ...d, runtime_offline_grace: daysIso(Number(e.target.value) || 0) }))} />
            ) : (
              // 일 단위가 아닌 값(시·초)은 원문 그대로 — 화면이 값을 잘라 저장하지 않는다.
              <input className="input" disabled={lock} value={draft.runtime_offline_grace} aria-label="컴퓨터 연결 끊김 유예" onChange={(e) => set((d) => ({ ...d, runtime_offline_grace: e.target.value }))} />
            )}
          </SettingRow>
          <p className="srow__apply">{IMPACT.sweep_apply}</p>
        </>
      )}

      {tab === "security" && (
        <SettingRow label="활동 기록 마스킹" defaultValue={SETTINGS_DEFAULTS.task_event_masking ? "켬" : "끔"} impact={IMPACT.task_event_masking} error={err("task_event_masking")} testid="row-masking">
          <label className="srow__check">
            <input type="checkbox" disabled={lock} checked={draft.task_event_masking} onChange={(e) => set((d) => ({ ...d, task_event_masking: e.target.checked }))} data-testid="masking-toggle" />
            diff·셸 출력을 요약만 저장
          </label>
        </SettingRow>
      )}
    </section>
  );
}

// ── 알림 탭(개인) ─────────────────────────────────────────────────────────────
export function NotificationsTab({ settings, onSave, error }: {
  settings: NotificationSettings | null;
  onSave: (next: NotificationSettings) => Promise<NotificationSettings | null>;
  /** 읽기·저장이 거절되면(T-S14 #209: 익명 401 · 에이전트 토큰 403 · enum 밖 422) 그 문장을 그대로 보인다 — 화면이 값을 지어내지 않는다. */
  error?: string | null;
}) {
  const [draft, setDraft] = useState<NotificationSettings | null>(settings);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  useEffect(() => setDraft(settings), [settings]);
  const dirty = !!draft && !!settings && JSON.stringify(draft) !== JSON.stringify(settings);

  async function save() {
    if (!draft) return;
    setBusy(true);
    try {
      const next = await onSave(draft);
      if (next) {
        setSaved(true);
        setTimeout(() => setSaved(false), 1500);
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="card" data-testid="settings-tab-notifications">
      <SaveBar tab="notifications" role={null} dirty={dirty} busy={busy} saved={saved} onSave={() => void save()} hintId="settings-notifications-hint" />
      {error && <p className="problem" role="alert" data-testid="notifications-error">{error}</p>}
      {!draft && !error && <p className="muted">불러오는 중…</p>}
      {draft && (
        <>
          <SettingRow label="이메일" defaultValue="켬" impact={IMPACT.email} testid="row-email">
            <label className="srow__check">
              <input type="checkbox" checked={draft.email} disabled={busy} onChange={(e) => setDraft({ ...draft, email: e.target.checked })} data-testid="notif-email" />
              받은 요청과 세션 소식을 이메일로
            </label>
          </SettingRow>
          <SettingRow label="푸시" defaultValue="끔" impact={IMPACT.push} testid="row-push">
            <label className="srow__check">
              <input type="checkbox" checked={draft.push} disabled={busy} onChange={(e) => setDraft({ ...draft, push: e.target.checked })} data-testid="notif-push" />
              브라우저 푸시 알림
            </label>
          </SettingRow>
          <SettingRow label="세션 구독 기본값" defaultValue={SUBSCRIPTION_LABEL.all} impact={IMPACT.default_subscription} testid="row-subscription">
            <select className="select" value={draft.default_subscription} disabled={busy} aria-label="세션 구독 기본값" onChange={(e) => setDraft({ ...draft, default_subscription: e.target.value as NotificationSettings["default_subscription"] })} data-testid="notif-subscription">
              {(Object.keys(SUBSCRIPTION_LABEL) as (keyof typeof SUBSCRIPTION_LABEL)[]).map((k) => <option key={k} value={k}>{SUBSCRIPTION_LABEL[k]}</option>)}
            </select>
          </SettingRow>
        </>
      )}
    </section>
  );
}
