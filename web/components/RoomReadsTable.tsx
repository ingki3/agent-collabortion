"use client";
/**
 * S23 맥락 읽기 기록(`/rooms/:id/reads`) — SCREEN v0.19.2 §4.13, T-R2-W3. **⑤의 안전장치다.**
 *
 * `room_read_log` 한 표를 세 방향으로 읽는다(`listRoomReads` `direction`): 이 방이 읽은 것(out) · 이 방이 읽힌 것(in) · 거부된 시도(denied).
 *   - 읽힌 쪽에는 **읽은 쪽 방 이름을 적는다**(그 사건으로 이미 드러났다).
 *   - 거부 행은 **어느 방인지 적지 않는다**(존재 숨김, FR-4.5) — 서버가 `other_room: null` 로 지우고, 화면도 `showsRoomName` 으로 한 번 더
 *     막는다. **예외 하나** `originator_left` — PRD 문장 그대로 「〈사람〉 님이 〈방〉 방을 떠나 〈에이전트〉의 참고 읽기가 막혔습니다」(D-18).
 *   - 잘린 행에는 「분량 상한으로 잘림」 칩(`truncated`).
 * 권한: 방을 볼 수 있는 사람 누구나(맥락이 새어 나간 사실은 그 방 사람 전원이 봐야 한다). 필터(기간·에이전트)는 보기 도구다.
 * 실시간: `room_read.recorded`. 「활동 로그에서 보기」 → S15.
 */
import { useCallback, useEffect, useId, useMemo, useState } from "react";
import Link from "next/link";
import { Slot } from "./Slot";
import { useRoomEvents } from "./RoomDialogShell";
import { api, errorMessage } from "@/lib/api/client";
import { useAuth } from "@/lib/auth/AuthContext";
import { clockTime, relativeTime } from "@/lib/time";
import { COMMON, deniedText, PERIOD_MS, personName, READS, showsRoomName, type Period, type RoomRead, type RoomReadDirection } from "@/lib/room-dialogs";
import { pageItems, type Room } from "@/lib/api/types";
import "./room-dialogs.css";

const GROUPS: readonly RoomReadDirection[] = ["out", "in", "denied"];
const READ_EVENTS = ["room_read.recorded"] as const;

