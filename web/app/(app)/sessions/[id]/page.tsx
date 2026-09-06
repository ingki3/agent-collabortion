"use client";
/**
 * S7 Session 상세 — **3열**(SCREEN §4.5). P1 은 중앙 열만이었고 P2 에서 좌·우열이 붙는다.
 *
 * 좌: 참여자 칩(파생 상태 6종 — 서버가 준 값을 그린다) + lane 보드(7상태 카드, 재진입·질문 배지, task 이력).
 * 중앙: 메시지 타임라인(kind 별 카드 · 질문 카드 스레드 · 에이전트 메시지의 활동 피드 5클래스) + 작성창.
 * 우: goal · 종료 조건 진행률 · 아티팩트 · 결정 기록 · 비용 · paused 배너(사유 5종).
 *
 * P3: 상단 액션(일시정지·재개·종료·참여자·Director 교체) · 타임라인의 **HITL 카드**(인박스와 같은 하위 컴포넌트) ·
 * lane 카드의 응답/예산 승인 연결. 권한이 없으면 숨기지 않고 비활성 + 사유(S7-P·S7-D, SCREEN §7).
 *
 * 실시간: 셸의 워크스페이스 SSE 하나를 구독하고 `session_id` 로 거른다(R4).
 * 좁은 화면에서는 좌·우열이 탭으로 접힌다.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Badge } from "@/components/Badge";
import { AgentChip } from "@/components/AgentChip";
import { MessageCard, authorName } from "@/components/MessageCard";
import { Composer, type ComposerAgent, type ComposerInput, type ComposerWarning } from "@/components/Composer";
import { ActivityFeed } from "@/components/ActivityFeed";
import { LaneBoard } from "@/components/LaneBoard";
import { SessionAside } from "@/components/SessionAside";
import { SessionActions } from "@/components/SessionActions";
import { ParticipantsDialog } from "@/components/ParticipantsDialog";
import { HitlCard } from "@/components/HitlCard";
import { ConnectionBanner } from "@/components/ConnectionBanner";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import type {
  Agent, Artifact, Decision, HitlRequest, HitlResponse, Lane, Member, Message, Participant, Session, StreamEvent,
  Task, TaskEvent, TriggerPreview,
} from "@/lib/api/types";

type Events = { events: TaskEvent[]; structured: boolean; loading: boolean };
type Col = "board" | "timeline" | "aside";

/** 에이전트 메시지의 "활동 보기" — 처음 펼칠 때 GET /tasks/{id}/events, 이후는 SSE 로 채워진다. */
function TaskActivity({ taskId, cache, load }: { taskId: string; cache: Record<string, Events>; load: (id: string) => void }) {
  useEffect(() => {
    if (!cache[taskId]) load(taskId);
  }, [taskId, cache, load]);
  const c = cache[taskId];
  return <ActivityFeed events={c?.events ?? []} structured={c?.structured ?? true} loading={!c || c.loading} title="이 run 의 활동" />;
}

function sortByTime(a: Message, b: Message) {
  return a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0;
}

