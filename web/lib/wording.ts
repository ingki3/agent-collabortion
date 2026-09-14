/**
 * 화면 문구 한곳 — S5 세션 카드 옵션(「…」)·삭제(FR-2.7, SCREEN §4.3 「카드 옵션(…) — 삭제」·§5 확인 다이얼로그, T-W13).
 *
 * 왜 여기인가: 문구는 §8.4 의 말로 쓰고 `lib/wording.test.ts` 자물쇠가 잰다. 같은 사건을 두 자리(메뉴·다이얼로그·안내 줄)가
 * 다른 말로 부르지 않게 문장은 이 표에서만 나온다 — 컴포넌트는 이 표를 그린다.
 *
 * **서버 문장은 여기 없다.** `409 session_active`·`409 workdir_unmerged`·`403` 의 `Problem.detail` 은 서버(T-S17)가 쓰고 화면은
 * 그대로 보인다(`errorMessage`). 목이 흉내 내는 그 문장은 `lib/mock/wording.ts` 의 `MOCK_ONLY` 에 있고 T-S17 뒤 `SERVER` 로 옮긴다.
 */
import type { SessionStatus, Workdir } from "@/lib/api/types";

/** 끝난 세션 — 계약 deleteSession "`draft`·`completed`·`cancelled` 만". 그 외는 409 `session_active`. */
export const DELETABLE_STATUS: ReadonlySet<SessionStatus> = new Set<SessionStatus>(["draft", "completed", "cancelled"]);

/**
 * 삭제 가부 — 화면은 판정하지 않는다(서버가 409·403 으로 다시 검사한다). 여기 있는 비활성은 §8.5 "왜 비활성인지 근처에서
 * 말한다" 를 위한 사유 선택이다. 상태가 먼저다: 진행 중이면 권한이 있어도 "먼저 종료" 가 다음 행동이기 때문.
 */
export type DeleteGate = { ok: true } | { ok: false; reason: string };
export function deleteGate(status: SessionStatus, canDelete: boolean): DeleteGate {
  if (!DELETABLE_STATUS.has(status)) return { ok: false, reason: SESSION_MENU.blocked_active };
  if (!canDelete) return { ok: false, reason: SESSION_MENU.blocked_role };
  return { ok: true };
}

/** 카드 「…」 메뉴 — 항목 둘(세션 열기·삭제)과 비활성 사유 둘(SCREEN §4.3). */
export const SESSION_MENU = {
  /** 「…」 버튼의 aria-label. */
  button: "세션 옵션",
  open: "세션 열기",
  delete: "삭제",
  /** 진행 중(`active`·`paused`·`completing`) — 계약·SCREEN 이 못박은 문장. */
  blocked_active: "진행 중인 세션은 먼저 종료하세요",
  /** Director 도 소유자·관리자도 아님. */
  blocked_role: "Director 나 소유자·관리자만 삭제할 수 있습니다",
} as const;

/** 확인 다이얼로그(§5 "무엇이 사라지는지 명시, 되돌릴 수 없으면 그렇다고"). */
export const DELETE_DIALOG = {
  /** 제목 — 세션 이름이 들어간다. */
  title: (sessionTitle: string) => `「${sessionTitle}」 세션을 삭제할까요?`,
  /** 본문 1 — 사라지는 것(계약 description 의 목록을 사용자의 말로). */
  loses: "메시지 · 작업 줄기 · 아티팩트 · 비용 기록이 함께 사라지고 워크스페이스 집계에서도 빠집니다.",
  /** 본문 2 — 되돌릴 수 없음 + 이 컴퓨터의 작업 폴더. */
  irreversible: "되돌릴 수 없습니다. 이 컴퓨터의 작업 폴더도 정리됩니다.",
  confirm: "삭제",
  cancel: "취소",
  /** 409 `workdir_unmerged` — `Problem.workdirs[]` 위의 머리말과 S13 링크. */
  workdirs_head: "삭제를 막은 작업 폴더:",
  workdirs_link: "작업 폴더 관리",
  /** 삭제 중 버튼 문구. */
  busy: "삭제 중…",
} as const;

/** 작업 폴더 한 줄 — 경로 · 브랜치 · 사유(미병합/미커밋). `gc_blocked_reason` 이 없고 `dirty` 만 있으면 미커밋으로 본다. */
export function workdirBlockLabel(w: Pick<Workdir, "gc_blocked_reason" | "dirty" | "commits_ahead">): string {
  if (w.gc_blocked_reason === "unmerged_commits") return w.commits_ahead != null && w.commits_ahead > 0 ? `미병합 커밋 ${w.commits_ahead}개` : "미병합 커밋";
  if (w.gc_blocked_reason === "uncommitted_changes" || w.dirty) return "미커밋 변경";
  return "정리 필요";
}

/** S7 을 보고 있다가 `session.deleted` 를 받은 사람에게 — 목록으로 돌아온 뒤 한 줄(SCREEN §4.3 · 계약 SSE 표). */
export const SESSION_DELETED_NOTICE = {
  /** 내가 지운 것이 아니라 다른 곳에서 지워진 경우. */
  elsewhere: (sessionTitle: string) => `「${sessionTitle}」 세션이 삭제되어 목록으로 돌아왔습니다.`,
  /** 다이얼로그에서 내가 지운 경우 — 카드가 빠진 자리를 설명한다. */
  mine: (sessionTitle: string) => `「${sessionTitle}」 세션을 삭제했습니다.`,
} as const;
