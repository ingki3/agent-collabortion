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

// ── 종료 조건 — S6 6단계 · S7 진행률 · 조건 고치기(T-W15, S-84 · W-19, SCREEN §4.4 6단계 · §4.5 "종료 조건 진행률") ──
//
// Director 지적(2026-09-15): "복잡하고 종료 조건의 파악이 어렵다". 조건 종류는 계약 enum 그대로 넷이고 화면은 **사람 말**로 부른다.
// 같은 조건을 마법사·요약·진행률·다이얼로그가 다른 이름으로 부르지 않게 이름은 `conditionName` 하나에서만 나온다.

/** 계약 CompletionAtom.type → 화면의 말. `agent_approval` 은 리뷰어 이름이 있으면 "Lead 의 검토 승인", 없으면(아직 안 골랐거나 옛 세션) 일반형. */
export const CONDITION_NAME = {
  artifact_submitted: "보고서 제출",
  agent_approval: "에이전트 검토 승인",
  user_approval: "Director 승인",
  manual: "수동 종료",
  /** v1.1 — 마법사에서 비활성으로만 보인다. */
  criteria_met: "성공 기준 충족",
} as const;
export function conditionName(type: string, agentName?: string | null): string {
  if (type === "agent_approval" && agentName) return `${agentName} 의 검토 승인`;
  return (CONDITION_NAME as Record<string, string>)[type] ?? type;
}

/** 마법사 행의 설명 한 줄 — 무엇을 하면 충족되는지. */
export const CONDITION_DESC: Record<keyof typeof CONDITION_NAME, string> = {
  artifact_submitted: "제출자로 지정한 에이전트가 산출물을 제출하면 충족됩니다",
  agent_approval: "리뷰어로 고른 에이전트가 검토를 승인하면 충족됩니다",
  user_approval: "Director 가 받은 요청에서 승인하면 충족됩니다 — 사람이 거는 마지막 관문",
  manual: "Director 가 「종료」 버튼으로 직접 끝냅니다",
  criteria_met: "성공 기준 자동 판정은 다음 버전입니다",
};

/** 조건 편집기(마법사 6단계 · 조건 고치기 다이얼로그가 같은 것을 그린다). */
export const CONDITION_EDITOR = {
  op_label: "조건 결합",
  op_and: "모두 충족해야 끝",
  op_or: "하나만 충족하면 끝",
  /** 요약 문장의 접속사 — "보고서 제출 그리고 Director 승인". */
  join_and: " 그리고 ",
  join_or: " 또는 ",
  submitter: "제출자",
  /** 제출자 미지정 — 담당 에이전트를 따라간다(계약 `who: assignee`). */
  submitter_default: "담당 에이전트 (기본) — 담당이 바뀌면 따라갑니다",
  submitter_default_short: "담당 에이전트",
  reviewer: "리뷰어",
  reviewer_placeholder: "리뷰어를 고르세요",
  /** 안내만 — 막지 않는다(자기 것을 자기가 검토하지 않게). */
  reviewer_is_assignee: "담당 에이전트가 자기 결과를 검토하게 됩니다 — 다른 에이전트를 권합니다",
  /** 다음 단계·저장을 막는 사유(§8.5 — 근처에서 말한다). */
  need_one: "종료 조건을 하나 이상 고르세요",
  reviewer_required: "리뷰어를 고르세요 — 리뷰어가 없으면 아무도 승인할 수 없어 세션이 끝나지 않습니다",
  reviewer_not_participant: "리뷰어는 참여자 중에서 골라야 합니다",
  no_human_gate: "사람 승인 없이 완료됩니다 — 종료 조건에 Director 승인이나 수동 종료가 없습니다.",
  /** v1.1 행의 비활성 사유. */
  criteria_met_note: "성공 기준 자동 판정은 다음 버전입니다",
} as const;

/** 요약 문장 — "보고서 제출 그리고 Director 승인" (`conditionName` 을 접속사로 잇는다). */
export function conditionSentence(names: string[], op: "and" | "or"): string {
  return names.join(op === "and" ? CONDITION_EDITOR.join_and : CONDITION_EDITOR.join_or);
}

