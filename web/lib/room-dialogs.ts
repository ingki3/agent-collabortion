/**
 * 방의 다이얼로그·설정 화면(T-R2-W3) — S19 참여자 · S20 방 설정 · S24 참고 방 링크 · S23 맥락 읽기 기록 · S21 미션 열기 · S26 미션 제안.
 * SCREEN v0.19.2 §4.7 · §4.9~§4.13 · §2.3 · §5 · §7.
 *
 * **문장은 여기 한곳에서만 나온다**(문구 자물쇠 `lib/room-dialogs.test.ts`). `lib/wording.ts` 와 나눈 이유는 하나 — 같은 시기에 S7 재작성
 * (T-R2-W2)이 `lib/wording.ts` 끝에 표를 붙인다. 두 PR 이 같은 파일 끝을 늘리면 머지 충돌이 난다. 규칙은 같다:
 *   - 수는 문장에 보간하지 않는다 — 수가 드는 문장은 두 토막(`Slotted`)이고 화면은 `<Slot>` 으로 그린다(COMPONENTS §8.5 v0.19).
 *   - 한 글자 토막은 줄을 나눈다(자물쇠 스캐너가 2자 이상 리터럴만 잡는다 — 한 줄에 두면 뒤 토막이 풀에서 빠진다).
 *   - 비활성 사유는 **층을 적는다**(§2.3 · §5) — 「권한 없음」이 아니라 누구의 일인지.
 *
 * 판정(누가 무엇을 누를 수 있나)은 서버 `rooms.Decide` 의 결과인 `Room.my_capabilities` 를 읽는다 — 화면이 표를 다시 세우지 않는다.
 * 계약 enum 에 없는 두 동작(부방장 지정 · 이 방에서 나가기)은 같은 열을 쓰는 동작으로 읽는다: 부방장 = `transfer_owner` 와 같은
 * 「방장·ws owner·admin」 열(authz.go `head()`), 나가기 = 방 참여자(`my_room_role`).
 */
import type { components } from "@/lib/api/schema";
import type { Room, WorkListItem, Member } from "@/lib/api/types";
import type { Slotted } from "@/lib/wording";

type S = components["schemas"];
export type RoomParticipant = S["RoomParticipant"];
export type RoomLink = S["RoomLink"];
export type RoomRead = S["RoomRead"];
export type RoomReadDirection = S["RoomReadDirection"];
export type RoomReadDeniedReason = S["RoomReadDeniedReason"];
export type WorkProposal = S["WorkProposal"];
export type Work = S["Work"];
export type WorkCreate = S["WorkCreate"];
export type RoomLimits = S["RoomLimits"];

// ── 공통 ────────────────────────────────────────────────────────────────

export const ROOM_ROLE_LABEL = { owner: "방장", deputy: "부방장", member: "참여자" } as const;

/** autonomy 값의 **차이를 문장으로**(§4.7 표) — S21 과 S20 이 같은 문구를 쓴다(§4.11 "§4.7 의 문장 표와 같은 문구"). */
export const AUTONOMY_TEXT = {
  guided: { label: "기다림", note: "질문 기한이 지나면 계속 기다립니다" },
  autonomous: { label: "알아서 진행", note: "질문 기한이 지나면 에이전트가 제안한 기본값으로 진행합니다. 승인 요청은 예외로 항상 기다립니다" },
  supervised: { label: "매번 확인", note: "Lead의 모든 위임을 Director가 먼저 승인합니다" },
  /** supervised 는 v1.1 — 비활성 + 배지. */
  next_version: "v1.1",
  default_tail: "(기본)",
} as const;

export const COMMON = {
  close: "닫기",
  cancel: "취소",
  save: "저장",
  saving: "저장 중…",
  saved: "저장했습니다",
  loading: "불러오는 중…",
  back_to_room: "방으로",
  retry: "다시 시도",
} as const;

// ── S19 참여자 초대·퇴장 ─────────────────────────────────────────────────