export default function SessionPage() {
  const { id: sessionId } = useParams<{ id: string }>();
  const search = useSearchParams();
  const { workspace, me } = useAuth();
  const [session, setSession] = useState<Session | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [replies, setReplies] = useState<Record<string, Message[]>>({});
  const [events, setEvents] = useState<Record<string, Events>>({});
  const [agents, setAgents] = useState<Agent[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [lanes, setLanes] = useState<Lane[]>([]);
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null);
  const [decisions, setDecisions] = useState<Decision[] | null>(null);
  /** 세션의 HITL 요청 — 타임라인의 `hitl` 메시지가 `message_id` 로 자기 요청을 찾는다. */
  const [hitls, setHitls] = useState<HitlRequest[]>([]);
  const [showParticipants, setShowParticipants] = useState(false);
  const [participantWarnings, setParticipantWarnings] = useState<string[]>([]);
  const [typing, setTyping] = useState<Record<string, boolean>>({});
  const [deltas, setDeltas] = useState<Record<string, string>>({});
  const [replyTo, setReplyTo] = useState<{ id: string; authorName: string } | null>(null);
  /** "중단하고 다시 지시"(FR-3.4 B) — 작성창이 restart 모드로 바뀐다(U4-3). */
  const [restart, setRestart] = useState<{ laneId: string; agentName: string } | null>(null);
  const [draft, setDraft] = useState<{ content: string; nonce: number } | null>(null);
  const [confirmCancel, setConfirmCancel] = useState<Lane | null>(null);
  const [selectedLane, setSelectedLane] = useState<string | null>(null);
  const [col, setCol] = useState<Col>("timeline");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(Date.now());
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [s, page, ags, mems] = await Promise.all([
        api.get("/sessions/{sessionId}", { path: { sessionId } }),
        api.get("/sessions/{sessionId}/messages", { path: { sessionId }, query: { limit: 200 } }),
        api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }),
        api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id } }),
      ]);
      setSession(s);
      setMessages(page.items.filter((m) => !m.parent_id).sort(sortByTime));
      setAgents(ags.items);
      setMembers(mems.items);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [sessionId, workspace]);

  /** 좌·우열 데이터. 서버가 아직 안 켠 operation 은 조용히 비운다 — 화면은 빈 상태로 말한다(§7). */
  const loadSide = useCallback(async () => {
    const [l, a, d, h] = await Promise.allSettled([
      api.get("/sessions/{sessionId}/lanes", { path: { sessionId } }),
      api.get("/sessions/{sessionId}/artifacts", { path: { sessionId } }),
      api.get("/sessions/{sessionId}/decisions", { path: { sessionId } }),
      api.get("/sessions/{sessionId}/hitl-requests", { path: { sessionId }, query: { limit: 100 } }),
    ]);
    setLanes(l.status === "fulfilled" ? l.value : []);
    setArtifacts(a.status === "fulfilled" ? a.value : []);
    setDecisions(d.status === "fulfilled" ? d.value : []);
    setHitls(h.status === "fulfilled" ? (h.value.items ?? []) : []);
  }, [sessionId]);

  useEffect(() => {
    void load();
    void loadSide();
  }, [load, loadSide]);

  const loadReplies = useCallback(
    async (rootId: string) => {
      const page = await api.get("/sessions/{sessionId}/messages", { path: { sessionId }, query: { thread: rootId, limit: 200 } });
      setReplies((r) => ({ ...r, [rootId]: page.items.filter((m) => m.id !== rootId).sort(sortByTime) }));
    },
    [sessionId],
  );

  const loadEvents = useCallback(async (taskId: string) => {
    setEvents((c) => ({ ...c, [taskId]: { events: [], structured: true, loading: true } }));
    try {
      const r = await api.get("/tasks/{taskId}/events", { path: { taskId }, query: { limit: 200 } });
      setEvents((c) => ({ ...c, [taskId]: { events: r.items, structured: r.structured ?? true, loading: false } }));
    } catch {
      setEvents((c) => ({ ...c, [taskId]: { events: [], structured: true, loading: false } }));
    }
  }, []);

  const loadLaneTasks = useCallback(async (laneId: string): Promise<Task[]> => {
    return api.get("/lanes/{laneId}/tasks", { path: { laneId } });
  }, []);

  // ── 실시간 ──
  const onEvent = useCallback((ev: StreamEvent) => {
    // 워크스페이스 전체 스트림이므로 다른 세션의 이벤트는 버린다
    if (ev.session_id && ev.session_id !== sessionId) return;
    switch (ev.type) {
      case "message.created": {
        const m = ev.payload as unknown as Message;
        if (m.session_id !== sessionId) return;
        if (m.author_id) setDeltas((d) => { const n = { ...d }; delete n[m.author_id!]; return n; });
        if (m.parent_id) {
          const root = m.parent_id;
          setReplies((r) => (r[root] ? { ...r, [root]: r[root].some((x) => x.id === m.id) ? r[root] : [...r[root], m].sort(sortByTime) } : r));
          setMessages((ms) => ms.map((x) => (x.id === root ? { ...x, reply_count: (x.reply_count ?? 0) + 1 } : x)));
        } else {
          setMessages((ms) => (ms.some((x) => x.id === m.id) ? ms : [...ms, m].sort(sortByTime)));
        }
        break;
      }
      case "message.updated": {
        const m = ev.payload as unknown as Message;
        setMessages((ms) => ms.map((x) => (x.id === m.id ? { ...x, ...m } : x)));
        break;
      }
      case "task_event.appended": {
        const te = ev.payload as unknown as TaskEvent;
        setEvents((c) => {
          const cur = c[te.task_id];
          if (!cur) return c;
          if (cur.events.some((e) => e.id === te.id)) return c;
          return { ...c, [te.task_id]: { ...cur, events: [...cur.events, te] } };
        });
        break;
      }
      case "task_event.superseded": {
        const p = ev.payload as { task_id: string; event_id: string; superseded_by: string };
        setEvents((c) => {
          const cur = c[p.task_id];
          if (!cur) return c;
          return { ...c, [p.task_id]: { ...cur, events: cur.events.map((e) => (e.id === p.event_id ? { ...e, superseded_by: p.superseded_by } : e)) } };
        });
        break;
      }
      case "lane.updated": {
        const l = ev.payload as unknown as Lane;
        if (l.session_id !== sessionId) return;
        setLanes((cur) => (cur.some((x) => x.id === l.id) ? cur.map((x) => (x.id === l.id ? l : x)) : [...cur, l]));
        break;
      }
      case "hitl.created":
      case "hitl.updated": {
        const h = ev.payload as unknown as HitlRequest;
        if (h.session_id !== sessionId) return;
        setHitls((cur) => (cur.some((x) => x.id === h.id) ? cur.map((x) => (x.id === h.id ? h : x)) : [...cur, h]));
        break;
      }
      case "artifact.created": {
        const a = ev.payload as unknown as Artifact;
        setArtifacts((cur) => (cur ? [a, ...cur.filter((x) => x.id !== a.id)] : [a]));
        break;
      }
      case "decision.created": {
        const d = ev.payload as unknown as Decision;
        setDecisions((cur) => (cur ? [d, ...cur.filter((x) => x.id !== d.id)] : [d]));
        break;
      }
      case "session.completion_progress": {
        const p = ev.payload as { session_id?: string; completion_progress?: Session["completion_progress"] };
        if (!p.completion_progress) return;
        setSession((s) => (s ? { ...s, completion_progress: p.completion_progress! } : s));
        break;
      }
      case "participant.updated": {
        const p = ev.payload as unknown as Participant;
        setSession((s) => (s && s.participants ? { ...s, participants: s.participants.map((x) => (x.agent_id === p.agent_id ? { ...x, ...p } : x)) } : s));
        break;
      }
      case "session.updated": {
        const p = ev.payload as Partial<Session>;
        if (p.id && p.id !== sessionId) return;
        setSession((s) => (s ? { ...s, ...p, participants: p.participants ?? s.participants } : s));
        break;
      }
      case "cost.updated": {
        const p = ev.payload as { session_id?: string; cost_usd?: number; estimated?: boolean };
        if (p.session_id && p.session_id !== sessionId) return;
        if (typeof p.cost_usd !== "number") return;
        setSession((s) => (s ? { ...s, cost_usd: p.cost_usd!, cost_estimated: p.estimated ?? s.cost_estimated } : s));
        break;
      }
      case "agent.typing": {
        const p = ev.payload as { agent_id: string; typing: boolean };
        setTyping((t) => ({ ...t, [p.agent_id]: p.typing }));
        break;
      }
      case "message.delta": {
        const p = ev.payload as { agent_id: string; text: string };
        setDeltas((d) => ({ ...d, [p.agent_id]: (d[p.agent_id] ?? "") + p.text }));
        break;
      }
      default:
        break;
    }
  }, [sessionId]);
  const conn = useWorkspaceStream(workspace?.id, onEvent, { onResync: () => { void load(); void loadSide(); } });

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [messages.length, deltas]);

  /**
   * 인박스의 `run_failed` "다시 지시"는 `?restart_lane=` 로 이 화면에 온다 — **인박스에서 맥락 없이 새 지시를
   * 만들 수 없기 때문**이다(SCREEN §4.5 m6: 사람은 항상 맥락을 더해 다시 지시한다). 여기서 작성창을 연다.
   */
  const restartParam = search.get("restart_lane");
  useEffect(() => {
    if (!restartParam || lanes.length === 0) return;
    const lane = lanes.find((l) => l.id === restartParam);
    if (!lane) return;
    const a = agents.find((x) => x.id === lane.agent_id);
    setRestart({ laneId: lane.id, agentName: a?.name ?? "agent" });
    setDraft({ content: a ? `[@${a.name}](mention://agent/${a.id}) ` : "", nonce: Date.now() });
    setCol("timeline");
  }, [restartParam, lanes, agents]);

  const participants = session?.participants ?? [];
  const composerAgents = useMemo<ComposerAgent[]>(() => {
    const pids = new Set(participants.map((p) => p.agent_id));
    return agents.map((a) => ({ id: a.id, name: a.name, participant: pids.has(a.id) }));
  }, [agents, participants]);
  const composerMembers = useMemo(() => members.map((m) => ({ id: m.user.id, name: m.user.display_name })), [members]);
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const agentName = useCallback((id: string) => agentById.get(id)?.name ?? id.slice(0, 8), [agentById]);

  const preview = useCallback(
    (input: ComposerInput): Promise<TriggerPreview> =>
      api.post("/sessions/{sessionId}/messages/preview", {
        path: { sessionId },
        body: { content: input.content, parent_id: input.parentId, new_lane: input.newLane, suppress_agent_ids: input.suppressAgentIds },
      }),
    [sessionId],
  );

  async function submit(input: ComposerInput): Promise<ComposerWarning[]> {
    // restart 모드면 게시가 아니라 lane 재지시다 — 서버가 진행 중 턴을 취소하고 새 task 를 만든다(FR-3.4 B)
    if (restart) {
      const r = await api.post("/lanes/{laneId}/restart", {
        path: { laneId: restart.laneId },
        idempotencyKey: newIdempotencyKey(),
        body: { content: input.content },
      });
      setRestart(null);
      setLanes((cur) => cur.map((l) => (l.id === r.lane.id ? r.lane : l)));
      onEvent({ id: "", type: "message.created", at: r.message.created_at, payload: r.message as unknown as Record<string, unknown> });
      return [];
    }
    const r = await api.post("/sessions/{sessionId}/messages", {
      path: { sessionId },
      idempotencyKey: newIdempotencyKey(),
      body: { content: input.content, parent_id: input.parentId, new_lane: input.newLane, suppress_agent_ids: input.suppressAgentIds },
    });
    // 스트림보다 먼저 도착할 수 있으므로 직접 넣는다(중복은 id 로 걸러진다)
    onEvent({ id: "", type: "message.created", at: r.message.created_at, payload: r.message as unknown as Record<string, unknown> });
    setReplyTo(null);
    return r.warnings;
  }

  function beginRestart(lane: Lane) {
    const a = agentById.get(lane.agent_id);
    const mention = a ? `[@${a.name}](mention://agent/${a.id}) ` : "";
    setRestart({ laneId: lane.id, agentName: a?.name ?? "agent" });
    setReplyTo(null);
    setDraft({ content: mention, nonce: Date.now() });
    setCol("timeline");
  }

  async function doCancel(lane: Lane) {
    setBusy(true);
    try {
      const l = await api.post("/lanes/{laneId}/cancel", { path: { laneId: lane.id } });
      setLanes((cur) => cur.map((x) => (x.id === l.id ? l : x)));
      setConfirmCancel(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function resume(body: { limits?: { budget_usd?: number; time_limit?: string }; reset_loop_counters?: boolean }) {
    setBusy(true);
    try {
      const s = await api.post("/sessions/{sessionId}/resume", { path: { sessionId }, body });
      setSession(s);
      void loadSide();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  /**
   * 상단 액션의 "재개". **사유별 본문의 주인은 우열 배너다**(O6: 예산은 새 상한, 시간은 연장 시간을 받는다) —
   * 여기서 빈 본문으로 보내면 계약이 "생략 시 현재 소진액 이상" 을 요구하는 예산 재개가 늘 422 가 된다.
   * 그래서 입력이 필요한 사유는 배너로 데려가고, 입력이 없는 사유만 여기서 바로 재개한다.
   */
  function topResume() {
    const r = session?.paused_reason;
    if (r === "budget" || r === "time") {
      setCol("aside");
      const el = document.querySelector('[data-testid="paused-banner"]');
      el?.scrollIntoView({ block: "center" });
      el?.classList.add("msg--flash");
      setTimeout(() => el?.classList.remove("msg--flash"), 1200);
      return;
    }
    // 루프는 카운터를 리셋하고 재개한다(계약 resumeSession `reset_loop_counters`, 기본 true).
    return resume(r === "loop" ? { reset_loop_counters: true } : {});
  }

  async function pause() {
    setBusy(true);
    try {
      setSession(await api.post("/sessions/{sessionId}/pause", { path: { sessionId } }));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  /** 종료(`manual`) — 진행 중 lane 이 있으면 `confirm: true` 가 있어야 서버가 받는다(409 running_lanes). */
  async function complete(confirm: boolean) {
    setBusy(true);
    try {
      setSession(await api.post("/sessions/{sessionId}/complete", { path: { sessionId }, body: { confirm } }));
      void loadSide();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function cancelSession(reason: string) {
    setBusy(true);
    try {
      setSession(await api.post("/sessions/{sessionId}/cancel", { path: { sessionId }, body: reason ? { reason } : {} }));
      void loadSide();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function changeDirector(userId: string) {
    setBusy(true);
    try {
      setSession(await api.put("/sessions/{sessionId}/director", { path: { sessionId }, body: { director_user_id: userId } }));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  /** HITL 응답 — 계약 필수 헤더 `Idempotency-Key` 를 보낸다. 두 번째 응답은 오류가 아니라 무시된다(E7-08). */
  async function respondHitl(id: string, body: HitlResponse) {
    setBusy(true);
    try {
      const r = await api.post("/hitl-requests/{hitlRequestId}/response", {
        path: { hitlRequestId: id },
        idempotencyKey: newIdempotencyKey(),
        body,
      });
      setHitls((cur) => cur.map((x) => (x.id === r.hitl_request.id ? r.hitl_request : x)));
      if (r.ignored) setError("이미 답변된 요청이라 이 응답을 무시했습니다 — 첫 응답이 유지됩니다.");
      void loadSide();
    } finally {
      setBusy(false);
    }
  }

  async function addParticipant(agentId: string, profileId: string | null) {
    setBusy(true);
    try {
      const p = await api.post("/sessions/{sessionId}/participants", {
        path: { sessionId },
        body: { agent_id: agentId, profile_id: profileId },
      });
      setParticipantWarnings(p.warnings ?? []);
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function removeParticipant(agentId: string) {
    setBusy(true);
    try {
      await api.delete("/sessions/{sessionId}/participants/{agentId}", { path: { sessionId, agentId } });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function setAssignee(agentId: string) {
    setBusy(true);
    try {
      await api.patch("/sessions/{sessionId}/participants/{agentId}", { path: { sessionId, agentId }, body: { assignee: true } });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  /**
   * lane 카드의 "응답하러 가기" · "계속 진행 승인" — 그 lane 의 HITL 카드로 타임라인을 옮긴다.
   * lane 이 카드 메시지를 모르면(`hitl_request_id` 만 있는 경우) 요청 목록에서 찾는다.
   */
  function openLaneHitl(lane: Lane) {
    const h = hitls.find((x) => x.id === lane.hitl_request_id) ?? hitls.find((x) => x.lane_id === lane.id && x.status === "open");
    if (h?.message_id) jumpToMessage(h.message_id);
    else setError("이 lane 의 HITL 카드를 찾지 못했습니다 — Inbox 에서 응답하세요.");
  }

  function jumpToMessage(messageId: string) {
    setCol("timeline");
    const el = document.querySelector(`[data-message-id="${messageId}"]`);
    el?.scrollIntoView({ block: "center" });
    el?.classList.add("msg--flash");
    setTimeout(() => el?.classList.remove("msg--flash"), 1200);
  }

  if (!workspace) return null;
  if (error && !session) {
    return (
      <div>
        <p className="problem">{error}</p>
        <Link href="/sessions" className="btn">Sessions 로</Link>
      </div>
    );
  }
  if (!session) return <p className="muted">불러오는 중…</p>;

  const closed = session.status === "completed" || session.status === "cancelled";
  const isDirector = session.my_role === "director" || session.my_role === "deputy";
  // 종료 확인 문구가 개수를 명시한다(SCREEN §4.5 상단 액션) — 계약 removeParticipant 와 같은 4상태다.
  const runningLanes = lanes.filter((l) => ["queued", "running", "waiting_human", "paused"].includes(l.status)).length;
  const hitlByMessage = new Map(hitls.filter((h) => h.message_id).map((h) => [h.message_id!, h]));
  const typingAgents = Object.entries(typing).filter(([, v]) => v).map(([id]) => agentById.get(id)?.name ?? "agent");
  const laneOfMessage = (m: Message) => (m.lane_id ? lanes.find((l) => l.id === m.lane_id) : undefined);

  return (
    <div className="s7" data-testid="session-detail" data-session-id={session.id} data-col={col}>
      <ConnectionBanner state={conn} />
      <header className="s7__head">
        <div className="row" style={{ gap: 10 }}>
          <Link href="/sessions" className="small muted-3">← Sessions</Link>
          <h1 style={{ margin: 0, fontSize: 18 }} data-testid="session-title">{session.title}</h1>
          <Badge kind="session" value={session.status} data-testid="session-status" />
          {session.status === "paused" && session.paused_reason && (
            <span className="small muted-3" data-testid="session-paused-reason">사유: {session.paused_reason}</span>
          )}
          <span className="s7__spacer" />
          <SessionActions
            session={session}
            runningLanes={runningLanes}
            members={members}
            onPause={pause}
            onResume={topResume}
            onComplete={complete}
            onCancel={cancelSession}
            onOpenParticipants={() => setShowParticipants((v) => !v)}
            onChangeDirector={changeDirector}
            busy={busy}
          />
        </div>
        {showParticipants && (
          <div className="s7__panel">
            <ParticipantsDialog
              participants={participants}
              agents={agents}
              lanes={lanes}
              assigneeAgentId={session.assignee_agent_id}
              canManage={session.my_role === "director"}
              warnings={participantWarnings}
              onAdd={addParticipant}
              onRemove={removeParticipant}
              onSetAssignee={setAssignee}
              onClose={() => { setShowParticipants(false); setParticipantWarnings([]); }}
              busy={busy}
            />
          </div>
        )}
        <p className="muted small" style={{ margin: "4px 0 8px" }} data-testid="session-goal">{session.goal}</p>
        <nav className="s7__tabs" aria-label="열 전환">
          {(["board", "timeline", "aside"] as Col[]).map((c) => (
            <button key={c} type="button" className={`s7__tab${col === c ? " s7__tab--on" : ""}`} onClick={() => setCol(c)} data-testid={`tab-${c}`}>
              {c === "board" ? `lane ${lanes.length}` : c === "timeline" ? "타임라인" : "진행"}
            </button>
          ))}
        </nav>
      </header>

      <div className="s7__cols">
        <section className="s7__left" data-testid="s7-left">
          <h2 className="s7__h">참여자</h2>
          <div className="s7__chips" data-testid="participants">
            {participants.map((p) => (
              <AgentChip
                key={p.agent_id}
                agentId={p.agent_id}
                name={p.agent.name}
                role={p.agent.role}
                status={p.status}
                statusNote={p.status_note}
                profile={p.profile ? `${p.profile.runtime_kind} · ${p.profile.model}` : null}
                isAssignee={p.is_assignee}
                archived={agentById.get(p.agent_id)?.archived_at != null}
                size="md"
              />
            ))}
            {participants.length === 0 && <span className="small muted-3">참여 에이전트 없음</span>}
          </div>
          <h2 className="s7__h">Lane 보드</h2>
          <LaneBoard
            lanes={lanes}
            selected={false}
            now={now}
            disabledReason={isDirector ? undefined : "Director·deputy 만 할 수 있습니다"}
            loadTasks={loadLaneTasks}
            onRestart={beginRestart}
            onCancel={(l) => setConfirmCancel(l)}
            onOpenQuestion={jumpToMessage}
            onRespondHitl={openLaneHitl}
            onApproveBudget={openLaneHitl}
            onSelect={(l) => setSelectedLane((cur) => (cur === l.id ? null : l.id))}
          />
          {confirmCancel && (
            <div className="s7__confirm" role="dialog" aria-label="lane 중단 확인" data-testid="cancel-confirm">
              <p className="small">이 lane 을 중단합니다. 새 지시 없이 종료됩니다.</p>
              <p className="small muted-3">되돌리기 어려운 작업 중이면 최대 30초 보류 후 종료됩니다.</p>
              <div className="row">
                <button type="button" className="btn btn--sm btn--primary" disabled={busy} onClick={() => void doCancel(confirmCancel)} data-testid="cancel-confirm-yes">중단</button>
                <button type="button" className="btn btn--sm" onClick={() => setConfirmCancel(null)}>취소</button>
              </div>
            </div>
          )}
        </section>

        <section className="s7__center">
          <div className="s7__timeline" data-testid="timeline">
            {messages.length === 0 && (
              <div className="empty" data-testid="timeline-empty">
                <div className="empty__title">아직 메시지가 없습니다</div>
                <div className="empty__body">@로 에이전트를 불러 시작하세요. 멘션 없이 보내면 assignee 에게 갑니다.</div>
              </div>
            )}
            {messages.map((m) => {
              const agentMsg = m.author_type === "agent" && m.source_task_id;
              const askee = m.kind === "blocked_q" ? m.mentions.find((x) => x.kind === "agent")?.display_name : undefined;
              const lane = laneOfMessage(m);
              const dim = selectedLane != null && lane?.id !== selectedLane;
              const hitl = hitlByMessage.get(m.id);
              // `hitl` 메시지는 **HITL 카드**로 그린다(SCREEN §4.5 중앙 표). 본문은 인박스와 같은 하위 컴포넌트다.
              if (m.kind === "hitl" && hitl) {
                return (
                  <div key={m.id} className={dim ? "s7__dim" : undefined} data-message-id={m.id}>
                    <HitlCard
                      request={hitl}
                      onRespond={(body) => respondHitl(hitl.id, body)}
                      budget={hitl.purpose === "budget" ? { current: hitl.budget_override_usd, spent: session.cost_usd } : null}
                      busy={busy}
                    />
                  </div>
                );
              }
              return (
                <div key={m.id} className={dim ? "s7__dim" : undefined}>
                  <MessageCard
                    message={m}
                    replies={replies[m.id]}
                    onLoadReplies={loadReplies}
                    onReply={(root) => { setRestart(null); setReplyTo({ id: root.id, authorName: authorName(root) }); }}
                    activity={agentMsg ? <TaskActivity taskId={m.source_task_id!} cache={events} load={loadEvents} /> : undefined}
                    askee={askee}
                    now={now}
                  />
                </div>
              );
            })}
            {Object.entries(deltas).map(([agentId, text]) => (
              <article key={agentId} className="msg" data-testid="message-delta">
                <div className="msg__head">
                  <span className="msg__author msg__author--agent">{agentById.get(agentId)?.name ?? "agent"}</span>
                  <span className="msg__meta">작성 중…</span>
                </div>
                <div className="msg__body">{text}▍</div>
              </article>
            ))}
            {typingAgents.length > 0 && (
              <p className="small muted-3" data-testid="typing">
                {typingAgents.map((n) => `@${n}`).join(", ")} 입력 중…
              </p>
            )}
            <div ref={bottomRef} />
          </div>

          <footer className="s7__composer">
            {restart && (
              <div className="row" style={{ justifyContent: "space-between" }}>
                <span />
                <button type="button" className="msg__link" onClick={() => { setRestart(null); setDraft({ content: "", nonce: Date.now() }); }} data-testid="restart-cancel">
                  재지시 취소
                </button>
              </div>
            )}
            <Composer
              agents={composerAgents}
              members={composerMembers}
              replyTo={restart ? null : replyTo}
              onCancelReply={() => setReplyTo(null)}
              onPreview={restart ? undefined : preview}
              onSubmit={submit}
              draft={draft}
              disabled={closed}
              disabledReason={closed ? "종료된 세션에는 게시할 수 없습니다" : undefined}
              notice={
                restart
                  ? `전송하면 @${restart.agentName} 의 진행 중인 턴을 취소하고 이 메시지만으로 다시 시작합니다`
                  : session.status === "paused"
                    ? "일시정지 중 — 게시는 되지만 재개 후 처리됩니다"
                    : undefined
              }
            />
          </footer>
        </section>

        <section className="s7__right" data-testid="s7-right">
          <SessionAside
            session={session}
            artifacts={artifacts}
            decisions={decisions}
            agentName={agentName}
            busy={busy}
            onResume={isDirector ? resume : undefined}
            onRebind={() => setError("재바인딩 다이얼로그(S17)는 P4 입니다 — Runtimes 화면에서 진행하세요.")}
            onCancelSession={isDirector ? () => void cancelSession("런타임 오프라인 — 재바인딩 대신 종료") : undefined}
          />
        </section>
      </div>
      {error && session && <p className="problem" role="alert" style={{ marginTop: 12 }}>{error}</p>}
      <style>{`
        .s7 { display: flex; flex-direction: column; min-height: calc(100vh - 48px - 48px); }
        .s7__head { padding-bottom: 10px; border-bottom: 1px solid var(--line); margin-bottom: 12px; }
        .s7__spacer { flex: 1; }
        .s7__cols { display: grid; grid-template-columns: 268px minmax(0, 1fr) 268px; gap: 16px; align-items: start; }
        .s7__left, .s7__right { position: sticky; top: 12px; max-height: calc(100vh - 140px); overflow: auto; display: flex; flex-direction: column; gap: 8px; }
        .s7__center { display: flex; flex-direction: column; min-width: 0; }
        .s7__h { margin: 4px 0 2px; font-size: 12px; font-weight: 600; color: var(--ink-3); }
        .s7__chips { display: flex; flex-direction: column; gap: 4px; }
        .s7__timeline { flex: 1; display: flex; flex-direction: column; gap: 6px; padding-bottom: 12px; }
        .s7__composer { position: sticky; bottom: 0; background: var(--bg); padding: 8px 0 4px; }
        .s7__dim { opacity: 0.4; }
        .s7__tabs { display: none; gap: 6px; }
        .s7__tab { border: 1px solid var(--line); background: var(--bg); border-radius: 999px; padding: 3px 10px; font-size: 12px; cursor: pointer; }
        .s7__tab--on { border-color: var(--ink); font-weight: 600; }
        .s7__panel { position: relative; display: flex; justify-content: flex-end; }
        .s7__panel .s7-actions__dialog { position: static; width: 380px; margin-top: 8px; }
        .s7__confirm { border: 1px solid var(--s-fail); border-radius: 8px; padding: 8px 10px; background: color-mix(in srgb, var(--s-fail) var(--soft-alpha), transparent); }
        .s7__confirm p { margin: 0 0 6px; }
        .msg--flash { outline: 2px solid var(--s-block); border-radius: 8px; }
        /* 좁은 화면에서는 좌·우열이 접히고 탭으로 전환된다(SCREEN §4.5) */
        @media (max-width: 1100px) {
          .s7__tabs { display: flex; margin-top: 6px; }
          .s7__cols { grid-template-columns: minmax(0, 1fr); }
          .s7__left, .s7__right { position: static; max-height: none; }
          .s7[data-col="timeline"] .s7__left, .s7[data-col="timeline"] .s7__right,
          .s7[data-col="board"] .s7__center, .s7[data-col="board"] .s7__right,
          .s7[data-col="aside"] .s7__center, .s7[data-col="aside"] .s7__left { display: none; }
        }
      `}</style>
    </div>
  );
}
