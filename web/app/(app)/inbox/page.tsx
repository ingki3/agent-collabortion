"use client";
/**
 * S8 받은 요청(SCREEN v0.19.2 §4.14 · 옛 §4.6) — "지금 내가 답해야 할 것"을 **방을 열지 않고** 처리한다(F2, U3).
 *
 * 정렬은 계약이 정한 서버 몫이다(`listInbox`: overdue → action_required → attention → info, 묶음 안에서
 * 기한 임박 순). 화면이 다시 정렬하면 서버와 다른 순서를 말하게 되므로 **받은 순서를 그대로 그린다**.
 *
 * **필터는 두 줄**(v0.19, §4.14): 첫 줄은 칩(전체·미읽음·조치 필요 — 계약 `filter`, 서버가 거른다), 둘째 줄은 **방·미션 선택 상자**.
 * 둘째 줄은 받은 목록 안에서 거른다 — 선택지가 그 목록에서 나오므로(없는 방을 고를 수 없다) 서버 페이지와 어긋나지 않는다.
 * 목록은 **한 열 그대로**다(COMPONENTS §8.5).
 *
 * **항목당 왕복은 원칙적으로 없다**(K-9, #147). v0.19 의 예외 둘만 있다 — 둘 다 드문 방 층 항목이다:
 *   · `room_paused` → 그 방의 `getRoom` 한 번(`blocked_detail`: 멈춘 미션 수·잔여 합계·위임 시각 — Lead Q5 「카드당 1회, 드묾」)
 *   · `isolation_confirm` → 그 요청의 `getHitlRequest` 한 번(저장소 고르기 선택지·위임 시각 — Lead Q6)
 * 방 이름은 계약 0.2.9 `InboxItem.room` 이 싣는다. 서버가 아직 안 채운 항목이 있으면 `listRooms` 한 번으로 이름표를 만든다(방마다 묻지 않는다).
 *
 * 응답은 `respondHitlRequest`(멱등키 필수) 로 보낸다. 성공하면 그 항목을 목록에서 내린다 — U3 2단계의
 * "카드가 사라짐"이 처리됐다는 유일한 신호다.
 *
 * 모바일: v1 은 데스크톱 우선이되 **인박스 응답만 모바일 웹에서 동작한다**(SCREEN §8.2 Q6) — 이 화면은
 * 한 열로 접히고 버튼이 터치 크기를 유지한다.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { InboxItemCard, type InboxAction } from "@/components/InboxItemCard";
import { PageHead } from "@/components/PageHead";
import { api, errorMessage, newIdempotencyKey } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { useWorkspaceStream } from "@/lib/realtime/StreamContext";
import { applyScope, filterOptions, INBOX_V19, isDelegatedBasis, needsDelegationLine, roomIdOf, type BlockedDetail } from "@/lib/inbox-v19";
import type { HitlRequest, HitlResponse, InboxItem, InboxSummary, Room, RoomListItem, StreamEvent } from "@/lib/api/types";

type Filter = "all" | "unread" | "action_required";

const FILTER_LABEL: Record<Filter, string> = {
  all: "전체",
  unread: "미읽음",
  action_required: "조치 필요",
};

/** 카드가 목록 밖에서 더 아는 것(방 층 항목만). 키는 방 id · 요청 id. */
interface Enrich {
  names: Map<string, string>;
  /** 방 id → 참여자(사람) 이름 — 위임 줄의 「방장 〈민호〉」. */
  people: Map<string, Map<string, string>>;
  rooms: Map<string, Room>;
  hitls: Map<string, HitlRequest>;
}
const EMPTY: Enrich = { names: new Map(), people: new Map(), rooms: new Map(), hitls: new Map() };

