/**
 * 목 API 저장소 — 서버 스트림(S)이 붙기 전에 웹을 단독으로 검증하기 위한 **인메모리 서버**(COLAB_MOCK_API=1).
 * openapi.yaml 의 P1 범위 operation 만 구현한다. 실서버가 오면 next.config 의 rewrite 가 :8080 으로 보내고 이 코드는 쓰이지 않는다.
 * 계약(응답 형태·오류 코드·SSE 프레임)은 openapi 를 그대로 따른다 — 화면이 목에만 맞게 되는 것을 막기 위해서다.
 */
import type {
  Agent, Member, Message, Pairing, Participant, Runtime, Session, StreamEventType, TaskEvent, User, Workspace,
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
    idem: new Map(), events: [], eventSeq: 0, subs: new Set(),
  };
  // 데모 워크스페이스: 초대 링크(S3)·비참여 에이전트 경고(E1-04) 검증용
  const demo: MockUser = { id: uuid(), email: "demo@colab.dev", display_name: "데모", avatar_url: null, created_at: now(), password: "password123" };
  s.users.set(demo.id, demo);
  const ws: Workspace = { id: uuid(), name: "데모팀", slug: "demo", created_at: now(), updated_at: now() };
  s.workspaces.set(ws.id, ws);
  s.members.push({ id: uuid(), workspace_id: ws.id, user: stripUser(demo), role: "owner", created_at: now() });
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
      { kind: "claude_code", version: "2.0.0", models: ["claude-sonnet-5", "claude-opus-5"], logged_in: true, transport: ["acp"], usage_reporting: true, options: { effort: ["low", "medium", "high"] } },
    ],
    repos: [], max_concurrent_tasks: null, running_task_count: 0, workdir_disk_bytes: 0, offline_since: null, grace_ends_at: null,
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

/** 세션 참여자 파생 상태(FR-1.3) — task 상태에서 계산한다. */
export function participantStatus(s: Store, sessionId: string, agentId: string): Participant["status"] {
  const ts = [...s.tasks.values()].filter((t) => t.session_id === sessionId && t.agent_id === agentId).map((t) => t.status);
  if (ts.some((x) => x === "running" || x === "dispatched" || x === "preparing")) return "working";
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