export const PARTICIPANTS = {
  title: "참여자",
  section_now: "지금 있는 사람·에이전트",
  section_invite: "초대하기",
  search_label: "이름으로 찾기",
  search_placeholder: "이름·이메일",
  tab_people: "사람",
  tab_agents: "에이전트",
  kind_person: "사람",
  kind_agent: "에이전트",
  joined: "합류",
  remove: "내보내기",
  leave: "이 방에서 나가기",
  invite: "초대",
  inviting: "초대 중…",
  profile: "프로파일",
  change_profile: "프로파일 바꾸기",
  more: "더 보기",
  make_deputy: "부방장으로 정하기",
  clear_deputy: "부방장 해제",
  /** 에이전트 탭 머리 한 줄(FR-2.2 — 합류 이전 히스토리도 읽고, 통제 수단은 초대를 막는 것뿐이다). */
  agents_head: "초대한 에이전트는 이 방의 지난 대화 전부를 읽습니다 — 민감한 대화가 있으면 초대하지 마세요",
  /** respond_to 로 초대할 수 없는 행(FR-1.9) — 서버 `invitable.reason` 이 없을 때의 폴백이자 SCREEN §4.10 의 두 문장. */
  not_invitable_owner: "이 에이전트는 소유자만 부를 수 있습니다",
  not_invitable_kill: "킬 스위치가 켜져 있습니다",
  /** 방 컴퓨터에 그 종류가 없다 — 〈컴퓨터〉·〈종류〉 는 이름이다(수가 아니다). */
  runtime_missing: (computer: string, kind: string) => `〈${computer}〉에 ${kind} 가 없습니다. 첫 실행에서 실패합니다 — 다른 프로파일을 고르거나 방 설정에서 컴퓨터를 바꾸세요`,
  runtime_unset: "컴퓨터가 첫 실행 때 정해집니다. 프로파일이 맞는지는 그때 검사됩니다",
  archived: "보관된 방에는 초대할 수 없습니다",
  /** 권한 없는 사람 — 다이얼로그는 읽기 전용으로 열린다(누가 있는지는 참여자 전원이 봐야 한다). */
  read_only: "초대·내보내기는 방장·부방장이나 워크스페이스 소유자·관리자만 할 수 있습니다 — 목록은 누구나 봅니다",
  deputy_role: "방장이나 워크스페이스 소유자·관리자만 부방장을 정할 수 있습니다",
  /** 거부 규칙(FR-2.2) — 누르기 전에 말한다. 〈미션〉 은 이름이다. */
  owner_cannot_leave: "방장입니다 — 먼저 방장을 넘기세요",
  owner_cannot_be_removed: "방장은 내보낼 수 없습니다 — 먼저 방장을 넘겨야 합니다",
  owner_leave_link: "방 설정",
  director_leave: (work: string) => `〈${work}〉의 Director 입니다 — 먼저 Director 를 넘기세요`,
  director_remove: (work: string) => `〈${work}〉의 Director 입니다 — 먼저 Director 를 교체하세요`,
  /** 확인 문구. */
  leave_title: "이 방에서 나갈까요?",
  leave_body: "이 방에서 나가면 게시·열람이 막힙니다. 워크스페이스 공개 방이면 다시 들어올 수 있습니다.",
  leave_confirm: "나가기",
  leave_busy: "나가는 중…",
  remove_title: (name: string) => `${name} 님을 내보낼까요?`,
  remove_agent_title: (name: string) => `${name}을(를) 내보낼까요?`,
  remove_person_body: "내보내면 이 방의 게시·열람이 막힙니다. 방 타임라인에 기록이 남습니다.",
  remove_agent_body: "내보내도 서브 미션·할 일은 기록으로 남고 새로 부를 수만 없습니다. 퇴장은 취소가 아닙니다.",
  remove_agent_running: "진행 중인 턴은 계속 돕니다. 멈추려면 서브 미션 보드에서 중단하세요.",
  remove_confirm: "내보내기",
  remove_busy: "내보내는 중…",
  /** 빈 상태(§4.10 · §7). */
  no_agents: "먼저 에이전트를 만드세요",
  no_agents_link: "에이전트",
  no_people: "워크스페이스 멤버가 당신뿐입니다",
  no_people_link: "멤버 초대",
  all_people_here: "워크스페이스 멤버가 모두 이 방에 있습니다",
  all_agents_here: "초대할 수 있는 에이전트가 모두 이 방에 있습니다",
  no_match: "찾는 이름이 없습니다",
  me: "나",
  status_working: "작업 중",
} as const;

