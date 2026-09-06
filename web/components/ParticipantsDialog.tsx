"use client";
/**
 * 참여 에이전트 추가·제거(SCREEN §4.5 O2 · FR-1.5) — S7 상단 액션의 다이얼로그.
 *
 * - **초대 권한은 `respond_to` 가 정한다**(FR-1.9). 계약이 `Agent.invitable {allowed, reason}` 으로 이미
 *   계산해 주므로 화면은 그 값을 읽어 비활성 + 사유를 보인다 — 규칙을 로컬로 다시 계산하면 서버와 반대로 말한다.
 * - **아카이브된 에이전트는 신규 초대 목록에서 제외**한다(SCREEN §8.2 Q3 Lead 결정). 과거 메시지의 칩은
 *   그대로 두고 "보관됨"으로 표시하는 쪽은 `AgentChip` 이다.
 * - **제거는 진행 중 lane 이 없을 때만**(계약 removeParticipant `409`). 화면이 먼저 막고, 서버 판정이 최종이다.
 * - 프로파일도 함께 고른다. 세션 런타임에 없는 `runtime_kind` 면 서버가 `warnings[]` 를 준다.
 */
import { useMemo, useState } from "react";
import "./session-actions.css";
import { AgentChip } from "./AgentChip";
import type { Agent, Lane, Participant } from "@/lib/api/types";

/** 진행 중 lane 상태 4종(계약 removeParticipant). 이 lane 이 있으면 제거할 수 없다. */
const BUSY_LANE = new Set<Lane["status"]>(["queued", "running", "waiting_human", "paused"]);

export function removalBlock(agentId: string, lanes: Lane[], assigneeAgentId: string | null | undefined): string | null {
  if (assigneeAgentId && agentId === assigneeAgentId) return "assignee 는 제거할 수 없습니다 — 먼저 다른 assignee 를 지정하세요";
  const busy = lanes.filter((l) => l.agent_id === agentId && BUSY_LANE.has(l.status));
  return busy.length > 0
    ? `진행 중 lane 이 ${busy.length}개 있습니다 — 먼저 끝내거나 중단하세요`
    : null;
}

export interface ParticipantsDialogProps {
  participants: Participant[];
  agents: Agent[];
  lanes: Lane[];
  assigneeAgentId: string | null | undefined;
  canManage: boolean;
  onAdd: (agentId: string, profileId: string | null) => Promise<void> | void;
  onRemove: (agentId: string) => Promise<void> | void;
  onSetAssignee: (agentId: string) => Promise<void> | void;
  onClose: () => void;
  busy?: boolean;
  warnings?: string[];
}

export function ParticipantsDialog(props: ParticipantsDialogProps) {
  const joined = useMemo(() => new Set(props.participants.map((p) => p.agent_id)), [props.participants]);
  const [pick, setPick] = useState("");
  const [profile, setProfile] = useState("");

  // 신규 초대 후보 — 이미 참여 중이거나 **보관된** 에이전트는 목록에서 뺀다(Q3).
  const candidates = props.agents.filter((a) => !joined.has(a.id) && !a.archived_at);
  const picked = candidates.find((a) => a.id === pick) ?? null;
  const reason = props.canManage ? undefined : "Director 만 참여자를 바꿀 수 있습니다";

  return (
    <div className="s7-actions__dialog s7-actions__dialog--wide" role="dialog" aria-label="참여 에이전트 관리" data-testid="participants-dialog">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <b className="small">참여 에이전트</b>
        <button type="button" className="msg__link" onClick={props.onClose} data-testid="participants-close">닫기</button>
      </div>

      <ul className="s7-actions__list" data-testid="participants-current">
        {props.participants.map((p) => {
          const block = removalBlock(p.agent_id, props.lanes, props.assigneeAgentId);
          return (
            <li key={p.agent_id} className="s7-actions__row" data-agent-id={p.agent_id}>
              <AgentChip
                agentId={p.agent_id}
                name={p.agent.name}
                role={p.agent.role}
                status={p.status}
                isAssignee={p.is_assignee}
                size="sm"
              />
              <span className="s7-actions__spacer" />
              {!p.is_assignee && (
                <button
                  type="button"
                  className="btn btn--sm"
                  disabled={!props.canManage || props.busy}
                  title={reason}
                  onClick={() => void props.onSetAssignee(p.agent_id)}
                  data-testid="participant-assignee"
                >
                  assignee 로
                </button>
              )}
              <button
                type="button"
                className="btn btn--sm"
                disabled={!props.canManage || props.busy || block !== null}
                title={reason ?? block ?? undefined}
                onClick={() => void props.onRemove(p.agent_id)}
                data-testid="participant-remove"
              >
                제거
              </button>
            </li>
          );
        })}
        {props.participants.length === 0 && <li className="small muted-3">참여 에이전트 없음</li>}
      </ul>

      <div className="s7-actions__add">
        <label className="s7-actions__field">
          <span className="small">에이전트 추가</span>
          <select
            className="select"
            value={pick}
            onChange={(e) => { setPick(e.target.value); setProfile(""); }}
            disabled={!props.canManage || props.busy}
            title={reason}
            data-testid="participant-pick"
          >
            <option value="">고르세요</option>
            {candidates.map((a) => (
              <option key={a.id} value={a.id} disabled={a.invitable?.allowed === false}>
                {a.name} · {a.role}
                {a.invitable?.allowed === false ? ` — ${a.invitable.reason ?? "초대 불가"}` : ""}
              </option>
            ))}
          </select>
        </label>
        {picked && picked.invitable?.allowed === false && (
          <p className="small s7-actions__warn" data-testid="participant-not-invitable">
            {picked.invitable.reason ?? "이 에이전트는 초대할 수 없습니다(respond_to)"}
          </p>
        )}
        {picked && picked.profiles.length > 1 && (
          <label className="s7-actions__field">
            <span className="small">프로파일</span>
            <select className="select" value={profile} onChange={(e) => setProfile(e.target.value)} data-testid="participant-profile">
              <option value="">기본 프로파일</option>
              {picked.profiles.map((p) => (
                <option key={p.id} value={p.id}>{p.name} · {p.runtime_kind} · {p.model}</option>
              ))}
            </select>
          </label>
        )}
        <button
          type="button"
          className="btn btn--sm btn--primary"
          disabled={!props.canManage || props.busy || !picked || picked.invitable?.allowed === false}
          title={reason}
          onClick={() => picked && void props.onAdd(picked.id, profile || null)}
          data-testid="participant-add"
        >
          추가
        </button>
      </div>

      {props.warnings?.map((w, i) => (
        <p key={i} className="small s7-actions__warn" data-testid="participant-warning">{w}</p>
      ))}
      <p className="small muted-3">
        추가·제거는 시스템 메시지로 타임라인에 남습니다 — 로스터가 바뀌면 이후 위임 판단이 달라지므로 에이전트도 알아야 합니다.
      </p>
    </div>
  );
}

export default ParticipantsDialog;
