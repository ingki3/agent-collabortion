/**
 * 목이 흉내 내는 **서버 문장** 한곳(T-W10).
 *
 * 서버(PR #192, S-67)가 `Problem.detail`·`title`·`errors[].message`·시스템 메시지를 §8.4 의 말로 바꿨다.
 * 목이 서버와 다른 말을 하면 화면 테스트가 실서버를 못 대변하므로, 서버가 만드는 문장은 전부 여기서만 적고
 * `handlers.ts` 는 이 표를 쓴다. `server-wording.test.ts` 가 각 항목을 `server/` 소스의 그 파일과 **글자 단위로**
 * 대조한다 — 서버가 문장을 바꾸면 여기가 빨개진다.
 *
 * 규칙:
 *   - `at` 은 `server/` 기준 경로. 그 파일에 `text` 가 리터럴로 있어야 한다(형식 문자열은 `%d`·`%s` 앞까지).
 *   - 목에만 있는 경로(서버가 아직 안 만든 op)의 문장은 `SERVER` 에 넣지 않는다 — 대조할 정답이 없다. 지금은 그런 op 이 없다(T-W12).
 */

import type { Metric, ObservationRow } from "@/lib/api/types";

export interface ServerSentence {
  /** 서버 소스의 문장(리터럴 그대로). */
  text: string;
  /** `server/` 기준 파일 경로 — 대조 테스트가 이 파일을 읽는다. */
  at: string;
}

/** `Problem.title` — `apperr.Title(status)` 의 표(server/internal/apperr/apperr.go `titles`). */
export const TITLE: Record<number, string> = {
  400: "잘못된 요청",
  401: "로그인 필요",
  403: "권한 없음",
  404: "찾을 수 없음",
  409: "지금은 할 수 없음",
  410: "더 이상 쓸 수 없음",
  413: "너무 큼",
  422: "입력값 확인 필요",
  429: "요청이 너무 잦음",
  500: "서버 오류",
  501: "아직 지원하지 않음",
};
export function titleOf(status: number): string {
  return TITLE[status] ?? (status >= 500 ? "서버 오류" : status >= 400 ? "요청 오류" : String(status));
}

/** 세션·작업 줄기 상태의 화면 말 — `apperr.StatusLabel` 의 표(`statusLabels`). "(현재 상태: …)" 에 쓴다. */
export const STATUS_LABEL: Record<string, string> = {
  draft: "시작 전",
  active: "진행 중",
  paused: "일시정지",
  completed: "완료",
  cancelled: "종료됨",
  queued: "대기 중",
  running: "실행 중",
  blocked: "답을 기다림",
  waiting_human: "사람 확인 대기",
  done: "완료",
  failed: "실패",
};
export const statusLabel = (status: string): string => STATUS_LABEL[status] ?? status;

/** 404 의 명사표 — `apperr.NotFoundNouns`. `notFound(key)` 가 을/를 을 붙여 문장을 만든다. */
export const NOT_FOUND_NOUN: Record<string, string> = {
  session: "세션",
  user: "사용자",
  workspace: "워크스페이스",
  workspace_settings: "워크스페이스 설정",
  invite: "초대",
  agent: "에이전트",
  agent_template: "에이전트 템플릿",
  profile: "프로파일",
  participant: "참여자",
  artifact: "아티팩트",
  workdir: "작업 폴더",
  runtime: "컴퓨터",
  pairing: "연결 코드",
  lane: "작업 줄기",
  task: "할 일",
  inbox_item: "받은 요청",
  hitl_request: "확인 요청",
};

/** `apperr.Josa` — 마지막 글자에 받침이 있으면 `with`, 없으면 `without`, 한글이 아니면 `with(without)`. */
export function josa(word: string, with_: string, without: string): string {
  const cp = word.length ? word.codePointAt(word.length - 1)! : 0;
  if (!word.length || cp < 0xac00 || cp > 0xd7a3) return `${word}${with_}(${without})`;
  return (cp - 0xac00) % 28 === 0 ? word + without : word + with_;
}
/** `apperr.NotFound(what)` 의 문장. */
export const notFound = (what: keyof typeof NOT_FOUND_NOUN): string => josa(NOT_FOUND_NOUN[what], "을", "를") + " 찾을 수 없습니다";