export interface RoomGateLike {
  status: Room["status"];
  my_room_role: Room["my_room_role"];
  my_capabilities?: Room["my_capabilities"];
}
export type Gate = { ok: true } | { ok: false; reason: string };
const OK: Gate = { ok: true };
const no = (reason: string): Gate => ({ ok: false, reason });
const caps = (r: RoomGateLike) => r.my_capabilities ?? [];

/** 초대·내보내기(ActInvite) — 보관이 먼저(권한이 있어도 다음 행동은 「보관 해제」), 그다음 층. */
export function inviteGate(room: RoomGateLike): Gate {
  if (room.status === "archived") return no(PARTICIPANTS.archived);
  if (!caps(room).includes("invite")) return no(PARTICIPANTS.read_only);
  return OK;
}
/** 부방장 지정·해제(ActSetDeputy = `head()` — transfer_owner 와 같은 열). */
export function deputyGate(room: RoomGateLike): Gate {
  return caps(room).includes("transfer_owner") ? OK : no(PARTICIPANTS.deputy_role);
}

/** 열린 미션(서버 `workRunningStatuses` + draft — removeRoomParticipant 의 is_director 판정 집합). */
export const DIRECTING_STATUSES: readonly string[] = ["draft", "active", "paused", "completing"];
export function directedWork(works: readonly Pick<WorkListItem, "status" | "title" | "director">[], userId: string): string | null {
  return works.find((w) => DIRECTING_STATUSES.includes(w.status) && w.director?.id === userId)?.title ?? null;
}

/**
 * 한 행의 「내보내기」/「이 방에서 나가기」 가부(FR-2.2) — 서버 409(is_owner · is_director)를 누르기 전에 말한다.
 * 본인 행은 나가기(읽기 전용이어도 활성), 남의 행은 내보내기(초대 권한).
 */
export function removeGate(room: RoomGateLike, p: Pick<RoomParticipant, "kind" | "room_role" | "user">, meId: string, works: readonly Pick<WorkListItem, "status" | "title" | "director">[]): Gate & { self: boolean; ownerLink?: boolean } {
  const self = p.kind === "user" && p.user?.id === meId;
  if (self) {
    if (p.room_role === "owner") return { ok: false, reason: PARTICIPANTS.owner_cannot_leave, self, ownerLink: true };
    const w = directedWork(works, meId);
    if (w) return { ok: false, reason: PARTICIPANTS.director_leave(w), self };
    return { ok: true, self };
  }
  const g = inviteGate(room);
  if (!g.ok) return { ...g, self };
  if (p.kind === "user") {
    if (p.room_role === "owner") return { ok: false, reason: PARTICIPANTS.owner_cannot_be_removed, self };
    const w = p.user ? directedWork(works, p.user.id) : null;
    if (w) return { ok: false, reason: PARTICIPANTS.director_remove(w), self };
  }
  return { ok: true, self };
}

/** 에이전트 행의 초대 가부 — 서버가 계산한 `invitable` 을 읽고, 사유 문장은 SCREEN 의 두 문장을 먼저 쓴다. */
export function agentInviteGate(a: { respond_to: string; invitable?: { allowed: boolean; reason?: string | null } }): Gate {
  if (a.respond_to === "nobody") return no(PARTICIPANTS.not_invitable_kill);
  if (a.invitable && !a.invitable.allowed) {
    if (a.respond_to === "owner") return no(PARTICIPANTS.not_invitable_owner);
    return no(a.invitable.reason ?? PARTICIPANTS.not_invitable_owner);
  }
  return OK;
}

/** 런타임 종류의 화면 이름 — 경고 문장의 〈종류〉 자리. */
export const RUNTIME_KIND_NAME: Record<string, string> = { claude_code: "Claude Code", codex: "Codex", hermes: "Hermes", gemini: "Gemini" };
export const kindName = (k: string) => RUNTIME_KIND_NAME[k] ?? k;

