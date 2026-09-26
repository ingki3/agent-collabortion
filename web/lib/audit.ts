/**
 * S15 활동 로그(SCREEN v0.19.2 §4.18, PRD §10 R2 M7, T-R2-W4a) — 문구와 순수 규칙. 화면은 `app/(app)/settings/audit/page.tsx`.
 *
 * **v1.1 → v1 승격 이유는 하나다**: 다른 방 읽기(FR-4.5)를 **읽힌 쪽**에도 남겨야 하는데 읽힌 쪽에는 할 일이 없어 `task_event` 에 붙일 수 없다 —
 * 남는 자리가 `activity_log` 뿐이다. 그래서 이 화면이 없으면 §12 위험표의 「양쪽 기록」이 절반만 작동한다.
 *
 * 실시간은 **없다** — 감사 화면은 흐르면 읽을 수 없다(§6 「`activity.appended` 는 넣지 않는다」). 새로고침으로 충분하다.
 * 권한은 owner·admin — 방 참여자는 자기 방 몫만 S23 에서 본다.
 */
import type { components } from "@/lib/api/schema";
import { READS } from "@/lib/room-dialogs";
import { VISIBILITY_LABEL } from "@/lib/screens-v19";
import { ROOM_BLOCKED_LABEL } from "@/lib/wording";

export type ActivityLogEntry = components["schemas"]["ActivityLogEntry"];

export const AUDIT = {
  tab: "활동 로그",
  tab_desc: "누가 언제 무엇을 했는지 — 다른 방 읽기·참고 방·공개 범위·방 삭제",
  title: "활동 로그",
  desc: "워크스페이스에서 일어난 일을 시각순으로 남깁니다. 방 참여자는 자기 방 몫만 방의 맥락 읽기 기록에서 봅니다.",
  back: "설정으로",
  /** 실시간이 없다는 사실을 말한다 — 흐르지 않는 화면을 멈춘 것으로 오해하지 않게. */
  static_note: "이 화면은 실시간으로 바뀌지 않습니다 — 새로고침하면 최신 기록을 읽습니다",
  refresh: "새로고침",
  more: "더 보기",
  empty: "기록된 활동이 없습니다",
  forbidden: "소유자·관리자만 활동 로그를 볼 수 있습니다",
  masked: "마스킹됨 — 보안 설정에 따라 긴 내용은 앞부분만 남습니다",
  // 1글자 리터럴(「방」)은 줄을 나눈다 — 문구 자물쇠의 스캐너(2자 이상)가 한 줄 안의 따옴표 짝을 잘못 맞춰 뒤 문장을 놓친다.
  cols: {
    at: "시각", actor: "행위자", action: "행위", object: "대상", payload: "내용",
    room: "방",
  },
  filters: {
    action: "행위", actor: "행위자", since: "시작일", until: "종료일", all_rooms: "모든 방", all_actions: "모든 행위", all_actors: "모든 행위자", apply: "거르기", clear: "지우기",
    room: "방",
  },
  actor_kind: { user: "사람", agent: "에이전트", system: "시스템" },
  no_room: "—",
  deleted_room: "(지워진 방)",
  /** 거부 행은 대상 방 이름을 적지 않는다(FR-4.5 — 존재를 숨긴다). */
  hidden_target: "(대상 방 숨김)",
} as const;

/**
 * 행위 이름 → 사람 말. §4.18 「반드시 담아야 하는 행」 전부와 서버가 남기는 나머지. 모르는 값은 원시 값 그대로 보인다(지어내지 않는다).
 * 행위 필터의 선택지도 이 표에서 나온다.
 */
export const ACTION_LABEL: Record<string, string> = {
  "room.read": "다른 방 읽기",
  "room.read.denied": "다른 방 읽기 거부",
  "room_link.created": "참고 방 연결",
  "room_link.deleted": "참고 방 연결 해제",
  "room.visibility_changed": "공개 범위 변경",
  "room.audit_viewed": "감사 열람",
  "room.deleted": "방 삭제",
  "room.owner_succeeded": "방장 승계",
  "room.owner_transferred": "방장 넘김",
  "room.deputy_changed": "부방장 변경",
  "room.blocked": "방 멈춤",
  "room.unblocked": "방 멈춤 해제",
  "room.archived": "방 보관",
  "room.unarchived": "방 보관 해제",
  "room.settings_changed": "방 설정 변경",
  "room.renamed": "방 이름 변경",
  "room.description_changed": "방 설명 변경",
  "member.role_changed": "멤버 역할 변경",
  "member.removed": "멤버 내보내기",
  "agent.respond_to_changed": "에이전트 응답 대상 변경",
  "work.director_changed": "미션 Director 변경",
  "work.director_succeeded": "미션 Director 승계",
  "work.deleted": "미션 삭제",
};
export const actionLabel = (a: string): string => ACTION_LABEL[a] ?? a;

