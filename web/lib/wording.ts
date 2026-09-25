/**
 * 화면 문구 한곳 — T-W13 의 S5 세션 카드 옵션(「…」)·삭제 다이얼로그 표에서 시작했다(그 표와 옛 세션 화면은 R1.5b 에서 지웠다 —
 * 옛 세션 상세 주소는 방으로 넘어가 닿는 자리가 없었다 — v0.3.0(R4)에서 그 넘김도 지웠다. 방 목록의 같은 자리는 ROOM_MENU·DELETE_ROOM_DIALOG).
 *
 * 왜 여기인가: 문구는 §8.4 의 말로 쓰고 `lib/wording.test.ts` 자물쇠가 잰다. 같은 사건을 두 자리(메뉴·다이얼로그·안내 줄)가
 * 다른 말로 부르지 않게 문장은 이 표에서만 나온다 — 컴포넌트는 이 표를 그린다.
 *
 * **서버 문장은 여기 없다.** `409 session_active`·`409 workdir_unmerged`·`403` 의 `Problem.detail` 은 서버(T-S17)가 쓰고 화면은
 * 그대로 보인다(`errorMessage`). 목이 흉내 내는 그 문장은 `lib/mock/wording.ts` 의 `MOCK_ONLY` 에 있고 T-S17 뒤 `SERVER` 로 옮긴다.
 */
import type { Workdir } from "@/lib/api/types";

/**
 * `completion_condition` 최상위의 결합 — `and`·`or`, 또는 **원자 하나**(`single`: 결합이 없다). 원자 하나를 `and` 로 뭉뚱그리지
 * 않는다(W-20, PR #234 NN4) — 요약이 "하나만 충족하면 끝" 을 붙일지는 or 에서만, 원자 하나에는 물을 것이 없다.
 */
export type TopOp = "and" | "or" | "single";

/** 작업 폴더 한 줄 — 경로 · 브랜치 · 사유(미병합/미커밋). `gc_blocked_reason` 이 없고 `dirty` 만 있으면 미커밋으로 본다. */
export function workdirBlockLabel(w: Pick<Workdir, "gc_blocked_reason" | "dirty" | "commits_ahead">): string {
  if (w.gc_blocked_reason === "unmerged_commits") return w.commits_ahead != null && w.commits_ahead > 0 ? `미병합 커밋 ${w.commits_ahead}개` : "미병합 커밋";
  if (w.gc_blocked_reason === "uncommitted_changes" || w.dirty) return "미커밋 변경";
  return "정리 필요";
}

// ── 종료 조건 — S6 6단계 · S7 진행률 · 조건 고치기(T-W15, S-84 · W-19, SCREEN §4.4 6단계 · §4.5 "종료 조건 진행률") ──
//
// Director 지적(2026-09-15): "복잡하고 종료 조건의 파악이 어렵다". 조건 종류는 계약 enum 그대로 넷이고 화면은 **사람 말**로 부른다.
// 같은 조건을 마법사·요약·진행률·다이얼로그가 다른 이름으로 부르지 않게 이름은 `conditionName` 하나에서만 나온다.

/** 계약 CompletionAtom.type → 화면의 말. `agent_approval` 은 리뷰어 이름이 있으면 "Lead 의 검토 승인", 없으면(아직 안 골랐거나 옛 세션) 일반형. */
export const CONDITION_NAME = {
  artifact_submitted: "아티팩트 제출",
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
  artifact_submitted: "제출자로 지정한 에이전트가 아티팩트를 제출하면 충족됩니다",
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
  submitter_default: "미션의 제출자 (기본) — 제출자가 바뀌면 따라갑니다",
  submitter_default_short: "미션의 제출자",
  reviewer: "리뷰어",
  reviewer_placeholder: "리뷰어를 고르세요",
  /** 안내만 — 막지 않는다(자기 것을 자기가 검토하지 않게). */
  reviewer_is_assignee: "제출자가 자기 결과를 검토하게 됩니다 — 다른 에이전트를 권합니다",
  /** 다음 단계·저장을 막는 사유(§8.5 — 근처에서 말한다). */
  need_one: "종료 조건을 하나 이상 고르세요",
  reviewer_required: "리뷰어를 고르세요 — 리뷰어가 없으면 아무도 승인할 수 없어 미션이 끝나지 않습니다",
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
  /**
   * 상단 한 줄 — "남은 것: <이름> N개 · <이름> N개 · 막힘 N개". 이름마다 자기 개수를 붙인다(W-20, PR #234 NN1 — 예전엔 "이름, 이름 2개" 로
   * 개수가 목록 뒤에 하나만 붙어 어순이 어색했다). 같은 이름이 둘이면(같은 리뷰어의 검토 둘) 묶어 "… 2개". OR 은 조건이 둘 이상일 때만
   * "하나만 충족하면 끝" 을 덧붙인다 — 원자 하나(`op: "single"`)는 결합이 없다.
   */
  summary: (remaining: string[], blocked: number, op: TopOp) => {
    const counts = new Map<string, number>();
    for (const name of remaining) counts.set(name, (counts.get(name) ?? 0) + 1);
    const parts = [...counts].map(([name, n]) => `${name} ${n}개`);
    if (blocked) parts.push(`막힘 ${blocked}개`);
    return `남은 것: ${parts.join(" · ")}${op === "or" && remaining.length + blocked > 1 ? " — 하나만 충족하면 끝" : ""}`;
  },
  summary_satisfied: "조건을 모두 충족했습니다 — 곧 완료됩니다",
  summary_completed: "미션이 끝났습니다",
  /** 막힌 조건이 있을 때 — 누가 고칠 수 있는지. */
  blocked_director: "조건을 고쳐야 미션이 끝날 수 있습니다",
  blocked_member: "Director 가 조건을 고쳐야 미션이 끝날 수 있습니다",
} as const;