/** 방 컴퓨터에 그 프로파일의 종류가 있나 — 컴퓨터가 없으면 `unset`(안내), 없으면 경고 문장, 있으면 null. */
export function runtimeKindNote(room: Pick<Room, "runtime_id" | "runtime">, kind: string | undefined): { tone: "info" | "warn"; text: string } | null {
  if (!room.runtime_id || !room.runtime) return { tone: "info", text: PARTICIPANTS.runtime_unset };
  if (!kind) return null;
  const has = (room.runtime.capabilities ?? []).some((c) => c.kind === kind);
  return has ? null : { tone: "warn", text: PARTICIPANTS.runtime_missing(room.runtime.name, kindName(kind)) };
}

export function personName(u: { display_name?: string | null; email?: string } | null | undefined): string {
  return u?.display_name || u?.email || "알 수 없는 사람";
}

// ── S20 방 설정 ─────────────────────────────────────────────────────────

export const SETTINGS = {
  title: "방 설정",
  desc: "기본값은 워크스페이스에서 상속됩니다. 한 번도 안 보면 모든 방이 같은 값으로 돕니다 — 이 방에 맞게 바꾸세요.",
  read_only: "방장·부방장이나 워크스페이스 소유자·관리자만 설정을 바꿀 수 있습니다 — 지금은 읽기 전용입니다",
  head_only: "방장이나 워크스페이스 소유자·관리자만 할 수 있습니다",
  /** 묶음 — 제목 · 바꿨을 때의 영향 한 줄(§4.11 "각 항목에 바꿨을 때의 영향을 한 줄로"). */
  visibility: {
    title: "공개 범위",
    impact: "바꾸면 방 타임라인에 기록이 남습니다. 워크스페이스 소유자·관리자는 어느 쪽이든 감사 목적으로 볼 수 있습니다.",
    workspace: "워크스페이스 멤버 모두",
    workspace_note: "누구나 보고 쓸 수 있습니다(기본)",
    invited: "초대된 사람만",
    invited_note: "초대된 사람만 보고 씁니다",
    /** invited 로 바꿀 때(§4.11) — 수는 슬롯. */
    losing: [
      "지금 이 방을 보는 사람 중 초대되지 않은 ",
      "명이 더는 볼 수 없게 됩니다",
    ] as Slotted,
  },
  runtime: {
    title: "컴퓨터·격리",
    impact: "첫 실행 전에만 바꿀 수 있습니다 — 첫 실행 순간 이 컴퓨터와 격리 방식에 고정됩니다.",
    computer: "컴퓨터",
    first_run: "첫 실행 때 정해짐",
    isolation: "격리",
    none: "격리 없음",
    none_note: "에이전트들이 같은 폴더에서 일합니다",
    worktree: "워크트리로 나눔",
    worktree_note: "에이전트마다 작업 폴더가 하나씩 생깁니다 — 서로의 변경이 섞이지 않습니다",
    container: "컨테이너로 나눔",
    repo: "저장소",
    repo_placeholder: "저장소를 고르세요",
    repo_none: "이 컴퓨터에 알려진 저장소가 없습니다",
    repo_needs_computer: "워크트리는 컴퓨터를 먼저 골라야 저장소를 고를 수 있습니다",
    pinned: (computer: string) => `〈${computer}〉에 고정됨 — 작업 폴더가 거기 묶여 있습니다. 바꾸려면`,
    rebind: "컴퓨터 바꾸기",
    no_computer: "연결된 컴퓨터가 없습니다",
    no_computer_link: "컴퓨터 연결",
  },
  limits: {
    title: "한도",
    impact: "동시 미션 상한을 1로 낮추면 새 미션을 열 때 기존 미션을 먼저 닫아야 합니다.",
    rule: "초과는 완료가 아니라 멈춤입니다. 방 상한을 넘기면 이 방의 모든 미션이 한꺼번에 멈춥니다.",
    smaller_wins: "예산·시간은 미션에도 걸 수 있고 작은 쪽이 이깁니다.",
    budget: "예산(USD)",
    budget_none: "비우면 상한 없음",
    time: "시간 상한",
    time_placeholder: "PT4H",
    works: "동시 진행 미션 수",
    lanes: "동시 서브 미션 수",
  },
  autonomy: {
    title: "자율성",
    impact: "방 기본값입니다. 미션을 열 때 덮어쓸 수 있습니다.",
  },
  director: {
    title: "기본 Director",
    impact: "지정하면 새 미션의 Director 기본값이 됩니다.",
    empty: "비워 둠 — 미션을 연 사람",
    empty_note: "미션을 연 사람이 Director 가 됩니다",
  },
  owner: {
    title: "방장·부방장",
    impact: "부방장은 초대·설정·멈춤·보관을 나눠 집니다. 방 삭제·방장 넘기기·부방장 지정은 방장만 합니다.",
    owner: "방장",
    deputy: "부방장",
    deputy_none: "없음",
    transfer: "방장 넘기기",
    set_deputy: "부방장 정하기",
    deputy_hint: "부방장은 이 방의 사람 참여자 중 한 명입니다",
  },
  links: {
    title: "참고 방 링크",
    impact: "연결한 방은 이 방의 에이전트가 읽을 수 있습니다. 쓰지는 못합니다.",
    none: "연결된 참고 방이 없습니다",
    manage: "연결 관리",
  },
  lifecycle: {
    title: "보관·삭제",
    impact: "보관은 새 대화와 새 미션만 막고 언제든 되돌릴 수 있습니다. 삭제는 되돌릴 수 없습니다.",
    archive: "보관",
    unarchive: "보관 해제",
    unarchiving: "해제 중…",
    delete: "삭제",
    archived_now: "보관된 방입니다",
  },
  reads_link: "맥락 읽기 기록",
} as const;