/** 필터 선택지 — §4.18 필수 행을 앞에. */
export const ACTION_FILTER: readonly string[] = [
  "room.read", "room.read.denied", "room_link.created", "room_link.deleted", "room.visibility_changed", "room.audit_viewed", "room.deleted", "room.owner_succeeded",
  "room.blocked", "room.unblocked", "room.archived", "member.role_changed", "member.removed",
];

/** 방 칸 — 지워진 방은 `room` 이 null 이고 id·이름이 내용(payload)에 남는다(서버 `room.deleted`). 거부 행은 대상 이름을 숨긴다. */
export function roomCell(e: Pick<ActivityLogEntry, "room" | "action" | "payload">): string {
  if (e.room) return e.room.name;
  if (e.action === "room.deleted") {
    const n = e.payload?.name;
    return typeof n === "string" && n ? `${n} ${AUDIT.deleted_room}` : AUDIT.deleted_room;
  }
  return AUDIT.no_room;
}

/** 대상 칸 — `object_ref`(`room:<id>`) 는 사람이 쓸 수 없는 문자열이라 종류 이름만 보인다. 거부 행은 「대상 방 숨김」. */
export function objectCell(e: Pick<ActivityLogEntry, "object_ref" | "action">): string {
  if (e.action === "room.read.denied") return AUDIT.hidden_target;
  const kind = e.object_ref.split(":")[0];
  return OBJECT_KIND[kind] ?? kind;
}
const OBJECT_KIND: Record<string, string> = {
  room_link: "참고 방 연결", member: "멤버", agent: "에이전트", work: "미션",
  room: "방",
  session: "방",
};
const YES_NO = {
  yes: "예",
  no: "아니오",
};

/** 내용 칸 — 한 줄 `키 값 · 키 값`. id(uuid)는 사람이 못 읽으니 뺀다. 마스킹 표시는 따로(`masked`). */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function payloadCell(p: Record<string, unknown> | null | undefined): { text: string; masked: boolean } {
  const masked = p?.masked === true;
  const parts: string[] = [];
  for (const [k, v] of Object.entries(p ?? {})) {
    if (k === "masked" || v == null) continue;
    if (typeof v === "string" && UUID_RE.test(v)) continue;
    if (typeof v === "object") continue;
    const val = typeof v === "boolean" ? (v ? YES_NO.yes : YES_NO.no) : (VALUE_WORDS[k]?.[String(v)] ?? String(v));
    parts.push(`${PAYLOAD_KEY[k] ?? k} ${val}`);
  }
  return { text: parts.join(" · "), masked };
}
/** 다른 방 읽기의 방향 — S23 과 같은 말(이 방이 읽은 것 · 읽힌 것 · 거부). */
const DIRECTION: Record<string, string> = { out: "읽음", in: "읽힘", denied: "거부" };
/** 값이 enum 인 칸은 화면의 말로 — 모르는 값은 원시 값 그대로(지어내지 않는다). 같은 사건을 다른 화면과 같은 말로 부른다. */
const VALUE_WORDS: Record<string, Record<string, string>> = {
  direction: DIRECTION,
  reason: ROOM_BLOCKED_LABEL,
  from: VISIBILITY_LABEL,
  to: VISIBILITY_LABEL,
  denied_reason: READS.denied,
};
const PAYLOAD_KEY: Record<string, string> = {
  reason: "사유", direction: "방향", denied_reason: "거부 사유", note: "메모", recent_n: "최근 메시지", summary: "요약 포함",
  from: "이전", to: "이후", name: "이름", target_room_name: "대상 방",
};

/** 시작일·종료일(날짜 입력, 로컬 자정) → 계약 `since`·`until`(date-time). 종료일은 그날 끝까지(다음 날 자정 미만). */
export function dayRange(since: string, until: string): { since?: string; until?: string } {
  const out: { since?: string; until?: string } = {};
  if (since) out.since = new Date(`${since}T00:00:00`).toISOString();
  if (until) {
    const d = new Date(`${until}T00:00:00`);
    d.setDate(d.getDate() + 1);
    out.until = d.toISOString();
  }
  return out;
}
