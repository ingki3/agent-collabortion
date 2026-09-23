/**
 * 문구 자물쇠 + 판정 — 방의 다이얼로그·설정 화면(T-R2-W3: S19 · S20 · S24 · S23 · S21 · S26).
 *
 * `lib/wording.test.ts` 의 풀(전체 소스 훑기)이 옛말·내부 용어를 이미 막는다(새 파일도 그 범위 안이다 — 아래 「범위」). 여기서는 이 화면들의
 * **새 문구**를 잰다: SCREEN v0.19.2 가 문장째 정한 것은 글자 그대로 · 수는 슬롯 · 문장은 표에서만(컴포넌트에 한국어 리터럴 없음) ·
 * 비활성은 DisabledHint + aria-describedby · 방 층은 「멈춤」, 층을 적은 사유.
 */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  agentInviteGate, AUTONOMY_TEXT, COMMON, CREATE_WORK, defaultConditionTypes, deniedText, directedWork, inviteGate, LINKS, linkGate, losingViewers, openWorkCount,
  openWorkGate, PARTICIPANTS, PROPOSAL, READS, removeGate, ROOM_ROLE_LABEL, runtimeKindNote, runtimePinned, SETTINGS, settingsGates, showsRoomName, suggestedAssignee, TRANSFER_DIALOG,
} from "./room-dialogs";
import type { Room } from "@/lib/api/types";

const ROOT = join(__dirname, "..");
const src = (f: string) => readFileSync(join(ROOT, f), "utf8");
const code = (f: string) => src(f).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "").replace(/\{\/\*[\s\S]*?\*\/\}/g, "");
const FILES = [
  "components/RoomParticipantsDialog.tsx", "components/RoomSettingsForm.tsx", "components/RoomLinksDialog.tsx", "components/RoomReadsTable.tsx",
  "components/CreateWorkDialog.tsx", "components/WorkProposalDialog.tsx", "components/RoomQueryDialogs.tsx", "components/RoomDialogShell.tsx",
  "app/(app)/rooms/[id]/participants/page.tsx", "app/(app)/rooms/[id]/settings/page.tsx", "app/(app)/rooms/[id]/settings/links/page.tsx", "app/(app)/rooms/[id]/reads/page.tsx",
];
/** 표의 문장 전부(함수는 예시 인자로, 슬롯은 두 토막으로). */
const texts = (o: object): string[] =>
  Object.values(o).flatMap((v) => (typeof v === "string" ? [v] : typeof v === "function" ? [(v as (...a: string[]) => string)("X", "Y", "Z")] : Array.isArray(v) ? v.filter((x) => typeof x === "string") : typeof v === "object" && v ? texts(v) : []));
const ALL = texts({ COMMON, PARTICIPANTS, SETTINGS, TRANSFER_DIALOG, LINKS, READS, CREATE_WORK, PROPOSAL, AUTONOMY_TEXT, ROOM_ROLE_LABEL });

