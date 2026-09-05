"use client";
/** S11 런타임 카드 — P1 최소: 이름 · 온라인 · CLI 목록 · 마지막 접속(SCREEN §4.8). */
import { relativeTime } from "@/lib/time";
import type { Runtime } from "@/lib/api/types";

const KIND = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" } as const;

export function RuntimeCard({ rt }: { rt: Runtime }) {
  const online = rt.status === "online";
  return (
    <div className="card" data-testid="runtime-card" data-status={rt.status}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <b>{rt.name}</b>
        <span className="small" style={{ color: online ? "var(--s-done-text)" : "var(--s-fail-text)" }}>
          {online ? "● 온라인" : "✕ 오프라인"}
        </span>
      </div>
      <div className="small muted-3">
        {rt.host ?? "—"} · 데몬 {rt.daemon_version ?? "—"} · 마지막 접속 {relativeTime(rt.last_seen_at)}
      </div>
      <ul className="small muted" style={{ margin: "8px 0 0", paddingLeft: 18 }}>
        {rt.capabilities.length === 0 && <li>감지된 CLI 없음</li>}
        {rt.capabilities.map((c) => (
          <li key={c.kind}>
            {KIND[c.kind]} {c.version ?? ""} · {c.logged_in ? "로그인됨" : "로그인 필요"} · 모델 {c.models?.length ?? 0}개
          </li>
        ))}
      </ul>
      {!online && rt.grace_ends_at && (
        <div className="small" style={{ marginTop: 8, color: "var(--s-fail-text)" }}>
          오프라인 {relativeTime(rt.offline_since)} 부터 · 유예 만료 {new Date(rt.grace_ends_at).toLocaleDateString("ko-KR")}
        </div>
      )}
      <div className="small muted-3" style={{ marginTop: 6 }}>실행 중 task {rt.running_task_count}</div>
    </div>
  );
}

export default RuntimeCard;
