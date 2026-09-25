"use client";
/**
 * S7 방 화면(`/rooms/:id`) · S22 미션 패널(`/rooms/:id?work=:workId`) — SCREEN §4.6 · §4.8 (T-R2-W2).
 *
 * 3열: 좌(참여자 한 목록 + 서브 미션 보드) · 가운데(타임라인 + 작성창) · 우((가) 미션 칸 ─ 굵은 구분선 ─ (나) 방 전체 칸, 기본 접힘).
 * 좁은 화면(≤1100px)은 탭 넷 — 「타임라인 · 보드 · 미션 · 방」(§4.8). `?work=` 로 들어오면 미션 탭이 열린 채 시작한다.
 *
 * **미션 칩은 거르개이자 선택자**다(§4.6): 선택(`ChipSel`, URL `?work=`)이 바뀌면 ① 타임라인·보드가 그 미션 것만 남고 ② 우열 미션 칸이
 * 그 미션으로 바뀐다. 두 곳의 값은 `lib/room-view.ts` 가 같은 선택에서 낸다 — 연동이 코드 한 곳에서 정해진다.
 *
 * 데이터: `getRoom` · `listWorks` · `listRoomParticipants` · `getWork`(우열) · 메시지·서브 미션·아티팩트·결정·확인 요청은 방 id 로
 * `/rooms/{roomId}/…`(v0.3.0 R4 — 옛 세션 경로는 지워졌다). 메시지는 계약대로 `work_id`·`no_work`·`around_message_id` 를 보내고 **받은 것을
 * `work_id` 로 한 번 더 거른다** — 서버가 세 파라미터를 아직 안 읽는다(Lead 판정 A, 서버 후속 T-S-wt).
 * 실시간: 셸의 워크스페이스 SSE 하나를 구독하고 봉투의 `room_id` 로 거른다 — 미션 단위 거르기는 `payload.work_id` 로 클라이언트가(§6).
 */
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { MessageBody, MessageCard, authorName, type ConversationSlot, type MessageLayerSlots } from "@/components/MessageCard";
import { ArtifactRef, DetailFold, ProcessFold, TimelineViewToggle, WorkingRow, type TimelineView } from "@/components/MessageLayers";
import { Composer, type ComposerAgent, type ComposerInput, type ComposerWarning } from "@/components/Composer";
import { ActivityFeed } from "@/components/ActivityFeed";
import { LaneBoard } from "@/components/LaneBoard";
import { HitlCard } from "@/components/HitlCard";
import { pairHitl, unpairedHitlCards, upsertHitl } from "@/lib/hitl-pairing";
import { ConnectionBanner } from "@/components/ConnectionBanner";
import { RoomParticipantsDialog } from "@/components/RoomParticipantsDialog";
import { RoomQueryDialogs, useRoomDialogQuery } from "@/components/RoomQueryDialogs";
import { ArchiveRoomDialog, DeleteRoomDialog } from "@/components/RoomDialogs";
import { RoomHead, type PickedRange } from "@/components/RoomHead";
import { RoomBlockedBanner } from "@/components/RoomBlockedBanner";
import { RoomParticipants } from "@/components/RoomParticipants";
import { WorkChipRow, WorkLabel } from "@/components/WorkChipRow";
import { WorkPanel } from "@/components/WorkPanel";
import { RoomPanel } from "@/components/RoomPanel";
import { CreateWorkDialog } from "@/components/CreateWorkDialog";
import { ChangeDirectorDialog, FixWorkConditionDialog } from "@/components/WorkEditDialogs";
import { DisabledHint } from "@/components/PageHead";
import { Slot, slotText } from "@/components/Slot";
import { api, errorMessage, isApiError, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { emptyTurnNote, isEmptyTurn, isTaskLive } from "@/lib/feed";
import { roomProcessSlices, workingTasks, type ProcessSlices, type ProcessWindow } from "@/lib/process-slice";
import { useMarkRoomRead } from "@/lib/unread";
import { isLayered, messageLayers, summarizeProcess, timelineViewKey } from "@/lib/message-layers";
import { groupsWith, speechOf, type ConversationCtx, type Speech } from "@/lib/conversation";
import {
  BOARD_FOLDED, filterLanes, isAuditView, isOpenWork, matchesSel, needsMe, panelMode, parseSel, pausedLayer, postBlockedBy, sameSel, selParam,
  type ChipSel,
} from "@/lib/room-view";
import {
  ROOM_CENTER, ROOM_HEAD, ROOM_LEFT, ROOM_NOTICES, ROOM_TABS, SUMMARY_PICK, WORK_CHIPS, WORK_SELECTOR, roomDefaultsLine,
} from "@/lib/wording";
import type {
  Agent, Artifact, Decision, HitlRequest, HitlResponse, Lane, LaneStatus, Member, Message, Room, RoomParticipant, Runtime,
  StreamEvent, Task, TaskEvent, TriggerPreview, Work, WorkListItem,
} from "@/lib/api/types";

/** `task` — 「진행 중…」 판정(T-FEED)의 task 상태·attempt. getTask 가 실패하면 null(이벤트만으로 판정), `task.updated` 로 갱신. */
type Events = { events: TaskEvent[]; structured: boolean; loading: boolean; task?: Pick<Task, "status" | "attempt"> | null };
type Col = "timeline" | "board" | "work" | "room";
const COLS: Col[] = ["timeline", "board", "work", "room"];

/** `slice` — 메시지의 조각(T-FEED A). 없으면 task 전체(서브 미션 카드의 할 일 이력). */
function TaskActivity({ taskId, cache, load, slice }: { taskId: string; cache: Record<string, Events>; load: (id: string) => void; slice?: ProcessWindow | null }) {
  useEffect(() => {
    if (!cache[taskId]) load(taskId);
  }, [taskId, cache, load]);
  const c = cache[taskId];
  return (
    <ActivityFeed
      events={c?.events ?? []} structured={c?.structured ?? true} loading={!c || c.loading} title="이 작업의 활동"
      taskStatus={c?.task?.status} attempt={c?.task?.attempt} slice={slice}
    />
  );
}

const byTime = (a: Message, b: Message) => (a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0);
/** 방마다 기억하는 펼침 — 브라우저 저장소가 막혀 있으면 기본값(접힘)으로. */
function readLocal<T>(key: string, fallback: T): T {
  try {
    const v = window.localStorage.getItem(key);
    return v == null ? fallback : (JSON.parse(v) as T);
  } catch {
    return fallback;
  }
}
function writeLocal(key: string, v: unknown) {
  try {
    window.localStorage.setItem(key, JSON.stringify(v));
  } catch {
    /* 저장 못 해도 화면은 그대로 돈다 */
  }
}
/** 메시지 id → 카드에서 따로 펼치거나 접은 층. 없으면 보기 전환을 따른다(작업 내용) · 접힘(작업 과정). */
type Folds = Record<string, { detail?: boolean; process?: boolean }>;
/**
 * 아티팩트 ↔ 메시지 — 계약에 메시지가 가리키는 아티팩트 칸은 없다. 잇는 수단은 `Artifact.submitted_by_task_id` = `Message.source_task_id`
 * (같은 턴이 제출하고 말했다) 하나뿐이다. 한 턴이 메시지를 여럿 남기면 **제출 시각 이후의 첫 메시지**(없으면 그 턴의 마지막 메시지)에 붙인다
 * — 「초안을 올렸습니다」는 제출 뒤에 온다.
 */
function artifactsByMessage(msgs: Message[], artifacts: Artifact[] | null): Map<string, Artifact[]> {
  const out = new Map<string, Artifact[]>();
  if (!artifacts?.length) return out;
  const byTask = new Map<string, Message[]>();
  for (const m of msgs) {
    if (!m.source_task_id || m.author_type !== "agent") continue;
    const l = byTask.get(m.source_task_id) ?? [];
    l.push(m);
    byTask.set(m.source_task_id, l);
  }
  for (const a of artifacts) {
    const l = a.submitted_by_task_id ? byTask.get(a.submitted_by_task_id) : undefined;
    if (!l?.length) continue;
    const sorted = [...l].sort(byTime);
    const at = sorted.find((m) => m.created_at >= a.created_at) ?? sorted[sorted.length - 1];
    out.set(at.id, [...(out.get(at.id) ?? []), a]);
  }
  return out;
}
/** `room.updated` 가 싣는 칸(계약 SSE 표 — Room 부분). 보는 사람 모양 칸(my_capabilities 등)은 없다. */
const ROOM_UPDATED_KEYS = ["name", "description", "status", "visibility", "blocked_reason", "blocked_detail", "last_activity_at"] as const;

export default function RoomPage() {
  const { id: roomId } = useParams<{ id: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const { workspace, me, canManage } = useAuth();
  const meId = me?.user.id ?? null;

  const sel = useMemo(() => parseSel(search.get("work")), [search]);
  const around = search.get("around_message_id");

  const [room, setRoom] = useState<Room | null>(null);
  const nameRef = useRef("");
  nameRef.current = room?.name ?? nameRef.current;
  const [works, setWorks] = useState<WorkListItem[]>([]);
  const [participants, setParticipants] = useState<RoomParticipant[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [hasOlder, setHasOlder] = useState(false);
  const [msgLoaded, setMsgLoaded] = useState(false);
  const [replies, setReplies] = useState<Record<string, Message[]>>({});
  const [events, setEvents] = useState<Record<string, Events>>({});
  const [agents, setAgents] = useState<Agent[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [runtimes, setRuntimes] = useState<Runtime[] | null>(null);
  const [lanes, setLanes] = useState<Lane[]>([]);
  const [emptyTurns, setEmptyTurns] = useState<Record<string, string>>({});
  const [artifacts, setArtifacts] = useState<Artifact[] | null>(null);
  const [decisions, setDecisions] = useState<Decision[] | null>(null);
  const [hitls, setHitls] = useState<HitlRequest[]>([]);
  const [work, setWork] = useState<Work | null>(null);
  const [workNotFound, setWorkNotFound] = useState(false);
  const [reads, setReads] = useState<{ out: number; in: number } | null>(null);
  const [typing, setTyping] = useState<Record<string, boolean>>({});
  const [deltas, setDeltas] = useState<Record<string, string>>({});
  const [replyTo, setReplyTo] = useState<{ id: string; authorName: string } | null>(null);
  const [restart, setRestart] = useState<{ laneId: string; agentName: string } | null>(null);
  const [draft, setDraft] = useState<{ content: string; nonce: number } | null>(null);
  const [confirmCancel, setConfirmCancel] = useState<Lane | null>(null);
  /** 작성창 미션 선택기 — 사람이 바꾼 값. 칩을 바꾸면 버린다(기본값 = 지금 고른 칩, §4.6 귀속 규칙 1). */
  const [composerPick, setComposerPick] = useState<{ value: string | null } | null>(null);
  const [col, setCol] = useState<Col>(() => (sel.kind === "work" ? "work" : "timeline"));
  const [error, setError] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState<"archive" | "delete" | null>(null);
  const [showParticipants, setShowParticipants] = useState(false);
  /** 미션 칸의 편집 다이얼로그(T-R2-W4b) — 설정 편집(S21 편집 모드) · Director 교체 · 조건 고치기. 우열에 실린 미션에 대해서만 뜬다. */
  const [workDialog, setWorkDialog] = useState<"edit" | "director" | "condition" | null>(null);
  const [panelOpen, setPanelOpen] = useState(false);
  const [boardOpen, setBoardOpen] = useState<Set<LaneStatus>>(new Set());
  /** 「여기까지 정리」 직접 고르기(T-R2-W4b) — 집는 중이면 시작 메시지(아직 없으면 null), 다 집으면 범위 + 다이얼로그를 다시 여는 신호. */
  const [pick, setPick] = useState<{ from: Message | null } | null>(null);
  const [picked, setPicked] = useState<PickedRange | null>(null);
  const [pickNonce, setPickNonce] = useState(0);
  const [now, setNow] = useState(Date.now());
  /** 세 층(FR-3.1.2) — 보기 전환과 카드마다의 펼침. 펼침은 메시지 id 로 여기(방 화면)에 둔다 — 카드가 다시 그려져도 남는다. */
  const [view, setView] = useState<TimelineView>("conversation");
  const [folds, setFolds] = useState<Folds>({});
  const requestedEvents = useRef(new Set<string>());
  const bottomRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<HTMLElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const focusedOnce = useRef(false);
  // 안 읽음(M4) — 보고 있는 동안 마지막 메시지까지 읽음으로 옮긴다. 참여하지 않은 사람(공개 방·감사 열람)은 표식이 없다(서버 403).
  useMarkRoomRead(roomId, messages, room?.my_room_role != null);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  // 펼침은 방마다 기억한다(§4.6 — 보드 done·failed 묶음 · 우열 방 전체 칸).
  useEffect(() => {
    setPanelOpen(readLocal(`colab.room.${roomId}.panel`, false));
    setBoardOpen(new Set(readLocal<LaneStatus[]>(`colab.room.${roomId}.board`, [])));
  }, [roomId]);
  useEffect(() => {
    setView(readLocal<string>(timelineViewKey(roomId), "conversation") === "detail" ? "detail" : "conversation");
    setFolds({});
  }, [roomId]);
  // 칩을 바꾸면 작성창 선택기는 다시 칩을 따른다.
  useEffect(() => setComposerPick(null), [sel]);

  // ── 읽기 ──
  const loadRoom = useCallback(async () => {
    const [r, ws] = await Promise.all([
      api.get("/rooms/{roomId}", { path: { roomId } }),
      api.get("/rooms/{roomId}/works", { path: { roomId }, query: { limit: 200 } }),
    ]);
    setRoom(r);
    // 칩 순서는 처음 읽은 순서를 지킨다 — 활동할 때마다 칩이 자리를 바꾸면 누르려던 칩이 도망간다. 새 미션은 뒤에 붙는다.
    setWorks((cur) => {
      const fresh = (ws.items ?? []) as WorkListItem[];
      if (!cur.length) return fresh;
      const byId = new Map(fresh.map((w) => [w.id, w]));
      const kept = cur.filter((w) => byId.has(w.id)).map((w) => byId.get(w.id)!);
      return [...kept, ...fresh.filter((w) => !cur.some((c) => c.id === w.id))];
    });
  }, [roomId]);
  const loadParticipants = useCallback(async () => {
    const p = await api.get("/rooms/{roomId}/participants", { path: { roomId } }).catch(() => null);
    if (p) setParticipants(p.items);
  }, [roomId]);
  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [, ags, mems, rts] = await Promise.all([
        loadRoom(),
        api.get("/workspaces/{workspaceId}/agents", { path: { workspaceId: workspace.id } }),
        api.get("/workspaces/{workspaceId}/members", { path: { workspaceId: workspace.id } }),
        api.get("/workspaces/{workspaceId}/runtimes", { path: { workspaceId: workspace.id } }).catch(() => null),
        loadParticipants(),
      ]);
      setAgents(ags.items);
      setMembers(mems.items);
      setRuntimes(rts);
      setLoadError(null);
    } catch (e) {
      setLoadError(errorMessage(e));
    }
  }, [workspace, loadRoom, loadParticipants]);

  /** 타임라인 — 최근 50건(또는 앵커 위아래 25건). 계약 파라미터 + 클라이언트 거르기(서버가 아직 안 읽는다). */
  const loadMessages = useCallback(async () => {
    const query: Record<string, string | number | boolean> = { limit: 50 };
    if (sel.kind === "work") query.work_id = sel.id;
    if (sel.kind === "none") query.no_work = true;
    if (around) query.around_message_id = around;
    try {
      const page = await api.get("/rooms/{roomId}/messages", { path: { roomId }, query });
      setMessages(page.items.filter((m) => !m.parent_id && matchesSel(m.work_id, sel)).sort(byTime));
      setHasOlder(!!page.has_more_before);
      setMsgLoaded(true);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId, sel, around]);
  const loadOlder = useCallback(async () => {
    const first = messages[0];
    if (!first) return;
    const query: Record<string, string | number | boolean> = { limit: 50, before: first.id };
    if (sel.kind === "work") query.work_id = sel.id;
    if (sel.kind === "none") query.no_work = true;
    try {
      const page = await api.get("/rooms/{roomId}/messages", { path: { roomId }, query });
      const older = page.items.filter((m) => !m.parent_id && matchesSel(m.work_id, sel));
      setMessages((cur) => [...older.filter((m) => !cur.some((c) => c.id === m.id)), ...cur].sort(byTime));
      setHasOlder(!!page.has_more_before);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId, sel, messages]);

  /** 좌·우열 데이터. 서버가 아직 안 켠 operation 은 조용히 비운다 — 화면은 빈 상태로 말한다(§7). */
  const loadSide = useCallback(async () => {
    const [l, a, d, h] = await Promise.allSettled([
      api.get("/rooms/{roomId}/lanes", { path: { roomId } }),
      api.get("/rooms/{roomId}/artifacts", { path: { roomId } }),
      api.get("/rooms/{roomId}/decisions", { path: { roomId } }),
      api.get("/rooms/{roomId}/hitl-requests", { path: { roomId }, query: { limit: 100 } }),
    ]);
    setLanes(l.status === "fulfilled" ? l.value : []);
    setArtifacts(a.status === "fulfilled" ? a.value : []);
    setDecisions(d.status === "fulfilled" ? d.value : []);
    setHitls(h.status === "fulfilled" ? (h.value.items ?? []) : []);
  }, [roomId]);

  useEffect(() => {
    void load();
    void loadSide();
  }, [load, loadSide]);
  useEffect(() => {
    setMsgLoaded(false);
    void loadMessages();
  }, [loadMessages]);

  // T-APPROVAL: `kind: hitl` 메시지는 절대 평문으로 떨어지지 않는다. 목록·`hitl.created` 로 짝이 안 오면 요청을 직접 읽고
  // (`getHitlRequest` — 메시지의 `hitl_request_id`), id 도 없으면 목록을 다시 읽는다. 한 카드에 한 번만(`hitlTried`).
  const hitlTried = useRef(new Set<string>());
  useEffect(() => {
    const need = unpairedHitlCards(messages, hitls, hitlTried.current);
    if (need.messageIds.length === 0) return;
    for (const id of need.messageIds) hitlTried.current.add(id);
    for (const id of need.ids) {
      void api.get("/hitl-requests/{hitlRequestId}", { path: { hitlRequestId: id } })
        .then((h) => { if (h.session_id === roomId) setHitls((cur) => upsertHitl(cur, h)); })
        .catch(() => undefined);
    }
    if (need.refetch) void loadSide();
  }, [messages, hitls, roomId, loadSide]);

  // 우열 미션 칸 — 선택(또는 (전체)의 최근 활동 미션)의 `Work` 를 읽는다.
  const mode = useMemo(() => panelMode(sel, works), [sel, works]);
  const panelWorkId = mode.kind === "picked" || mode.kind === "recent" ? mode.workId : null;
  const loadWork = useCallback(async (id: string | null) => {
    if (!id) {
      setWork(null);
      setWorkNotFound(false);
      return;
    }
    try {
      const w = await api.get("/works/{workId}", { path: { workId: id } });
      // 다른 방의 미션 id 면 「이 방에 그 미션이 없습니다」(§4.8 빈 상태).
      if (w.room_id !== roomId) {
        setWork(null);
        setWorkNotFound(true);
        return;
      }
      setWork(w);
      setWorkNotFound(false);
    } catch (e) {
      setWork(null);
      if (isApiError(e) && (e.status === 404 || e.status === 403)) setWorkNotFound(true);
      else setError(errorMessage(e));
    }
  }, [roomId]);
  useEffect(() => {
    void loadWork(panelWorkId);
  }, [loadWork, panelWorkId]);

  const loadReplies = useCallback(async (rootId: string) => {
    const page = await api.get("/rooms/{roomId}/messages", { path: { roomId }, query: { thread: rootId, limit: 200 } });
    setReplies((r) => ({ ...r, [rootId]: page.items.filter((m) => m.id !== rootId).sort(byTime) }));
  }, [roomId]);
  const loadEvents = useCallback(async (taskId: string) => {
    setEvents((c) => ({ ...c, [taskId]: { events: [], structured: true, loading: true } }));
    try {
      // task 상태는 「진행 중…」 판정에만 쓴다 — 못 읽어도 피드는 그린다(이벤트만으로 판정).
      const [r, t] = await Promise.all([
        api.get("/tasks/{taskId}/events", { path: { taskId }, query: { limit: 200 } }),
        api.get("/tasks/{taskId}", { path: { taskId } }).catch(() => null),
      ]);
      setEvents((c) => ({
        ...c,
        [taskId]: { events: r.items, structured: r.structured ?? true, loading: false, task: c[taskId]?.task ?? (t && typeof t.status === "string" ? { status: t.status, attempt: t.attempt } : null) },
      }));
      const empty = r.items.find(isEmptyTurn);
      if (empty) setEmptyTurns((m) => (m[taskId] ? m : { ...m, [taskId]: emptyTurnNote(empty) }));
    } catch {
      setEvents((c) => ({ ...c, [taskId]: { events: [], structured: true, loading: false } }));
    }
  }, []);
  // 「작업 과정」 접힌 줄의 요약은 펼치기 전부터 보여야 한다(실패 꼬리 포함) — 세 층 메시지의 턴 기록을 한 번씩 읽어 둔다.
  useEffect(() => {
    for (const m of [...messages, ...Object.values(replies).flat()]) {
      const tid = m.source_task_id;
      if (!tid || !isLayered(m) || requestedEvents.current.has(tid)) continue;
      requestedEvents.current.add(tid);
      void loadEvents(tid);
    }
  }, [messages, replies, loadEvents]);
  // 「작업 중」 줄(T-FEED B) — 도는 서브 미션의 현재 할 일 기록도 읽어 둔다(마지막 메시지 뒤의 작업이 여기 쌓인다).
  useEffect(() => {
    for (const l of lanes) {
      const t = l.current_task;
      if (l.status !== "running" || !t || requestedEvents.current.has(t.id)) continue;
      requestedEvents.current.add(t.id);
      void loadEvents(t.id);
    }
  }, [lanes, loadEvents]);
  const loadLaneTasks = useCallback(async (laneId: string): Promise<Task[]> => api.get("/lanes/{laneId}/tasks", { path: { laneId } }), []);
  /**
   * 대화 배치(FR-3.1.3)의 「↩ … 에 대한 보고」 — 판정은 서버가 내려주고(openapi v0.3.2 `responds_to_message_id`),
   * 화면은 그 메시지의 인용문 한 줄이 필요할 뿐이다. 타임라인·스레드에 없으면 `getMessage` 를 그 id 당 한 번.
   */
  const [farMessages, setFarMessages] = useState<Record<string, Message | null>>({});
  const requestedMessages = useRef(new Set<string>());
  const lookupMessage = useCallback((id: string): Message | null | undefined => {
    return messages.find((x) => x.id === id) ?? Object.values(replies).flat().find((x) => x.id === id) ?? (id in farMessages ? farMessages[id] : undefined);
  }, [messages, replies, farMessages]);
  useEffect(() => {
    for (const m of [...messages, ...Object.values(replies).flat()]) {
      const trig = m.responds_to_message_id;
      if (!trig || requestedMessages.current.has(trig) || lookupMessage(trig) !== undefined) continue;
      requestedMessages.current.add(trig);
      void api.get("/messages/{messageId}", { path: { messageId: trig } })
        .then((x) => setFarMessages((cur) => ({ ...cur, [trig]: x })))
        .catch(() => setFarMessages((cur) => ({ ...cur, [trig]: null })));
    }
  }, [messages, replies, lookupMessage]);
  const renderTaskActivity = useCallback((taskId: string) => <TaskActivity taskId={taskId} cache={events} load={loadEvents} />, [events, loadEvents]);

  // 서브 미션·미션 수(세 층 요약)는 room 행의 `counts` — 서브 미션·미션이 움직이면 방을 다시 읽는다(한 번에 몰아서).
  const roomTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const refreshRoom = useCallback(() => {
    if (roomTimer.current) return;
    roomTimer.current = setTimeout(() => {
      roomTimer.current = null;
      void loadRoom().catch(() => undefined);
    }, 400);
  }, [loadRoom]);
  useEffect(() => () => { if (roomTimer.current) clearTimeout(roomTimer.current); }, []);

  // ── 실시간 ──
  const selRef = useRef(sel);
  selRef.current = sel;
  const panelRef = useRef(panelWorkId);
  panelRef.current = panelWorkId;
  const onEvent = useCallback((ev: StreamEvent) => {
    // 워크스페이스 전체 스트림 — 다른 방의 프레임은 버린다(§6: 구독 범위는 방, 미션은 클라이언트가 거른다).
    const rid = ev.room_id;
    if (rid && rid !== roomId) return;
    switch (ev.type) {
      case "message.created": {
        const m = ev.payload as unknown as Message;
        if (m.session_id !== roomId) return;
        if (m.author_id) setDeltas((d) => { const n = { ...d }; delete n[m.author_id!]; return n; });
        if (m.parent_id) {
          const root = m.parent_id;
          setReplies((r) => (r[root] ? { ...r, [root]: r[root].some((x) => x.id === m.id) ? r[root] : [...r[root], m].sort(byTime) } : r));
          setMessages((ms) => ms.map((x) => (x.id === root ? { ...x, reply_count: (x.reply_count ?? 0) + 1 } : x)));
        } else if (matchesSel(m.work_id, selRef.current)) {
          setMessages((ms) => (ms.some((x) => x.id === m.id) ? ms : [...ms, m].sort(byTime)));
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
        if (isEmptyTurn(te)) setEmptyTurns((m) => (m[te.task_id] ? m : { ...m, [te.task_id]: emptyTurnNote(te) }));
        setEvents((c) => {
          const cur = c[te.task_id];
          if (!cur || cur.events.some((e) => e.id === te.id)) return c;
          return { ...c, [te.task_id]: { ...cur, events: [...cur.events, te] } };
        });
        break;
      }
      case "task.updated": {
        // 「진행 중…」 판정(T-FEED) — task 가 끝나면 그 피드의 짝 없는 started 줄이 「결과 없음」으로 바뀐다.
        const t = ev.payload as unknown as Task;
        setEvents((c) => {
          const cur = c[t.id];
          if (!cur) return c;
          return { ...c, [t.id]: { ...cur, task: { status: t.status, attempt: t.attempt } } };
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
        if (l.session_id !== roomId) return;
        setLanes((cur) => (cur.some((x) => x.id === l.id) ? cur.map((x) => (x.id === l.id ? { ...x, ...l } : x)) : [...cur, l]));
        if (l.status !== "running") setDeltas((d) => { if (!(l.agent_id in d)) return d; const n = { ...d }; delete n[l.agent_id]; return n; });
        refreshRoom();
        break;
      }
      case "hitl.created":
      case "hitl.updated": {
        const h = ev.payload as unknown as HitlRequest;
        if (h.session_id !== roomId) return;
        setHitls((cur) => upsertHitl(cur, h));
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
      case "work.created":
      case "work.updated": {
        const w = ev.payload as unknown as Partial<WorkListItem> & { id: string };
        setWorks((cur) => (cur.some((x) => x.id === w.id) ? cur.map((x) => (x.id === w.id ? { ...x, ...w } : x)) : [...cur, w as WorkListItem]));
        if (w.id === panelRef.current) void loadWork(w.id);
        refreshRoom();
        break;
      }
      case "work.closed": {
        const p = ev.payload as { work_id: string; status: WorkListItem["status"] };
        setWorks((cur) => cur.map((x) => (x.id === p.work_id ? { ...x, status: p.status } : x)));
        if (p.work_id === panelRef.current) void loadWork(p.work_id);
        refreshRoom();
        break;
      }
      case "work.deleted": {
        const p = ev.payload as { work_id: string };
        setWorks((cur) => cur.filter((x) => x.id !== p.work_id));
        const s = selRef.current;
        if (s.kind === "work" && s.id === p.work_id) router.replace(`/rooms/${roomId}`);
        break;
      }
      case "work.completion_progress": {
        const p = ev.payload as { work_id: string; completion_progress?: Work["completion_progress"] };
        if (!p.completion_progress) return;
        setWork((w) => (w && w.id === p.work_id ? { ...w, completion_progress: p.completion_progress! } : w));
        setWorks((cur) => cur.map((x) => (x.id === p.work_id ? { ...x, completion_progress: { met: p.completion_progress!.met, total: p.completion_progress!.total } } : x)));
        break;
      }
      case "room.updated": {
        const p = ev.payload as Partial<Room>;
        setRoom((r) => {
          if (!r) return r;
          const patch: Partial<Room> = {};
          for (const k of ROOM_UPDATED_KEYS) if (k in p) (patch as Record<string, unknown>)[k] = p[k];
          return { ...r, ...patch };
        });
        // 멈춤·보관이 바뀌면 권한 칸(my_capabilities)·수도 바뀐다 — 방 행을 다시 읽는다.
        refreshRoom();
        break;
      }
      case "room.deleted": {
        const p = ev.payload as { room_id?: string; session_id?: string };
        if ((p.room_id ?? p.session_id ?? rid) !== roomId) return;
        router.replace(`/rooms?deleted=${encodeURIComponent(nameRef.current)}`);
        break;
      }
      case "participant.joined":
      case "participant.left":
      case "participant.updated":
        void loadParticipants();
        break;
      case "room_read.recorded": {
        const p = ev.payload as { direction?: string };
        setReads((r) => {
          const cur = r ?? { out: 0, in: 0 };
          return p.direction === "read_by" || p.direction === "in" ? { ...cur, in: cur.in + 1 } : { ...cur, out: cur.out + 1 };
        });
        break;
      }
      case "cost.updated": {
        const p = ev.payload as { room_cost_usd?: number; cost_usd?: number; work_id?: string; work_cost_usd?: number; estimated?: boolean };
        const roomCost = p.room_cost_usd ?? p.cost_usd;
        if (typeof roomCost === "number") setRoom((r) => (r ? { ...r, cost_usd: roomCost, cost_estimated: p.estimated ?? r.cost_estimated } : r));
        if (p.work_id && typeof p.work_cost_usd === "number") {
          setWork((w) => (w && w.id === p.work_id ? { ...w, cost_usd: p.work_cost_usd! } : w));
          setWorks((cur) => cur.map((x) => (x.id === p.work_id ? { ...x, cost_usd: p.work_cost_usd! } : x)));
        }
        break;
      }
      case "agent.typing": {
        const p = ev.payload as { agent_id: string; typing: boolean };
        setTyping((t) => ({ ...t, [p.agent_id]: p.typing }));
        break;
      }
      case "message.delta": {
        const p = ev.payload as { agent_id: string; text: string };
        setDeltas((d) => (d[p.agent_id] === p.text ? d : { ...d, [p.agent_id]: p.text }));
        break;
      }
      default:
        break;
    }
  }, [roomId, router, loadWork, loadParticipants, refreshRoom]);
  const conn = useWorkspaceStream(workspace?.id, onEvent, { onResync: () => { void load(); void loadSide(); void loadMessages(); } });

  // 새 메시지·델타마다 맨 아래로(앵커로 들어왔으면 앵커 자리를 지킨다). 작성창 높이만큼 끝 표식의 scroll-margin 을 둔다(W-18).
  useEffect(() => {
    if (around) return;
    const end = bottomRef.current;
    if (!end) return;
    end.style.scrollMarginBottom = `${composerRef.current?.offsetHeight ?? 0}px`;
    end.scrollIntoView({ block: "end" });
  }, [messages.length, deltas, around]);
  // 앵커(`?around_message_id=`) — 그 메시지를 가운데에.
  useEffect(() => {
    if (!around || !msgLoaded) return;
    document.querySelector(`[data-message-id="${around}"]`)?.scrollIntoView({ block: "center" });
  }, [around, msgLoaded]);
  // 가운데가 기본이다(SCR-C B) — 방을 열면 포커스는 작성창.
  useEffect(() => {
    if (!room || focusedOnce.current) return;
    focusedOnce.current = true;
    inputRef.current?.focus({ preventScroll: true });
  }, [room]);

  // 인박스 「다시 지시」(`?restart_lane=`) — 여기서 작성창을 연다.
  const restartParam = search.get("restart_lane");
  useEffect(() => {
    if (!restartParam || lanes.length === 0) return;
    const lane = lanes.find((l) => l.id === restartParam);
    if (!lane) return;
    const a = agents.find((x) => x.id === lane.agent_id);
    setRestart({ laneId: lane.id, agentName: a?.name ?? "agent" });
    setDraft({ content: a ? `@${a.name} ` : "", nonce: Date.now() });
    setCol("timeline");
  }, [restartParam, lanes, agents]);

  // ── 파생 ──
  const agentById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const agentName = useCallback((id: string) => agentById.get(id)?.name ?? "agent", [agentById]);
  const workById = useMemo(() => new Map(works.map((w) => [w.id, w])), [works]);
  const workTitle = useCallback((id: string | null | undefined) => (id ? workById.get(id)?.title ?? (work?.id === id ? work.title : "") : ""), [workById, work]);
  const roomAgents = useMemo(() => participants.filter((p) => p.kind === "agent" && !p.left_at && p.agent), [participants]);
  const composerAgents = useMemo<ComposerAgent[]>(() => {
    const pids = new Set(roomAgents.map((p) => p.agent!.id));
    return agents.map((a) => ({ id: a.id, name: a.name, participant: pids.has(a.id) }));
  }, [agents, roomAgents]);
  const composerMembers = useMemo(() => members.map((m) => ({ id: m.user.id, name: m.user.display_name })), [members]);
  const shownLanes = useMemo(() => filterLanes(lanes, sel), [lanes, sel]);
  const laneEmptyTurns = useMemo(() => {
    const out: Record<string, string> = {};
    for (const l of lanes) {
      const tid = l.current_task?.id;
      if (tid && emptyTurns[tid]) out[l.id] = emptyTurns[tid];
    }
    return out;
  }, [lanes, emptyTurns]);
  const needs = useMemo(() => (room ? needsMe({ me: meId, room, hitls, lanes, works }) : []), [room, meId, hitls, lanes, works]);
  const openWorks = useMemo(() => works.filter(isOpenWork), [works]);

  const preview = useCallback(
    (input: ComposerInput): Promise<TriggerPreview> =>
      api.post("/rooms/{roomId}/messages/preview", {
        path: { roomId },
        body: { content: input.content, parent_id: input.parentId, new_lane: input.newLane, suppress_agent_ids: input.suppressAgentIds, ...(input.workId !== undefined ? { work_id: input.workId } : {}) },
      }),
    [roomId],
  );

  async function submit(input: ComposerInput): Promise<ComposerWarning[]> {
    if (restart) {
      const r = await api.post("/lanes/{laneId}/restart", { path: { laneId: restart.laneId }, idempotencyKey: newIdempotencyKey(), body: { content: input.content } });
      setRestart(null);
      setLanes((cur) => cur.map((l) => (l.id === r.lane.id ? r.lane : l)));
      onEvent({ id: "", type: "message.created", at: r.message.created_at, payload: r.message as unknown as Record<string, unknown> });
      return [];
    }
    const r = await api.post("/rooms/{roomId}/messages", {
      path: { roomId },
      idempotencyKey: newIdempotencyKey(),
      body: { content: input.content, parent_id: input.parentId, new_lane: input.newLane, suppress_agent_ids: input.suppressAgentIds, ...(input.workId !== undefined ? { work_id: input.workId } : {}) },
    });
    onEvent({ id: "", type: "message.created", at: r.message.created_at, payload: r.message as unknown as Record<string, unknown> });
    setReplyTo(null);
    return r.warnings;
  }

  function beginRestart(lane: Lane) {
    const a = agentById.get(lane.agent_id);
    setRestart({ laneId: lane.id, agentName: a?.name ?? "agent" });
    setReplyTo(null);
    setDraft({ content: a ? `@${a.name} ` : "", nonce: Date.now() });
    setCol("timeline");
  }
  async function act<T>(f: () => Promise<T>): Promise<T | undefined> {
    setBusy(true);
    try {
      return await f();
    } catch (e) {
      setError(errorMessage(e));
      return undefined;
    } finally {
      setBusy(false);
    }
  }
  const doCancel = (lane: Lane) => act(async () => {
    const l = await api.post("/lanes/{laneId}/cancel", { path: { laneId: lane.id } });
    setLanes((cur) => cur.map((x) => (x.id === l.id ? l : x)));
    setConfirmCancel(null);
  });
  const respondHitl = async (id: string, body: HitlResponse) => {
    setBusy(true);
    try {
      const r = await api.post("/hitl-requests/{hitlRequestId}/response", { path: { hitlRequestId: id }, idempotencyKey: newIdempotencyKey(), body });
      setHitls((cur) => cur.map((x) => (x.id === r.hitl_request.id ? r.hitl_request : x)));
      void loadSide();
      void loadRoom();
    } finally {
      setBusy(false);
    }
  };
  // 미션 동작 — 사람이 고른 미션에만(`WorkPanel` 이 (전체)·(미션 없음)에서 막는다).
  const workAct = (path: "/works/{workId}/pause" | "/works/{workId}/resume" | "/works/{workId}/complete" | "/works/{workId}/cancel", body?: Record<string, unknown>) =>
    work && act(async () => {
      const w = await api.post(path, { path: { workId: work.id }, body: body as never });
      setWork(w as Work);
      void loadRoom();
    });

  function jumpToMessage(messageId: string) {
    setCol("timeline");
    const el = document.querySelector(`[data-message-id="${messageId}"]`);
    el?.scrollIntoView({ block: "center" });
    el?.classList.add("msg--flash");
    setTimeout(() => el?.classList.remove("msg--flash"), 1200);
  }
  function openLaneHitl(lane: Lane) {
    const h = hitls.find((x) => x.id === lane.hitl_request_id) ?? hitls.find((x) => x.lane_id === lane.id && x.status === "open");
    if (h?.message_id) jumpToMessage(h.message_id);
  }
  function jumpNeed() {
    const first = needs[0];
    if (!first) return;
    const [kind, ref] = first.target.split(":");
    if (kind === "message") return jumpToMessage(ref);
    if (first.target === "work-panel") setCol("work");
    else if (kind === "lane") setCol("board");
    else setCol("timeline");
    const el = kind === "lane" ? document.querySelector(`[data-lane-id="${ref}"]`) : document.querySelector(`[data-need="${first.target}"]`);
    el?.scrollIntoView({ block: "center" });
    (el as HTMLElement | null)?.focus?.();
  }
  const select = (s: ChipSel) => {
    if (sameSel(s, sel)) return;
    const q = selParam(s);
    router.replace(q ? `/rooms/${roomId}?work=${encodeURIComponent(q)}` : `/rooms/${roomId}`, { scroll: false });
  };
  /** S21 미션 열기 · S26 제안(T-R2-W3 `RoomQueryDialogs`) — 쿼리(`?work=new` · `?work=from&message=` · `?work_proposal=`)로 뜬다. */
  const dialogQuery = useRoomDialogQuery();
  const openNewWork = () => dialogQuery.openNewWork();
  const openWorkFrom = (m: Message) => dialogQuery.openFromMessage(m.id);
  /** 인용·멘션 제시에 쓸 메시지 — 서버 getMessage 가 오기 전까지 이 화면이 가진 것에서 찾는다. */
  const findMessage = (mid: string) => messages.find((m) => m.id === mid) ?? Object.values(replies).flat().find((m) => m.id === mid) ?? null;

  /** 편집 다이얼로그가 저장하면 — 응답 본문(Work)으로 우열을 바로 바꾸고 칩 줄(제목·Director)은 방을 다시 읽어 맞춘다. */
  const onWorkSaved = (w: Work) => {
    setWork(w);
    setWorkDialog(null);
    void loadRoom().catch(() => undefined);
  };

  /** S19 참여자(T-R2-W3 `RoomParticipantsDialog`) — 초대·퇴장 · 부방장 · 본인 「이 방에서 나가기」가 한 다이얼로그. */
  const openParticipants = () => setShowParticipants(true);

  // ── 그리기 ──
  if (!workspace) return null;
  if (loadError && !room) {
    return (
      <div data-testid="room-error">
        <p className="problem">{loadError}</p>
        <Link href="/rooms" className="btn">{ROOM_HEAD.back}</Link>
      </div>
    );
  }
  if (!room) return <p className="muted">{ROOM_HEAD.loading}</p>;

  const caps = new Set(room.my_capabilities ?? []);
  const blockedBy = postBlockedBy(room);
  const audit = isAuditView(room);
  const archived = room.status === "archived";
  const newWorkWhy = archived ? ROOM_HEAD.archived : null;
  const onlineComputers = runtimes?.filter((r) => r.status === "online").length ?? null;
  const typingAgents = Object.entries(typing).filter(([, v]) => v).map(([id]) => agentById.get(id)?.name ?? "agent");
  const showLabels = sel.kind === "all";
  // 칸이 없으면(undefined) 라벨을 그리지 않는다 — 「미션 없음」이라고 단정할 근거가 없다(matchesSel 과 같은 규칙).
  const labelFor = (workId: string | null | undefined) => (workId ? ROOM_LEFT.work_label(workTitle(workId)) : ROOM_LEFT.no_work_label);
  const workLabelOf = (workId: string | null | undefined, testId: string) => (showLabels && workId !== undefined ? <WorkLabel text={labelFor(workId)} testId={testId} /> : undefined);
  const people = participants.filter((p) => p.kind === "user" && !p.left_at);
  const freshRoom = msgLoaded && messages.length === 0 && !hasOlder && sel.kind === "all" && people.length <= 1 && roomAgents.length === 0;
  const selectedWorkOpen = sel.kind === "work" && openWorks.some((w) => w.id === sel.id) ? sel.id : null;
  const composerValue = composerPick ? composerPick.value : selectedWorkOpen;
  const announce = [
    sel.kind === "all" ? WORK_CHIPS.announce_all : sel.kind === "none" ? WORK_CHIPS.announce_none : WORK_CHIPS.announce_work(workTitle(sel.id)),
    slotText(WORK_CHIPS.announce_lanes, shownLanes.length),
    slotText(WORK_CHIPS.announce_messages, messages.length),
  ].join(" · ");
  const firstAgent = roomAgents[0]?.agent?.name;
  const placeholder = msgLoaded && messages.length === 0 && !hasOlder && firstAgent ? WORK_SELECTOR.first_order(firstAgent) : WORK_SELECTOR.placeholder;
  const boardEmpty = sel.kind === "work"
    ? (work?.assignee_agent_id ? ROOM_LEFT.board_empty_assignee : ROOM_LEFT.board_empty_no_assignee)
    : works.length === 0 ? ROOM_LEFT.board_empty_no_work : ROOM_LEFT.board_empty;
  const runtimeName = room.runtime?.name ?? (room.runtime_id ? runtimes?.find((r) => r.id === room.runtime_id)?.name ?? null : null);
  const defaultDirector = room.default_director_user_id ? members.find((m) => m.user.id === room.default_director_user_id)?.user.display_name ?? null : null;
  const composerDisabledWhy = blockedBy === "archived" ? WORK_SELECTOR.archived : blockedBy === "audit" ? WORK_SELECTOR.audit : null;

  /** 직접 고르기 — 메시지마다의 집기 단추. 두 번째를 집으면 시각 순서로 맞춰 범위를 다이얼로그에 돌려준다. */
  const pickText = (m: Message) => `${authorName(m)} · ${m.content.replace(/\s+/g, " ").slice(0, 40)}`;
  const pickMessage = (m: Message) => {
    if (!pick) return;
    if (!pick.from) {
      setPick({ from: m });
      return;
    }
    const [a, b] = byTime(pick.from, m) <= 0 ? [pick.from, m] : [m, pick.from];
    // 수는 다 읽은 (전체) 타임라인에서만 센다 — 거른 보기·앞이 남은 목록에서는 모른다.
    const count = sel.kind === "all" && !hasOlder ? messages.filter((x) => x.created_at >= a.created_at && x.created_at <= b.created_at).length : null;
    setPicked({ from: { id: a.id, text: pickText(a) }, to: { id: b.id, text: pickText(b) }, count });
    setPick(null);
    setPickNonce((n) => n + 1);
  };
  const pickButton = (m: Message) => pick && (
    <button type="button" className="btn btn--sm s7__pickbtn" onClick={() => pickMessage(m)} aria-pressed={pick.from?.id === m.id} data-testid="pick-message">
      {pick.from ? SUMMARY_PICK.here_to : SUMMARY_PICK.here_from}
    </button>
  );

  /** 보기 전환 — 모든 카드의 「작업 내용」 기본을 바꾸고, 카드마다 따로 펼치거나 접은 작업 내용은 비운다(전환이 「전부」를 뜻하게). 작업 과정은 건드리지 않는다. */
  const changeView = (v: TimelineView) => {
    setView(v);
    setFolds((cur) => {
      const n: Folds = {};
      for (const [id, f] of Object.entries(cur)) if (f.process !== undefined) n[id] = { process: f.process };
      return n;
    });
    writeLocal(timelineViewKey(roomId), v);
  };
  const toggleFold = (id: string, layer: "detail" | "process", open: boolean) => setFolds((cur) => ({ ...cur, [id]: { ...cur[id], [layer]: !open } }));
  const artsByMsg = artifactsByMessage([...messages, ...Object.values(replies).flat()], artifacts);
  // 메시지별 「작업 과정」 조각(T-FEED A) — 한 턴의 기록을 그 턴이 올린 메시지 경계로 자른다. 도는 턴의 꼬리는 「작업 중」 줄(B)로.
  // 「작업 중」 줄 — 보이는 서브 미션(미션 칩 거름)의 도는 턴, 에이전트마다 하나. 메시지가 아직 없는 턴이면 턴 전체가 꼬리다.
  const working = workingTasks(shownLanes, events);
  const slices: Map<string, ProcessSlices> = roomProcessSlices(events, [...messages, ...Object.values(replies).flat()], new Set(working.map((w) => w.taskId)));
  const sliceOf = (m: Message): ProcessWindow | undefined => (m.source_task_id ? slices.get(m.source_task_id)?.byMessage.get(m.id) : undefined);
  /** 메시지(스레드 답글 포함) → 대화 층의 글 + 그 아래 줄들. 세 층이 아닌 메시지는 undefined(본문 그대로). */
  const layersFor = (m: Message, o: { asAnswer: boolean }): MessageLayerSlots | undefined => {
    if (!isLayered(m, o)) return undefined;
    const v = messageLayers(m);
    const f = folds[m.id];
    const detailOpen = f?.detail ?? view === "detail";
    const processOpen = f?.process ?? false;
    const tid = m.source_task_id;
    return {
      body: v.conversation,
      below: (
        <>
          {(artsByMsg.get(m.id) ?? []).map((a) => <ArtifactRef key={a.id} artifact={a} />)}
          {v.work && <DetailFold messageId={m.id} text={v.work.text} auto={v.work.auto} open={detailOpen} onToggle={() => toggleFold(m.id, "detail", detailOpen)} />}
          {tid && (
            <ProcessFold messageId={m.id} summary={summarizeProcess(events[tid], sliceOf(m))} open={processOpen} onToggle={() => toggleFold(m.id, "process", processOpen)}>
              <TaskActivity taskId={tid} cache={events} load={loadEvents} slice={sliceOf(m)} />
            </ProcessFold>
          )}
        </>
      ),
    };
  };

  /** 대화 배치(FR-3.1.3) — 말의 종류·받는 쪽은 서버가 판정해 내려준 칸 그대로(openapi v0.3.2 D24). */
  const convCtx: ConversationCtx = { lanes, messageById: (id) => lookupMessage(id), authorName };
  const jumpOrAnchor = (id: string) => {
    if (document.querySelector(`[data-message-id="${id}"]`)) return jumpToMessage(id);
    router.replace(`/rooms/${roomId}?around_message_id=${encodeURIComponent(id)}`, { scroll: false });
  };
  const speeches = new Map<string, Speech>();
  const groupedIds = new Set<string>();
  {
    let prev: { m: Message; s: Speech } | undefined;
    for (const m of messages) {
      const s = speechOf(m, convCtx);
      speeches.set(m.id, s);
      const cur = { m, s };
      if (m.kind !== "hitl" && groupsWith(prev, cur)) groupedIds.add(m.id);
      prev = m.kind === "hitl" ? undefined : cur;
    }
  }
  const conversationFor = (m: Message, o: { parent?: Message }): ConversationSlot => ({
    speech: speeches.get(m.id) ?? speechOf(m, convCtx),
    grouped: !o.parent && groupedIds.has(m.id),
    onJump: jumpOrAnchor,
  });

  const toggleBoard = (s: LaneStatus) => setBoardOpen((cur) => {
    const n = new Set(cur);
    if (n.has(s)) n.delete(s);
    else n.add(s);
    writeLocal(`colab.room.${roomId}.board`, [...n]);
    return n;
  });

  return (
    <div className="s7 room" data-testid="room-detail" data-room-id={room.id} data-col={col} data-sel={sel.kind === "work" ? sel.id : sel.kind}>
      <a
        href="#room-composer"
        className="skip-link"
        onClick={(e) => { e.preventDefault(); setCol("timeline"); inputRef.current?.focus(); }}
        data-testid="skip-to-composer"
      >
        {ROOM_HEAD.skip_to_composer}
      </a>
      <ConnectionBanner state={conn} />
      <header className="s7__head">
        <RoomHead
          room={room}
          needs={needs.length}
          onJumpNeed={jumpNeed}
          onParticipants={openParticipants}
          onLeave={openParticipants}
          busy={busy}
          onBlock={async () => {
            const r = await api.post("/rooms/{roomId}/block", { path: { roomId } });
            setRoom(r);
            void loadSide();
          }}
          onUnblock={() => void act(async () => setRoom(await api.post("/rooms/{roomId}/unblock", { path: { roomId } })))}
          onSummarize={async (range) => {
            const body = "days" in range
              ? { since: new Date(Date.now() - range.days * 864e5).toISOString() }
              : { from_message_id: range.from, to_message_id: range.to };
            await api.post("/rooms/{roomId}/summaries", { path: { roomId }, idempotencyKey: newIdempotencyKey(), body });
            setPicked(null);
          }}
          onStartPick={() => { setPick({ from: null }); setCol("timeline"); }}
          picked={picked}
          pickNonce={pickNonce}
          onArchive={() => setDialog("archive")}
          onUnarchive={() => void act(async () => setRoom(await api.post("/rooms/{roomId}/unarchive", { path: { roomId } })))}
          onDelete={() => setDialog("delete")}
          onRename={async (name) => setRoom(await api.patch("/rooms/{roomId}", { path: { roomId }, body: { name } }))}
          runningTurns={lanes.filter((l) => l.status === "running").length}
          openWorks={openWorks.filter((w) => w.status === "active").length}
          countSince={(days) => {
            if (hasOlder || sel.kind !== "all") return null;
            const since = new Date(Date.now() - days * 864e5).toISOString();
            return messages.filter((m) => m.created_at >= since && m.kind !== "summary").length;
          }}
        />
        <WorkChipRow works={works} sel={sel} onSelect={select} onNewWork={openNewWork} newWorkDisabled={newWorkWhy} announce={announce} />
        {/* 탭 패턴(#305 NN3) — 스크린리더가 「N개 중 M번째 탭」을 읽는다. 선택된 탭 하나만 Tab 순서에 들고 ←→ 로 옮긴다. */}
        <div className="s7__tabs" role="tablist" aria-label={ROOM_TABS.label}>
          {COLS.map((c, i) => (
            <button
              key={c}
              type="button"
              role="tab"
              id={`s7-tab-${c}`}
              className={`s7__tab${col === c ? " s7__tab--on" : ""}`}
              aria-selected={col === c}
              tabIndex={col === c ? 0 : -1}
              onClick={() => setCol(c)}
              onKeyDown={(e) => {
                const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
                const to = e.key === "Home" ? 0 : e.key === "End" ? COLS.length - 1 : step ? (i + step + COLS.length) % COLS.length : -1;
                if (to < 0) return;
                e.preventDefault();
                setCol(COLS[to]);
                document.getElementById(`s7-tab-${COLS[to]}`)?.focus();
              }}
              data-testid={`tab-${c}`}
            >
              {ROOM_TABS[c]}
            </button>
          ))}
        </div>
      </header>

      {room.blocked_reason && (
        <RoomBlockedBanner
          room={room}
          me={meId}
          agentName={agentName}
          busy={busy}
          onUnblock={() => void act(async () => setRoom(await api.post("/rooms/{roomId}/unblock", { path: { roomId } })))}
          onApprove={(() => {
            const h = hitls.find((x) => x.status === "open" && x.can_respond && (x.purpose === "budget" || x.purpose === "loop"));
            return h?.message_id ? () => jumpToMessage(h.message_id!) : undefined;
          })()}
          rebindHref={room.runtime_id ? `/runtimes/${room.runtime_id}/rebind?room=${roomId}` : "/runtimes"}
        />
      )}
      {archived && (
        <div className="room-notice" role="status" data-testid="room-archived-banner">
          <span className="room-notice__text">{ROOM_NOTICES.archived}</span>
          <span className="room-head__act">
            <button type="button" className="btn btn--sm" disabled={!caps.has("archive") || busy} aria-describedby={!caps.has("archive") ? "room-unarchive-hint" : undefined}
              onClick={() => void act(async () => setRoom(await api.post("/rooms/{roomId}/unarchive", { path: { roomId } })))} data-testid="room-unarchive">
              {ROOM_NOTICES.unarchive}
            </button>
            {!caps.has("archive") && <DisabledHint id="room-unarchive-hint">{ROOM_NOTICES.unarchive_role}</DisabledHint>}
          </span>
        </div>
      )}
      {audit && (
        <div className="room-notice" role="status" data-testid="room-audit-banner">
          <span className="room-notice__text">{ROOM_NOTICES.audit}</span>
        </div>
      )}
      {onlineComputers === 0 && (
        <div className="room-notice" role="status" data-testid="room-no-computer">
          <span className="room-notice__text">{ROOM_NOTICES.no_computer}</span>
          <Link href="/runtimes/new" className="btn btn--sm">{ROOM_NOTICES.no_computer_link}</Link>
        </div>
      )}

      <div className="s7__cols">
        <section className="s7__left" data-testid="s7-left">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h2 className="s7__h">{ROOM_LEFT.participants}</h2>
            <button type="button" className="msg__link" onClick={openParticipants} data-testid="participants-invite">{ROOM_LEFT.invite}</button>
          </div>
          <RoomParticipants participants={participants} lanes={lanes} archivedAgent={(id) => agentById.get(id)?.archived_at != null} />
          {people.length <= 1 && roomAgents.length === 0 && (
            <p className="small muted" data-testid="participants-alone">
              {ROOM_LEFT.alone}
              {" · "}
              <button type="button" className="msg__link" onClick={openParticipants}>{ROOM_LEFT.alone_cta}</button>
            </p>
          )}
          <h2 className="s7__h">{ROOM_LEFT.board}</h2>
          <LaneBoard
            lanes={shownLanes}
            emptyHint={boardEmpty}
            emptyTurns={laneEmptyTurns}
            selected={false}
            now={now}
            disabledReason={ROOM_LEFT.control_role}
            loadTasks={loadLaneTasks}
            renderTaskActivity={renderTaskActivity}
            onRestart={beginRestart}
            onCancel={(l) => setConfirmCancel(l)}
            onOpenQuestion={jumpToMessage}
            onRespondHitl={openLaneHitl}
            onApproveBudget={openLaneHitl}
            fold={{ statuses: BOARD_FOLDED, open: boardOpen, onToggle: toggleBoard, labels: { open: ROOM_LEFT.fold_open, close: ROOM_LEFT.fold_close } }}
            decorate={(l) => {
              const layer = pausedLayer(l, room, works);
              const reason = l.queued_reason;
              const maxConc = agentById.get(l.agent_id)?.max_concurrent_tasks ?? null;
              return {
                // 미션 라벨은 양방향 — (전체)에서 보이고 칩을 고르면 감춘다(§4.6).
                workLabel: workLabelOf(l.work_id, "lane-work-label"),
                queuedReason: !reason ? undefined
                  : reason === "room_lanes" ? (room.limits?.max_parallel_lanes != null ? <Slot text={ROOM_LEFT.queued_room_lanes} n={room.limits.max_parallel_lanes} /> : ROOM_LEFT.queued_room_lanes_plain)
                  : reason === "agent_global" ? (maxConc != null ? <Slot text={ROOM_LEFT.queued_agent_global} n={maxConc} /> : ROOM_LEFT.queued_agent_global_plain)
                  : reason === "runtime" ? (room.isolation?.kind === "worktree" && !room.runtime_id ? ROOM_LEFT.queued_runtime_repo : ROOM_LEFT.queued_runtime)
                  : ROOM_LEFT.queued_workspace,
                pausedLayer: layer ? { label: layer === "task" ? ROOM_LEFT.paused_task : layer === "work" ? ROOM_LEFT.paused_work : ROOM_LEFT.paused_room, taskOnly: layer === "task" ? ROOM_LEFT.paused_task_only : null } : null,
              };
            }}
          />
          {confirmCancel && (
            <div className="s7__confirm" role="dialog" aria-label={ROOM_LEFT.cancel_yes} data-testid="cancel-confirm">
              <p className="small">{confirmCancel.status === "done" ? ROOM_LEFT.cancel_confirm_done : ROOM_LEFT.cancel_confirm}</p>
              <p className="small muted-3">{ROOM_LEFT.cancel_hold}</p>
              <div className="row">
                <button type="button" className="btn btn--sm btn--primary" disabled={busy} onClick={() => void doCancel(confirmCancel)} data-testid="cancel-confirm-yes">{ROOM_LEFT.cancel_yes}</button>
                <button type="button" className="btn btn--sm" onClick={() => setConfirmCancel(null)} data-testid="cancel-confirm-no">{ROOM_LEFT.cancel_no}</button>
              </div>
            </div>
          )}
        </section>

        <section className="s7__center" aria-label={ROOM_CENTER.timeline}>
          <div className="s7__timeline" data-testid="timeline">
            <TimelineViewToggle value={view} onChange={changeView} />
            {around && (
              <button type="button" className="btn btn--sm s7__latest" onClick={() => router.replace(selParam(sel) ? `/rooms/${roomId}?work=${selParam(sel)}` : `/rooms/${roomId}`, { scroll: false })} data-testid="to-latest">
                {ROOM_CENTER.to_latest}
              </button>
            )}
            {hasOlder && (
              <button type="button" className="msg__link s7__older" onClick={() => void loadOlder()} data-testid="load-older">{ROOM_CENTER.load_older}</button>
            )}
            {pick && (
              <div className="s7__pickbar" role="status" data-testid="pick-bar" data-step={pick.from ? "to" : "from"}>
                <span>{pick.from ? SUMMARY_PICK.bar_to : SUMMARY_PICK.bar_from}</span>
                <button type="button" className="msg__link" onClick={() => { setPick(null); setPickNonce((n) => n + 1); }} data-testid="pick-cancel">{SUMMARY_PICK.cancel}</button>
              </div>
            )}
            {freshRoom && (
              <div className="empty" data-testid="room-fresh">
                <div className="empty__title">{ROOM_CENTER.empty_title}</div>
                <div className="row" style={{ gap: 8, justifyContent: "center", flexWrap: "wrap" }}>
                  <button type="button" className="btn btn--primary btn--sm" onClick={openParticipants} data-testid="fresh-invite">{ROOM_CENTER.empty_invite}</button>
                  <button type="button" className="btn btn--sm" onClick={() => inputRef.current?.focus()} data-testid="fresh-talk">{ROOM_CENTER.empty_talk}</button>
                  <button type="button" className="btn btn--sm" onClick={openNewWork} disabled={!!newWorkWhy} data-testid="fresh-work">{ROOM_CENTER.empty_open_work}</button>
                </div>
                <div className="empty__body small muted" data-testid="fresh-defaults">
                  {roomDefaultsLine({ room_defaults: { isolation_kind: room.isolation?.kind ?? "none" } })}
                  {" · "}
                  <Link href={`/rooms/${room.id}/settings`}>{ROOM_CENTER.empty_settings}</Link>
                </div>
              </div>
            )}
            {!freshRoom && msgLoaded && messages.length === 0 && !hasOlder && (
              <div className="empty" data-testid="timeline-empty">
                {sel.kind === "all" ? (
                  <>
                    <div className="empty__title">{ROOM_CENTER.empty_messages}</div>
                    <div className="row" style={{ gap: 6, justifyContent: "center", flexWrap: "wrap" }}>
                      {roomAgents.map((p) => (
                        <button key={p.id} type="button" className="chip" onClick={() => setDraft({ content: `@${p.agent!.name} `, nonce: Date.now() })} data-testid="mention-chip">
                          @{p.agent!.name}
                        </button>
                      ))}
                    </div>
                  </>
                ) : (
                  <div className="empty__body">{ROOM_CENTER.empty_filtered}</div>
                )}
              </div>
            )}
            {messages.map((m) => {
              const agentMsg = m.author_type === "agent" && m.source_task_id;
              const askee = m.kind === "blocked_q" ? m.mentions.find((x) => x.kind === "agent")?.display_name : undefined;
              // T-APPROVAL: 확인 요청은 대화 배치(T-CONVO)에서도 가운데 전폭 카드다 — 짝을 못 찾았으면 불러오는 중 자리.
              if (m.kind === "hitl") {
                const hitl = pairHitl(m, hitls);
                return (
                  <div key={m.id} data-message-id={m.id} className="s7__hitl" data-testid="timeline-hitl">
                    {pickButton(m)}
                    {hitl ? (
                      <HitlCard
                        request={hitl}
                        onRespond={(body) => respondHitl(hitl.id, body)}
                        budget={hitl.task_id ? { scope: "task", current: hitl.budget_override_usd, spent: null } : { scope: "session", current: room.limits?.budget_usd ?? null, spent: room.cost_usd ?? 0 }}
                        busy={busy}
                        userName={(uid) => members.find((x) => x.user.id === uid)?.user.display_name}
                      />
                    ) : (
                      <article className="hitl hitl--loading" data-testid="hitl-card-loading" aria-busy="true">
                        <p className="hitl__q">{m.content}</p>
                        <p className="hitl__gate">확인 요청을 불러오는 중…</p>
                      </article>
                    )}
                  </div>
                );
              }
              const hasWork = !!m.work_id;
              const toWorkWhy = hasWork ? ROOM_CENTER.has_work(workTitle(m.work_id)) : archived ? ROOM_HEAD.archived : null;
              return (
                <div key={m.id}>
                  {pickButton(m)}
                  <MessageCard
                    message={m}
                    replies={replies[m.id]}
                    onLoadReplies={loadReplies}
                    onReply={(root) => { setRestart(null); setReplyTo({ id: root.id, authorName: authorName(root) }); }}
                    activity={agentMsg && !isLayered(m) ? <TaskActivity taskId={m.source_task_id!} cache={events} load={loadEvents} slice={sliceOf(m)} /> : undefined}
                    layers={layersFor}
                    conversation={conversationFor}
                    askee={askee}
                    now={now}
                    workLabel={workLabelOf(m.work_id, "message-work-label")}
                    menu={m.kind !== "system" && m.kind !== "summary" ? <MessageMenu id={m.id} why={toWorkWhy} onToWork={() => openWorkFrom(m)} /> : undefined}
                    footer={
                      m.kind === "summary" ? (
                        <p className="small muted" data-testid="summary-label">{m.work_id ? ROOM_CENTER.summary_of(workTitle(m.work_id)) : ROOM_CENTER.summary_room}</p>
                      ) : m.kind === "system" && m.work_id && sel.kind !== "work" ? (
                        <button type="button" className="msg__link" onClick={() => select({ kind: "work", id: m.work_id! })} data-testid="system-work-link">{ROOM_CENTER.open_work_chip}</button>
                      ) : undefined
                    }
                  />
                </div>
              );
            })}
            {Object.entries(deltas).map(([agentId, text]) => (
              <article key={agentId} className="msg" data-testid="message-delta">
                <div className="msg__head">
                  <span className="msg__author msg__author--agent">{agentById.get(agentId)?.name ?? "agent"}</span>
                  <span className="msg__meta">{ROOM_CENTER.writing}</span>
                </div>
                <MessageBody content={text} typing />
              </article>
            ))}
            {working.map(({ taskId, agentId }) => {
              const tail = slices.get(taskId)?.tail ?? null;
              const key = `working:${taskId}`;
              const open = folds[key]?.process ?? false;
              return (
                <WorkingRow
                  key={taskId}
                  taskId={taskId}
                  agentName={agentById.get(agentId)?.name ?? "agent"}
                  summary={summarizeProcess(events[taskId], tail)}
                  open={open}
                  onToggle={() => toggleFold(key, "process", open)}
                >
                  <TaskActivity taskId={taskId} cache={events} load={loadEvents} slice={tail} />
                </WorkingRow>
              );
            })}
            {typingAgents.length > 0 && (
              <p className="small muted-3" data-testid="typing">
                {typingAgents.map((n) => `@${n}`).join(", ")} {ROOM_CENTER.typing}
              </p>
            )}
            <div ref={bottomRef} data-testid="timeline-end" />
          </div>
          <footer className="s7__composer" ref={composerRef} id="room-composer">
            {restart && (
              <div className="row" style={{ justifyContent: "flex-end" }}>
                <button type="button" className="msg__link" onClick={() => { setRestart(null); setDraft({ content: "", nonce: Date.now() }); }} data-testid="restart-cancel">
                  {ROOM_LEFT.cancel_no}
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
              disabled={!!composerDisabledWhy}
              disabledReason={composerDisabledWhy ?? undefined}
              notice={composerDisabledWhy ?? undefined}
              placeholder={placeholder}
              inputRef={inputRef}
              workSelector={restart ? undefined : {
                options: openWorks.map((w) => ({ id: w.id, title: w.title })),
                value: composerValue,
                onChange: (v) => setComposerPick({ value: v }),
              }}
            />
          </footer>
        </section>

        <section className="s7__right" data-testid="s7-right">
          <div className="s7__work" data-testid="s7-work">
            <WorkPanel
              mode={mode}
              work={work}
              notFound={workNotFound}
              busy={busy}
              agentName={agentName}
              onPause={() => void workAct("/works/{workId}/pause")}
              onResume={(body) => void workAct("/works/{workId}/resume", body)}
              onComplete={() => void workAct("/works/{workId}/complete", { confirm: true })}
              onCancel={() => void workAct("/works/{workId}/cancel", {})}
              onOpenHitl={(hid) => { const h = hitls.find((x) => x.id === hid); if (h?.message_id) jumpToMessage(h.message_id); }}
              onNewWork={openNewWork}
              newWorkDisabled={newWorkWhy}
              onOpenSummary={jumpToMessage}
              onBackToRoom={() => select({ kind: "all" })}
              onEdit={() => setWorkDialog("edit")}
              onChangeDirector={() => setWorkDialog("director")}
              onFixCondition={() => setWorkDialog("condition")}
              canManage={canManage}
              roomBudget={room.limits?.budget_usd ?? null}
              onSetBudget={async (usd) => {
                if (!work) return;
                setWork(await api.patch("/works/{workId}", { path: { workId: work.id }, body: { limits: { budget_usd: usd } } }));
                void loadRoom().catch(() => undefined);
              }}
            />
          </div>
          <div className="s7__room" data-testid="s7-room">
            <RoomPanel
              room={room}
              works={works}
              artifacts={artifacts}
              decisions={decisions}
              selectedWorkId={panelWorkId}
              open={panelOpen || col === "room"}
              onToggle={() => setPanelOpen((v) => { writeLocal(`colab.room.${roomId}.panel`, !v); return !v; })}
              runtimeName={runtimeName}
              defaultDirectorName={defaultDirector}
              reads={reads}
              busy={busy}
              onSetBudget={caps.has("configure") && !archived ? async (usd) => setRoom(await api.patch("/rooms/{roomId}", { path: { roomId }, body: { limits: { budget_usd: usd } } })) : undefined}
            />
          </div>
        </section>
      </div>

      {showParticipants && (
        <RoomParticipantsDialog
          roomId={roomId}
          onClose={() => { setShowParticipants(false); void loadParticipants(); void loadRoom().catch(() => undefined); }}
          onLeft={() => router.replace("/rooms")}
        />
      )}
      <Suspense fallback={null}>
        <RoomQueryDialogs
          roomId={roomId}
          findMessage={findMessage}
          onWorkOpened={(w) => {
            // 미션이 열리면 그 칩이 선택된 방 화면(S22) — 목록은 work.created 로도 오지만 먼저 읽어 칩이 바로 선다.
            void loadRoom().catch(() => undefined);
            select({ kind: "work", id: w.id });
          }}
        />
      </Suspense>
      {work && workDialog === "edit" && (
        <CreateWorkDialog roomId={roomId} mode="edit" work={work} onOpened={onWorkSaved} onClose={() => setWorkDialog(null)} />
      )}
      {work && workDialog === "director" && (
        <ChangeDirectorDialog work={work} members={members} onSaved={onWorkSaved} onClose={() => setWorkDialog(null)} />
      )}
      {work && workDialog === "condition" && (
        <FixWorkConditionDialog
          work={work}
          agents={roomAgents.map((p) => ({ id: p.agent!.id, name: p.agent!.name }))}
          onSaved={onWorkSaved}
          onClose={() => setWorkDialog(null)}
        />
      )}
      {dialog === "archive" && (
        <ArchiveRoomDialog room={room} onArchived={(r) => { setRoom(r); setDialog(null); }} onClose={() => setDialog(null)} />
      )}
      {dialog === "delete" && (
        <DeleteRoomDialog room={room} onDeleted={() => router.replace(`/rooms?deleted=${encodeURIComponent(room.name)}`)} onClose={() => setDialog(null)} />
      )}
      {error && <p className="problem" role="alert" style={{ marginTop: 12 }} data-testid="room-problem">{error}</p>}
      <style>{`
        .s7 { display: flex; flex-direction: column; min-height: calc(100vh - 48px - 48px); }
        .s7__head { padding-bottom: 10px; border-bottom: 1px solid var(--line); margin-bottom: 12px; display: flex; flex-direction: column; gap: 8px; }
        .s7__spacer { flex: 1; }
        .s7__cols { display: grid; grid-template-columns: 268px minmax(0, 1fr) 268px; gap: 16px; align-items: start; }
        .s7__left, .s7__right { position: sticky; top: 12px; max-height: calc(100vh - 140px); overflow: auto; display: flex; flex-direction: column; gap: 8px; }
        .s7__center { display: flex; flex-direction: column; min-width: 0; }
        .s7__h { margin: 4px 0 2px; font-size: var(--fs-sub); font-weight: 600; color: var(--ink-2); }
        .s7__chips { display: flex; flex-direction: column; gap: 4px; }
        .s7__timeline { flex: 1; display: flex; flex-direction: column; gap: 6px; padding-bottom: 12px; }
        .s7__hitl { align-self: stretch; margin: 8px 0; }
        .s7__older, .s7__latest { align-self: center; }
        .s7__composer { position: sticky; bottom: 0; background: var(--bg); padding: 8px 0 4px; }
        .s7__tabs { display: none; gap: 6px; }
        .s7__tab { border: 1px solid var(--line); background: var(--bg); color: var(--ink); border-radius: 999px; padding: 3px 10px; font-size: var(--fs-body); cursor: pointer; }
        .s7__tab--on { border: 2px solid var(--ink); padding: 2px 9px; font-weight: 600; }
        .s7__panel { position: relative; display: flex; justify-content: flex-end; }
        .s7__panel .s7-actions__dialog { position: static; width: 380px; margin-top: 8px; }
        .s7__confirm { border: 1px solid var(--s-fail); border-radius: 8px; padding: 8px 10px; background: color-mix(in srgb, var(--s-fail) var(--soft-alpha), transparent); }
        .s7__confirm p { margin: 0 0 6px; }
        .s7__pickbar { position: sticky; top: 0; z-index: 2; display: flex; gap: 10px; align-items: center; justify-content: space-between; padding: 6px 10px; border: 2px solid var(--s-block); border-radius: 8px; background: var(--bg); }
        .s7__pickbtn { align-self: flex-start; margin-bottom: 2px; }
        .msg--flash { outline: 2px solid var(--s-block); border-radius: 8px; }
        .skip-link { position: absolute; left: -9999px; top: 0; }
        .skip-link:focus { position: static; align-self: flex-start; margin-bottom: 6px; padding: 4px 10px; border: 2px solid var(--ink); border-radius: 6px; background: var(--bg); color: var(--ink); }
        /* 좁은 화면(≤1100px) — 탭 넷(타임라인 · 보드 · 미션 · 방). 우열이 미션 칸과 방 전체 칸으로 갈려 탭도 둘이다(§4.8). */
        @media (max-width: 1100px) {
          .s7__tabs { display: flex; flex-wrap: wrap; }
          .s7__cols { grid-template-columns: minmax(0, 1fr); }
          .s7__left, .s7__right { position: static; max-height: none; }
          .s7[data-col="timeline"] .s7__left, .s7[data-col="timeline"] .s7__right,
          .s7[data-col="board"] .s7__center, .s7[data-col="board"] .s7__right,
          .s7[data-col="work"] .s7__center, .s7[data-col="work"] .s7__left, .s7[data-col="work"] .s7__room,
          .s7[data-col="room"] .s7__center, .s7[data-col="room"] .s7__left, .s7[data-col="room"] .s7__work { display: none; }
        }
      `}</style>
    </div>
  );
}

/** 메시지 「…」 메뉴 — 「이걸 미션으로」(S21 은 W3). 이미 미션에 속한 메시지·보관된 방에서는 비활성 + 사유(버튼 아래 글자). */
function MessageMenu({ id, why, onToWork }: { id: string; why: string | null; onToWork: () => void }) {
  const [open, setOpen] = useState(false);
  const hint = `msg-menu-hint-${id}`;
  return (
    <span className="msg-menu" onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOpen(false); }}>
      <button type="button" className="msg__link" aria-haspopup="menu" aria-expanded={open} aria-label={ROOM_CENTER.msg_menu} onClick={() => setOpen((v) => !v)} data-testid="message-menu">
        …
      </button>
      {open && (
        <span className="card-menu__list msg-menu__list" role="menu" data-testid="message-menu-list">
          <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!!why || undefined} aria-describedby={why ? hint : undefined}
            onClick={() => { if (why) return; setOpen(false); onToWork(); }} data-testid="message-to-work">
            {ROOM_CENTER.to_work}
          </button>
          {why && <DisabledHint id={hint}>{why}</DisabledHint>}
        </span>
      )}
    </span>
  );
}
