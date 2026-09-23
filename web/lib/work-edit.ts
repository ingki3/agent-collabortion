/**
 * 미션 칸의 편집 동작 — 설정 편집 · Director 교체 · 조건 고치기(SCREEN v0.19.2 §4.6 우열 (가) 「미션 동작」 · §2.3 미션 층, T-R2-W4b).
 *
 * 문구는 여기서만 나온다(병렬 워커와 `lib/wording.ts` 끝을 나눠 쓰지 않으려고 파일을 나눴다 — 문구 자물쇠는 `lib/` 전체를 센다).
 * 권한 판정도 여기 한 곳 — 우열 버튼·다이얼로그가 같은 함수를 부른다.
 *  - 설정 편집 · 조건 고치기: **그 미션의 director**(계약 updateWork 「권한: 그 미션의 director」).
 *  - Director 교체: **그 미션의 director · ws owner·admin**(계약 changeWorkDirector) — 떠난 Director 는 스스로 넘길 수 없어서다(FR-5.3).
 */
import type { Work } from "@/lib/api/types";
import { WORK_PANEL } from "@/lib/wording";

export const WORK_EDIT = {
  edit: "설정 편집",
  change_director: "Director 교체",
  fix_condition: "조건 고치기",
  /** S21 폼의 편집 모드 — 제목과 저장 단추. */
  title: "미션 설정 편집",
  sub: "바꾼 칸만 저장합니다. 종료 조건을 바꾸면 진행률을 다시 계산하고, 이미 충족된 조건은 그대로 유지됩니다.",
  save: "저장",
  saving: "저장하는 중…",
  director_elsewhere: "Director 는 미션 칸의 「Director 교체」에서 바꿉니다",
  closed: "끝난 미션은 고칠 수 없습니다",
  /** 교체 권한이 없는 사람에게(버튼 아래 글자). */
  change_director_role: "이 미션의 Director 나 워크스페이스 소유자·관리자만 Director 를 바꿀 수 있습니다",
  /** 막힌 조건이 있을 때 — Director 에게는 단추와 함께, 그 밖에게는 누가 고쳐야 하는지. */
  blocked_director: "조건을 고쳐야 미션이 끝날 수 있습니다",
  blocked_member: "Director 가 조건을 고쳐야 미션이 끝날 수 있습니다",
} as const;

export const CHANGE_DIRECTOR = {
  title: "미션 Director 교체",
  note: "새 Director 가 이 미션의 확인 요청에 답하고 미션을 끝냅니다. 바뀐 사실은 방 타임라인에 남습니다.",
  current: "지금 Director",
  director: "새 Director",
  choose: "고르세요",
  deputy: "deputy(선택)",
  deputy_keep: "그대로 둡니다",
  deputy_none: "없음",
  deputy_note: "취소는 즉시, 승인은 기한 절반 후 가능합니다",
  same: "지금 Director 입니다 — 다른 사람을 고르세요",
  pick: "새 Director 를 고르세요",
  confirm: "교체",
  busy: "바꾸는 중…",
  cancel: "취소",
} as const;

type WorkLike = Pick<Work, "status" | "my_work_role"> & { director?: { display_name?: string } | null };
const CLOSED = new Set(["completed", "cancelled"]);

/** 설정 편집·조건 고치기 — 비활성 사유(없으면 null). */
export function workEditBlocked(w: WorkLike): string | null {
  if (CLOSED.has(w.status)) return WORK_EDIT.closed;
  if (w.my_work_role !== "director") return WORK_PANEL.not_director(w.director?.display_name ?? "");
  return null;
}

/** Director 교체 — 그 미션의 director 또는 ws owner·admin(`canManage`). */
export function changeDirectorBlocked(w: WorkLike, canManage: boolean): string | null {
  if (w.my_work_role === "director" || canManage) return null;
  return WORK_EDIT.change_director_role;
}