/** `CompletionProgress.conditions[].blocked_reason` — ✗ 대신 이 문장을 보인다(계약 v0.1.4). */
export const BLOCKED_REASON: Record<string, string> = {
  reviewer_missing: "리뷰어가 지정되지 않아 아무도 승인할 수 없습니다",
  reviewer_not_participant: "리뷰어가 이 방의 참여자가 아니어서 승인할 수 없습니다",
  agent_archived: "리뷰어 에이전트가 보관되어 승인할 수 없습니다",
};
export function blockedReasonText(reason: string): string {
  return BLOCKED_REASON[reason] ?? "지금 구조상 충족될 수 없는 조건입니다";
}

/** 「조건 고치기」 다이얼로그(updateWork completion_condition — active·paused 에서도, Director). */
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
  room_get: "방 읽기",
  room_messages: "메시지 읽기",
  artifact_get: "아티팩트 읽기",
  message_post: "메시지 게시",
  status_set: "상태 알리기",
  decision_record: "결정 기록",
  lane_delegate: "위임",
  artifact_submit: "아티팩트 제출",
  review_approve: "검토 승인",
  review_reject: "검토 반려",
  hitl_ask: "사람에게 질문",
  hitl_approve_request: "완료 승인 요청",
  hitl_request_info: "사람에게 정보 요청",
  room_list: "다른 방 목록",
  room_read: "다른 방 읽기",
  work_propose: "미션 제안",
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
  reason_worker: "위임·검토 승인·완료 승인 요청·미션 제안은 Lead 의 일",
  reason_reviewer: "위임·완료 승인 요청·미션 제안은 Lead 의 일, 아티팩트 대신 검토 반려 사유를 남깁니다",
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
  /** 「다시 세기」 — 지표 표와 **같은 버튼**(두 op 을 함께 다시 부른다, V-1 #247 NN1). 관찰 표 머리에도 놓는다. */
  reload: "다시 세기",
  counting: "세는 중…",
  /** `routing_concentration.value` 옆 한 줄 — 값이 무엇의 비율인지(계약: 규칙 6·7 폴백 비율, 아래 하위 행은 규칙별 분포). V-1 #247 NN2. */
  routing_value_hint: "규칙 6·7 폴백 비율 — 아래는 규칙별 분포",
  /** 모르는 `breakdown[].kind` 의 꼬리 — 원시 값을 그대로 두고 "새 규칙" 임을 말한다(V-1 #247 NN4). */
  unknown_kind_tail: "(새 규칙)",
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
  // 모르는 값(계약이 규칙을 더했는데 화면이 아직 모를 때) — 원시 값 그대로 + "(새 규칙)" 꼬리. 숨기거나 지어내지 않는다.
  return what ? `${OBSERVATIONS.rule(kind)} · ${what}` : `${kind} ${OBSERVATIONS.unknown_kind_tail}`;
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

// ── 방(v0.19, T-R2-W1) — S5 방 목록 · S25 방 찾기 · S18 방 만들기(SCREEN §4.3·§4.4·§4.5) ──
//
// **수를 문장에 보간하지 않는다**(COMPONENTS §8.5 v0.19) — 「진행 중인 미션 **2**」·「미션 **2**개가 진행 중입니다」의 수는 슬롯이다.
// 그래서 수가 드는 문장은 `[앞, 뒤]` 두 토막(`Slotted`)으로 두고 화면이 `앞{수}뒤` 로 그린다(`<Slot>`). 문구 자물쇠가 두 토막을
// 그대로 잰다 — 템플릿 리터럴 안에 넣으면 그 문장이 자물쇠 밖으로 빠진다.
// **「멈춤」과 「일시정지」의 층**(§8.4 v0.19): 방 `blocked_reason` 은 「멈춤」이다. 이 표에 「일시정지」는 없다.

/** `앞{수}뒤` — 수 자리가 있는 문장의 두 토막. */
export type Slotted = readonly [head: string, tail: string];

/** 방 멈춤 배지의 라벨(COMPONENTS §9.5 `room` kind) — 사유별. `manual` 에 역할을 넣지 않는다(누가 멈췄는지는 방 배너가 이름으로). */
export const ROOM_BLOCKED_LABEL = {
  budget: "예산으로 멈춤",
  runtime_offline: "컴퓨터 연결 끊김으로 멈춤",
  loop: "루프 상한으로 멈춤",
  manual: "직접 멈춤",
} as const;