/** `apperr.Validation` 의 고정 detail — 필드별 문장은 `errors[]` 에 실린다. */
export const VALIDATION_DETAIL = "입력값을 확인해 주세요";

/**
 * 서버 문장 표 — 키는 handlers.ts 가 쓰는 이름, 값은 서버 소스의 리터럴과 그 자리.
 * `%d`·`%s`·이어 붙이는 자리는 `text` 를 앞부분까지만 적고 handlers 가 뒤를 붙인다.
 */
export const SERVER = {
  // ── auth (internal/auth/auth.go · httpapi/principal.go) ──
  login_required: { text: "로그인이 필요합니다", at: "internal/httpapi/principal.go" },
  not_member: { text: "이 워크스페이스의 멤버가 아닙니다", at: "internal/httpapi/principal.go" },
  admin_only: { text: "소유자·관리자만 할 수 있습니다", at: "internal/httpapi/principal.go" },
  name_1_80: { text: "이름은 1~80자로 입력해 주세요", at: "internal/auth/auth.go" },
  email_format: { text: "이메일 주소 형식이 아닙니다", at: "internal/auth/auth.go" },
  password_min: { text: "비밀번호는 8자 이상이어야 합니다", at: "internal/auth/auth.go" },
  email_taken: { text: "이미 가입된 이메일입니다 — 로그인해 주세요", at: "internal/auth/auth.go" },
  account_not_found: { text: "이 이메일로 가입된 계정이 없습니다", at: "internal/auth/auth.go" },
  password_mismatch: { text: "비밀번호가 맞지 않습니다", at: "internal/auth/auth.go" },
  invite_revoked: { text: "취소된 초대입니다 — 새 초대를 받아 주세요", at: "internal/auth/auth.go" },
  invite_expired: { text: "초대 링크가 만료되었습니다 — 새 초대를 받아 주세요", at: "internal/auth/auth.go" },
  // ── 연결 코드 (internal/runtimes/runtimes.go) ──
  pairing_expired: { text: "연결 코드가 만료되었습니다 — 새 코드를 만들어 주세요", at: "internal/runtimes/runtimes.go" },
  // ── 에이전트·프로파일 (internal/agents/agents.go) ──
  agent_name_1_40: { text: "이름은 1~40자로 입력해 주세요", at: "internal/agents/agents.go" },
  agent_name_taken: { text: "같은 이름의 에이전트가 있습니다 — 다른 이름을 골라 주세요", at: "internal/agents/agents.go" },
  runtime_kind_enum: { text: "지금은 Claude Code 와 Hermes 만 고를 수 있습니다", at: "internal/agents/agents.go" },
  model_required: { text: "모델을 골라 주세요", at: "internal/agents/agents.go" },
  profile_name_taken: { text: "이 에이전트에 같은 이름의 프로파일이 있습니다", at: "internal/agents/agents.go" },
  fallback_other_profile: { text: "이 에이전트의 다른 프로파일만 대체 프로파일로 고를 수 있습니다", at: "internal/agents/agents.go" },
  last_default: { text: "기본 프로파일은 비울 수 없습니다 — 다른 프로파일을 먼저 기본으로 지정해 주세요", at: "internal/agents/agents.go" },
  // ── 세션 만들기 (internal/sessions/sessions.go) ──
  title_1_200: { text: "제목은 1~200자로 입력해 주세요", at: "internal/sessions/sessions.go" },
  goal_required: { text: "목표를 입력해 주세요", at: "internal/sessions/sessions.go" },
  participants_required: { text: "에이전트를 한 명 이상 초대해 주세요", at: "internal/sessions/sessions.go" },
  container_unsupported: { text: "컨테이너 격리는 아직 지원하지 않습니다", at: "internal/sessions/sessions.go" },
  no_runtime: { text: "연결된 컴퓨터가 없습니다 — 먼저 컴퓨터를 연결해 주세요", at: "internal/sessions/sessions.go" },
  // 템플릿 매핑 사유(`AgentTemplate.mapping.reason`) — S-67 의 sink 밖이라 서버가 아직 옛말이다. 목은 서버를 따른다.
  template_unmapped: { text: "감지된 런타임이 없습니다 — 먼저 컴퓨터를 연결하세요", at: "internal/agents/templates.go" },
  template_fallback_mid: { text: " 가 없어 ", at: "internal/agents/templates.go" },
  template_fallback_tail: { text: " 로 매핑했습니다", at: "internal/agents/templates.go" },
  agent_not_in_workspace: { text: "이 워크스페이스의 에이전트가 아닙니다", at: "internal/sessions/sessions.go" },
  session_started: { text: "세션을 시작했습니다. 목표: ", at: "internal/sessions/sessions.go" },
  // ── 메시지 (internal/httpapi/handlers_sessions.go · server.go) ──
  content_required: { text: "내용을 입력해 주세요", at: "internal/httpapi/handlers_sessions.go" },
  reply_target_missing: { text: "답글 대상 메시지가 이 세션에 없습니다", at: "internal/httpapi/handlers_sessions.go" },
  idempotency_key_required: { text: "같은 요청을 구분할 키가 빠졌습니다 — 화면을 새로고침한 뒤 다시 시도해 주세요", at: "internal/httpapi/server.go" },
  // ── 작업 줄기 (internal/httpapi/handlers_lanes.go · handlers_lanes_p3.go) ──
  lane_control: { text: "작업 줄기는 이 세션의 Director 나 deputy 만 중단할 수 있습니다", at: "internal/httpapi/handlers_lanes.go" },
  lane_not_cancellable: { text: "진행 중이거나 대기 중인 작업 줄기만 중단할 수 있습니다", at: "internal/httpapi/handlers_lanes.go" },
  new_instruction_required: { text: "새 지시를 적어 주세요", at: "internal/httpapi/handlers_lanes_p3.go" },
  lane_not_restartable: { text: "이 작업 줄기는 다시 지시할 수 없습니다 (현재 상태: ", at: "internal/httpapi/handlers_lanes_p3.go" },
  // ── 세션 제어 (internal/httpapi/handlers_sessions_p3.go · sessions/pause.go · sessions/budget.go) ──
  director_required: { text: "Director 권한이 필요합니다", at: "internal/httpapi/handlers_sessions_p3.go" },
  pause_only_active: { text: "진행 중인 세션만 일시정지할 수 있습니다 (현재 상태: ", at: "internal/httpapi/handlers_sessions_p3.go" },
  resume_only_paused: { text: "일시정지된 세션만 재개할 수 있습니다 (현재 상태: ", at: "internal/httpapi/handlers_sessions_p3.go" },
  cancel_only_live: { text: "진행 중이거나 일시정지된 세션만 종료할 수 있습니다 (현재 상태: ", at: "internal/httpapi/handlers_sessions_p3.go" },
  resume_offline_hint: { text: "컴퓨터 연결이 끊겼습니다 — 컴퓨터를 다시 연결하거나, 다른 컴퓨터로 옮기거나, 세션을 종료해 주세요", at: "internal/sessions/pause.go" },
  budget_too_low: { text: "이미 $%.2f를 썼습니다 — 새 상한은 그보다 커야 합니다", at: "internal/sessions/budget.go" },
  director_changed: { text: "Director가 교체되었습니다.", at: "internal/httpapi/handlers_sessions_p3.go" },
  new_director_not_member: { text: "워크스페이스 멤버가 아닙니다", at: "internal/httpapi/handlers_sessions_p3.go" },
  // ── 종료 (internal/httpapi/handlers_completion.go) ──
  running_lanes_confirm: { text: "진행 중인 작업 줄기가 있습니다 — 그래도 끝내려면 확인 후 다시 요청해 주세요", at: "internal/httpapi/handlers_completion.go" },
  // ── HITL (internal/httpapi/handlers_hitl.go) ──
  not_approver: { text: "이 요청에 답할 권한이 없습니다", at: "internal/httpapi/handlers_hitl.go" },
  deputy_not_yet: { text: "Director 응답 대기 중 · %s부터 승인 가능", at: "internal/httpapi/handlers_hitl.go" },
  // ── 참여자 (internal/httpapi/handlers_participants.go · tasks/cancel.go) ──
  participant_agent_missing: { text: "그런 에이전트가 없습니다", at: "internal/httpapi/handlers_participants.go" },
  already_participant: { text: "이미 참여 중인 에이전트입니다", at: "internal/httpapi/handlers_participants.go" },
  profile_not_of_agent: { text: "이 에이전트의 프로파일이 아닙니다", at: "internal/httpapi/handlers_participants.go" },
  assignee_participant: { text: "담당 에이전트는 뺄 수 없습니다 — 먼저 다른 에이전트를 담당으로 지정해 주세요", at: "internal/httpapi/handlers_participants.go" },
  running_lanes_participant: { text: "진행 중인 작업 줄기가 있습니다 — 먼저 끝내거나 중단해 주세요", at: "internal/httpapi/handlers_participants.go" },
  participant_joined: { text: " 세션에 참여했습니다.", at: "internal/httpapi/handlers_participants.go" },
  participant_removed: { text: " 세션에서 제외되었습니다.", at: "internal/httpapi/handlers_participants.go" },
  not_invitable_nobody: { text: "이 에이전트는 응답 대상이 「아무도 아님」이라 초대할 수 없습니다", at: "internal/tasks/cancel.go" },
  // ── 라우터 미리보기 경고 (internal/router/rules.go) — `TriggerPreview.warnings[].message` ──
  warn_not_participant: { text: "은(는) 이 세션 참여자가 아닙니다", at: "internal/router/rules.go" },
  warn_agent_disabled: { text: " 응답 대상이 「아무도 아님」으로 꺼져 있어 깨우지 않습니다", at: "internal/router/rules.go" },
  // ── 컴퓨터·재바인딩 (internal/runtimes/candidates.go · offline.go) ──
  isolation_enum: { text: "격리 방식은 worktree · container · none 중 하나여야 합니다", at: "internal/runtimes/candidates.go" },
  runtime_offline_repo_check: { text: "이 컴퓨터의 데몬이 연결돼 있지 않습니다 — 저장소를 확인할 수 없습니다", at: "internal/httpapi/handlers_p4.go" },
  session_not_paused_offline: { text: "컴퓨터 연결이 끊겨 일시정지된 세션만 다른 컴퓨터로 옮길 수 있습니다", at: "internal/runtimes/offline.go" },
  acknowledge_loss_required: { text: "워크트리 격리에서는 끊긴 컴퓨터에 남은 커밋을 잃습니다 — 경고를 확인한 뒤 진행해 주세요", at: "internal/runtimes/offline.go" },
  not_a_candidate: { text: "이 컴퓨터로는 옮길 수 없습니다 — 같은 저장소가 없거나 연결이 끊겨 있습니다", at: "internal/runtimes/offline.go" },
  runtime_has_active_sessions: { text: "이 컴퓨터를 쓰는 중인 세션이 %d개 있습니다 — 먼저 다른 컴퓨터로 옮기거나 세션을 종료해 주세요", at: "internal/runtimes/offline.go" },
  // ── 워크스페이스 설정 (internal/httpapi/handlers_settings.go) — S14 (T-W6) ──
  loop_limit_range: { text: "1~%d 사이여야 합니다", at: "internal/httpapi/handlers_settings.go" },
  retention_min: { text: "0 이상이어야 합니다", at: "internal/httpapi/handlers_settings.go" },
  quota_positive: { text: "0보다 커야 합니다", at: "internal/httpapi/handlers_settings.go" },
  isolation_unknown: { text: "알 수 없는 격리 방식입니다", at: "internal/httpapi/handlers_settings.go" },
  // ── 초대 (internal/auth/auth.go) — S14 멤버 탭 ──
  invite_owner_role: { text: "소유자 역할은 초대로 줄 수 없습니다", at: "internal/auth/auth.go" },
  invite_expiry_max: { text: "초대 유효 기간은 최대 720시간입니다", at: "internal/auth/auth.go" },
  // ── 워크스페이스 설정 보안 탭 (internal/httpapi/handlers_settings.go) — S-70(T-S12 #200): 본문에 `task_event_masking` 이 있으면
  //    owner 아닌 admin 은 403 `owner_required`, 거절은 통째(같은 본문의 다른 칸도 적용 안 됨). ──
  masking_owner_required: { text: "활동 기록 마스킹은 워크스페이스 소유자만 바꿀 수 있습니다", at: "internal/httpapi/handlers_settings.go" },
  // ── 관측 지표 (internal/httpapi/handlers_metrics.go) — `window` 가 ISO 8601 기간이 아니면 422 ──
  metrics_window_format: { text: "집계 기간은 P30D · P2W · PT12H 같은 기간 표기로 적어 주세요", at: "internal/httpapi/handlers_metrics.go" },
  // ── 시험 대화 (internal/testchat · httpapi/handlers_testchat.go) — T-S12 #200 ──
  test_chat_no_default_profile: { text: "이 에이전트에 기본 프로파일이 없습니다 — 프로파일 하나를 기본으로 지정한 뒤 다시 시도해 주세요", at: "internal/testchat/testchat.go" },
  test_chat_agent_archived: { text: "보관된 에이전트와는 시험 대화를 할 수 없습니다", at: "internal/testchat/testchat.go" },
  test_chat_runtime_offline: { text: "선택한 컴퓨터가 연결돼 있지 않습니다 — 컴퓨터를 켜고 다시 연결하거나 다른 컴퓨터를 골라 주세요", at: "internal/testchat/testchat.go" },
  test_chat_no_online_runtime: { text: "이 프로파일을 실행할 수 있는 연결된 컴퓨터가 없습니다 — 컴퓨터를 연결하거나 다른 프로파일로 시험해 주세요", at: "internal/testchat/testchat.go" },
  test_chat_closed: { text: "이미 끝난 시험 대화입니다 — 새 시험 대화를 시작해 주세요", at: "internal/testchat/testchat.go" },
  test_chat_turn_in_progress: { text: "에이전트가 아직 답하는 중입니다 — 답이 오면 다음 메시지를 보낼 수 있습니다", at: "internal/testchat/testchat.go" },
  // 닫을 때 아직 컴퓨터에 넘기지 못한(queued) 턴 — 서버가 빈 agent 턴에 이 `error` 를 붙인다.
  test_chat_closed_before_answer: { text: "답을 받기 전에 시험 대화를 끝냈습니다", at: "internal/testchat/testchat.go" },
  // 진행 중(dispatched 이상) 턴이 있는 채팅을 닫으면 cancel → 데몬 finish(failed/cancelled) → `FailureText` 의 이 문장.
  test_chat_cancelled: { text: "사람이 중단했습니다", at: "internal/testchat/daemon.go" },
  test_chat_not_owner: { text: "이 시험 대화를 연 사람만 볼 수 있습니다", at: "internal/httpapi/handlers_testchat.go" },
  // 404 — 서버는 `apperr.NotFoundNouns` 밖에서 조립한다(handlers_testchat.go `testChatNotFound`, 이유는 그 주석). 서버가 표로
  // 옮기면 이 항목은 빨개지고 `NOT_FOUND_NOUN.test_chat` 으로 옮긴다.
  test_chat_not_found: { text: "시험 대화를 찾을 수 없습니다", at: "internal/httpapi/handlers_testchat.go" },
  test_chat_content_required: { text: "보낼 메시지를 적어 주세요", at: "internal/httpapi/handlers_testchat.go" },
  // ── 멤버 역할·제거 (internal/auth/members.go · httpapi/handlers_members.go) — T-S14 #209. 판정은 서버의 순수 함수
  //    PlanRoleChange · PlanRemoval 그대로: 소유자 층(대상이 소유자 **또는** 새 역할이 소유자)은 소유자만(403 owner_only),
  //    마지막 소유자는 강등·제거 불가(409 last_owner), 그 멤버가 Director 인 끝나지 않은 세션(draft·active·paused·completing)이
  //    있으면 제거 불가(409 member_is_director, %d 개). ──
  owner_only_role: { text: "소유자 역할을 주거나 거두는 것은 소유자만 할 수 있습니다", at: "internal/auth/members.go" },
  last_owner_demote: { text: "마지막 소유자는 강등할 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요", at: "internal/auth/members.go" },
  owner_only_remove: { text: "소유자를 내보내는 것은 소유자만 할 수 있습니다", at: "internal/auth/members.go" },
  last_owner_remove: { text: "마지막 소유자는 내보낼 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요", at: "internal/auth/members.go" },
  member_is_director: { text: "이 멤버가 Director 인 진행 중 세션이 %d개 있습니다 — 먼저 그 세션의 Director 를 교체해 주세요", at: "internal/auth/members.go" },
  role_enum: { text: "역할은 소유자 · 관리자 · 멤버 중 하나여야 합니다", at: "internal/httpapi/handlers_members.go" },
  // ── deleteSession (T-S17 #220 · internal/sessions/delete.go) — 순서 404 → 403 → 409 session_active → 409 workdir_unmerged
  delete_forbidden: { text: "Director 나 소유자·관리자만 삭제할 수 있습니다", at: "internal/sessions/delete.go" },
  session_active: { text: "진행 중인 세션은 먼저 종료하세요", at: "internal/sessions/delete.go" },
  workdir_unmerged: { text: "미병합 커밋이나 미커밋 변경이 남은 작업 폴더가 있어 삭제할 수 없습니다 — 먼저 병합하거나 정리해 주세요", at: "internal/sessions/delete.go" },
  // ── 리뷰어 검사 (T-S18 #233 · internal/sessions/reviewer.go · handlers_sessions_p3.go) — 계약 #232 v0.1.4
  reviewer_required: { text: "「검토 승인」에는 리뷰어를 참여자 중에서 골라 주세요 — 리뷰어가 없으면 아무도 승인할 수 없어 세션이 끝나지 않습니다", at: "internal/sessions/reviewer.go" },
  reviewer_not_participant: { text: "리뷰어는 이 세션의 참여자 중에서 골라 주세요", at: "internal/sessions/reviewer.go" },
  submitter_not_participant: { text: "제출자는 이 세션의 참여자 중에서 골라 주세요", at: "internal/sessions/reviewer.go" },
  condition_immutable: { text: "끝났거나 끝나는 중인 세션의 종료 조건은 바꿀 수 없습니다", at: "internal/httpapi/handlers_sessions_p3.go" },
  // 404 — 서버는 `NotFoundNouns` 밖에서 조립한다(handlers_members.go `memberNotFound`, 이유는 test_chat_not_found 와 같다).
  // 서버가 표로 옮기면 이 항목은 빨개지고 `NOT_FOUND_NOUN.member` 로 옮긴다.
  member_not_found: { text: "멤버를 찾을 수 없습니다", at: "internal/httpapi/handlers_members.go" },
  // ── 알림 설정(개인) (internal/auth/notifications.go) — T-S14 #209. 부분 갱신(빠진 키는 저장값 유지), enum 밖 422. ──
  subscription_enum: { text: "구독 기본값은 전부 · 사람 확인만 · 종료만 중 하나여야 합니다", at: "internal/auth/notifications.go" },
  // ── updateSession (internal/httpapi/handlers_sessions_p3.go) — T-W15 가 목에 PATCH /sessions/{id} 를 처음 만들었다. 시작 뒤 못 바꾸는 두 칸(422 immutable). ──
  isolation_immutable: { text: "격리 방식은 시작 전에만 바꿀 수 있습니다", at: "internal/httpapi/handlers_sessions_p3.go" },
  runtime_immutable: { text: "컴퓨터는 시작 전에만 바꿀 수 있습니다", at: "internal/httpapi/handlers_sessions_p3.go" },
} as const satisfies Record<string, ServerSentence>;

