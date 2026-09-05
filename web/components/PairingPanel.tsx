"use client";
/**
 * S12 Add a computer(SCREEN §4.8) — S4 2단계에 인라인, /runtimes/new 에 단독.
 * 설치 명령 2줄(복사) + `waiting → connected → probing → ready` 4단계. SSE `pairing.updated` 로 갱신하고(셸 안에서는 셸의
 * 연결을 공유, 온보딩에서는 자기 연결), 스트림이 열려 있지 않은 동안만 5초 폴링(GET pairing)으로 보완한다(N5).
 * resync 면 pairing 을 REST 로 다시 읽는다(N4). 3분 넘게 waiting 이면 문제 해결 안내를 편다.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage, isApiError } from "@/lib/api/client";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { Pairing, PairingStatus, Runtime, StreamEvent } from "@/lib/api/types";

export const PAIRING_STAGES: { key: PairingStatus; label: string }[] = [
  { key: "waiting", label: "대기 중" },
  { key: "connected", label: "연결됨" },
  { key: "probing", label: "CLI 감지 중" },
  { key: "ready", label: "준비 완료" },
];

export const TROUBLESHOOT_AFTER_MS = 3 * 60 * 1000;

export function stageIndex(s: PairingStatus): number {
  return PAIRING_STAGES.findIndex((x) => x.key === s);
}

export function capabilitySummary(rt: Runtime | undefined): string[] {
  if (!rt) return [];
  const kinds = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" } as const;
  const lines: string[] = [];
  for (const k of ["claude_code", "hermes"] as const) {
    const c = rt.capabilities.find((c) => c.kind === k);
    if (!c) lines.push(`${kinds[k]} 없음`);
    else lines.push(`${kinds[k]} 감지됨 · ${c.logged_in ? "로그인됨" : "로그인 필요"} · 모델 ${c.models?.length ?? 0}개`);
  }
  return lines;
}

export interface PairingPanelProps {
  workspaceId: string;
  /** owner·admin 만 발급 가능(openapi createPairing). 아니면 비활성 + 사유. */
  canManage: boolean;
  onReady?: (pairing: Pairing) => void;
  /** 테스트·데모용 시계. */
  now?: () => number;
}

