"use client";
/**
 * S11 런타임 카드(SCREEN §4.8) — 머신 카드. 계약 v0.4.1 `RuntimeCapability` 의 새 키를 전부 읽는다(W-1):
 * `adapter_version` · `protocol_version` · `resume` · `usage` · `tool_disallow` · `brief_transport` · `allow_once_missing`.
 *
 * **`colab_cli` 는 probe 최상위**다(머신 속성 — 런타임이 둘이어도 바이너리는 하나, 0개인 머신에서도 보고된다).
 * 에이전트는 colab CLI 로 서버에 말하므로 `present: false` 면 **세션이 조용히 아무 말도 못 한다** — 경고로 드러낸다.
 *
 * 능력이 없는 것도 침묵이 아니라 문장으로 말한다(SCREEN §1 원칙 3·5):
 * `usage:false` 면 비용이 추정 배지가 되고(FR-7.3), `resume:false` 면 재진입이 늘 콜드 스타트다.
 */
import "./runtime-card.css";
import { durationSince, relativeTime } from "@/lib/time";
import type { Runtime, RuntimeCapability } from "@/lib/api/types";

const KIND = { claude_code: "Claude Code", hermes: "Hermes", antigravity: "Antigravity" } as const;
const BRIEF = { acp_meta_system_prompt: "ACP _meta 시스템 프롬프트", instruction_file: "지시 파일(CLAUDE.md·AGENTS.md)" } as const;

/** 능력 한 줄 요약 — 있는 것은 조용히, **없는 것은 결과와 함께** 말한다. */
export function capabilityNotes(c: RuntimeCapability): string[] {
  const out: string[] = [];
  out.push(c.logged_in ? "로그인됨" : "로그인 필요 — 이 런타임으로는 실행할 수 없습니다");
  out.push(`모델 ${c.models?.length ?? 0}개`);
  if (c.protocol_version != null) out.push(`ACP protocol v${c.protocol_version}`);
  out.push(c.adapter_version ? `어댑터 ${c.adapter_version}` : "어댑터 버전 실측 실패");
  if (c.resume === false) out.push("resume 미지원 — 재진입이 늘 콜드 스타트입니다");
  if (c.usage === false) out.push("사용량 미보고 — 비용이 추정치가 됩니다(하드 컷 없음)");
  if (c.tool_disallow === false) out.push("툴 차단 수단 없음 — 프로파일의 툴 허용 목록이 강제되지 않습니다");
  if (c.brief_transport) out.push(`브리프: ${BRIEF[c.brief_transport]}`);
  if (c.allow_once_missing) out.push("allow_once 부재 — 권한 협상이 매번 always 로 떨어집니다");
  return out;
}

/**
 * 오프라인 유예 한 줄(SCREEN §4.8 · U12 1·2 · E14-01·02).
 *
 * "오프라인"만 쓰면 사람은 **언제까지 기다리면 되는지**를 모른다. U12 1단계가 요구하는 문장은
 * `오프라인 · 1일 경과 · 유예 6일 남음` 이고, 유예를 넘기면(E14-02) 남은 시간 대신 **묶인 세션 수와
 * 재바인딩** 을 말해야 한다 — 그때부터는 기다림이 아니라 선택이 할 일이기 때문이다.
 *
 * `grace_ends_at` 은 서버가 준다(계약 `Runtime.grace_ends_at`). 화면이 7일을 더하지 않는다 —
 * 유예는 워크스페이스 설정(`runtime_offline_grace`)이라 7일이 아닐 수 있다.
 */
export function graceView(
  rt: Pick<Runtime, "offline_since" | "grace_ends_at" | "paused_session_count">,
  now = Date.now(),
): { text: string; expired: boolean; daysLeft: number | null } {
  const since = rt.offline_since ? `오프라인 · ${durationSince(rt.offline_since, null, now)} 경과` : "오프라인";
  const end = rt.grace_ends_at ? Date.parse(rt.grace_ends_at) : NaN;
  if (Number.isNaN(end)) return { text: since, expired: false, daysLeft: null };
  const left = end - now;
  if (left > 0) {
    const days = Math.ceil(left / 86_400_000);
    return { text: `${since} · 유예 ${days}일 남음`, expired: false, daysLeft: days };
  }
  const paused = rt.paused_session_count ?? 0;
  return {
    text: `${since} · 유예 만료(${new Date(end).toLocaleDateString("ko-KR")})${paused ? ` · 이 런타임에 묶인 세션 ${paused}개가 일시정지됨` : ""}`,
    expired: true,
    daysLeft: 0,
  };
}

