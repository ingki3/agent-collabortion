"use client";
/**
 * S7 Session 상세 — P1 은 **중앙 열만**(SCREEN §4.5): 헤더(제목·상태·goal·참여자 칩) · 타임라인(Message Card, 스레드 접기,
 * 에이전트 메시지의 "활동 보기" = task_event 원본 레일) · 작성창(@ 자동완성, 비참여자 경고 칩 E1-04).
 * 실시간: 세션 범위 SSE — message.created/updated · task_event.appended/superseded · participant.updated · session.updated ·
 * agent.typing · message.delta. 새로고침 없이 갱신된다. 좌·우열(lane 보드·진행)은 P2.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { Badge } from "@/components/Badge";
import { AgentChip } from "@/components/AgentChip";
import { MessageCard, authorName } from "@/components/MessageCard";
import { Composer, type ComposerAgent, type ComposerWarning } from "@/components/Composer";
import { ActivityRail } from "@/components/ActivityRail";
import { ConnectionBanner } from "@/components/ConnectionBanner";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useStream } from "@/lib/realtime/stream";
import type { Agent, Member, Message, Participant, Session, StreamEvent, TaskEvent } from "@/lib/api/types";

type Events = { events: TaskEvent[]; structured: boolean; loading: boolean };

/** 에이전트 메시지의 "활동 보기" — 처음 펼칠 때 GET /tasks/{id}/events, 이후는 SSE 로 채워진다. */
function TaskActivity({ taskId, cache, load }: { taskId: string; cache: Record<string, Events>; load: (id: string) => void }) {
  useEffect(() => {
    if (!cache[taskId]) load(taskId);
  }, [taskId, cache, load]);
  const c = cache[taskId];
  return <ActivityRail events={c?.events ?? []} structured={c?.structured ?? true} loading={!c || c.loading} />;
}

function sortByTime(a: Message, b: Message) {
  return a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0;
}

export default function SessionPage() {
  const { id: sessionId } = useParams<{ id: string }>();
  const { workspace, me } = useAuth();
  const [session, setSession] = useState<Session | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [replies, setReplies] = useState<Record<string, Message[]>>({});
  const [events, setEvents] = useState<Record<string, Events>>({});
  const [agents, setAgents] = useState<Agent[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [typing, setTyping] = useState<Record<string, boolean>>({});
  const [deltas, setDeltas] = useState<Record<string, string>>({});
  const [replyTo, setReplyTo] = useState<{ id: string; authorName: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
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

  useEffect(() => {
    void load();
  }, [load]);

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

  // ── 실시간 ──
  const onEvent = useCallback((ev: StreamEvent) => {
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
  const conn = useStream(workspace?.id, onEvent, { sessionIds: [sessionId], onResync: () => void load() });

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [messages.length, deltas]);

  const participants = session?.participants ?? [];
  const composerAgents = useMemo<ComposerAgent[]>(() => {
    const pids = new Set(participants.map((p) => p.agent_id));
    return agents.map((a) => ({ id: a.id, name: a.name, participant: pids.has(a.id) }));
  }, [agents, participants]);
  const composerMembers = useMemo(() => members.map((m) => ({ id: m.user.id, name: m.user.display_name })), [members]);
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);

  async function submit(input: { content: string; parentId: string | null; suppressAgentIds: string[] }): Promise<ComposerWarning[]> {
    const r = await api.post("/sessions/{sessionId}/messages", {
      path: { sessionId },
      idempotencyKey: newIdempotencyKey(),
      body: { content: input.content, parent_id: input.parentId, suppress_agent_ids: input.suppressAgentIds },
    });
    // 스트림보다 먼저 도착할 수 있으므로 직접 넣는다(중복은 id 로 걸러진다)
    onEvent({ id: "", type: "message.created", at: r.message.created_at, payload: r.message as unknown as Record<string, unknown> });
    setReplyTo(null);
    return r.warnings;
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

  const composerDisabled = session.status === "completed" || session.status === "cancelled";
  const typingAgents = Object.entries(typing).filter(([, v]) => v).map(([id]) => agentById.get(id)?.name ?? "agent");

  return (
    <div className="s7" data-testid="session-detail" data-session-id={session.id}>
      <ConnectionBanner state={conn} />
      <header className="s7__head">
        <div className="row" style={{ gap: 10 }}>
          <Link href="/sessions" className="small muted-3">← Sessions</Link>
          <h1 style={{ margin: 0, fontSize: 18 }} data-testid="session-title">{session.title}</h1>
          <Badge kind="session" value={session.status} data-testid="session-status" />
          {session.cost_usd > 0 && (
            <span className="small muted-3">${session.cost_usd.toFixed(2)}{session.limits.budget_usd ? ` / $${session.limits.budget_usd}` : ""}{session.cost_estimated ? " · 추정" : ""}</span>
          )}
        </div>
        <p className="muted small" style={{ margin: "4px 0 8px" }} data-testid="session-goal">{session.goal}</p>
        <div className="row" data-testid="participants">
          {participants.map((p) => (
            <AgentChip
              key={p.agent_id}
              name={p.agent.name}
              role={p.agent.role}
              status={p.status}
              statusNote={p.status_note}
              profile={p.profile ? `${p.profile.runtime_kind} · ${p.profile.model}` : null}
              isAssignee={p.is_assignee}
              size="sm"
            />
          ))}
          {participants.length === 0 && <span className="small muted-3">참여 에이전트 없음</span>}
        </div>
      </header>

      <section className="s7__timeline" data-testid="timeline">
        {messages.length === 0 && (
          <div className="empty" data-testid="timeline-empty">
            <div className="empty__title">아직 메시지가 없습니다</div>
            <div className="empty__body">@로 에이전트를 불러 시작하세요. 멘션 없이 보내면 assignee 에게 갑니다.</div>
          </div>
        )}
        {messages.map((m) => {
          const agentMsg = m.author_type === "agent" && m.source_task_id;
          const askee = m.kind === "blocked_q" ? m.mentions.find((x) => x.kind === "agent")?.display_name : undefined;
          return (
            <MessageCard
              key={m.id}
              message={m}
              replies={replies[m.id]}
              onLoadReplies={loadReplies}
              onReply={(root) => setReplyTo({ id: root.id, authorName: authorName(root) })}
              activity={agentMsg ? <TaskActivity taskId={m.source_task_id!} cache={events} load={loadEvents} /> : undefined}
              askee={askee}
              now={now}
            />
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
      </section>

      <footer className="s7__composer">
        <Composer
          agents={composerAgents}
          members={composerMembers}
          replyTo={replyTo}
          onCancelReply={() => setReplyTo(null)}
          onSubmit={submit}
          disabled={composerDisabled}
          disabledReason={composerDisabled ? "종료된 세션에는 게시할 수 없습니다" : undefined}
        />
      </footer>
      <style>{`
        .s7 { display: flex; flex-direction: column; min-height: calc(100vh - 48px - 48px); max-width: 760px; }
        .s7__head { padding-bottom: 12px; border-bottom: 1px solid var(--line); margin-bottom: 12px; }
        .s7__timeline { flex: 1; display: flex; flex-direction: column; gap: 6px; padding-bottom: 12px; }
        .s7__composer { position: sticky; bottom: 0; background: var(--bg); padding: 8px 0 4px; }
      `}</style>
    </div>
  );
}
