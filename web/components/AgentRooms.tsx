"use client";
/**
 * S9 에이전트 카드의 방 줄(SCREEN v0.19.2 §4.15, T-R2-W4a) — 「참여 중인 방 N」을 누르면 그 방 목록이 펼쳐진다.
 *
 * **볼 수 있는 방만** 이름으로 나오고, 못 보는 방은 개수에만 포함해 「+ 볼 수 없는 방 1」로 적는다 — 개수를 속이면 "왜 3인데 2개만
 * 보이나" 가 되고, 이름을 보이면 `invited` 방의 존재가 샌다(§2.4). 이 칸이 없으면 "이 Writer 가 어느 방들에 있나" 를 물을 자리가 없다.
 *
 * 카드 전체가 링크라 이 줄은 그 **밖**에 둔다(링크 안의 버튼은 누를 수 없다).
 */
import { useState } from "react";
import Link from "next/link";
import { AGENT_ROOMS, agentRoomsView } from "@/lib/screens-v19";
import type { Agent } from "@/lib/api/types";

/** 링크처럼 보이는 펼침 버튼(카드 안의 두 번째 행동이라 무게를 낮춘다). */
const TOGGLE: React.CSSProperties = { background: "none", border: 0, padding: 0, color: "var(--ink-2)", textDecoration: "underline", cursor: "pointer" };

export function AgentRooms({ agent }: { agent: Agent }) {
  const [open, setOpen] = useState(false);
  const v = agentRoomsView(agent);
  const listId = `agent-rooms-${agent.id}`;
  return (
    <div className="agent-rooms" data-testid="agent-rooms" data-total={v.total}>
      {agent.running_task_count != null && (
        <div className="small muted-3" data-testid="agent-concurrent">{AGENT_ROOMS.concurrent(agent.max_concurrent_tasks, agent.running_task_count)}</div>
      )}
      {v.total === 0 ? (
        <div className="small muted-3" data-testid="agent-rooms-none">{AGENT_ROOMS.none}</div>
      ) : (
        <button type="button" className="small" style={TOGGLE} aria-expanded={open} aria-controls={listId} onClick={() => setOpen((x) => !x)} data-testid="agent-rooms-toggle">
          {AGENT_ROOMS.count(v.total)} {open ? "▴" : "▾"}
        </button>
      )}
      {open && (
        <ul id={listId} className="agent-rooms__list small" aria-label={AGENT_ROOMS.list_label} data-testid="agent-rooms-list">
          {v.rooms.map((r) => (
            <li key={r.id}><Link href={`/rooms/${r.id}`} data-testid="agent-room-link">{r.name}</Link></li>
          ))}
          {v.hidden > 0 && <li className="muted-3" data-testid="agent-rooms-hidden">{AGENT_ROOMS.hidden(v.hidden)}</li>}
        </ul>
      )}
    </div>
  );
}