export default function InboxPage() {
  const router = useRouter();
  const { workspace, me } = useAuth();
  const [items, setItems] = useState<InboxItem[] | null>(null);
  const [summary, setSummary] = useState<InboxSummary | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [roomFilter, setRoomFilter] = useState<string>("");
  const [workFilter, setWorkFilter] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [enrich, setEnrich] = useState<Enrich>(EMPTY);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  const load = useCallback(async () => {
    if (!workspace) return;
    try {
      const [page, sum] = await Promise.all([
        api.get("/inbox", { query: { workspace_id: workspace.id, filter } }),
        api.get("/inbox/summary", { query: { workspace_id: workspace.id } }),
      ]);
      setItems(page.items ?? []);
      setSummary(sum);
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
      setItems([]);
    }
  }, [workspace, filter]);

  useEffect(() => {
    void load();
  }, [load]);

  /**
   * 방 층 항목의 덧붙임. 이미 읽은 방·요청은 다시 묻지 않는다(항목이 SSE 로 바뀌어도 같은 방이면 한 번).
   * 실패는 조용히 넘긴다 — 덧붙임이 없으면 카드는 그 줄을 그리지 않을 뿐이다(수를 지어내지 않는다).
   */
  useEffect(() => {
    if (!workspace || !items) return;
    let dead = false;
    void (async () => {
      const next: Enrich = { names: new Map(enrich.names), people: new Map(enrich.people), rooms: new Map(enrich.rooms), hitls: new Map(enrich.hitls) };
      let changed = false;
      const needNames = items.some((it) => (!it.room?.name && roomIdOf(it) && !next.names.has(roomIdOf(it)!)) || (isDelegatedBasis(it.recipient_basis) && roomIdOf(it) && !next.people.has(roomIdOf(it)!)));
      if (needNames) {
        const page = await api
          .get("/workspaces/{workspaceId}/rooms", { path: { workspaceId: workspace.id }, query: { participating: false, include_archived: true, limit: 200 } })
          .catch(() => null);
        for (const r of (page?.items ?? []) as RoomListItem[]) {
          next.names.set(r.id, r.name);
          next.people.set(r.id, new Map(r.participants.filter((p) => p.kind === "user").map((p) => [p.id, p.name])));
        }
        changed = true;
      }
      const roomIds = new Set<string>();
      const hitlIds = new Set<string>();
      for (const it of items) {
        const rid = roomIdOf(it);
        if (it.type === "room_paused" && rid && !next.rooms.has(rid)) roomIds.add(rid);
        // 근거가 비어 온 방 층 항목 — 방장이 누구인지 보고 「방장으로서」를 정한다(basisFallback).
        if (!it.recipient_basis && (it.type === "room_paused" || it.type === "isolation_confirm") && rid && !next.rooms.has(rid)) roomIds.add(rid);
        if (isDelegatedBasis(it.recipient_basis) && needsDelegationLine(it) && rid && !next.rooms.has(rid)) roomIds.add(rid);
        if (it.ref_id && !next.hitls.has(it.ref_id) && (it.type === "isolation_confirm" || (it.type === "hitl_request" && needsDelegationLine(it) && isDelegatedBasis(it.recipient_basis)))) hitlIds.add(it.ref_id);
      }
      await Promise.all([
        ...[...roomIds].map(async (id) => {
          const r = await api.get("/rooms/{roomId}", { path: { roomId: id } }).catch(() => null);
          if (r) next.rooms.set(id, r);
        }),
        ...[...hitlIds].map(async (id) => {
          const h = await api.get("/hitl-requests/{hitlRequestId}", { path: { hitlRequestId: id } }).catch(() => null);
          if (h) next.hitls.set(id, h);
        }),
      ]);
      if (roomIds.size || hitlIds.size) changed = true;
      if (!dead && changed) setEnrich(next);
    })();
    return () => {
      dead = true;
    };
    // 덧붙임 자체는 의존성에서 뺀다 — 넣으면 채울 때마다 다시 돈다. 새 항목(items)이 올 때만 모자란 것을 채운다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, workspace]);

  // 실시간(R4) — 셸의 워크스페이스 SSE 하나를 구독한다. 항목이 생기거나 해소되면 다시 읽는다.
  const onEvent = useCallback((ev: StreamEvent) => {
    switch (ev.type) {
      case "inbox.item_created":
      case "inbox.item_updated": {
        const it = ev.payload as unknown as InboxItem;
        setItems((cur) => {
          if (!cur) return cur;
          const at = cur.findIndex((x) => x.id === it.id);
          if (at < 0) return [it, ...cur];
          const next = [...cur];
          next[at] = it;
          return next;
        });
        break;
      }
      case "inbox.summary": {
        const p = ev.payload as unknown as InboxSummary;
        if (typeof p.action_required === "number") setSummary(p);
        break;
      }
      case "room.updated": {
        // 방 멈춤이 풀리거나 위임 시각이 바뀌면 그 방의 덧붙임을 버린다 — 다음 렌더에서 다시 읽는다.
        const p = ev.payload as { id?: string; room_id?: string };
        const id = p.room_id ?? p.id ?? ev.room_id ?? null;
        if (id) setEnrich((cur) => (cur.rooms.has(id) ? { ...cur, rooms: new Map([...cur.rooms].filter(([k]) => k !== id)) } : cur));
        break;
      }
      default:
        break;
    }
  }, []);
  useWorkspaceStream(workspace?.id, onEvent, { onResync: () => void load() });

  /** 둘째 줄 선택지 — 지금 받은 목록에 있는 방·미션만(SCREEN §4.14 「필터: … 방별 / 미션별」). */
  const options = useMemo(() => filterOptions(items ?? [], enrich.names, roomFilter || undefined), [items, enrich.names, roomFilter]);
  const shown = useMemo(() => applyScope(items ?? [], roomFilter, workFilter), [items, roomFilter, workFilter]);

  async function respond(item: InboxItem, body: HitlResponse) {
    if (!item.ref_id) throw new Error("이 항목에는 응답할 요청이 연결돼 있지 않습니다");
    setBusy(true);
    try {
      const r = await api.post("/hitl-requests/{hitlRequestId}/response", {
        path: { hitlRequestId: item.ref_id },
        // 계약 필수 헤더 — 두 번째 응답은 오류가 아니라 무시(`ignored: true`, E7-08).
        idempotencyKey: newIdempotencyKey(),
        body,
      });
      setItems((cur) => (cur ? cur.filter((x) => x.id !== item.id) : cur));
      setSummary((cur) => (cur ? { ...cur, action_required: Math.max(0, cur.action_required - (item.severity === "action_required" ? 1 : 0)) } : cur));
      setToast(
        r.ignored
          ? "이미 답변된 요청이라 무시했습니다 — 첫 응답이 유지됩니다."
          : item.type === "room_paused"
            ? INBOX_V19.room_resumed
            : item.type === "isolation_confirm"
              ? INBOX_V19.isolation_answered
              : `${item.card?.agent_name ? `@${item.card.agent_name}가` : "에이전트가"} 재개됩니다`,
      );
    } finally {
      setBusy(false);
    }
  }

  async function act(item: InboxItem, action: InboxAction | "open_workdirs") {
    const rid = roomIdOf(item);
    const roomHref = rid ? `/rooms/${rid}` : null;
    switch (action) {
      case "open_session":
      case "open_room":
      case "reply":
        if (roomHref) router.push(item.work_id && action !== "open_room" ? `${roomHref}?work=${item.work_id}` : roomHref);
        return;
      case "open_work":
        if (roomHref) router.push(item.work_id ? `${roomHref}?work=${item.work_id}` : roomHref);
        return;
      case "restart":
        // 재지시는 작업 줄기 카드의 작성창을 거친다 — 인박스에서 맥락 없이 새 지시를 만들 수 없다(SCREEN §4.5 m6).
        if (roomHref) router.push(`${roomHref}?restart_lane=${item.card?.lane_id ?? item.lane_id ?? ""}`);
        return;
      case "open_runtimes":
      case "rebind":
      case "open_workdirs":
        router.push("/runtimes");
        return;
      default:
        return;
    }
  }

  async function markRead(item: InboxItem) {
    try {
      const updated = await api.post("/inbox/{inboxItemId}/read", { path: { inboxItemId: item.id } });
      setItems((cur) => (cur ? cur.map((x) => (x.id === item.id ? updated : x)) : cur));
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  async function markAllRead() {
    if (!workspace) return;
    try {
      // `action_required` 는 건드리지 않는다(계약 markAllInboxRead) — 해소는 응답이 한다.
      await api.post("/inbox/read-all", { query: { workspace_id: workspace.id } });
      await load();
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  /** 카드 한 장의 덧붙임 — 방 층 항목만 채운다. */
  function extrasOf(it: InboxItem) {
    const rid = roomIdOf(it);
    const room = rid ? enrich.rooms.get(rid) : undefined;
    const h = it.ref_id ? enrich.hitls.get(it.ref_id) : undefined;
    const ownerName = room && rid ? (enrich.people.get(rid)?.get(room.owner_user_id) ?? null) : null;
    return {
      blocked: it.type === "room_paused" ? ((room?.blocked_detail ?? null) as BlockedDetail | null) : null,
      ownerName,
      respondFrom: h?.can_respond_from ?? (it.type === "room_paused" ? (room?.blocked_detail?.delegate_at ?? null) : null),
      options: h?.options ?? null,
      basisFallback: !it.recipient_basis && room && me && room.owner_user_id === me.user.id && (it.type === "room_paused" || it.type === "isolation_confirm")
        ? ("room_owner" as const)
        : null,
    };
  }

  if (!workspace) return null;

  return (
    <div className="s8" data-testid="inbox-page" data-filter={filter}>
      <PageHead screen="inbox">
        <span className="s8__counts" data-testid="inbox-counts">
          조치 필요 <b data-testid="inbox-count-action">{summary?.action_required ?? 0}</b>
          {summary?.overdue ? <> · 기한 지남 <b data-testid="inbox-count-overdue">{summary.overdue}</b></> : null}
        </span>
      </PageHead>

      {/* 필터 첫 줄 — 칩(서버 `filter`). */}
      <nav className="s8__filters" aria-label="인박스 필터">
        {(["all", "unread", "action_required"] as Filter[]).map((f) => (
          <button
            key={f}
            type="button"
            className={`s8__filter${filter === f ? " s8__filter--on" : ""}`}
            aria-pressed={filter === f}
            onClick={() => setFilter(f)}
            data-testid={`inbox-filter-${f}`}
          >
            {FILTER_LABEL[f]}
          </button>
        ))}
        <span className="s8__spacer" />
        <button type="button" className="msg__link" onClick={() => void markAllRead()} data-testid="inbox-read-all">
          전부 읽음(알림·주의만)
        </button>
      </nav>

      {/* 필터 둘째 줄 — 방·미션 선택 상자(§4.14 「칩과 선택 상자를 줄을 나눠 놓는다」). 고를 것이 하나뿐이면 그리지 않는다. */}
      {(options.rooms.length > 1 || options.works.length + (options.noWork ? 1 : 0) > 1 || roomFilter || workFilter) && (
        <div className="s8__scope" data-testid="inbox-scope">
          <select
            className="select"
            value={roomFilter}
            onChange={(e) => { setRoomFilter(e.target.value); setWorkFilter(""); }}
            aria-label={INBOX_V19.filter_room}
            data-testid="inbox-filter-room"
          >
            <option value="">{INBOX_V19.all_rooms}</option>
            {options.rooms.map(([id, name]) => <option key={id} value={id}>{name}</option>)}
          </select>
          <select
            className="select"
            value={workFilter}
            onChange={(e) => setWorkFilter(e.target.value)}
            aria-label={INBOX_V19.filter_work}
            data-testid="inbox-filter-work"
          >
            <option value="">{INBOX_V19.all_works}</option>
            {options.works.map(([id, title]) => <option key={id} value={id}>{title}</option>)}
            {options.noWork && <option value="none">{INBOX_V19.no_work}</option>}
          </select>
        </div>
      )}

      {toast && (
        <p className="s8__toast" role="status" data-testid="inbox-toast">{toast}</p>
      )}
      {error && <p className="problem" role="alert" data-testid="inbox-error">{error}</p>}

      {items === null ? (
        <p className="muted">불러오는 중…</p>
      ) : shown.length === 0 ? (
        <div className="empty" data-testid="inbox-empty">
          {/* 부정형이 아니라 상태로(SCREEN §7). */}
          <div className="empty__title">지금 답할 것이 없습니다</div>
          <div className="empty__body">응답이 필요해지면 여기와 이메일로 알려 드립니다.</div>
        </div>
      ) : (
        <div className="s8__list" data-testid="inbox-list">
          {shown.map((it) => (
            <InboxItemCard
              key={it.id}
              item={it}
              onRespond={respond}
              onAction={(i, a) => void act(i, a)}
              onMarkRead={(i) => void markRead(i)}
              busy={busy}
              now={now}
              roomNames={enrich.names}
              {...extrasOf(it)}
            />
          ))}
        </div>
      )}

      <style>{`
        .s8__counts { color: var(--ink-2); font-size: var(--fs-sub); }
        .s8__filters { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin: 8px 0 6px; }
        .s8__filter { border: 1px solid var(--line); background: var(--bg); border-radius: 999px; padding: 4px 12px; font-size: var(--fs-body); cursor: pointer; }
        .s8__filter--on { border-color: var(--ink); font-weight: 600; }
        .s8__spacer { flex: 1; }
        .s8__scope { display: flex; gap: 6px; flex-wrap: wrap; margin: 0 0 10px; }
        .s8__scope .select { width: auto; max-width: 100%; }
        /*
         * 인박스는 **한 열**이다(§8.5, PR #191 R1) — 위에서 아래로 읽고 답하는 목록이라 열이 갈리면 순서가 흐려지고,
         * 항목마다 답 입력·버튼이 있어 높이가 제각각이라 다열과 상성이 나쁘다. 열 하나의 폭은 --card-min(tokens.css) 이상,
         * 760px 이하 — 1920px 에서 한 줄이 화면 폭만큼 늘어지지 않게 한다.
         */
        .s8__list { display: grid; gap: 10px; grid-template-columns: minmax(var(--card-min), 760px); align-items: start; }
        .s8__toast { margin: 0 0 8px; font-size: var(--fs-sub); color: var(--s-done-text); }
        /* 인박스 응답만 모바일 웹 대상이다(SCREEN §8.2 Q6) — 한 열, 버튼은 줄바꿈해도 크기를 지킨다. */
        @media (max-width: 640px) {
          .s8__filters { gap: 4px; }
          .s8__list { grid-template-columns: minmax(0, 1fr); gap: 10px; }
        }
      `}</style>
    </div>
  );
}