/** S7 진행률 행의 두 번째 줄 — 충족했으면 누가·언제, 아니면 다음 행동. */
export const PROGRESS = {
  /** "받은 요청에서 승인하세요" — `hitl_request_id` 가 있으면 그 카드로 가는 링크. */
  user_approval_next: "받은 요청에서 승인하세요",
  /** "Lead 차례" — `next_actor`(또는 지정 에이전트)가 할 일이 남았다. */
  turn: (actor: string) => `${actor} 차례`,
  manual_next: "Director 가 「종료」 로 끝냅니다",
  waiting: "대기 중",
  /** 충족 — "(Writer, 9/13)". */
  met_by: (who: string | null, when: string | null) => (who && when ? `${who}, ${when}` : (who ?? when ?? "충족")),
  /** 상단 한 줄 — "남은 것: Director 승인 1개 · 막힘 1개". 막힌 조건은 이름 대신 개수로 센다(이유는 행이 말한다). */
  summary: (remaining: string[], blocked: number, op: "and" | "or") => {
    const parts: string[] = [];
    if (remaining.length) parts.push(`${remaining.join(", ")} ${remaining.length}개`);
    if (blocked) parts.push(`막힘 ${blocked}개`);
    return `남은 것: ${parts.join(" · ")}${op === "or" && remaining.length + blocked > 1 ? " — 하나만 충족하면 끝" : ""}`;
  },
  summary_satisfied: "조건을 모두 충족했습니다 — 곧 완료됩니다",
  summary_completed: "세션이 끝났습니다",
  /** 막힌 조건이 있을 때 — 누가 고칠 수 있는지. */
  blocked_director: "조건을 고쳐야 세션이 끝날 수 있습니다",
  blocked_member: "Director 가 조건을 고쳐야 세션이 끝날 수 있습니다",
} as const;

/** `CompletionProgress.conditions[].blocked_reason` — ✗ 대신 이 문장을 보인다(계약 v0.1.4). */
export const BLOCKED_REASON: Record<string, string> = {
  reviewer_missing: "리뷰어가 지정되지 않아 아무도 승인할 수 없습니다",
  reviewer_not_participant: "리뷰어가 이 세션의 참여자가 아니어서 승인할 수 없습니다",
  agent_archived: "리뷰어 에이전트가 보관되어 승인할 수 없습니다",
};
export function blockedReasonText(reason: string): string {
  return BLOCKED_REASON[reason] ?? "지금 구조상 충족될 수 없는 조건입니다";
}

/** 「조건 고치기」 다이얼로그(updateSession completion_condition — active·paused 에서도, Director). */
export const FIX_CONDITION = {
  button: "조건 고치기",
  title: "종료 조건 고치기",
  note: "바꾸면 진행률을 다시 계산합니다. 이미 충족된 조건은 그대로 유지됩니다.",
  save: "저장",
  cancel: "취소",
  busy: "저장 중…",
} as const;

// ── v1.1 첫 라운드(T-W16) — S14 「관찰」 표(K-18) · S10 역할의 허용 명령(K-19) · 빈 턴 카드(FR-7.2) ──

/**
 * `ColabCommand`(계약 enum 13개, `colab-cli.md` §2) → 사람 말. S10 역할 구역이 "이 에이전트가 할 수 있는 일: …" 로 그린다.
 * 명령 이름은 내부어라 화면에 나오지 않는다 — 이 표만 나온다. 13개 전부를 `lib/wording.test.ts` 가 계약 enum 과 대조한다.
 */
export const COMMAND_LABEL = {
  session_get: "세션 읽기",
  session_messages: "메시지 읽기",
  artifact_get: "산출물 읽기",
  message_post: "메시지 게시",
  status_set: "상태 알리기",
  decision_record: "결정 기록",
  lane_delegate: "위임",
  artifact_submit: "산출물 제출",
  review_approve: "검토 승인",
  review_reject: "검토 반려",
  hitl_ask: "사람에게 질문",
  hitl_approve_request: "완료 승인 요청",
  hitl_request_info: "사람에게 정보 요청",
} as const;

