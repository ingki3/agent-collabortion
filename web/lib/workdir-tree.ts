/**
 * S13 작업 폴더 트리(SCREEN §4.16 `[FOLDERS]`, PRD FR-6.1 폴더 배치 · daemon-protocol v0.10.0 §6.1)의 순수 계산과 문구.
 *
 * 서버가 준 `Workdir` 행을 **방 → 미션 → 에이전트** 로 묶는다. 디스크의 배치(`rooms/<방>/<미션>/<에이전트>/`)와
 * 같은 모양이라 사람이 경로와 화면을 맞춰 본다. 묶는 기준은 행의 `work_id`·`role`·`kind` 와 경로의 모양뿐이다 —
 * 화면은 GC 를 판정하지 않는다(보존 기한은 서버의 `retain_until`).
 *
 * **굵게는 현재 이름, 경로는 만들 때의 이름**이다(§6.1 만들 때 고정). 이름을 바꿔도 폴더는 옮기지 않는다.
 */
import type { Workdir } from "@/lib/api/types";
import { formatBytes } from "@/lib/workdir";

/** 문구 자물쇠 — 화면·목·테스트가 이 표 하나를 본다. */
export const FOLDERS_WORDING = {
  outside: "미션 밖",
  checkouts: "저장소 체크아웃",
  legacy: "옛 배치(세션 폴더)",
  legacy_note: "옮기지 않은 옛 폴더입니다 — 기존 규칙으로 정리됩니다.",
  shared: "공용",
  shared_name: "미션 공용",
  fixed_name: "폴더 이름은 만들 때의 이름입니다",
  fixed_name_tip: "폴더 이름은 만들 때의 이름입니다 — 이름을 바꿔도 폴더는 옮기지 않습니다(실행 중인 작업이 이 경로에서 일합니다).",
  mission_open: "미션이 닫힌 뒤 보존 기한이 지나면 정리",
  /** S17 `none` 유실 경고(SCREEN §4.16 S17 표 · Director 판정). */
  rebind_none_loss: "작업 폴더(미션 공용 포함)는 옮겨지지 않습니다. 아티팩트만 새 컴퓨터로 갑니다",
  /** 미션 닫기 확인 한 줄(D8 B — 닫힘 즉시가 아니라 보존 기한 뒤). */
  closeLine: (count: number, bytes: number, retentionDays: number) =>
    `이 미션의 작업 폴더 ${count}개(${formatBytes(bytes)})는 ${retentionDays}일 뒤 정리됩니다 — 남길 것은 아티팩트로 제출하세요`,
} as const;

/** 같은 경로 조각의 모양 — `/rooms/<room>/<group>/<leaf>` 인가. */
const ROOMS_RE = /\/rooms\/[^/]+\/([^/]+)\/[^/]+\/?$/;

export type GroupKind = "mission" | "outside" | "checkouts" | "legacy";

export interface FolderGroup {
  key: string;
  kind: GroupKind;
  /** 굵게 쓰는 현재 이름 — 미션 제목 또는 묶음 이름. */
  title: string;
  workId: string | null;
  bytes: number;
  /** `_shared` 가 먼저, 그다음 에이전트 행. */
  rows: Workdir[];
}

export interface RoomNode {
  roomId: string;
  /** 현재 방 이름(`Workdir.session.title`). */
  title: string;
  bytes: number;
  lastUsedAt: string | null;
  groups: FolderGroup[];
}

/** 행이 어느 묶음인가. */
export function groupOf(w: Workdir): GroupKind {
  if (w.work_id) return "mission";
  const m = ROOMS_RE.exec(w.path_or_ref);
  if (!m) return "legacy";
  if (m[1] === "_worktrees") return "checkouts";
  if (m[1] === "_room") return "outside";
  // rooms/ 아래인데 work_id 가 없다 = 미션이 지워졌다(ON DELETE SET NULL) — 미션 밖에 둔다.
  return "outside";
}

const ORDER: Record<GroupKind, number> = { mission: 0, outside: 1, checkouts: 2, legacy: 3 };

/** 방 → 묶음 트리. 방·미션 순서는 마지막 사용 최신순, 묶음 안은 공용 먼저. */
export function buildFolderTree(items: Workdir[]): RoomNode[] {
  const rooms = new Map<string, RoomNode>();
  for (const w of items) {
    let room = rooms.get(w.session_id);
    if (!room) {
      room = { roomId: w.session_id, title: w.session?.title ?? w.session_id.slice(0, 8), bytes: 0, lastUsedAt: null, groups: [] };
      rooms.set(w.session_id, room);
    }
    const kind = groupOf(w);
    const key = kind === "mission" ? `m:${w.work_id}` : kind;
    let g = room.groups.find((x) => x.key === key);
    if (!g) {
      const title =
        kind === "mission" ? (w.work?.title ?? (w.work_id ?? "").slice(0, 8))
          : kind === "outside" ? FOLDERS_WORDING.outside
            : kind === "checkouts" ? FOLDERS_WORDING.checkouts
              : FOLDERS_WORDING.legacy;
      g = { key, kind, title, workId: w.work_id ?? null, bytes: 0, rows: [] };
      room.groups.push(g);
    }
    g.rows.push(w);
    const b = w.status === "deleted" ? 0 : (w.disk_bytes ?? 0);
    g.bytes += b;
    room.bytes += b;
    if (w.last_used_at && (!room.lastUsedAt || w.last_used_at > room.lastUsedAt)) room.lastUsedAt = w.last_used_at;
  }
  const out = [...rooms.values()];
  for (const r of out) {
    for (const g of r.groups) g.rows.sort((a, b) => Number(b.role === "shared") - Number(a.role === "shared") || a.path_or_ref.localeCompare(b.path_or_ref));
    r.groups.sort((a, b) => ORDER[a.kind] - ORDER[b.kind] || a.title.localeCompare(b.title));
  }
  return out.sort((a, b) => (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? ""));
}

/**
 * 경로의 마지막 조각이 만들 때의 이름 — `<slug>-<id8>`. 현재 이름의 경로 슬러그와 다르면 「만들 때의 이름」 표시를 붙인다.
 * 서버 `workdirs.PathSlug` 와 같은 규칙(NFC → 소문자 → 글자·숫자·_ 만, 나머지는 `-` 하나, 40자).
 */
export function pathSlug(s: string): string {
  const out = s.normalize("NFC").toLowerCase().replace(/[^\p{L}\p{N}_]+/gu, "-").replace(/^-+|-+$/g, "");
  const cut = [...out].slice(0, 40).join("").replace(/-+$/, "");
  return cut || "x";
}

/** 이 행의 경로가 지금 이름과 다른 이름으로 지어졌는가(이름을 바꾼 뒤). */
export function renamedSince(w: Workdir, currentName: string | null | undefined): boolean {
  if (!currentName) return false;
  const leaf = w.path_or_ref.replace(/\/+$/, "").split("/").pop() ?? "";
  const m = /^(.*)-[0-9a-f]{8,12}$/.exec(leaf);
  if (!m) return false;
  return m[1] !== pathSlug(currentName);
}
