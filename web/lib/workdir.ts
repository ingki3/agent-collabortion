/**
 * S13 Workdir 관리(SCREEN §4.8 · FR-6.4 M4 · E13-09~16 · U6 9·10·11)의 순수 계산.
 *
 * 화면은 GC 를 **판정하지 않는다** — 판정은 서버가 하고(daemon-protocol §6 "GC 판정은 서버가 한다"),
 * 여기 있는 것은 서버가 준 `Workdir.gc_blocked_reason`·`dirty`·`commits_ahead`·`retain_until` 을
 * 사람의 **다음 행동**으로 번역하는 문장뿐이다. 그 번역이 두 갈래인 것이 이 파일의 존재 이유다:
 * `unmerged_commits` 는 **병합해라**(E13-12), `uncommitted_changes` 는 **커밋하거나 버려라**(E13-13).
 * 한 문장으로 뭉치면 골든 표의 "the two blocked reasons are distinguishable" 행이 말하는 결함이 된다.
 *
 * 쿼터도 마찬가지로 서버 판정(`createSession` 이 `workdir_quota_exceeded` 로 막는다)이지만, S13 상단의
 * 사용률 막대가 **≥ 로 빨강**이 되어야 사람이 막히기 전에 안다(E13-16 은 > 가 아니라 ≥ 다).
 * 쿼터 미설정(null)은 0 이 아니라 무제한이다(E13-19).
 */
import type { Workdir, WorkdirKind, WorkdirStatus } from "@/lib/api/types";

export const WORKDIR_KIND_LABEL: Record<WorkdirKind, string> = {
  worktree: "worktree",
  container: "container",
  dir: "dir",
};

export const WORKDIR_STATUS_LABEL: Record<WorkdirStatus, string> = {
  active: "활성",
  retained: "보존 중",
  deleted: "삭제됨",
};

/** GB 단위. 1GB = 2^30 — 서버 쿼터(`workdir_disk_quota_gb`)와 같은 눈금을 쓴다. */
export const GB = 1024 ** 3;

export function formatBytes(n: number | null | undefined): string {
  if (n == null) return "—";
  if (n >= GB) return `${(n / GB).toFixed(1)}GB`;
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(0)}MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(0)}KB`;
  return `${n}B`;
}

export interface GcBlock {
  /** 왜 지우지 못했는지. */
  title: string;
  /** Director 가 **다음에 할 일**. 두 사유가 서로 다른 행동을 요구한다. */
  next: string;
}

/**
 * GC 가 삭제를 막은 사유 → 문장 2줄. 사유가 없으면 null.
 * `commits_ahead` 는 `unmerged_commits` 일 때만 수를 말한다 — 0 개를 "0개의 미병합 커밋"이라 쓰면 거짓이다.
 */
export function gcBlockText(w: Pick<Workdir, "gc_blocked_reason" | "commits_ahead">): GcBlock | null {
  switch (w.gc_blocked_reason) {
    case "unmerged_commits":
      return {
        title:
          w.commits_ahead != null && w.commits_ahead > 0
            ? `미병합 커밋 ${w.commits_ahead}개가 있어 정리하지 않았습니다`
            : "미병합 커밋이 있어 정리하지 않았습니다",
        next: "이 커밋은 원래 머신의 이 브랜치에만 있습니다 — 먼저 병합하세요(E13-12).",
      };
    case "uncommitted_changes":
      return {
        title: "미커밋 변경이 있어 정리하지 않았습니다",
        next: "diff 만 제출하고 커밋하지 않은 경우입니다 — 커밋하거나 버린 뒤 다시 지우세요(E13-13).",
      };
    default:
      return null;
  }
}

/** 삭제 버튼을 기본으로 막는가(FR-6.4 M4). `dirty` 든 GC 차단 사유든 둘 중 하나면 막는다. */
export function deleteBlocked(w: Pick<Workdir, "dirty" | "gc_blocked_reason">): boolean {
  return w.dirty === true || w.gc_blocked_reason != null;
}

/** 보존 만료 한 줄. `retain_until` 이 없으면 보존 기한이 걸리지 않는 종류다(container·none 은 즉시 정리). */
export function retentionLabel(w: Pick<Workdir, "retain_until" | "status">, now = Date.now()): string {
  if (w.status === "deleted") return "삭제됨";
  if (!w.retain_until) return "보존 기한 없음";
  const t = Date.parse(w.retain_until);
  if (Number.isNaN(t)) return w.retain_until;
  const d = new Date(t).toLocaleDateString("ko-KR");
  const days = Math.ceil((t - now) / 86_400_000);
  if (days <= 0) return `${d} · 정리 대상`;
  return `${d} · ${days}일 남음`;
}

export interface QuotaView {
  /** 0~1 이상. 쿼터가 없으면 null(막대를 그리지 않는다). */
  ratio: number | null;
  /** E13-16 은 `≥` 다 — 정확히 꽉 찬 상태도 막힌다. */
  atLimit: boolean;
  text: string;
}

/** 상단 사용률(SCREEN §4.8 "워크스페이스 용량 상한 대비 사용률"). `quotaGb` null·0 = 미설정 = 무제한(E13-19). */
export function quotaView(usedBytes: number, quotaGb: number | null | undefined): QuotaView {
  if (quotaGb == null || quotaGb <= 0) {
    return { ratio: null, atLimit: false, text: `${formatBytes(usedBytes)} 사용 · 용량 상한 미설정(무제한)` };
  }
  const limit = quotaGb * GB;
  const ratio = usedBytes / limit;
  return {
    ratio,
    atLimit: usedBytes >= limit,
    text: `${formatBytes(usedBytes)} / ${quotaGb}GB (${Math.round(ratio * 100)}%)`,
  };
}