/** S5 카드 · S25 제어 · 빈 상태(SCREEN §4.3 · §4.4 · §7). */
export const ROOM_LIST = {
  new_room: "새 방",
  /** 안 읽음 배지 — 숫자만 있는 요소라 라벨을 단다(SCREEN §7 "숫자만 있는 요소에는 라벨"). */
  unread_label: ["안 읽은 메시지 ", "개"] as Slotted,
  works_active: ["진행 중인 미션 ", ""] as Slotted,
  works_none: "열린 미션 없음",
  /** 주의 배지 셋 — 각 수에 라벨(§4.3 "세 숫자에 각각 라벨"). 내 것만 센다. */
  attention_hitl: ["내가 답할 요청 ", ""] as Slotted,
  attention_blocked: ["막힘 ", ""] as Slotted,
  attention_failed: ["실패 ", ""] as Slotted,
  participants_label: ["참여자 ", "명"] as Slotted,
  // 한 글자 토막은 줄을 나눈다 — 한 줄에 두면 문구 자물쇠의 리터럴 스캐너(2자 이상)가 따옴표 짝을 잘못 맞춰 뒤 토막을 놓친다.
  more_participants: [
    "+",
    "",
  ] as Slotted,
  archived: "보관됨",
  /** S25 제어. */
  search_label: "방 찾기",
  search_placeholder: "방 이름·설명 검색",
  unread_only: "안 읽음만",
  participating: "내가 참여한 방만",
  include_archived: "보관 포함",
  sort_fixed: "정렬: 마지막 활동순",
  /** 「내가 참여한 방만」이 켜져 있어 안 보이는 공개 방(SCREEN §4.4 · SCR-A 막힘 5). 0 이면 그리지 않는다. */
  more_public: ["워크스페이스에 공개된 방이 ", "개 더 있습니다"] as Slotted,
  more_public_action: "「내가 참여한 방만」 끄기",
  /** 빈 상태(§4.3 · §7). */
  empty_title: "첫 방을 만들어 보세요",
  empty_examples: ["결제팀 — 결제 관련 논의와 작업", "인프라 — 배포·모니터링"],
  empty_no_computer: "컴퓨터를 연결하면 에이전트가 일을 시작할 수 있습니다",
  empty_no_computer_link: "컴퓨터 연결",
  /** 검색 결과 0(§4.4) — 〈말〉 자리는 검색어다. */
  no_match: [
    "「",
    "」에 걸리는 방이 없습니다",
  ] as Slotted,
  no_match_filters: "조건에 맞는 방이 없습니다",
  retry_with_archived: "보관 포함해서 다시 찾기",
  loading: "불러오는 중…",
} as const;

/** 카드 「…」 메뉴(SCREEN §4.3) — 보관·보관 해제·삭제. 삭제에는 「되돌릴 수 없음」을 붙인다(FR-2.4 [V19-C]). */
export const ROOM_MENU = {
  button: "방 옵션",
  archive: "보관",
  unarchive: "보관 해제",
  delete: "삭제",
  delete_tail: "되돌릴 수 없음",
  /** 비활성 사유 — 층을 적는다(§2.3 · §5 "Director만 가능" 이 아니라 누구의 일인지). */
  archive_role: "방장·부방장이나 워크스페이스 소유자·관리자만 보관할 수 있습니다",
  delete_role: "방장이나 워크스페이스 소유자·관리자만 삭제할 수 있습니다",
  /** 진행 중인 미션이 있을 때(§4.3 문장) — 수는 슬롯. */
  delete_works: ["미션 ", "개가 진행 중입니다 — 먼저 끝내거나 취소하세요"] as Slotted,
} as const;

/** 보관 확인(§4.3 · §5 "보관은 되돌릴 수 있다고 적는다"). */
export const ARCHIVE_DIALOG = {
  title: (name: string) => `「${name}」 방을 보관할까요?`,
  body: "새 대화와 새 미션만 막습니다. 메시지·미션·아티팩트는 그대로 남고 검색에도 걸립니다. 언제든 되돌릴 수 있습니다.",
  confirm: "보관",
  cancel: "취소",
  busy: "보관 중…",
} as const;

/** 방 삭제 확인(§4.3 · FR-2.6) — 사라지는 것을 나열한다. 작업 폴더는 지우는 목록에 넣지 않는다. */
export const DELETE_ROOM_DIALOG = {
  title: (name: string) => `「${name}」 방을 삭제할까요?`,
  loses: "메시지·미션·서브 미션·할 일·사람 확인 요청·활동 기록·아티팩트·결정 기록·비용 기록이 함께 사라집니다(워크스페이스 집계에서도 빠집니다). 활동 로그에는 방을 지웠다는 한 줄만 남습니다.",
  workdirs: "이 방의 작업 폴더 중 병합·정리된 것은 정리 대상으로 넘어갑니다.",
  irreversible: "되돌릴 수 없습니다.",
  confirm: "삭제",
  cancel: "취소",
  busy: "삭제 중…",
  workdirs_head: "삭제를 막은 작업 폴더:",
  workdirs_link: "작업 폴더 관리",
} as const;

/** 방이 지워진 뒤 S5 의 안내 한 줄 — 내가 지운 것과 다른 곳(S7)에서 지워진 것. */
export const ROOM_DELETED_NOTICE = {
  elsewhere: (name: string) => `「${name}」 방이 삭제되어 목록으로 돌아왔습니다.`,
  mine: (name: string) => `「${name}」 방을 삭제했습니다.`,
} as const;