export function RoomReadsTable({ roomId }: { roomId: string }) {
  const { workspace } = useAuth();
  const [room, setRoom] = useState<Room | null>(null);
  const [rows, setRows] = useState<RoomRead[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [period, setPeriod] = useState<Period>("all");
  const [agent, setAgent] = useState("");
  const base = useId();

  const load = useCallback(async () => {
    try {
      const since = PERIOD_MS[period] ? new Date(Date.now() - PERIOD_MS[period]).toISOString() : undefined;
      const [r, page] = await Promise.all([
        api.get("/rooms/{roomId}", { path: { roomId } }),
        api.get("/rooms/{roomId}/reads", { path: { roomId }, query: { since, agent_id: agent || undefined, limit: 200 } }),
      ]);
      setRoom(r);
      setRows(pageItems<RoomRead>(page));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    }
  }, [roomId, period, agent]);
  useEffect(() => {
    void load();
  }, [load]);
  useRoomEvents(workspace?.id, roomId, READ_EVENTS, () => void load());

  // 에이전트 거르개의 후보 — 거르지 않은 목록에서 한 번 모은다(거른 뒤에 모으면 고른 것만 남는다).
  const [agents, setAgents] = useState<{ id: string; name: string }[]>([]);
  useEffect(() => {
    if (!rows || agent) return;
    const m = new Map(rows.map((r) => [r.agent.id, r.agent.name]));
    setAgents([...m].map(([id, name]) => ({ id, name })));
  }, [rows, agent]);

  const grouped = useMemo(() => Object.fromEntries(GROUPS.map((g) => [g, (rows ?? []).filter((r) => r.direction === g)])) as Record<RoomReadDirection, RoomRead[]>, [rows]);
  const empty = rows !== null && rows.length === 0 && period === "all" && !agent;

  return (
    <div className="rd-page" data-testid="rd-reads">
      <div className="rd-page__head">
        <Link href={`/rooms/${roomId}`} className="small">← {COMMON.back_to_room}</Link>
        <h1 className="rd-page__title">{READS.title}</h1>
        {room && <p className="rd-section__note">{room.name}</p>}
        <p className="rd-section__note">{READS.desc}</p>
      </div>
      {error && <p className="problem" role="alert" data-testid="rd-reads-error">{error}</p>}
      {empty ? (
        <div className="empty" data-testid="rd-reads-empty">
          <div className="empty__body">
            {READS.empty_head} <code>colab room read</code> — {READS.empty_tail}
          </div>
        </div>
      ) : (
        <>
          <div className="rd-filters">
            <label className="rd-field">
              <span className="rd-field__label">{READS.filter_period}</span>
              <select className="select" value={period} onChange={(e) => setPeriod(e.target.value as Period)} data-testid="rd-reads-period">
                <option value="all">{READS.period_all}</option>
                <option value="1d">{READS.period_1d}</option>
                <option value="7d">{READS.period_7d}</option>
                <option value="30d">{READS.period_30d}</option>
              </select>
            </label>
            <label className="rd-field">
              <span className="rd-field__label">{READS.filter_agent}</span>
              <select className="select" value={agent} onChange={(e) => setAgent(e.target.value)} data-testid="rd-reads-agent">
                <option value="">{READS.agent_all}</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}</option>
                ))}
              </select>
            </label>
            <Link href="/settings/audit" className="btn btn--sm" data-testid="rd-reads-audit">{READS.audit_link}</Link>
          </div>
          {GROUPS.map((g) => (
            <section key={g} className="rd-section" aria-labelledby={`${base}-${g}`} data-testid={`rd-reads-${g}`}>
              <h2 className="rd-group__title" id={`${base}-${g}`}>{READS.groups[g]}</h2>
              <p className="rd-section__note">{READS.group_notes[g]}</p>
              {!rows ? (
                <p className="rd-hint">{COMMON.loading}</p>
              ) : grouped[g].length === 0 ? (
                <p className="rd-hint">{READS.group_empty}</p>
              ) : (
                <div className="rd-table-wrap">
                  <table className="rd-table">
                    <thead>
                      <tr>
                        <th scope="col">{READS.col_at}</th>
                        <th scope="col">{READS.col_agent}</th>
                        <th scope="col">{READS.col_originator}</th>
                        {g === "denied" ? <th scope="col">{READS.col_reason}</th> : <th scope="col">{g === "out" ? READS.col_target : READS.col_source}</th>}
                        {g !== "denied" && <th scope="col">{READS.col_scope}</th>}
                      </tr>
                    </thead>
                    <tbody>
                      {grouped[g].map((r) => (
                        <tr key={r.id} data-testid="rd-read-row" data-direction={r.direction}>
                          <td title={r.at}>
                            {clockTime(r.at)} <span className="muted">· {relativeTime(r.at)}</span>
                          </td>
                          <td>{r.agent.name}</td>
                          <td>{r.originator_user ? personName(r.originator_user) : READS.no_originator_user}</td>
                          {g === "denied" ? (
                            <td data-testid="rd-read-reason">{deniedText(r)}</td>
                          ) : (
                            <td data-testid="rd-read-room">{showsRoomName(r) ? r.other_room?.name ?? READS.hidden_room : READS.hidden_room}</td>
                          )}
                          {g !== "denied" && (
                            <td>
                              {r.scope.summary && READS.scope_summary}
                              {r.scope.summary && (r.scope.recent_n ?? 0) > 0 && READS.scope_join}
                              {(r.scope.recent_n ?? 0) > 0 && <Slot text={READS.scope_recent} n={r.scope.recent_n!} />}
                              {r.truncated && <span className="rd-chip" data-testid="rd-read-truncated">{READS.truncated}</span>}
                            </td>
                          )}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          ))}
        </>
      )}
    </div>
  );
}

export default RoomReadsTable;