describe("범위 — 새 화면 파일이 문구 풀(lib/wording.test.ts) 안에 있다", () => {
  it("app·components 아래 .tsx 이고 제외 경로(lib/mock · app/dev) 밖이다", () => {
    for (const f of FILES) {
      expect(src(f).length).toBeGreaterThan(0);
      expect(f).toMatch(/^(app|components)\//);
      expect(f).not.toMatch(/^app\/dev\//);
    }
  });
});

describe("문장은 표에서만 — 컴포넌트·페이지에 한국어 리터럴이 없다", () => {
  it.each(FILES)("%s", (f) => {
    const c = code(f).replace(/^\s*import\s.*$/gm, "");
    // JSX 텍스트 노드와 문자열 리터럴 — 한글이 하나라도 있으면 표를 우회한 것이다.
    const jsx = [...c.matchAll(/>([^<>{}]*[가-힣][^<>{}]*)</g)].map((m) => m[1].trim());
    const lit = [...c.matchAll(/"([^"\n]*[가-힣][^"\n]*)"|'([^'\n]*[가-힣][^'\n]*)'|`([^`\n]*[가-힣][^`\n]*)`/g)].map((m) => m[1] ?? m[2] ?? m[3]);
    expect([...jsx, ...lit]).toEqual([]);
  });
});

describe("SCREEN v0.19.2 가 문장째 정한 것 — 글자 그대로", () => {
  it("S19 §4.10 — 에이전트 탭 머리 · 두 초대 불가 사유 · 컴퓨터 경고/안내 · 보관 · 나가기 확인 · 빈 상태", () => {
    expect(PARTICIPANTS.agents_head).toBe("초대한 에이전트는 이 방의 지난 대화 전부를 읽습니다 — 민감한 대화가 있으면 초대하지 마세요");
    expect(PARTICIPANTS.not_invitable_owner).toBe("이 에이전트는 소유자만 부를 수 있습니다");
    expect(PARTICIPANTS.not_invitable_kill).toBe("킬 스위치가 켜져 있습니다");
    expect(PARTICIPANTS.runtime_missing("MacBook", "Hermes")).toBe("〈MacBook〉에 Hermes 가 없습니다. 첫 실행에서 실패합니다 — 다른 프로파일을 고르거나 방 설정에서 컴퓨터를 바꾸세요");
    expect(PARTICIPANTS.runtime_unset).toBe("컴퓨터가 첫 실행 때 정해집니다. 프로파일이 맞는지는 그때 검사됩니다");
    expect(PARTICIPANTS.archived).toBe("보관된 방에는 초대할 수 없습니다");
    expect(PARTICIPANTS.director_leave("보고서 초안")).toBe("〈보고서 초안〉의 Director 입니다 — 먼저 Director 를 넘기세요");
    expect(PARTICIPANTS.director_remove("보고서 초안")).toBe("〈보고서 초안〉의 Director 입니다 — 먼저 Director 를 교체하세요");
    expect(PARTICIPANTS.owner_cannot_leave).toBe("방장입니다 — 먼저 방장을 넘기세요");
    expect(PARTICIPANTS.leave_body).toMatch(/^이 방에서 나가면 게시·열람이 막힙니다\. /);
    expect(PARTICIPANTS.remove_agent_running).toBe("진행 중인 턴은 계속 돕니다. 멈추려면 서브 미션 보드에서 중단하세요.");
    expect(PARTICIPANTS.no_agents).toBe("먼저 에이전트를 만드세요");
    expect(PARTICIPANTS.no_people).toBe("워크스페이스 멤버가 당신뿐입니다");
  });

  it("S20 §4.11 — 한도 규칙 · 영향 예문 · 고정 문장 · 기본 Director 빈칸 · 컴퓨터 없음", () => {
    expect(SETTINGS.limits.rule).toBe("초과는 완료가 아니라 멈춤입니다. 방 상한을 넘기면 이 방의 모든 미션이 한꺼번에 멈춥니다.");
    expect(SETTINGS.limits.impact).toBe("동시 미션 상한을 1로 낮추면 새 미션을 열 때 기존 미션을 먼저 닫아야 합니다.");
    expect(SETTINGS.runtime.pinned("MacBook")).toBe("〈MacBook〉에 고정됨 — 작업 폴더가 거기 묶여 있습니다. 바꾸려면");
    expect(SETTINGS.runtime.rebind).toBe("컴퓨터 바꾸기");
    expect(SETTINGS.director.empty_note).toBe("미션을 연 사람이 Director 가 됩니다");
    expect(SETTINGS.runtime.no_computer).toBe("연결된 컴퓨터가 없습니다");
    expect(SETTINGS.visibility.losing.join("3")).toBe("지금 이 방을 보는 사람 중 초대되지 않은 3명이 더는 볼 수 없게 됩니다");
    // 여덟 묶음 모두 영향 한 줄이 있다(§4.11 「각 항목에 바꿨을 때의 영향을 한 줄로」).
    for (const g of ["visibility", "runtime", "limits", "autonomy", "director", "owner", "links", "lifecycle"] as const) expect(SETTINGS[g].impact.length).toBeGreaterThan(10);
  });

  it("S24 §4.12 — 설명 · 에이전트 쪽 조건 · 빈 상태 둘", () => {
    expect(LINKS.explain).toBe("연결하면 이 방의 에이전트가 저 방을 읽을 수 있습니다. 쓰지는 못합니다. 읽을 때마다 양쪽 방에 기록이 남습니다.");
    expect(LINKS.agent_side_only).toBe("연결은 에이전트 쪽 조건만 풉니다. 읽는 순간 요청한 사람이 저 방의 참여자인지도 검사합니다.");
    expect(LINKS.empty).toBe("연결된 참고 방이 없습니다. 이 방의 에이전트는 자기가 참여한 방만 읽을 수 있습니다.");
    expect(LINKS.search_empty).toBe("내가 참여한 방 중에 없습니다 — 먼저 그 방에 참여하세요");
  });

  it("S23 §4.13 — 세 묶음 이름 · 거부 사유 셋 · originator_left 는 PRD 문장 · 잘림 칩 · 빈 상태", () => {
    expect(READS.groups).toEqual({ out: "이 방이 읽은 것", in: "이 방이 읽힌 것", denied: "거부된 시도" });
    expect(READS.denied).toEqual({
      originator_not_participant: "요청자가 그 방의 참여자가 아닙니다",
      agent_not_allowed: "이 에이전트가 읽을 수 없는 방입니다",
      no_originator: "이 턴은 사람의 요청에서 시작하지 않아 다른 방을 읽을 수 없습니다",
    });
    expect(READS.originator_left("서연", "결제팀", "Lead")).toBe("서연 님이 결제팀 방을 떠나 Lead의 참고 읽기가 막혔습니다");
    expect(READS.truncated).toBe("분량 상한으로 잘림");
    expect(READS.empty_head).toBe("이 방의 맥락이 오간 적이 없습니다.");
  });

  it("S21 §4.7 — 정의 한 줄 · 담당 안내 · 기본값 이유 · 상한 문장 · 보관 · 에이전트 없음 · deputy", () => {
    expect(CREATE_WORK.definition).toBe("미션은 끝이 있는 일입니다. 목표·마칠 조건·예산·Director 가 붙고, 끝나면 요약이 방에 남습니다.");
    // SCREEN 은 「아티팩트 제출」이라 적었지만 §8.4 가 조건 이름을 「보고서 제출」로 바꿨다(T-W15 자물쇠) — 이름은 그 표를 따른다.
    expect(CREATE_WORK.assignee_hint).toBe("담당 에이전트를 고르면 「보고서 제출」이 종료 조건에 들어갑니다");
    expect(CREATE_WORK.condition_default_hint).toBe("담당 에이전트를 고르지 않아 「Director 승인」만 걸었습니다 — 대상 없는 제출 조건은 아무도 채울 수 없습니다");
    expect(CREATE_WORK.limit_reached.join("3") + CREATE_WORK.limit_cap.join("3")).toBe("이 방에 열린 미션이 3개입니다(상한 3) — 열린 미션을 닫거나 상한을 올리세요");
    expect(CREATE_WORK.archived).toBe("보관된 방에서는 새 미션을 열 수 없습니다");
    expect(CREATE_WORK.no_agents).toBe("이 방에 에이전트가 없습니다 — 먼저 초대하세요");
    expect(CREATE_WORK.deputy_note).toBe("취소는 즉시, 승인은 기한 절반 후 가능합니다");
    expect(CREATE_WORK.budget_room.join("$50") + CREATE_WORK.budget_work.join("$20")).toBe("방 한도 $50 중 이 미션에 $20");
  });

  it("autonomy 문장 표(§4.7) — S21 과 S20 이 같은 표를 쓴다 · supervised 는 v1.1", () => {
    expect(AUTONOMY_TEXT.guided.note).toBe("질문 기한이 지나면 계속 기다립니다");
    expect(AUTONOMY_TEXT.autonomous.note).toBe("질문 기한이 지나면 에이전트가 제안한 기본값으로 진행합니다. 승인 요청은 예외로 항상 기다립니다");
    expect(AUTONOMY_TEXT.supervised.note).toBe("Lead의 모든 위임을 Director가 먼저 승인합니다");
    for (const f of ["components/CreateWorkDialog.tsx", "components/RoomSettingsForm.tsx"]) {
      expect(src(f)).toContain("AUTONOMY_TEXT[a].note");
      expect(src(f)).toContain("AUTONOMY_TEXT.next_version");
    }
  });

  it("S26 §4.9 — 세 버튼 이름 · 처리된 제안 두 문장(만료 없음)", () => {
    expect([PROPOSAL.accept, PROPOSAL.edit, PROPOSAL.reject]).toEqual(["이대로 열기", "고쳐서 열기", "거절"]);
    expect(PROPOSAL.accepted("서연", "2026-09-21")).toBe("이 제안은 서연 님이 2026-09-21 에 열었습니다");
    expect(PROPOSAL.rejected("서연", "2026-09-21")).toBe("이 제안은 서연 님이 2026-09-21 에 거절했습니다");
    expect(ALL.some((t) => /만료/.test(t))).toBe(false);
  });
});

describe("새 문구의 규칙", () => {
  it("옛말(세션 · 작업 줄기 · 산출물 · 런타임)과 방 층의 「일시정지」가 없다 — 방은 「멈춤」(§8.4 v0.19 층 분담)", () => {
    for (const t of ALL) {
      expect(t, t).not.toMatch(/세션|작업 줄기|산출물|런타임/);
      expect(t, t).not.toMatch(/일시정지/);
    }
  });

  it("수는 문장에 보간하지 않는다 — 수가 드는 문장은 두 토막이고 화면은 <Slot> 으로 그린다(COMPONENTS §8.5)", () => {
    for (const s of [SETTINGS.visibility.losing, READS.scope_recent, CREATE_WORK.limit_reached, CREATE_WORK.limit_cap, CREATE_WORK.budget_room, CREATE_WORK.budget_work]) {
      expect(Array.isArray(s)).toBe(true);
      expect(s).toHaveLength(2);
    }
    expect(src("components/RoomSettingsForm.tsx")).toContain("<Slot text={SETTINGS.visibility.losing} n={losing} />");
    expect(src("components/RoomReadsTable.tsx")).toContain("<Slot text={READS.scope_recent}");
    expect(src("components/CreateWorkDialog.tsx")).toContain("<Slot text={CREATE_WORK.limit_reached}");
    expect(src("components/CreateWorkDialog.tsx")).toContain("<Slot text={CREATE_WORK.limit_cap} n={cap} />");
    // 표의 함수 문장은 이름·날짜만 받는다 — 수를 받는 함수가 없다.
    for (const f of [PARTICIPANTS.runtime_missing, PARTICIPANTS.director_leave, SETTINGS.runtime.pinned, READS.originator_left, PROPOSAL.accepted]) expect(f.length).toBeGreaterThan(0);
  });

  it("비활성은 숨기지 않고 DisabledHint + aria-describedby — title 툴팁으로 사유를 주지 않는다(§5)", () => {
    for (const f of ["components/RoomParticipantsDialog.tsx", "components/RoomSettingsForm.tsx", "components/RoomLinksDialog.tsx", "components/CreateWorkDialog.tsx", "components/WorkProposalDialog.tsx"]) {
      expect(src(f), f).toMatch(/<DisabledHint id=/);
      expect(src(f), f).toMatch(/aria-describedby=\{!/);
      // 툴팁 자리(`title=`)를 쓰는 HTML 요소·링크가 하나도 없다 — 컴포넌트의 `title` prop(다이얼로그·묶음 제목)은 툴팁이 아니다.
      const tags = [...code(f).matchAll(/<([A-Za-z]+)\b((?:[^<>]|=>)*?)\/?>/g)].filter((m) => /\stitle=/.test(m[2]) && (/^[a-z]/.test(m[1]) || m[1] === "Link"));
      expect(tags.map((m) => m[0].slice(0, 80)), f).toEqual([]);
    }
  });

  it("사유에 층을 적는다 — 「권한 없음」이 아니라 누구의 일인지", () => {
    for (const t of [PARTICIPANTS.read_only, PARTICIPANTS.deputy_role, SETTINGS.read_only, SETTINGS.head_only, LINKS.read_only, CREATE_WORK.not_participant, PROPOSAL.not_participant]) {
      expect(t, t).toMatch(/방장|부방장|소유자|관리자|참여자/);
      expect(t).not.toMatch(/권한 없음|권한이 없습니다/);
    }
  });
});

describe("판정 — 누르기 전에 말한다", () => {
  const room = (over: Partial<Room> = {}) => ({ status: "active", my_room_role: "member", my_capabilities: [], blocked_reason: null, ...over }) as Room;
  const steward = room({ my_room_role: "deputy", my_capabilities: ["invite", "configure", "link", "archive"] });
  const works = [{ status: "active", title: "보고서 초안", director: { id: "u2" } }, { status: "completed", title: "지난 일", director: { id: "u3" } }] as never[];

  it("본인 행 — 방장이면 나갈 수 없고(방 설정 링크), 열린 미션의 Director 면 그 미션 이름, 아니면 읽기 전용이어도 나갈 수 있다", () => {
    expect(removeGate(room({ my_room_role: "owner" }), { kind: "user", room_role: "owner", user: { id: "me" } as never }, "me", [])).toMatchObject({ ok: false, reason: PARTICIPANTS.owner_cannot_leave, self: true, ownerLink: true });
    expect(removeGate(room(), { kind: "user", room_role: "member", user: { id: "u2" } as never }, "u2", works)).toMatchObject({ ok: false, reason: PARTICIPANTS.director_leave("보고서 초안") });
    expect(removeGate(room(), { kind: "user", room_role: "member", user: { id: "u3" } as never }, "u3", works)).toMatchObject({ ok: true, self: true }); // 끝난 미션은 막지 않는다
  });

  it("남의 행 — 보관 → 층 → 방장 → Director 순", () => {
    const u2 = { kind: "user", room_role: "member", user: { id: "u2" } } as never;
    expect(removeGate(room({ ...steward, status: "archived" }), u2, "me", works)).toMatchObject({ ok: false, reason: PARTICIPANTS.archived });
    expect(removeGate(room(), u2, "me", works)).toMatchObject({ ok: false, reason: PARTICIPANTS.read_only });
    expect(removeGate(steward, { kind: "user", room_role: "owner", user: { id: "u9" } } as never, "me", [])).toMatchObject({ ok: false, reason: PARTICIPANTS.owner_cannot_be_removed });
    expect(removeGate(steward, u2, "me", works)).toMatchObject({ ok: false, reason: PARTICIPANTS.director_remove("보고서 초안") });
    expect(removeGate(steward, { kind: "agent", room_role: "member" } as never, "me", works)).toMatchObject({ ok: true, self: false });
    expect(directedWork(works, "u3")).toBeNull();
  });

  it("초대 · 연결 · 설정 · 미션 열기의 가부", () => {
    expect(inviteGate(steward)).toEqual({ ok: true });
    expect(inviteGate(room())).toEqual({ ok: false, reason: PARTICIPANTS.read_only });
    expect(linkGate(room({ ...steward, status: "archived" }))).toEqual({ ok: false, reason: LINKS.archived });
    expect(settingsGates(steward)).toMatchObject({ configure: { ok: true }, head: { ok: false, reason: SETTINGS.head_only }, delete: { ok: false } });
    expect(openWorkGate(room({ my_room_role: null }))).toEqual({ ok: false, reason: CREATE_WORK.not_participant });
    expect(openWorkGate(room({ status: "archived" }))).toEqual({ ok: false, reason: CREATE_WORK.archived });
    expect(openWorkGate(room({ blocked_reason: "manual" }))).toEqual({ ok: false, reason: CREATE_WORK.blocked });
    expect(openWorkGate(room())).toEqual({ ok: true });
  });

  it("에이전트 초대 가부 — 킬 스위치 · 소유자만 · 허용 목록(서버 사유)", () => {
    expect(agentInviteGate({ respond_to: "nobody" })).toEqual({ ok: false, reason: PARTICIPANTS.not_invitable_kill });
    expect(agentInviteGate({ respond_to: "owner", invitable: { allowed: false, reason: "x" } })).toEqual({ ok: false, reason: PARTICIPANTS.not_invitable_owner });
    expect(agentInviteGate({ respond_to: "allowlist", invitable: { allowed: false, reason: "허용 목록에 없음" } })).toEqual({ ok: false, reason: "허용 목록에 없음" });
    expect(agentInviteGate({ respond_to: "workspace", invitable: { allowed: true } })).toEqual({ ok: true });
  });

  it("컴퓨터 종류 — 없으면 안내(info), 있는데 종류가 없으면 경고(warn), 있으면 null", () => {
    expect(runtimeKindNote({ runtime_id: null } as Room, "hermes")).toEqual({ tone: "info", text: PARTICIPANTS.runtime_unset });
    const withRt = { runtime_id: "r1", runtime: { name: "MacBook", capabilities: [{ kind: "claude_code" }] } } as unknown as Room;
    expect(runtimeKindNote(withRt, "hermes")).toEqual({ tone: "warn", text: PARTICIPANTS.runtime_missing("MacBook", "Hermes") });
    expect(runtimeKindNote(withRt, "claude_code")).toBeNull();
  });

  it("컴퓨터 고정은 runtime_pinned 로만 — 미리 고른 runtime_id 는 고정이 아니다(계약 0.2.7)", () => {
    expect(runtimePinned({ runtime_pinned: true } as Room)).toBe(true);
    expect(runtimePinned({ runtime_pinned: false, runtime_id: "r1" } as Room)).toBe(false);
    expect(runtimePinned({ runtime_id: "r1" } as Room)).toBe(false);
  });

  it("못 보게 될 사람 — 워크스페이스 멤버 중 비참여자(owner·admin 은 감사 열람이라 세지 않는다)", () => {
    const members = [{ role: "owner", user: { id: "a" } }, { role: "member", user: { id: "b" } }, { role: "member", user: { id: "c" } }, { role: "admin", user: { id: "d" } }] as never[];
    expect(losingViewers(members, [{ kind: "user", user: { id: "b" } }] as never[])).toBe(1);
  });

  it("미션 열기 — 담당 유무로 종료 조건 기본값 · 멘션 하나만 제시 · 열린 미션 수는 active·paused·completing", () => {
    expect(defaultConditionTypes(false)).toEqual(["user_approval"]);
    expect(defaultConditionTypes(true)).toEqual(["artifact_submitted", "user_approval"]);
    expect(suggestedAssignee([{ kind: "agent", id: "a1" }], ["a1", "a2"])).toBe("a1");
    expect(suggestedAssignee([{ kind: "agent", id: "a1" }, { kind: "agent", id: "a2" }], ["a1", "a2"])).toBeNull();
    expect(suggestedAssignee([{ kind: "agent", id: "a9" }], ["a1"])).toBeNull(); // 방 참여자가 아니면 제시하지 않는다
    expect(suggestedAssignee([{ kind: "user", id: "u1" }, { kind: "agent", id: "a1" }], ["a1"])).toBe("a1");
    expect(openWorkCount([{ status: "active" }, { status: "paused" }, { status: "completing" }, { status: "draft" }, { status: "completed" }] as never[])).toBe(3);
  });

  it("거부 행 — 방 이름은 originator_left 에서만", () => {
    const base = { agent: { id: "a", name: "Lead" }, originator_user: { display_name: "서연" }, other_room: { id: "r", name: "비밀" } } as never;
    expect(showsRoomName({ direction: "denied", denied_reason: "agent_not_allowed" })).toBe(false);
    expect(showsRoomName({ direction: "denied", denied_reason: "originator_left" })).toBe(true);
    expect(showsRoomName({ direction: "in", denied_reason: null })).toBe(true);
    expect(deniedText({ ...(base as object), denied_reason: "originator_left" } as never)).toBe(READS.originator_left("서연", "비밀", "Lead"));
    expect(deniedText({ ...(base as object), denied_reason: "agent_not_allowed" } as never)).not.toContain("비밀");
  });
});
