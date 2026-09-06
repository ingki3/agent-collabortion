/**
 * 목 API 저장소 — 서버 스트림(S)이 붙기 전에 웹을 단독으로 검증하기 위한 **인메모리 서버**(COLAB_MOCK_API=1).
 * openapi.yaml 의 P1 범위 operation 만 구현한다. 실서버가 오면 next.config 의 rewrite 가 :8080 으로 보내고 이 코드는 쓰이지 않는다.
 * 계약(응답 형태·오류 코드·SSE 프레임)은 openapi 를 그대로 따른다 — 화면이 목에만 맞게 되는 것을 막기 위해서다.
 */
import type {
  Agent, AgentTemplate, Artifact, Decision, HitlRequest, InboxItem, Lane, Member, Message, Pairing, Participant,
  Runtime, Session, StreamEventType, TaskEvent, User, Workdir, Workspace,
} from "@/lib/api/types";

export interface MockUser extends User {
  password: string;
}
export interface MockInvite {
  id: string;
  token: string;
  workspace_id: string;
  role: "owner" | "admin" | "member";
  invited_by: string; // user id
  expires_at: string;
  status: "pending" | "accepted" | "expired" | "revoked";
}
export interface MockTask {
  id: string;
  session_id: string;
  agent_id: string;
  lane_id: string;
  status: string;
  attempt: number;
  trigger_message_id: string | null;
  created_at: string;
  /** 재지시(FR-3.4 B) — 새 task 이고 이전 task 를 가리킨다. 재시도는 같은 task 의 attempt 다. */
  restarted_from_task_id?: string | null;
  failure_kind?: string | null;
  started_at?: string | null;
  finished_at?: string | null;
  resumed?: boolean | null;
  cost_usd?: number;
  /** HITL 예산 승인값(C2′) — **task 범위**다. 에이전트의 `budget_per_task` 는 건드리지 않는다(E9-02). */
  budget_override?: number | null;
  /**
   * 시도별 기록(계약 `TaskAttempt`) — lane 카드 펼침의 "실행" 열이다(SCREEN §4.5 O3 정보 5종).
   * HITL 재개·재시도는 **같은 task 의 새 attempt** 이므로 여기 한 줄이 늘어난다(E7-07·E8-07).
   */
  attempts?: { attempt: number; started_at: string | null; finished_at: string | null; resumed: boolean | null; outcome: string | null; cost_usd: number }[];
}
export interface StoredEvent {
  id: number;
  type: StreamEventType;
  workspace_id: string;
  session_id: string | null;
  at: string;
  payload: unknown;
  ephemeral: boolean;
}
export interface Subscriber {
  workspace_id: string;
  session_ids: string[] | null;
  write: (frame: string) => void;
}

export interface Store {
  users: Map<string, MockUser>;
  cookies: Map<string, string>; // token → user id
  workspaces: Map<string, Workspace>;
  members: Member[];
  invites: Map<string, MockInvite>;
  runtimes: Map<string, Runtime>;
  pairings: Map<string, Pairing>;
  agents: Map<string, Agent>;
  sessions: Map<string, Session>;
  messages: Map<string, Message>;
  tasks: Map<string, MockTask>;
  taskEvents: Map<string, TaskEvent[]>;
  lanes: Map<string, Lane>;
  artifacts: Map<string, Artifact>;
  decisions: Map<string, Decision>;
  /** P3 — HITL 요청(S7 카드 · S8 인박스가 같은 행을 본다). */
  hitls: Map<string, HitlRequest>;
  /** P3 — 인박스 항목. `member_id` 대신 `user_id` 로 소유자를 들고 있다(목은 워크스페이스 하나 기준). */
  inbox: Map<string, InboxItem & { user_id: string }>;
  /** P4 — workdir(S13). 어느 런타임 것인지는 계약 `Workdir` 에 없으므로 목이 곁에 들고 있다. */
  workdirs: Map<string, Workdir & { runtime_id: string }>;
  /** P4 — 워크스페이스 workdir 용량 상한(GB). null 이면 미설정 = 무제한(E13-19). */
  workdirQuotaGb: number | null;
  idem: Map<string, unknown>;
  events: StoredEvent[];
  eventSeq: number;
  subs: Set<Subscriber>;
}

declare global {
  // eslint-disable-next-line no-var
  var __colabMock: Store | undefined;
}

export const now = () => new Date().toISOString();
export const uuid = () => crypto.randomUUID();

