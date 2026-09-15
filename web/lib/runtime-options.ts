/**
 * 프로파일 옵션의 지원 범위 — `RuntimeCapability.supported_options`(계약, Lead 결정 2026-09-06).
 *
 * 규칙: **키가 있으면 그 값만 고를 수 있고, 키가 없거나 비어 있으면 "광고 없음"이다.** `runtime_kind` 로 추측하지 않는다.
 * 데몬이 실제로 채우는 것은 후속(D-5)이라 당분간 비어 있을 수 있다 — 그래서 "광고 없음"이 첫 화면이고,
 * 그 화면이 막다른 길로 보이지 않아야 한다(옵션 없이도 프로파일은 런타임 기본값으로 동작한다).
 */
import type { Runtime, RuntimeCapability, RuntimeKind } from "@/lib/api/types";

export type SupportedOptions = Record<string, string[]>;

/** 이 능력이 광고한 옵션 범위. 없거나 비어 있으면 빈 객체 — 호출부는 그것을 "광고 없음"으로 그린다. */
export function supportedOptions(c: RuntimeCapability): SupportedOptions {
  const raw = c.supported_options;
  if (!raw || typeof raw !== "object") return {};
  const out: SupportedOptions = {};
  for (const [k, v] of Object.entries(raw)) if (Array.isArray(v) && v.length) out[k] = v.map(String);
  return out;
}

export interface KindCapability {
  models: string[];
  options: SupportedOptions;
}

/**
 * 온라인·로그인된 능력을 `runtime_kind` 별로 합친다. 여러 머신이 같은 종류를 광고하면 모델·옵션 값의 합집합이다.
 * `runtimeId` 를 주면 그 머신만 본다(세션이 런타임을 고정한 경우).
 */
export function capabilityIndex(runtimes: Runtime[], runtimeId?: string | null): Map<RuntimeKind, KindCapability> {
  const m = new Map<RuntimeKind, KindCapability>();
  for (const r of runtimes) {
    if (runtimeId ? r.id !== runtimeId : r.status !== "online") continue;
    for (const c of r.capabilities) {
      if (!c.logged_in) continue;
      const cur = m.get(c.kind) ?? { models: [], options: {} };
      cur.models = [...new Set([...cur.models, ...(c.models ?? [])])];
      for (const [k, vs] of Object.entries(supportedOptions(c))) {
        cur.options[k] = [...new Set([...(cur.options[k] ?? []), ...vs])];
      }
      m.set(c.kind, cur);
    }
  }
  return m;
}