/** S18 방 만들기(SCREEN §4.5). */
export const CREATE_ROOM = {
  title: "새 방",
  name: "이름",
  name_placeholder: "결제팀",
  description: "한 줄 설명(선택)",
  description_placeholder: "결제 관련 논의와 작업",
  name_required: "방 이름을 적어 주세요",
  duplicate: "같은 이름의 방이 이미 있습니다 — 작업 폴더 브랜치 이름이 헷갈릴 수 있습니다",
  settings_link: "방 설정",
  settings_later: "만든 뒤 바꿀 수 있습니다",
  /** ⓘ 한 줄의 뒤 절 — 앞 절은 워크스페이스 기본값에서 만든다(`roomDefaultsLine`). */
  info_tail: "방 설정에서 미리 바꿀 수 있습니다",
  runtime_first_run: "컴퓨터는 첫 실행 때 정해집니다",
  isolation: { none: "격리 없음", worktree: "워크트리로 나눔", container: "컨테이너로 나눔" },
  cancel: "취소",
  create: "만들기",
  busy: "만드는 중…",
} as const;

/**
 * S18 ⓘ 한 줄의 앞 절 — **워크스페이스 기본값을 읽어 쓴다**(§4.5 "문장을 고정하지 않는다"). 기본 격리는 `room_defaults.isolation_kind`,
 * 없으면 옛 `default_isolation`(서버 loadRoomDefaults 와 같은 순서). 설정을 못 읽었으면 계약 기본값(`none`).
 */
export function roomDefaultsLine(settings: { default_isolation?: string | null; room_defaults?: { isolation_kind?: string | null } | null } | null): string {
  const kind = (settings?.room_defaults?.isolation_kind ?? settings?.default_isolation ?? "none") as keyof typeof CREATE_ROOM.isolation;
  return `${CREATE_ROOM.isolation[kind] ?? CREATE_ROOM.isolation.none} · ${CREATE_ROOM.runtime_first_run}`;
}

// ── S7 방 화면 · S22 미션 패널(v0.19, T-R2-W2 — SCREEN §4.6 · §4.8 · §5 · §7) ──
//
// 층 분담(§8.4 v0.19): 방은 「멈춤」, 미션·서브 미션·할 일의 paused 는 「일시정지」. 수는 슬롯(`Slotted`) — 「미션 **2**개와 대화 전부」.
// 역할어: 방 역할은 한국어(방장·부방장), 미션 역할은 영어(Director·deputy) — 층을 함께 적는다.

/** 상단 머리 — 세 층 요약 · 「나에게 필요한 것」 · 액션. */
export const ROOM_HEAD = {
  back: "방 목록으로",
  loading: "불러오는 중…",
  /** 세 층 요약 — 가운뎃점으로 이은 세 수가 한 덩어리로 읽히지 않게 수마다 라벨(§4.6 · §7 「숫자만 있는 요소에는 라벨」). 0 인 층은 생략한다. */
  layers_label: "이 방의 미션·서브 미션·할 일 수",
  layer_works: ["미션 ", "개"] as Slotted,
  layer_lanes: ["서브 미션 ", "개"] as Slotted,
  layer_tasks: ["할 일 ", "개"] as Slotted,
  layer_works_none: "미션 없음",
  /** 이 방에서 내가 누를 수 있는 것의 수 — 중복 없이(§4.6). 0 이면 그리지 않는다. */
  needs_me: ["나에게 필요한 것 ", ""] as Slotted,
  participants: "참여자",
  settings: "방 설정",
  block: "이 방 멈춤",
  unblock: "멈춤 해제",
  more: "방 메뉴",
  summarize: "여기까지 정리",
  reads: "맥락 오간 기록",
  archive: "보관",
  unarchive: "보관 해제",
  delete: "삭제",
  delete_tail: "되돌릴 수 없음",
  leave: "이 방에서 나가기",
  /** 비활성 사유 — 층을 적는다(§2.3 · §5). */
  block_role: "방장·부방장이나 워크스페이스 소유자·관리자만 이 방을 멈출 수 있습니다",
  archived: "보관된 방입니다 — 먼저 보관을 해제하세요",
  leave_not_participant: "이 방의 참여자만 나갈 수 있습니다",
  skip_to_composer: "작성창으로 건너뛰기",
} as const;

/** 「이 방 멈춤」 확인(§4.6) — 수는 슬롯. 중단된 서브 미션은 「실패 · 사람이 중단」으로 남는다(내부 키를 문장에 넣지 않는다). */
export const BLOCK_DIALOG = {
  title: "이 방을 멈출까요?",
  turns: ["이 방의 진행 중인 턴 ", "개가 중단되고 새 트리거가 막힙니다."] as Slotted,
  works: ["미션 ", "개와 미션 밖 대화 전부가 멈춥니다."] as Slotted,
  resume: "중단된 서브 미션은 실패(사람이 중단)로 남고, 멈춤을 풀면 「다시 지시」로 이어갈 수 있습니다.",
  confirm: "이 방 멈춤",
  busy: "멈추는 중…",
  cancel: "취소",
} as const;