function seed(): Store {
  const s: Store = {
    users: new Map(), cookies: new Map(), workspaces: new Map(), members: [], invites: new Map(), runtimes: new Map(),
    pairings: new Map(), agents: new Map(), sessions: new Map(), messages: new Map(), tasks: new Map(), taskEvents: new Map(),
    lanes: new Map(), artifacts: new Map(), decisions: new Map(), hitls: new Map(), inbox: new Map(),
    workdirs: new Map(), workdirQuotaGb: 50,
    idem: new Map(), events: [], eventSeq: 0, subs: new Set(),
  };
  // 데모 워크스페이스: 초대 링크(S3)·비참여 에이전트 경고(E1-04) 검증용
  const demo: MockUser = { id: uuid(), email: "demo@colab.dev", display_name: "데모", avatar_url: null, created_at: now(), password: "password123" };
  s.users.set(demo.id, demo);
  const ws: Workspace = { id: uuid(), name: "데모팀", slug: "demo", created_at: now(), updated_at: now() };
  s.workspaces.set(ws.id, ws);
  s.members.push({ id: uuid(), workspace_id: ws.id, user: stripUser(demo), role: "owner", created_at: now() });
  // deputy·일반 멤버 시나리오(U9·U10)와 Director 교체 다이얼로그가 고를 상대가 필요하다.
  for (const [name, email] of [["서연", "seoyeon@colab.dev"], ["준호", "junho@colab.dev"]] as const) {
    const u: MockUser = { id: uuid(), email, display_name: name, avatar_url: null, created_at: now(), password: "password123" };
    s.users.set(u.id, u);
    s.members.push({ id: uuid(), workspace_id: ws.id, user: stripUser(u), role: "member", created_at: now() });
  }
  s.invites.set("demo-invite", {
    id: uuid(), token: "demo-invite", workspace_id: ws.id, role: "member", invited_by: demo.id,
    expires_at: new Date(Date.now() + 7 * 864e5).toISOString(), status: "pending",
  });
  s.invites.set("expired-invite", {
    id: uuid(), token: "expired-invite", workspace_id: ws.id, role: "member", invited_by: demo.id,
    expires_at: new Date(Date.now() - 864e5).toISOString(), status: "expired",
  });
  const rt = makeRuntime(ws.id, "demo-macbook");
  s.runtimes.set(rt.id, rt);
  for (const [name, role, desc] of [
    ["Lead", "lead", "팀을 이끌고 위임·종합한다"],
    ["Researcher", "researcher", "자료를 조사한다"],
  ] as const) {
    const a = makeAgent(ws.id, demo.id, name, role, desc);
    s.agents.set(a.id, a);
  }
  return s;
}

export function stripUser(u: MockUser | User): User {
  return { id: u.id, email: u.email, display_name: u.display_name, avatar_url: u.avatar_url ?? null, created_at: u.created_at };
}

export function makeRuntime(workspaceId: string, name: string): Runtime {
  const t = now();
  return {
    id: uuid(), workspace_id: workspaceId, name, host: `${name}.local`, status: "online", daemon_version: "0.1.0-mock",
    last_seen_at: t,
    capabilities: [
      // supported_options 를 광고하는 쪽(값이 있으면 그 값만 고를 수 있다)
      { kind: "claude_code", version: "2.1.258", adapter_version: "0.74.0", models: ["claude-sonnet-5", "claude-opus-5"], logged_in: true, protocol_version: 1, resume: true, usage: true, tool_disallow: true, brief_transport: "acp_meta_system_prompt", allow_once_missing: false, supported_options: { effort: ["low", "medium", "high", "xhigh"] } },
      // 광고하지 않는 쪽 — "광고 없음" 경로가 화면의 첫 상태다. usage=false·resume 미지원 문구도 함께 검증한다
      { kind: "hermes", version: "0.20.6", adapter_version: null, models: ["hermes-4"], logged_in: true, protocol_version: 1, resume: false, usage: false, tool_disallow: false, brief_transport: "instruction_file", allow_once_missing: true },
    ],
    // probe 최상위(머신 속성) — 런타임이 둘이어도 바이너리는 하나다(daemon-protocol §3)
    colab_cli: { present: true, version: "0.4.0" },
    repos: [{ path: "~/dev/colab", remote_url: "git@github.com:ingki3/agent-collabortion.git", branch: "main", clean: true }],
    max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null,
    paused_session_count: 0, created_at: t, updated_at: t,
  };
}