/** S20 권한 — 설정(ActConfigure) · 방장 넘기기/부방장(head) · 보관(ActArchive) · 삭제(ActDelete). */
export function settingsGates(room: RoomGateLike) {
  const c = caps(room);
  return {
    configure: c.includes("configure") ? OK : no(SETTINGS.read_only),
    head: c.includes("transfer_owner") ? OK : no(SETTINGS.head_only),
    archive: c.includes("archive") ? OK : no(SETTINGS.read_only),
    delete: c.includes("delete") ? OK : no(SETTINGS.head_only),
  };
}
/**
 * 첫 dispatch 뒤에는 컴퓨터·격리가 읽기 전용이다 — 서버가 `Room.runtime_pinned`(계약 0.2.7, updateRoom `409 runtime_pinned` 와 같은 판정)로
 * 알려 준다. `runtime_id` 로 대리하지 않는다: 방 설정에서 첫 실행 전에 컴퓨터를 미리 고르면 `runtime_id` 가 채워져도 아직 바꿀 수 있다.
 */
export const runtimePinned = (room: Pick<Room, "runtime_pinned">) => room.runtime_pinned === true;

/** invited 로 바꾸면 못 보게 되는 사람 수 — 워크스페이스 멤버 중 이 방 참여자가 아니고 owner·admin(감사 열람)도 아닌 사람. */
export function losingViewers(members: readonly Pick<Member, "role" | "user">[], participants: readonly Pick<RoomParticipant, "kind" | "user">[]): number {
  const inRoom = new Set(participants.filter((p) => p.kind === "user" && p.user).map((p) => p.user!.id));
  return members.filter((m) => m.role === "member" && !inRoom.has(m.user.id)).length;
}

export const TRANSFER_DIALOG = {
  title: "방장을 넘길까요?",
  pick: "새 방장",
  pick_placeholder: "이 방의 사람 참여자 중 고르세요",
  body: "넘기면 방 삭제·방장 넘기기·부방장 지정은 새 방장이 합니다. 방 타임라인에 기록이 남습니다.",
  none: "넘길 사람이 없습니다 — 먼저 이 방에 사람을 초대하세요",
  confirm: "넘기기",
  busy: "넘기는 중…",
} as const;

// ── S24 참고 방 링크 ────────────────────────────────────────────────────

export const LINKS = {
  title: "참고 방 링크",
  explain: "연결하면 이 방의 에이전트가 저 방을 읽을 수 있습니다. 쓰지는 못합니다. 읽을 때마다 양쪽 방에 기록이 남습니다.",
  section_linked: "연결된 방",
  section_find: "방 찾아 연결",
  search_label: "내가 참여한 방 찾기",
  search_placeholder: "방 이름",
  last_activity: "마지막 활동",
  no_activity: "활동 없음",
  linked_by: "연결",
  unlink: "연결 풀기",
  unlinking: "푸는 중…",
  link: "연결",
  linking: "연결 중…",
  /** 연결해도 읽히지 않을 수 있다(FR-4.5 조건 1) — 다이얼로그 아래 한 줄. */
  agent_side_only: "연결은 에이전트 쪽 조건만 풉니다. 읽는 순간 요청한 사람이 저 방의 참여자인지도 검사합니다.",
  empty: "연결된 참고 방이 없습니다. 이 방의 에이전트는 자기가 참여한 방만 읽을 수 있습니다.",
  search_empty: "내가 참여한 방 중에 없습니다 — 먼저 그 방에 참여하세요",
  read_only: "방장·부방장이나 워크스페이스 소유자·관리자만 연결하고 풀 수 있습니다",
  archived: "보관된 방에서는 참고 방을 연결하거나 풀 수 없습니다",
} as const;

