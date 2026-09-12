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
 *   - 목에만 있는 경로(서버가 아직 안 만든 op)의 문장은 여기 넣지 않는다 — 대조할 정답이 없다.
 */

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
} as const satisfies Record<string, ServerSentence>;

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