export function RuntimeCard({ rt, children }: { rt: Runtime; children?: React.ReactNode }) {
  const online = rt.status === "online";
  const cli = rt.colab_cli;
  return (
    <div className="card rtcard" data-testid="runtime-card" data-status={rt.status} data-runtime-id={rt.id}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <b>{rt.name}</b>
        <span className="small" style={{ color: online ? "var(--s-done-text)" : "var(--s-fail-text)" }}>
          {online ? "● 온라인" : "✕ 오프라인"}
        </span>
      </div>
      <div className="small muted-3">
        {rt.host ?? "—"} · 데몬 {rt.daemon_version ?? "—"} · 마지막 접속 {relativeTime(rt.last_seen_at)}
      </div>

      {cli && !cli.present && (
        <p className="rtcard__alarm" role="alert" data-testid="colab-cli-missing">
          ⚠ colab CLI 가 설치되어 있지 않습니다. 에이전트는 colab CLI 로 서버에 말합니다 — 없으면 세션이 조용히 아무 말도 못 합니다.
          이 머신에서 <code>colab</code> 을 설치한 뒤 데몬을 다시 시작하세요.
        </p>
      )}
      {cli?.present && (
        <div className="small muted-3" data-testid="colab-cli-present">colab CLI {cli.version || "버전 미상"}</div>
      )}
      {!cli && <div className="small muted-3" data-testid="colab-cli-unknown">colab CLI 상태를 보고받지 못했습니다</div>}

      <ul className="rtcard__caps" data-testid="runtime-capabilities">
        {rt.capabilities.length === 0 && <li data-testid="runtime-no-cli">감지된 CLI 없음 — 이 머신에서는 세션을 실행할 수 없습니다</li>}
        {rt.capabilities.map((c) => (
          <li key={c.kind} data-testid="runtime-capability" data-kind={c.kind} data-logged-in={String(c.logged_in)}>
            <b>{KIND[c.kind]}</b> {c.version ?? "버전 미상"}
            <div className="rtcard__notes">{capabilityNotes(c).join(" · ")}</div>
          </li>
        ))}
      </ul>

      {rt.repos.length > 0 && (
        <ul className="rtcard__repos" data-testid="runtime-repos">
          {rt.repos.map((r) => (
            <li key={r.path} data-testid="runtime-repo" data-remote-url={r.remote_url ?? ""}>
              <code>{r.path}</code>
              {r.branch ? ` · ${r.branch}` : ""}
              {r.clean === false ? <span className="rtcard__dirty"> · 클린 아님</span> : r.clean ? " · 클린" : ""}
              {/* remote URL 이 재바인딩의 "같은 저장소" 키다(FR-9.2 F) — 경로만 보여 주면 후보 판정의 근거가 화면에서 사라진다. */}
              <div className="rtcard__remote" data-testid="runtime-repo-remote">{r.remote_url ?? "remote 없음"}</div>
            </li>
          ))}
        </ul>
      )}

      {!online && (rt.grace_ends_at || rt.offline_since) && (
        <div className="small" style={{ marginTop: 8, color: "var(--s-fail-text)" }} data-testid="runtime-grace" data-grace-expired={String(graceView(rt).expired)}>
          {graceView(rt).text}
        </div>
      )}
      <div className="small muted-3" style={{ marginTop: 6 }}>
        실행 중 task {rt.running_task_count}
        {rt.workdir_disk_bytes ? ` · workdir ${(rt.workdir_disk_bytes / 1e9).toFixed(1)}GB` : ""}
      </div>
      {children}
    </div>
  );
}

export default RuntimeCard;