/** 연결·풀기(ActLink) — 보관이 먼저, 그다음 층. */
export function linkGate(room: RoomGateLike): Gate {
  if (room.status === "archived") return no(LINKS.archived);
  return caps(room).includes("link") ? OK : no(LINKS.read_only);
}

// ── S23 맥락 읽기 기록 ──────────────────────────────────────────────────

export const READS = {
  title: "맥락 읽기 기록",
  desc: "다른 방과 오간 맥락을 한곳에 모읍니다. 맥락이 새어 나간 사실은 이 방 사람 모두가 봅니다.",
  groups: {
    out: "이 방이 읽은 것",
    in: "이 방이 읽힌 것",
    denied: "거부된 시도",
  },
  group_notes: {
    out: "이 방의 에이전트가 다른 방을 읽었습니다",
    in: "다른 방의 에이전트가 이 방을 읽었습니다 — 타임라인의 기록과 같은 사건입니다",
    denied: "이 방의 에이전트가 시도했으나 막힌 것입니다. 어느 방인지는 적지 않습니다",
  },
  col_at: "시각",
  col_agent: "에이전트",
  col_originator: "요청한 사람",
  col_target: "대상 방",
  col_source: "읽은 쪽 방",
  col_scope: "범위",
  col_reason: "사유",
  no_originator_user: "사람 없음",
  scope_summary: "요약",
  scope_recent: [
    "최근 ",
    "건",
  ] as Slotted,
  scope_join: " + ",
  truncated: "분량 상한으로 잘림",
  denied: {
    originator_not_participant: "요청자가 그 방의 참여자가 아닙니다",
    agent_not_allowed: "이 에이전트가 읽을 수 없는 방입니다",
    no_originator: "이 턴은 사람의 요청에서 시작하지 않아 다른 방을 읽을 수 없습니다",
  },
  /** originator 가 그 방을 떠나 막힌 행 — PRD FR-4.5 문장 그대로. 이 문장만은 대상 방 이름을 담는다(§4.13 · D-18). */
  originator_left: (person: string, room: string, agent: string) => `${person} 님이 ${room} 방을 떠나 ${agent}의 참고 읽기가 막혔습니다`,
  hidden_room: "—",
  filter_period: "기간",
  filter_agent: "에이전트",
  period_all: "전체",
  period_1d: "최근 하루",
  period_7d: "최근 7일",
  period_30d: "최근 30일",
  agent_all: "모든 에이전트",
  audit_link: "활동 로그에서 보기",
  group_empty: "없음",
  empty_head: "이 방의 맥락이 오간 적이 없습니다.",
  empty_tail: "에이전트가 다른 방을 읽으면 여기에 남습니다.",
  more: "더 불러오기",
} as const;

export function deniedText(r: Pick<RoomRead, "denied_reason" | "originator_user" | "other_room" | "agent">): string {
  if (r.denied_reason === "originator_left") return READS.originator_left(personName(r.originator_user), r.other_room?.name ?? READS.hidden_room, r.agent.name);
  return r.denied_reason ? READS.denied[r.denied_reason] : "";
}
/**
 * 거부 행에서 방 이름을 보일 수 있나 — 존재를 숨기는 규칙(FR-4.5). 서버가 이미 `other_room: null` 로 지우지만, 화면도 한 번 더
 * 막는다: `originator_left` 가 아니면 이름을 그리지 않는다(서버가 실수로 채워 보내도 새지 않게).
 */
export const showsRoomName = (r: Pick<RoomRead, "direction" | "denied_reason">) => r.direction !== "denied" || r.denied_reason === "originator_left";

