"use client";
/**
 * 좌열 참여자 — **사람과 에이전트가 한 목록**(SCREEN §4.6 좌열 · §7 `room_participant`).
 *
 * 사람 칩(COMPONENTS §9.3 Person Chip): 에이전트 칩과 같은 격자, 배지 슬롯은 꺼진다(사람에게 실행 상태는 없다 — 빈 자리를 남기지 않는다).
 * 방 역할은 한국어(방장·부방장).
 *
 * 에이전트 칩의 **「다른 방에서 작업 중」**(§4.6 · §9.3 주의): 에이전트 상태는 워크스페이스 전역 할 일에서 파생되므로, 이 방에 실행 중인
 * 서브 미션이 없는데 `working` 이면 둘째 줄의 **프로파일 표시를 밀어내고** 그 자리에 쓴다 — 더하지 않고 바꿔 넣는다. 어느 방인지는 적지 않는다.
 */
import "./agent-chip.css";
import { AgentChip } from "./AgentChip";
import { ROOM_LEFT } from "@/lib/wording";
import type { Lane } from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

export type RoomParticipant = components["schemas"]["RoomParticipant"];

export function PersonChip({ name, avatarUrl, role }: { name: string; avatarUrl?: string | null; role: RoomParticipant["room_role"] }) {
  return (
    <div className="agent-chip person-chip" data-testid="person-chip" data-room-role={role}>
      <span className="agent-chip__avatar" aria-hidden="true">
        {avatarUrl ? <img src={avatarUrl} alt="" /> : name.slice(0, 1).toUpperCase()}
      </span>
      <span className="agent-chip__text">
        <span className="agent-chip__line1">
          <span className="agent-chip__name">{name}</span>
          {role === "owner" && <span className="agent-chip__assignee" data-testid="person-role">{ROOM_LEFT.owner}</span>}
          {role === "deputy" && <span className="agent-chip__assignee" data-testid="person-role">{ROOM_LEFT.deputy}</span>}
        </span>
      </span>
    </div>
  );
}

/** 이 방에 실행 중인 서브 미션이 없는데 칩이 working 인가 — 「다른 방에서 작업 중」의 판정. */
export function workingElsewhere(p: Pick<RoomParticipant, "kind" | "status" | "agent">, lanes: Pick<Lane, "agent_id" | "status">[]): boolean {
  if (p.kind !== "agent" || p.status !== "working" || !p.agent) return false;
  return !lanes.some((l) => l.agent_id === p.agent!.id && l.status === "running");
}

export function RoomParticipants({ participants, lanes, archivedAgent }: { participants: RoomParticipant[]; lanes: Lane[]; archivedAgent?: (id: string) => boolean }) {
  const people = participants.filter((p) => p.kind === "user" && !p.left_at);
  const agents = participants.filter((p) => p.kind === "agent" && !p.left_at && p.agent);
  // 방장 → 부방장 → 나머지(사람) → 에이전트 — 한 목록이되 아이콘으로 구분한다.
  const rank = (r: RoomParticipant["room_role"]) => (r === "owner" ? 0 : r === "deputy" ? 1 : 2);
  people.sort((a, b) => rank(a.room_role) - rank(b.room_role));
  return (
    <div className="s7__chips" data-testid="participants">
      {people.map((p) => (
        <PersonChip key={p.id} name={p.user?.display_name || p.user?.email || ""} avatarUrl={p.user?.avatar_url} role={p.room_role} />
      ))}
      {agents.map((p) => {
        const away = workingElsewhere(p, lanes);
        return (
          <AgentChip
            key={p.id}
            agentId={p.agent!.id}
            name={p.agent!.name}
            role={p.agent!.role}
            status={p.status ?? "idle"}
            // 바꿔 넣는다 — 프로파일 자리에 「다른 방에서 작업 중」(더하면 268px 에서 세 줄이 된다).
            profile={away ? ROOM_LEFT.elsewhere : p.profile ? `${p.profile.runtime_kind} · ${p.profile.model}` : null}
            statusNote={away ? null : p.status_note}
            archived={archivedAgent?.(p.agent!.id)}
            size="md"
          />
        );
      })}
    </div>
  );
}

export default RoomParticipants;