/**
 * 목에만 있는 op 의 문장 — **서버가 아직 안 만든 op**(T-W13 시점: `deleteSession`, 계약 PR #218 · 서버는 T-S17 이 동시에 만든다).
 * `SERVER` 에 넣지 않는 이유는 대조할 정답이 없어서다. 규칙은 T-W6·T-W12 때와 같다: §8.4 의 말, `Problem.detail` 한 문장,
 * 계약 description 이 문장을 못박았으면 **그대로**(`session_active`), 서버가 만들면 `SERVER` 로 옮기며 `at` 을 채운다.
 * `server-wording.test.ts` (g) 가 "그 op 이 정말 미구현인가"(`func (s *Server) DeleteSession(` 부재)를 재므로 T-S17 이 머지되면
 * 이 표는 빨개진다 — 그때 T-S17 의 문장으로 옮긴다.
 */
/**
 * 서버가 **아직 dev 에 안 올린** 검증의 문장 — T-W15 시점: createSession/updateSession 의 **리뷰어 검사**(계약 PR #232 v0.1.4, `errors[].code`
 * `reviewer_required` · `reviewer_not_participant`)와 **끝난 세션의 조건 수정 거절**(422 immutable). 서버는 T-S18(PR #233, 열림)이 동시에
 * 만들었고 아래 문장은 **그 PR 의 `sessions.ValidateReviewers` · `handlers_sessions_p3.go` 리터럴 그대로**다 — 머지되면 `SERVER` 로
 * 옮기며 `at` 을 채운다(`server-wording.test.ts` (h) 가 dev 에 리터럴이 생기는 순간부터 글자 단위로 대조한다).
 */