export const PERIOD_MS = { all: 0, "1d": 864e5, "7d": 7 * 864e5, "30d": 30 * 864e5 } as const;
export type Period = keyof typeof PERIOD_MS;

// ── S21 미션 열기 ───────────────────────────────────────────────────────

export const CREATE_WORK = {
  title_new: "새 미션",
  title_from: "이 메시지로 미션 열기",
  title_proposal: "제안으로 미션 열기",
  /** 제목 바로 아래 회색 한 줄(SCR-C H) — 세 길 모두 같은 문장. */
  definition: "미션은 끝이 있는 일입니다. 목표·마칠 조건·예산·Director 가 붙고, 끝나면 요약이 방에 남습니다.",
  quoted: "원 메시지",
  quoted_note: "이 메시지와 그 스레드 중 아직 미션이 없는 것이 이 미션으로 묶입니다",
  goal: "목표",
  goal_placeholder: "무엇을 끝내면 되나요?",
  goal_required: "목표를 적어 주세요",
  defaults: "기본값 — 필요할 때만 바꾸세요",
  title: "제목(선택)",
  title_placeholder: "비우면 목표의 첫 줄",
  criteria: "성공 기준(선택)",
  criteria_add: "기준 추가",
  criteria_placeholder: "예: 표 3개가 들어간다",
  criteria_remove: "빼기",
  assignee: "제출자",
  assignee_none: "고르지 않음",
  assignee_hint: "제출자를 고르면 「아티팩트 제출」이 종료 조건에 들어갑니다",
  /** 메시지가 멘션한 에이전트가 하나뿐이면 제시만 한다 — 사람이 확인한다(FR-2A.1). */
  assignee_suggested: "메시지가 부른 에이전트입니다 — 맞는지 확인하세요",
  no_agents: "이 방에 에이전트가 없습니다 — 먼저 초대하세요",
  no_agents_link: "참여자 초대",
  condition: "종료 조건",
  condition_edit: "종료 조건 바꾸기",
  condition_default_hint: "제출자를 고르지 않아 「Director 승인」만 걸었습니다 — 대상 없는 제출 조건은 아무도 채울 수 없습니다",
  /** agent_approval 에 리뷰어가 없으면 막는다(S-84, 서버 422) — 편집기 문장은 옛 표에 있어 여기서 방의 말로 다시 적는다. */
  reviewer_required: "「에이전트 검토 승인」에는 리뷰어를 이 방의 에이전트 중에서 골라 주세요 — 리뷰어가 없으면 아무도 승인할 수 없습니다",
  need_condition: "종료 조건을 하나 이상 고르세요",
  director: "Director",
  director_me: "나(연 사람)",
  director_room_default: "방 기본 Director",
  director_note: "Director 가 확인 요청에 답하고 미션을 끝냅니다",
  deputy: "deputy(선택)",
  deputy_none: "없음",
  deputy_note: "취소는 즉시, 승인은 기한 절반 후 가능합니다",
  budget: "예산 상한(USD, 선택)",
  budget_placeholder: "비우면 방 상한을 따릅니다",
  /** 새 미션 칸을 비웠는데 워크스페이스 기본 상한(S14)이 있으면 — 서버가 그 값을 채운다. */
  budget_ws_default: [
    "비우면 워크스페이스 기본 ",
    " 이 걸립니다",
  ] as Slotted,
  time: "시간 상한(선택)",
  /** 「방 한도 $50 중 이 미션에 $20」 — 수가 둘이라 두 토막 둘. */
  budget_room: [
    "방 한도 ",
    " 중",
  ] as Slotted,
  budget_work: [
    " 이 미션에 ",
    "",
  ] as Slotted,
  budget_room_none: "방 예산 한도 없음",
  /** 미션 칸이 비었을 때 — 「방 한도 $50 중」은 뒤에 미션 몫이 올 때만 말이 된다. */
  budget_room_only: [
    "방 한도 ",
    "",
  ] as Slotted,
  smaller_wins: "방 한도와 미션 한도 중 작은 쪽이 이깁니다.",
  autonomy: "자율성",
  /** 동시 미션 상한 — 열기 **전에** 알린다(§4.7). 수는 슬롯. */
  limit_reached: [
    "이 방에 열린 미션이 ",
    "개입니다",
  ] as Slotted,
  limit_cap: [
    "(상한 ",
    ") — 열린 미션을 닫거나 상한을 올리세요",
  ] as Slotted,
  limit_link: "방 설정",
  open_works: "열린 미션",
  archived: "보관된 방에서는 새 미션을 열 수 없습니다",
  not_participant: "이 방의 참여자만 미션을 열 수 있습니다 — 방장에게 초대를 요청하세요",
  blocked: "이 방은 멈춰 있어 미션을 열 수 없습니다 — 방을 다시 움직인 뒤 여세요",
  open: "열기",
  opening: "여는 중…",
} as const;