/** 「여기까지 정리」 범위 다이얼로그(§4.6 · FR-2.5). 「직접 고르기」(타임라인에서 시작·끝 집기)는 이 판에 없다. */
export const SUMMARIZE_DIALOG = {
  title: "여기까지 정리",
  range: "범위",
  days7: "최근 7일",
  days30: "최근 30일",
  preview: ["메시지 ", "건이 이 범위에 듭니다"] as Slotted,
  result: "요약은 방에 메시지로 남고 범위와 인용한 메시지가 함께 기록됩니다 — 요약이 틀리면 원문으로 내려갈 수 있습니다.",
  confirm: "정리",
  busy: "정리하는 중…",
  cancel: "취소",
  /** 「직접 고르기」 — 시작·끝 메시지를 타임라인에서 집는다(§4.6 「여기까지 정리」 범위 표, T-R2-W4b). */
  pick: "직접 고르기",
  pick_start: "타임라인에서 고르기",
  pick_again: "다시 고르기",
  pick_from: "시작",
  pick_to: "끝",
  pick_need: "시작과 끝 메시지를 타임라인에서 고르세요",
} as const;

/** 「직접 고르기」 중 타임라인 위 안내 줄과 메시지마다의 집기 단추. */
export const SUMMARY_PICK = {
  bar_from: "정리를 시작할 메시지를 누르세요",
  bar_to: "정리를 끝낼 메시지를 누르세요",
  here_from: "여기서 시작",
  here_to: "여기까지",
  cancel: "그만 고르기",
} as const;

/** 미션 칩 줄(COMPONENTS §9.1). 특수 칩 둘은 글자 그대로 `전체`·`미션 없음`. */
export const WORK_CHIPS = {
  group: "미션 거르개",
  all: "전체",
  none: "미션 없음",
  /** ⏳ 는 상태가 아니라 파생(열린 확인 요청) — 툴팁이 아니라 aria-label. */
  waiting: "사람 대기",
  past: ["지난 미션 ", "개"] as Slotted,
  paused: ["일시정지 ", ""] as Slotted,
  overflow: ["미션 ", "개 더"] as Slotted,
  new_work: "+ 새 미션",
  /** 「일시정지 N ▾」 펼침 한 줄 — 이름 · 사유 · 승인 권한자. */
  approver: "승인: ",
  /** 칩을 누르면 가운데·우열 두 곳이 바뀐다 — 조용한 안내(aria-live). */
  announce_all: "전체 미션을 봅니다",
  announce_none: "미션 없이 오간 대화만 봅니다",
  /** 받침에 따라 조사가 갈리지 않게 「〈이름〉 미션으로」(「미션」은 받침이 있어 늘 「으로」). */
  announce_work: (title: string) => `「${title}」 미션으로 걸렀습니다`,
  announce_lanes: ["서브 미션 ", "개"] as Slotted,
  announce_messages: ["메시지 ", "개"] as Slotted,
} as const;

/** 미션 `paused` 사유의 이름(미션 층 — 방 사유는 여기 오지 않는다, §4.6). */
export const WORK_PAUSE_LABEL = {
  budget: "미션 예산 초과",
  time: "시간 상한 도달",
  director: "Director 가 일시정지",
} as const;

/** 방 멈춤 배너(COMPONENTS §9.4) — 사유 문장 + 멈춘 수(슬롯) + 위임 줄. `role="alert"`. */
export const ROOM_BANNER = {
  budget: ["이 방의 예산 $", "을 넘겼습니다"] as Slotted,
  budget_plain: "이 방의 예산을 넘겼습니다",
  runtime_offline: [
    "컴퓨터 「",
    "」와 연결이 끊겼습니다",
  ] as Slotted,
  runtime_offline_plain: "컴퓨터와 연결이 끊겼습니다",
  loop: "에이전트 간 왕복이 방 상한에 닿았습니다",
  manual: [
    "「",
    "」님이 이 방을 멈췄습니다",
  ] as Slotted,
  manual_plain: "누군가 이 방을 멈췄습니다",
  stopped: ["미션 ", "개와 대화 전부가 멈췄습니다"] as Slotted,
  stopped_no_works: "미션 밖 대화 전부가 멈췄습니다",
  /** 내가 승인 권한자가 아닐 때 — 누가 언제부터(FR-2A.3). */
  waiting: ["「", "」의 승인을 기다립니다"] as Slotted,
  delegate: ["", "부터 다음 권한자가 답할 수 있습니다"] as Slotted,
  /** 다음 권한자를 서버가 알려 줄 때(0.2.8 `next_approver`·`next_approver_role`) — 「14:30부터 부방장 「서연」님이 답할 수 있습니다」. */
  delegate_at: ["", "부터 "] as Slotted,
  next_room_deputy: ["부방장 「", "」님이 답할 수 있습니다"] as Slotted,
  next_workspace_owner: ["워크스페이스 소유자 「", "」님이 답할 수 있습니다"] as Slotted,
  next_plain: ["「", "」님이 답할 수 있습니다"] as Slotted,
  approve: "계속 진행 승인",
  approve_where: "받은 요청에서 승인합니다",
  rebind: "컴퓨터 바꾸기",
  unblock: "멈춤 해제",
} as const;

/** 보관·감사 열람·컴퓨터 없음 배너(§4.6 · §2.4 · §7). */
export const ROOM_NOTICES = {
  archived: "보관된 방입니다 — 새 메시지·새 미션을 받지 않습니다",
  unarchive: "보관 해제",
  unarchive_role: "방장·부방장이나 워크스페이스 소유자·관리자만 보관을 해제할 수 있습니다",
  audit: "감사 목적으로 열람 중입니다 — 게시하려면 참여해야 합니다",
  no_computer: "연결된 컴퓨터가 없어 에이전트가 아직 일을 시작할 수 없습니다",
  no_computer_link: "컴퓨터 연결",
} as const;

