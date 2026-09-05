"use client";
/**
 * S12 Add a computer(SCREEN §4.8) — S4 2단계에 인라인, /runtimes/new 에 단독.
 * 설치 명령 2줄(복사) + `waiting → connected → probing → ready` 4단계. SSE `pairing.updated` 로 갱신하고(셸 안에서는 셸의
 * 연결을 공유, 온보딩에서는 자기 연결), ready 전까지는 스트림이 열려 있어도 5초 폴링(GET pairing)을 **백업**으로 유지한다
 * (G3 W-2: 이벤트 유실·프록시 버퍼링에 대비, N5). resync 면 pairing 을 REST 로 다시 읽는다(N4). 3분 넘게 waiting 이면 문제 해결 안내를 편다.
 * 발급(createPairing)은 마운트당 정확히 1회 — StrictMode 의 이중 effect 를 ref 로 막고, 재시도까지 같은 `Idempotency-Key` 를
 * 보내 네트워크 재시도에도 페어링이 1건만 생긴다(G3 W-1).
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type { Pairing, PairingStatus, Runtime, StreamEvent } from "@/lib/api/types";

export const PAIRING_STAGES: { key: PairingStatus; label: string }[] = [
  { key: "waiting", label: "대기 중" },
  { key: "connected", label: "연결됨" },
  { key: "probing", label: "CLI 감지 중" },
  { key: "ready", label: "준비 완료" },
];

export const TROUBLESHOOT_AFTER_MS = 3 * 60 * 1000;
/** ready 전까지 도는 백업 폴링 간격(N5). */
export const POLL_INTERVAL_MS = 5000;

export function stageIndex(s: PairingStatus): number {
  return PAIRING_STAGES.findIndex((x) => x.key === s);
}

/**
 * 스트림·폴링에서 온 pairing 을 현재 상태에 합친다. 다른 페어링(id 불일치)은 무시하고, 단계는 **뒤로 가지 않는다**
 * (재연결 backfill 로 `connected` 프레임이 REST 의 `ready` 뒤에 도착해도 준비 완료가 풀리지 않는다). `expired` 는 항상 받는다.
 * 설치 명령 2줄과 pairing_token 은 **발급 응답의 것을 유지**한다 — 서버는 1회용 토큰을 발급 때만 주고 이후 GET·SSE 에서는
 * `<pairing_token>` 자리표시자로 가리므로, 그대로 덮어쓰면 대기 중에 복사 버튼이 쓸 수 없는 명령을 보여준다.
 */
export function mergePairing(cur: Pairing | null, next: Pairing): Pairing | null {
  if (!cur || cur.id !== next.id) return cur;
  const merged: Pairing = {
    ...next,
    install_commands: cur.install_commands?.length ? cur.install_commands : next.install_commands,
    pairing_token: cur.pairing_token ?? next.pairing_token,
  };
  if (next.status === "expired") return merged;
  if (cur.status === "expired") return cur;
  return stageIndex(next.status) >= stageIndex(cur.status) ? merged : cur;
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
  // 발급 1회당 멱등키 1개. 같은 발급의 재시도(네트워크 오류 뒤 "다시 시도")는 같은 키 → 서버가 같은 페어링을 돌려준다.
  // "다시 발급"(만료)만 새 키를 쓴다.
  const issueKey = useRef<string | null>(null);
  // 마운트당 1회 가드 — dev StrictMode 는 effect 를 mount→cleanup→mount 로 두 번 돌리지만 ref 는 유지된다(W-1).
  const issuedFor = useRef<string | null>(null);

  const create = useCallback(
    async (opts: { fresh?: boolean } = {}) => {
      setError(null);
      setTroubleOpen(false);
      readyFired.current = false;
      startedAt.current = now();
      if (opts.fresh || !issueKey.current) issueKey.current = newIdempotencyKey();
      try {
        const p = await api.post("/workspaces/{workspaceId}/runtimes/pairings", {
          path: { workspaceId },
          body: {},
          idempotencyKey: issueKey.current,
        });
        setPairing(p);
      } catch (e) {
        setError(errorMessage(e));
      }
    },
    [workspaceId, now],
  );

  useEffect(() => {
    if (!canManage || issuedFor.current === workspaceId) return;
    issuedFor.current = workspaceId;
    void create();
  }, [canManage, workspaceId, create]);

  // 실시간: pairing.updated (셸 안에서는 StreamProvider 의 공용 연결, 온보딩에서는 자기 연결)
  const onEvent = useCallback((ev: StreamEvent) => {
    if (ev.type !== "pairing.updated") return;
    const p = ev.payload as unknown as Pairing;
    if (!p || typeof p.id !== "string" || typeof p.status !== "string") return;
    setPairing((cur) => mergePairing(cur, p));
  }, []);
  const pairingId = pairing?.id ?? null;
  const refetch = useCallback(async () => {
    if (!pairingId) return;
    try {
      const p = await api.get("/workspaces/{workspaceId}/runtimes/pairings/{pairingId}", {
        path: { workspaceId, pairingId },
      });
      setPairing((cur) => mergePairing(cur, p));
    } catch (e) {
      if (isApiError(e) && e.status === 410) setPairing((cur) => (cur ? { ...cur, status: "expired" } : cur));
    }
  }, [pairingId, workspaceId]);
  const conn = useWorkspaceStream(workspaceId, onEvent, { enabled: !!pairing, onResync: () => void refetch() });

  // 폴링 백업(5초) — ready·expired 전까지는 스트림이 열려 있어도 계속 돈다(W-2: 프레임 유실·프록시 버퍼링 대비). 단계를 옮기는 주 경로는
  // pairing.updated 이고, 폴링은 mergePairing 으로 합쳐지므로 뒤로 가지 않는다.
  const settled = !pairing || pairing.status === "ready" || pairing.status === "expired";
  useEffect(() => {
    if (settled) return;
    const t = setInterval(() => void refetch(), POLL_INTERVAL_MS);
    return () => clearInterval(t);
  }, [settled, refetch]);

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
    <div className="stack" data-testid="pairing-panel" data-status={pairing.status} data-conn={conn}>
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
        <button type="button" className="btn" onClick={() => void create({ fresh: true })}>
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