/**
 * T-W16 시점: **빈 턴 행의 문장**(PRD FR-7.2 v0.18 "판정과 기록" — `payload.args.note`). 서버 T-S19 가 finish 에서 남기고, 문장은 PRD 가
 * 못박았다("아무것도 하지 않고 턴을 끝냈습니다"). 서버가 머지되면 `SERVER` 로 옮기며 `at` 을 채운다 — `server-wording.test.ts` (i) 가 dev 에
 * 그 리터럴이 오르는 순간부터 글자 단위로 대조한다. 화면 쪽 같은 문장은 `lib/wording.ts` `EMPTY_TURN.note`(note 가 없을 때의 폴백).
 */
export const MOCK_ONLY = {
  empty_turn_note: "아무것도 하지 않고 턴을 끝냈습니다",
} as const satisfies Record<string, string>;

/**
 * 관측 지표 10개의 정의 — 서버 `internal/metrics/metrics.go` 의 `Defs` 표(PRD §11 열 순서)를 **그대로** 옮긴 것.
 * `label`·`note` 는 S14 「대시보드」에 그대로 보이는 문장이라(§8.4) 서버가 정하고, `server-wording.test.ts` 가 Go 소스를 파싱해
 * 항목 단위(key·unit·target·target_op·label·note)로 대조한다 — 서버가 한 글자라도 바꾸면 여기가 빨개진다.
 */