/** 좌열 — 참여자 한 목록 + 서브 미션 보드(§4.6 좌열 · §5 사람 칩). */
export const ROOM_LEFT = {
  participants: "참여자",
  invite: "초대·퇴장",
  owner: "방장",
  deputy: "부방장",
  /** 이 방에 실행 중인 할 일이 없는데 working — 프로파일 자리를 바꿔 넣는다(더하지 않는다). 어느 방인지는 적지 않는다. */
  elsewhere: "다른 방에서 작업 중",
  alone: "아직 아무도 없습니다",
  alone_cta: "참여자 초대",
  board: "서브 미션 보드",
  board_empty: "아직 위임이 없습니다",
  board_empty_assignee: "담당 에이전트가 계획을 세우는 중입니다",
  board_empty_no_assignee: "이 미션에는 제출자가 없습니다 — 에이전트를 멘션해 시작하세요",
  board_empty_no_work: "에이전트를 부르면 여기에 나타납니다",
  /** 카드의 미션 라벨(§3.2) — 「미션 〈…〉」 또는 「미션 없음」. */
  work_label: (title: string) => `미션 「${title}」`,
  no_work_label: "미션 없음",
  /** `queued_reason` 4값의 사람 말(§4.6 표) — 상한 수는 슬롯. */
  queued_room_lanes: ["이 방의 동시 서브 미션 상한(", ")에 닿았습니다"] as Slotted,
  queued_room_lanes_plain: "이 방의 동시 서브 미션 상한에 닿았습니다",
  queued_agent_global: ["이 에이전트가 다른 방 일로 꽉 찼습니다(동시 ", "개)"] as Slotted,
  queued_agent_global_plain: "이 에이전트가 다른 방 일로 꽉 찼습니다",
  queued_runtime: "이 컴퓨터의 동시 상한에 닿았습니다",
  /** 워크트리 방인데 아직 컴퓨터가 정해지지 않았다(runtime_id null) — 저장소가 있는 컴퓨터가 붙기를 기다린다(T-S-wt #302). */
  queued_runtime_repo: "저장소가 있는 컴퓨터를 기다립니다",
  queued_workspace: "워크스페이스 동시 상한에 닿았습니다",
  /** `paused` 는 어느 층의 예산인가(§4.6 · §5 「멈춘 것은 층을 함께 적는다」). */
  paused_task: "⏸ 일시정지 · 할 일 예산",
  paused_work: "⏸ 일시정지 · 미션 예산",
  paused_room: "⏸ 멈춤 · 방 예산",
  paused_task_only: "이 승인은 이 할 일에만 적용됩니다",
  /** 접힌 묶음(done·failed)의 펼침 단추 — 라벨은 배지 말, 수는 슬롯. */
  fold_open: "펼치기",
  fold_close: "접기",
  cancel_confirm: "이 서브 미션을 중단합니다. 새 지시 없이 끝납니다.",
  cancel_confirm_done: "제출은 끝났습니다 — 아직 도는 실행만 멈춥니다(서브 미션은 끝난 채로 남습니다).",
  cancel_hold: "되돌리기 어려운 작업 중이면 최대 30초 보류 후 끝납니다.",
  cancel_yes: "중단",
  cancel_no: "취소",
  control_role: "그 미션의 Director·deputy 나 방장·부방장만 할 수 있습니다",
} as const;

/** 가운데 — 타임라인 · 페이지네이션 · 메시지 메뉴 · 빈 방 안내(§4.6 가운데 · §7). */
export const ROOM_CENTER = {
  timeline: "메시지 타임라인",
  load_older: "이전 대화 더 보기",
  to_latest: "최신으로",
  empty_title: "이제 무엇을 하나요?",
  empty_invite: "참여자 초대",
  empty_talk: "그냥 말 걸기",
  empty_open_work: "미션 열기",
  empty_settings: "방 설정",
  empty_messages: "@로 에이전트를 불러 시작하세요",
  empty_filtered: "이 거르개에 맞는 메시지가 없습니다",
  msg_menu: "메시지 메뉴",
  to_work: "이걸 미션으로",
  has_work: (title: string) => `이미 미션 「${title}」에 속한 메시지입니다`,
  /** 시스템 메시지(미션 열림·닫힘)의 칩 링크 — 그 미션 칩을 고른다. */
  open_work_chip: "이 미션으로 거르기",
  summary_of: (title: string) => `미션 「${title}」 요약`,
  summary_room: "여기까지 정리",
  typing: "입력 중…",
  writing: "작성 중…",
} as const;

/** 작성창 미션 선택기(COMPONENTS §9.2) — 열림 / 잠김 / 자동. 자동은 「자동: 」 접두로 열림과 갈린다. */
export const WORK_SELECTOR = {
  label: "귀속될 미션",
  none: "미션 없음",
  into: (title: string) => `이 메시지는 미션 「${title}」에 들어갑니다`,
  into_none: "미션 없음에 들어갑니다",
  auto_prefix: "자동: ",
  auto_tail: " — 바꾸려면 선택기를 누르세요",
  locked: (title: string) => `이 스레드는 미션 「${title}」의 것입니다`,
  /** 빈 방의 placeholder — 첫 지시 예시(§4.6, SCR-A P-3). 에이전트 이름은 이 방의 첫 참여 에이전트. */
  first_order: (agent: string) => `@${agent} 국내 B2B SaaS 결제 시장을 조사해서 보고서 10페이지로 정리해 줘`,
  placeholder: "메시지 — @로 에이전트를 부릅니다",
  archived: "보관된 방입니다 — 먼저 보관을 해제하세요",
  audit: "감사 목적으로 열람 중입니다 — 게시하려면 참여해야 합니다",
  closed_work: "끝난 미션입니다 — 미션 없이 보내거나 열린 미션을 고르세요",
} as const;