export function PairingPanel({ workspaceId, canManage, onReady, now = Date.now }: PairingPanelProps) {
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<number | null>(null);
  const [troubleOpen, setTroubleOpen] = useState(false);
  const startedAt = useRef<number>(now());
  const readyFired = useRef(false);

  const create = useCallback(async () => {
    setError(null);
    setTroubleOpen(false);
    readyFired.current = false;
    startedAt.current = now();
    try {
      const p = await api.post("/workspaces/{workspaceId}/runtimes/pairings", {
        path: { workspaceId },
        body: {},
      });
      setPairing(p);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [workspaceId, now]);

  useEffect(() => {
    if (canManage) void create();
  }, [canManage, create]);

  // 실시간: pairing.updated
  const onEvent = useCallback(
    (ev: StreamEvent) => {
      if (ev.type !== "pairing.updated") return;
      const p = ev.payload as unknown as Pairing;
      setPairing((cur) => (cur && p.id === cur.id ? p : cur));
    },
    [],
  );
  const refetch = useCallback(async () => {
    if (!pairing) return;
    try {
      const p = await api.get("/workspaces/{workspaceId}/runtimes/pairings/{pairingId}", {
        path: { workspaceId, pairingId: pairing.id },
      });
      setPairing((cur) => (cur && cur.id === p.id ? p : cur));
    } catch (e) {
      if (isApiError(e) && e.status === 410) setPairing((cur) => (cur ? { ...cur, status: "expired" } : cur));
    }
  }, [pairing, workspaceId]);
  const conn = useWorkspaceStream(workspaceId, onEvent, { enabled: !!pairing, onResync: () => void refetch() });

  // 폴링 폴백(5초) — 스트림이 아직 안 열렸거나 끊긴 동안만. 열려 있으면 pairing.updated 가 단계를 옮긴다.
  useEffect(() => {
    if (!pairing || pairing.status === "ready" || pairing.status === "expired") return;
    if (conn === "open") return;
    const t = setInterval(() => void refetch(), 5000);
    return () => clearInterval(t);
  }, [pairing, conn, refetch]);

  // 3분 타이머 → 문제 해결 안내 자동 펼침
  useEffect(() => {
    if (!pairing || pairing.status !== "waiting") return;
    const remain = TROUBLESHOOT_AFTER_MS - (now() - startedAt.current);
    const t = setTimeout(() => setTroubleOpen(true), Math.max(0, remain));
    return () => clearTimeout(t);
  }, [pairing, now]);

  useEffect(() => {
    if (pairing?.status === "ready" && !readyFired.current) {
      readyFired.current = true;
      onReady?.(pairing);
    }
  }, [pairing, onReady]);

  async function copy(i: number, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(i);
      setTimeout(() => setCopied(null), 1500);
    } catch {
      setCopied(null);
    }
  }

  if (!canManage) {
    return (
      <div className="notice notice--info" data-testid="pairing-forbidden">
        런타임 추가는 owner·admin 만 할 수 있습니다. 관리자에게 요청하세요.
      </div>
    );
  }
  if (error) {
    return (
      <div>
        <p className="problem">{error}</p>
        <button type="button" className="btn" onClick={() => void create()}>
          다시 시도
        </button>
      </div>
    );
  }
  if (!pairing) return <p className="muted">설치 명령을 만드는 중…</p>;

  const idx = stageIndex(pairing.status);
  const expired = pairing.status === "expired";

  return (
    <div className="stack" data-testid="pairing-panel" data-status={pairing.status}>
      <p className="muted small" style={{ margin: 0 }}>
        연결할 컴퓨터의 터미널에서 아래 두 줄을 순서대로 실행하세요. 데몬이 붙으면 이 화면이 자동으로 바뀝니다.
      </p>
      {pairing.install_commands.map((cmd, i) => (
        <div className="cmd" key={i} data-testid={`install-cmd-${i + 1}`}>
          <code>{cmd}</code>
          <button type="button" className="btn btn--sm" onClick={() => void copy(i, cmd)} aria-label={`명령 ${i + 1} 복사`}>
            {copied === i ? "복사됨" : "복사"}
          </button>
        </div>
      ))}
      <div className="stages" role="list" aria-label="연결 단계">
        {PAIRING_STAGES.map((s, i) => (
          <span
            key={s.key}
            role="listitem"
            className={`stage${i === idx ? " stage--current" : i < idx ? " stage--done" : ""}`}
            data-testid={`stage-${s.key}`}
            aria-current={i === idx ? "step" : undefined}
          >
            {i < idx ? "✓" : i === idx ? "●" : "○"} {s.label}
          </span>
        ))}
        {expired && (
          <span className="stage" style={{ color: "var(--s-fail-text)", borderColor: "var(--s-fail)" }}>
            ✕ 만료됨
          </span>
        )}
      </div>
      <p data-testid="pairing-status" data-status={pairing.status} className="muted small" style={{ margin: 0 }}>
        {pairing.status === "waiting" && "대기 중 — 명령을 실행하면 몇 초 안에 연결됩니다"}
        {pairing.status === "connected" && "연결됨 — CLI 를 찾는 중입니다"}
        {pairing.status === "probing" && "CLI 감지 중…"}
        {pairing.status === "ready" && (
          <span style={{ color: "var(--s-done-text)" }}>
            준비 완료 — {pairing.runtime?.name ?? "런타임"}
          </span>
        )}
        {expired && "페어링이 만료되었습니다(30분). 새 명령을 발급하세요."}
      </p>
      {pairing.status === "ready" && (
        <ul className="small muted" style={{ margin: 0, paddingLeft: 18 }} data-testid="pairing-capabilities">
          {capabilitySummary(pairing.runtime).map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      )}
      {expired && (
        <button type="button" className="btn" onClick={() => void create()}>
          다시 발급
        </button>
      )}
      {pairing.status === "waiting" && (
        <details className="troubleshoot" open={troubleOpen} onToggle={(e) => setTroubleOpen((e.target as HTMLDetailsElement).open)} data-testid="troubleshoot">
          <summary>연결이 안 되나요? 문제 해결</summary>
          <ul>
            <li>방화벽·프록시: 컴퓨터에서 이 서버 주소로 나가는 HTTPS 가 열려 있어야 합니다.</li>
            <li>권한: 설치 명령이 홈 디렉터리(~/.colab)에 쓸 수 있어야 합니다.</li>
            <li>CLI 미설치: Claude Code 또는 Hermes 가 PATH 에 있고 로그인돼 있어야 감지됩니다.</li>
            <li>토큰 만료: 발급 후 30분이 지나면 "다시 발급"으로 새 명령을 받으세요.</li>
          </ul>
        </details>
      )}
    </div>
  );
}

export default PairingPanel;
