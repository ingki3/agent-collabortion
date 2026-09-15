"use client";
import type { ConnectionState } from "@/lib/realtime/stream";

/** SCREEN §6 — 끊긴 채로 조용히 낡은 화면을 보여주지 않는다. */
export function ConnectionBanner({ state }: { state: ConnectionState }) {
  if (state !== "reconnecting") return null;
  return (
    <div className="conn-banner" role="status" data-testid="conn-banner">
      실시간 연결 끊김 · 재연결 중 — 복구되면 놓친 이벤트를 채웁니다
    </div>
  );
}
export default ConnectionBanner;