/** 우열 (가) 미션 칸 · (나) 방 전체 칸(§4.6 우열 · §4.8 S22). */
export const WORK_PANEL = {
  title: "미션",
  recent: (title: string) => `최근 활동: 「${title}」 · 다른 미션을 보려면 위 칩을 누르세요`,
  none_view: "이 방에서 미션 없이 오간 대화입니다",
  not_applicable: "미션이 없어 해당 없음",
  pick_first: "어느 미션인지 먼저 고르세요 — 위 칩에서 미션을 누르면 이 버튼이 켜집니다",
  no_end: "미션 없이 오간 대화에는 끝이 없습니다",
  no_works_title: "아직 연 미션이 없습니다.",
  no_works_body: "끝을 정해 추적하고 싶은 일이 생기면 미션을 엽니다",
  no_works_alt: "이미 오간 메시지에서 열려면 그 메시지의 「…」 → 「이걸 미션으로」",
  director: "Director",
  deputy: "deputy",
  goal: "미션 목표",
  progress: "종료 조건 진행률",
  cost: "미션 비용",
  cost_this: "이 미션 ",
  estimated: "추정",
  pause: "일시정지",
  resume: "재개",
  complete: "종료",
  cancel: "취소",
  not_director: (name: string) => `「${name}」님이 이 미션의 Director 입니다`,
  assignee: "제출자: ",
  no_assignee: "제출자 없음 — 종료 조건이 「Director 승인」 하나입니다",
  ended: "끝난 미션입니다",
  ended_completed: "완료",
  ended_cancelled: "취소됨",
  summary_link: "요약 메시지 보기",
  not_found: "이 방에 그 미션이 없습니다",
  back_to_room: "방 화면으로",
  paused_budget: ["이 미션의 예산 $", "을 넘겼습니다"] as Slotted,
  paused_budget_now: ["현재 $", ""] as Slotted,
  paused_time: "시간 상한에 도달했습니다",
  paused_director: "Director가 일시정지했습니다",
  approve: "계속 진행 승인",
} as const;

export const ROOM_PANEL = {
  title: "방 전체",
  count_artifacts: ["아티팩트 ", ""] as Slotted,
  count_decisions: ["결정 ", ""] as Slotted,
  count_cost: ["누적 $", ""] as Slotted,
  artifacts: "아티팩트",
  decisions: "결정 기록",
  room_cost: "방 누적 비용",
  not_sum: "미션 비용의 합이 아닙니다 — 미션 밖 대화 비용이 함께 듭니다",
  reads: "맥락 오간 기록",
  reads_link: "기록 보기",
  reads_empty: "이 방의 맥락이 오간 적이 없습니다",
  reads_out: ["읽음 ", ""] as Slotted,
  reads_in: ["읽힘 ", ""] as Slotted,
  settings: "방 설정 요약",
  settings_link: "방 설정",
  no_work_group: "미션 없음",
  more: ["더 보기 ", ""] as Slotted,
  artifacts_empty: "아직 제출된 아티팩트가 없습니다",
  decisions_empty: "아직 기록된 결정이 없습니다",
  from_hitl: "사람 확인",
  from_agent: "에이전트",
  row_computer: "컴퓨터",
  row_isolation: "격리",
  row_autonomy: "자율성",
  row_limits: "한도",
  row_director: "기본 Director",
  row_visibility: "공개 범위",
  computer_first_run: "첫 실행 때 정해집니다",
  no_budget: "예산 없음",
  no_time: "시간 제한 없음",
  concurrent_works: ["동시 미션 ", "개"] as Slotted,
  concurrent_lanes: ["동시 서브 미션 ", "개"] as Slotted,
  director_opener: "미션을 연 사람",
  visibility: { workspace: "워크스페이스 전체", invited: "초대된 사람만" },
  autonomy: { guided: "질문 기한이 지나면 계속 기다립니다", autonomous: "질문 기한이 지나면 제안한 기본값으로 진행합니다", supervised: "모든 위임을 Director가 먼저 승인합니다" },
} as const;

/**
 * 에이전트 메시지 세 층(PRD FR-3.1.2 · SCREEN §4.6 「에이전트 메시지 카드」 · COMPONENTS §9.6 Fold Row · §9.7 View Toggle, v0.19.3).
 * 대화(content)는 늘 보이고, 작업 내용(detail)·작업 과정(활동 피드)은 접힌 줄 하나로 시작한다. 수가 드는 말은 두 토막(Slotted)이다.
 */