export function makeAgent(workspaceId: string, ownerId: string, name: string, role: Agent["role"], desc: string, profile?: { runtime_kind: Agent["profiles"][number]["runtime_kind"]; model: string }): Agent {
  const t = now();
  const id = uuid();
  return {
    id, workspace_id: workspaceId, name, role, role_description: desc, instructions: `You are ${name}.`, tools: [], owner_id: ownerId,
    respond_to: "workspace", respond_to_allowlist: [], avatar_url: null, budget_per_task: null, max_concurrent_tasks: 3,
    definition_source: null, definition_version: null, definition_update_available: null, status: "idle",
    profiles: [{ id: uuid(), agent_id: id, name: "default", runtime_kind: profile?.runtime_kind ?? "claude_code", model: profile?.model ?? "claude-sonnet-5", options: {}, env: {}, args: [], is_default: true, fallback_profile_id: null, created_at: t, updated_at: t }],
    invitable: { allowed: true, reason: null }, usage: { cost_usd: 0, task_count: 0 }, archived_at: null, created_at: t, updated_at: t,
  };
}

export function store(): Store {
  if (!globalThis.__colabMock) globalThis.__colabMock = seed();
  return globalThis.__colabMock;
}

export function resetStore(): void {
  globalThis.__colabMock = seed();
}

/**
 * 세션 참여자 파생 상태(FR-1.3) — task 상태에서 계산한다. `working` 은 **`running` 만**이다(W-5):
 * `dispatched`·`preparing` 은 아직 턴이 시작되지 않았다. `disabled`(respond_to: nobody)가 가장 먼저다.
 */
export function participantStatus(s: Store, sessionId: string, agentId: string): Participant["status"] {
  if (s.agents.get(agentId)?.respond_to === "nobody") return "disabled";
  const ts = [...s.tasks.values()].filter((t) => t.session_id === sessionId && t.agent_id === agentId).map((t) => t.status);
  if (ts.some((x) => x === "running")) return "working";
  if (ts.some((x) => x === "waiting_human")) return "waiting_human";
  return "idle";
}

/** SSE 발행 — 링 버퍼(백필) + 구독자에게 프레임. */
export function emit(s: Store, workspaceId: string, type: StreamEventType, payload: unknown, sessionId: string | null = null, ephemeral = false): void {
  const ev: StoredEvent = { id: ++s.eventSeq, type, workspace_id: workspaceId, session_id: sessionId, at: now(), payload, ephemeral };
  if (!ephemeral) {
    s.events.push(ev);
    if (s.events.length > 2000) s.events.splice(0, s.events.length - 2000);
  }
  const frame = sseFrame(ev);
  for (const sub of s.subs) {
    if (sub.workspace_id !== workspaceId) continue;
    if (sub.session_ids && sessionId && !sub.session_ids.includes(sessionId)) continue;
    try {
      sub.write(frame);
    } catch {
      s.subs.delete(sub);
    }
  }
}

export function sseFrame(ev: StoredEvent): string {
  const data = JSON.stringify({ id: String(ev.id), type: ev.type, at: ev.at, workspace_id: ev.workspace_id, session_id: ev.session_id, ephemeral: ev.ephemeral, payload: ev.payload });
  return `event: ${ev.type}\nid: ${ev.id}\ndata: ${data}\n\n`;
}

/**
 * 팀 템플릿 3종(FR-1.4 · SCREEN §4.7). 프리셋은 **역할·instruction 만** 담고 프로파일은 사용자의 런타임에 맞춰
 * 첫 실행 시 매핑한다 — `mapping` 은 요청 시점에 온라인 런타임 능력으로 계산해 채운다(`listAgentTemplates`).
 * G5 에서 Director 가 "템플릿에서 팀 생성까지 3분"을 실측하는 대상이다.
 */
export interface TemplateAgentSeed {
  key: string;
  name: string;
  role: Agent["role"];
  role_description: string;
  instructions: string;
  /** 이 역할이 선호하는 런타임 종류. 워크스페이스에 없으면 `unmapped` + 사유. */
  prefer: Runtime["capabilities"][number]["kind"];
}
export interface TemplateSeed {
  key: AgentTemplate["key"];
  name: string;
  description: string;
  version: string;
  agents: TemplateAgentSeed[];
}