export type MetricDef = Pick<Metric, "key" | "unit" | "target" | "target_op" | "label" | "note">;
export const METRIC_DEFS: readonly MetricDef[] = [
  { key: "f1_minutes", unit: "minutes", target: 15, target_op: "lt",
    label: "컴퓨터 연결부터 첫 세션 완료까지 걸린 시간",
    note: "사용자별로 첫 컴퓨터가 연결된 시각부터 그 사용자가 Director 인 첫 완료 세션이 끝난 시각까지, 그 중앙값(분). 표본 수는 그런 사용자 수." },
  { key: "auto_complete_rate", unit: "ratio", target: 0.6, target_op: "gt",
    label: "세션이 저절로 끝난 비율",
    note: "완료된 세션 중 Director 가 직접 끝내지 않고 종료 조건으로 끝난 비율." },
  { key: "hitl_response_minutes", unit: "minutes", target: 30, target_op: "lt",
    label: "확인 요청에 사람이 답하기까지 걸린 시간",
    note: "확인 요청이 만들어진 때부터 사람이 답한 때까지, 그 중앙값(분). 기한이 지나 자동으로 진행된 요청은 빼고 센다." },
  { key: "delegation_autonomous_rate", unit: "ratio", target: 0.7, target_op: "gt",
    label: "에이전트 사이의 위임이 사람 개입 없이 처리된 비율",
    note: "다른 에이전트가 넘긴 할 일 중 확인 요청도 막힘도 없이 완료된 비율." },
  { key: "parallel_wallclock_reduction", unit: "ratio", target: 0.4, target_op: "gt",
    label: "여러 작업 줄기를 함께 돌려 줄어든 시간의 비율",
    note: "작업 줄기가 둘 이상인 완료 세션에서, 세션 시작부터 완료까지 걸린 시간이 각 할 일에 걸린 시간의 합보다 얼마나 짧았는지(1 - 전체 시간 ÷ 합)의 평균. 전체 시간에는 사람을 기다린 시간(확인 요청)도 들어간다. 표본 수는 세션 수." },
  { key: "task_success_rate_by_runtime", unit: "ratio", target: 0.85, target_op: "gt",
    label: "컴퓨터 종류별 할 일 성공률",
    note: "컴퓨터 종류별로 완료된 할 일 ÷ (완료 + 실패). 종류별 값과 목표는 따로 나눠 준다." },
  { key: "duplicate_after_resume_rate", unit: "ratio", target: 0.01, target_op: "lt",
    label: "다시 이어 간 뒤 같은 메시지를 두 번 올린 비율",
    note: "두 번째 이상 실행된 할 일 중 같은 내용의 메시지가 두 번 올라간 것이 관측된 비율. 멱등키가 아니라 내용이 같은지로 세므로, 에이전트가 같은 말을 두 번 한 경우도 함께 잡힙니다." },
  { key: "resume_success_rate", unit: "ratio", target: 0.9, target_op: "gt",
    label: "다시 이어 갈 때 이전 대화를 그대로 이어받은 비율",
    note: "이전 대화를 이어받으려 한 실행 중 실제로 이어받은(처음부터 다시 시작하지 않은) 비율." },
  { key: "blocked_response_minutes", unit: "minutes", target: 5, target_op: "lt",
    label: "막힌 질문에 답이 닿기까지 걸린 시간",
    note: "작업 줄기가 막혀 질문을 올린 때부터 그 질문에 첫 답글이 달린 때까지, 그 중앙값(분)." },
  { key: "weekly_active_sessions", unit: "count", target: 5, target_op: "gt",
    label: "이번 주에 움직인 세션 수",
    note: "최근 7일 안에 할 일이 하나라도 돌아간 세션 수. 표본 수는 이 워크스페이스에서 할 일을 돌린 적 있는 세션 수." },
];