/** S10 역할 구역 — 허용 명령 목록의 머리말·"전부"·못 하는 것 한 줄(FR-1.9.1 표의 "막는 것과 이유" 열을 사람 말로). */
export const ROLE_COMMANDS = {
  head: "이 에이전트가 할 수 있는 일:",
  all: "전부",
  /** 「전부」 뒤 괄호 — lead 는 코디네이터라서, custom 은 Director 가 지시문으로 정해서. */
  all_lead: "코디네이터는 모든 명령을 씁니다",
  all_custom: "역할 대신 지시문이 정합니다",
  /** "<못 하는 것>은 못 합니다 — <이유>" 의 뒤 절. */
  cannot: (denied: string) => `${denied}은 못 합니다`,
  reason_worker: "위임·검토 승인·완료 승인 요청은 Lead 의 일",
  reason_reviewer: "위임·완료 승인 요청은 Lead 의 일, 산출물 대신 검토 반려 사유를 남깁니다",
  /** 저장 전 미리보기 — 고른 역할이 저장된 역할과 다를 때. */
  preview: "저장하면 이 목록으로 바뀝니다",
  readonly: "역할이 정합니다 — 여기서 고칠 수 없습니다",
} as const;

/** S14 「관찰」 표(PRD §11 관찰 행 — 목표치 없음, 지표 10개 표와 **별도**, Director 확정 2026-09-15). */
export const OBSERVATIONS = {
  title: "관찰",
  subtitle: "목표치 없이 분포만 봅니다",
  col_name: "관찰",
  col_value: "값",
  col_n: "표본",
  median: "중앙값",
  p95: "p95",
  note_summary: "세는 법",
  /** `breakdown[]` 의 하위 행 머리 — "규칙 6 · 담당 에이전트 폴백". */
  rule: (n: string) => `규칙 ${n}`,
  reload: "다시 세기",
  counting: "세는 중…",
} as const;

/**
 * `routing_concentration.breakdown[].kind` — FR-3.3 규칙 번호("1"~"8") 또는 "platform" 을 사람 말로.
 * 규칙 번호는 PRD 의 것이라 그대로 두고(Director 가 PRD 와 대조한다) 뒤에 무엇인지 한 마디를 붙인다. 배열 인덱스 = 규칙 번호 - 1.
 */
export const ROUTING_RULE_LABEL: readonly string[] = [
  "기록만",
  "에이전트 멘션",
  "@all·사람만 멘션",
  "에이전트가 멘션",
  "답글",
  "담당 에이전트 폴백",
  "지연 폴백",
  "위임자 멘션 억제",
];
export const ROUTING_PLATFORM_LABEL = "플랫폼(위임·다시 지시)";
export function routingKindLabel(kind: string): string {
  if (kind === "platform") return ROUTING_PLATFORM_LABEL;
  const what = /^[1-8]$/.test(kind) ? ROUTING_RULE_LABEL[Number(kind) - 1] : undefined;
  return what ? `${OBSERVATIONS.rule(kind)} · ${what}` : kind;
}

/**
 * 빈 턴(FR-7.2 v0.17) — 메시지 0·플랫폼 조작 0·편집 0 으로 끝난 턴. 서버가 finish 에서 `status/turn_end/empty_turn/info` 행을 남기고
 * 문장은 `payload.args.note` 에 싣는다. 화면은 그 note 를 **그대로** 보이고, note 가 없을 때만 여기 문장을 쓴다(같은 문장).
 * 오류가 아니다 — 정보 카드(ⓘ). 멘션이 낭비됐는지 정당한 무응답인지는 Director 가 카드를 보고 판단한다.
 */
export const EMPTY_TURN = {
  note: "아무것도 하지 않고 턴을 끝냈습니다",
  /** 카드의 종류 표시(스크린리더·툴팁). */
  kind: "정보",
} as const;