/** assignee 유무로 종료 조건 기본값이 바뀐다(FR-2A.1) — 있으면 제출 AND Director 승인, 없으면 Director 승인 단독. */
export function defaultConditionTypes(hasAssignee: boolean): ("artifact_submitted" | "user_approval")[] {
  return hasAssignee ? ["artifact_submitted", "user_approval"] : ["user_approval"];
}

/** 메시지에서 열 때 제시할 담당 — 그 메시지가 멘션한 **에이전트가 하나뿐이고** 방 참여자일 때만. */
export function suggestedAssignee(mentions: readonly { kind: string; id: string }[] | undefined, agentIds: readonly string[]): string | null {
  const ids = [...new Set((mentions ?? []).filter((m) => m.kind === "agent").map((m) => m.id))];
  return ids.length === 1 && agentIds.includes(ids[0]) ? ids[0] : null;
}

/** 열린 미션 수(상한 검사) — 서버 `workRunningStatuses`(active · paused · completing). draft 는 세지 않는다. */
export const RUNNING_WORK: readonly string[] = ["active", "paused", "completing"];
export function openWorkCount(works: readonly Pick<WorkListItem, "status">[]): number {
  return works.filter((w) => RUNNING_WORK.includes(w.status)).length;
}
export const maxConcurrentWorks = (room: Pick<Room, "limits">) => room.limits?.max_concurrent_works ?? 3;

/** 미션 열기 가부(ActOpenWork) — 보관 → 참여자 → 멈춤 순(서버 openWork 와 같은 순서). 상한 초과는 막지 않고 경고(서버 409 가 최종). */
export function openWorkGate(room: RoomGateLike & Pick<Room, "blocked_reason">): Gate {
  if (room.status === "archived") return no(CREATE_WORK.archived);
  if (!room.my_room_role) return no(CREATE_WORK.not_participant);
  if (room.blocked_reason) return no(CREATE_WORK.blocked);
  return OK;
}

export const firstLine = (s: string) => s.trim().split("\n")[0]?.trim() ?? "";

// ── S26 미션 제안 확인 ──────────────────────────────────────────────────

export const PROPOSAL = {
  title: "미션 제안 확인",
  proposed_by: (agent: string) => `${agent}의 제안`,
  goal: "제안한 목표",
  rationale: "제안 근거",
  trigger: "제안이 나온 메시지",
  director_note: "여는 사람이 Director 가 됩니다 — 제안한 에이전트가 아닙니다.",
  accept: "이대로 열기",
  edit: "고쳐서 열기",
  reject: "거절",
  reject_reason: "거절 사유(선택)",
  reject_placeholder: "에이전트가 같은 제안을 반복하지 않게 이유를 적어 주세요",
  reject_note: "거절하면 방 타임라인에 남아 제안한 에이전트도 압니다.",
  reject_confirm: "거절하기",
  rejecting: "거절하는 중…",
  accepting: "여는 중…",
  /** 이미 처리된 제안(빈 상태) — 제안에는 기한이 없어 「만료됨」은 없다. */
  accepted: (who: string, date: string) => `이 제안은 ${who} 님이 ${date} 에 열었습니다`,
  rejected: (who: string, date: string) => `이 제안은 ${who} 님이 ${date} 에 거절했습니다`,
  reason_prefix: "사유: ",
  go_work: "그 미션 보기",
  not_participant: "이 방의 사람 참여자만 제안을 열거나 거절할 수 있습니다",
  not_found: "이 방에 그 제안이 없습니다",
} as const;

export const ymd = (iso: string | null | undefined) => (iso ? iso.slice(0, 10) : "");