export const TEMPLATES: readonly TemplateSeed[] = [
  {
    key: "research_team",
    name: "리서치 팀",
    description: "조사 → 정리 → 검토. Lead 가 위임하고 Writer 가 보고서를 제출합니다.",
    version: "1",
    agents: [
      { key: "lead", name: "Lead", role: "lead", role_description: "goal 을 쪼개 위임하고 결과를 종합한다", instructions: "너는 리서치 팀의 Lead 다. goal 을 조사 단위로 쪼개 참여자에게 위임하고, 결과를 종합해 Writer 에게 넘긴다.", prefer: "claude_code" },
      { key: "researcher", name: "Researcher", role: "researcher", role_description: "자료를 찾아 근거와 함께 정리한다", instructions: "너는 조사 담당이다. 출처를 반드시 남기고, 확인되지 않은 것은 확인되지 않았다고 쓴다.", prefer: "claude_code" },
      { key: "writer", name: "Writer", role: "writer", role_description: "조사 결과를 읽는 사람 기준으로 다시 쓴다", instructions: "너는 작성 담당이다. 조사 결과를 독자 기준으로 다시 쓰고 아티팩트로 제출한다.", prefer: "claude_code" },
    ],
  },
  {
    key: "dev_team",
    name: "개발 팀",
    description: "설계 → 구현 → 리뷰. worktree 격리와 diff 아티팩트에 맞춘 구성입니다.",
    version: "1",
    agents: [
      { key: "lead", name: "Lead", role: "lead", role_description: "작업을 쪼개 위임하고 통합한다", instructions: "너는 개발 팀의 Lead 다. 변경을 독립적인 단위로 쪼개 위임하고 충돌을 조정한다.", prefer: "claude_code" },
      { key: "engineer", name: "Engineer", role: "engineer", role_description: "코드를 쓰고 diff 를 제출한다", instructions: "너는 구현 담당이다. 작은 단위로 커밋하고 diff 를 아티팩트로 제출한다. 테스트 없이 제출하지 않는다.", prefer: "claude_code" },
      { key: "reviewer", name: "Reviewer", role: "reviewer", role_description: "제출된 diff 를 검토하고 승인·반려한다", instructions: "너는 리뷰 담당이다. 결함을 찾고 근거와 함께 승인 또는 반려한다. 반려에는 반드시 사유를 쓴다.", prefer: "hermes" },
    ],
  },
  {
    key: "content_team",
    name: "콘텐츠 팀",
    description: "기획 → 초안 → 교정. 문서·마케팅 산출물에 맞춘 구성입니다.",
    version: "1",
    agents: [
      { key: "lead", name: "Lead", role: "lead", role_description: "주제를 쪼개 위임하고 톤을 맞춘다", instructions: "너는 콘텐츠 팀의 Lead 다. 주제를 쪼개 위임하고 전체 톤을 맞춘다.", prefer: "claude_code" },
      { key: "writer", name: "Writer", role: "writer", role_description: "초안을 쓴다", instructions: "너는 초안 담당이다. 독자와 목적을 먼저 확인하고, 모르면 묻는다.", prefer: "claude_code" },
      { key: "reviewer", name: "Editor", role: "reviewer", role_description: "사실과 문장을 교정한다", instructions: "너는 교정 담당이다. 사실 오류를 먼저 잡고 그 다음 문장을 고친다.", prefer: "claude_code" },
    ],
  },
];

/** 워크스페이스의 온라인 런타임이 광고한 (kind → 모델 목록). 템플릿 매핑과 프로파일 편집기 모델 목록의 근거다. */
export function runtimeModels(s: Store, workspaceId: string, runtimeId?: string | null): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const r of s.runtimes.values()) {
    if (r.workspace_id !== workspaceId) continue;
    if (runtimeId && r.id !== runtimeId) continue;
    if (!runtimeId && r.status !== "online") continue;
    for (const c of r.capabilities) {
      if (!c.logged_in) continue;
      out.set(c.kind, [...new Set([...(out.get(c.kind) ?? []), ...(c.models ?? [])])]);
    }
  }
  return out;
}

/**
 * (kind → 옵션 키 → 허용 값). `RuntimeCapability.supported_options` 가 근거다.
 * **키가 없으면 "광고 없음"** 이고, 그러면 서버는 그 키를 거부한다(§8.2.6 "능력 범위 안") — 화면도 비활성으로 그린다.
 */
export function runtimeOptionRanges(s: Store, workspaceId: string, runtimeId?: string | null): Map<string, Record<string, string[]>> {
  const out = new Map<string, Record<string, string[]>>();
  for (const r of s.runtimes.values()) {
    if (r.workspace_id !== workspaceId) continue;
    if (runtimeId && r.id !== runtimeId) continue;
    if (!runtimeId && r.status !== "online") continue;
    for (const c of r.capabilities) {
      if (!c.logged_in) continue;
      const adv = c.supported_options ?? {};
      const cur = out.get(c.kind) ?? {};
      for (const [k, vs] of Object.entries(adv)) if (Array.isArray(vs) && vs.length) cur[k] = [...new Set([...(cur[k] ?? []), ...vs.map(String)])];
      out.set(c.kind, cur);
    }
  }
  return out;
}