export const MESSAGE_LAYERS = {
  detail: "작업 내용",
  process: "작업 과정",
  /** 화면이 나눈 경우(폴백)만 — 흐린 기울임. 에이전트가 나눈 것과 구분돼야 사람이 브리프를 의심할 수 있다. */
  auto_folded: "자동으로 접음",
  view_label: "보기",
  view_group: "타임라인 보기",
  view_conversation: "대화만",
  view_detail: "작업 내용 펼침",
  open_window: "새 창으로 보기",
  chars: ["", "자"] as Slotted,
  chars_man: ["", "만 자"] as Slotted,
  tables: ["표 ", "개"] as Slotted,
  failures: ["실패 ", ""] as Slotted,
  /** 작업 과정 요약 — 「활동 피드 없음」(SCREEN §7)과 같은 규약: 구조화 이벤트 미지원이면 그 사실을, 아니면 「대기 중」. */
  process_loading: "불러오는 중…",
  process_waiting: "대기 중…",
  process_unstructured: "도구 단위 기록 없음",
  process_no_actions: "도구·파일·플랫폼 조작 없음",
  duration_sec: ["", "초"] as Slotted,
  duration_min: ["", "분"] as Slotted,
  duration_hour: ["", "시간"] as Slotted,
  artifact: "아티팩트",
  artifact_version: ["v", ""] as Slotted,
  artifact_open: "열기",
} as const;

/**
 * 타임라인 대화 배치(PRD FR-3.1.3 · SCREEN §4.6 「대화 배치」 · COMPONENTS §9.8) — 머리 한 줄 「작성자 ‹종류› → 받는 쪽」의 말.
 * 종류 라벨은 서버 칸으로 판정한 것만 쓴다(lib/conversation.ts). 수가 드는 말은 두 토막.
 */
export const CONVERSATION = {
  kind: {
    order: "지시",
    delegate: "위임",
    request: "요청",
    report: "보고",
    question: "질문",
    answer: "답",
    note: "메모",
    summary: "요약",
  },
  /** 받는 쪽이 없을 때 · @all · /note. */
  to_room: "방 전체",
  to_all: "모두",
  to_record: "기록만",
  more: ["외 ", "명"] as Slotted,
  /** 「↩ 〈요청자〉의 「첫 줄」에 대한 보고」 — 요청자 · 인용은 화면이 끼운다. */
  report_of_head: "↩ ",
  report_of_mid: "의 「",
  report_of_tail: "」에 대한 보고",
  /** aria-label 「〈작성자〉: 〈받는 쪽〉에게 〈종류〉」 — 받는 쪽이 방 전체면 「방 전체에」. */
  aria_to: "에게",
  aria_room: "방 전체에",
} as const;

/**
 * 작업 과정 요약의 동작 이름 — task_event `class/verb`(contracts/task_event.schema.json) → 사람 말 + 수. 편집은 **파일 수**(같은 파일을
 * 여러 번 고쳐도 하나), 나머지는 횟수다. 여기 없는 동작(발화·사고·사용량·턴 생명주기)은 요약에서 세지 않는다 — 펼친 피드에는 그대로 있다.
 */
export const PROCESS_ACTION: Record<string, Slotted> = {
  "tool/edit_file": ["파일 ", "개 편집"],
  "tool/run_shell": ["셸 명령 ", "회"],
  "tool/read": ["파일 읽기 ", "회"],
  "tool/search": ["검색 ", "회"],
  "tool/use_tool": ["도구 ", "회"],
  "tool/permission": ["권한 요청 ", "회"],
  "plan/update": ["계획 갱신 ", "회"],
  "status/post_message": ["메시지 게시 ", "회"],
  "status/delegate": ["위임 ", "회"],
  "status/set_status": ["상태 보고 ", "회"],
  "status/submit_artifact": ["아티팩트 제출 ", "건"],
  "status/record_decision": ["결정 기록 ", "건"],
  "status/hitl": ["사람 확인 요청 ", "건"],
  "status/review": ["검토 ", "건"],
};

/** 좁은 화면(≤1100px) 열 탭 넷(§4.8). */
export const ROOM_TABS = {
  label: "열 전환",
  timeline: "타임라인",
  board: "보드",
  work: "미션",
  room: "방",
} as const;

export type RoomGate = { ok: true } | { ok: false; reason: string; count?: undefined } | { ok: false; reason: Slotted; count: number };
export interface RoomGateMe {
  /** 워크스페이스 owner·admin(AuthContext `canManage`). */
  canManage: boolean;
}
/** 보관 가부 — 방장·부방장·ws owner·admin(서버 `rooms.Decide` ActArchive). 진행 중 할 일은 목록에서 모른다 — 서버 409 가 다이얼로그에서 말한다. */
export function archiveGate(room: { my_room_role: string | null }, me: RoomGateMe): RoomGate {
  if (room.my_room_role === "owner" || room.my_room_role === "deputy" || me.canManage) return { ok: true };
  return { ok: false, reason: ROOM_MENU.archive_role };
}
/** 삭제 가부 — 진행 중인 미션이 먼저(권한이 있어도 다음 행동은 "먼저 끝내기"), 그다음 방장·ws owner·admin(ActDelete). */
export function deleteRoomGate(room: { my_room_role: string | null; active_work_count: number }, me: RoomGateMe): RoomGate {
  if (room.active_work_count > 0) return { ok: false, reason: ROOM_MENU.delete_works, count: room.active_work_count };
  if (room.my_room_role === "owner" || me.canManage) return { ok: true };
  return { ok: false, reason: ROOM_MENU.delete_role };
}