/**
 * 「관찰」 표 5행의 정의(key·label·note) — openapi 0.1.5 `getWorkspaceObservations` description 의 정의 1~5 를 옮긴 것(v1.1 K-18, T-W16).
 * `label` 은 PRD §11 관찰 행의 이름, `note` 는 계약 description 의 "세는 법" 문장을 §8.4 의 말로. 서버 T-S19 가 같은 표를 만들면
 * `METRIC_DEFS` 처럼 `server-wording.test.ts` 가 Go 소스를 파싱해 항목 단위로 대조한다 — 그때까지는 목에만 있다(MOCK_ONLY 규칙).
 * 순서는 계약 `rows` 의 표 순서(ObservationRow.key enum 순서).
 */
export type ObservationDef = Pick<ObservationRow, "key" | "label" | "note">;
export const OBSERVATION_DEFS: readonly ObservationDef[] = [
  { key: "chain_scale", label: "트리거 사슬 규모",
    note: "사람이 올린 메시지 하나가 다음 사람 메시지 전까지 만든 할 일의 수, 그 중앙값과 p95. 표본 수는 사람 메시지 수." },
  { key: "chain_depth", label: "트리거 사슬 깊이",
    note: "세션이 도달한 가장 깊은 인과 사슬 깊이(사람 메시지 → 에이전트 → 에이전트 …), 그 중앙값과 p95. 표본 수는 세션 수." },
  { key: "join_breadth", label: "합류 폭",
    note: "한 번의 위임에서 갈라진 자식 작업 줄기의 수, 그 중앙값과 p95. 표본 수는 합류 그룹 수." },
  { key: "routing_concentration", label: "라우팅 집중",
    note: "할 일을 만든 라우팅 규칙 번호의 분포. 값은 멘션 없이 담당 에이전트에게 간 비율(규칙 6·7 폴백). 표본 수는 라우팅된 메시지 수." },
  { key: "empty_turn_rate", label: "빈 턴 비율",
    note: "메시지 게시·플랫폼 조작·파일 편집이 하나도 없이 끝난 실행 ÷ 전체 완료 실행. 표본 수는 완료 실행 수. 활동 피드의 빈 턴 카드와 같은 판정." },
];

export type ServerKey = keyof typeof SERVER;
/** 문장만 — `W.no_runtime` 처럼 쓴다. */
export const W: { readonly [K in ServerKey]: (typeof SERVER)[K]["text"] } = Object.fromEntries(
  Object.entries(SERVER).map(([k, v]) => [k, v.text]),
) as { [K in ServerKey]: (typeof SERVER)[K]["text"] };

/** Go `fmt.Sprintf` 의 `%d`·`%s`·`%.2f` 한 자리를 채운다 — 서버 형식 문자열을 그대로 두고 쓰기 위해. */
export function fmt(text: string, ...args: (string | number)[]): string {
  let i = 0;
  return text.replace(/%(\.\d+f|d|s)/g, (_, spec: string) => {
    const a = args[i++];
    if (spec.endsWith("f")) return Number(a).toFixed(Number(spec.slice(1, -1)));
    return String(a);
  });
}
