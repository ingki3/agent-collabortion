"use client";
/**
 * S7 상단 — 방 머리(SCREEN §4.6 상단). 방 이름·설명 · **세 층 요약**(수마다 라벨) · 「나에게 필요한 것 N」(중복 없이, 0 이면 없음) ·
 * 방 층 액션(참여자 · 방 설정 · **「이 방 멈춤」은 메뉴 밖 액션 줄에 직접** · `⋯` 안에 여기까지 정리·맥락 오간 기록·보관·삭제·나가기).
 * 미션 층 동작(일시정지·종료·Director 교체…)은 여기 없다 — 우열 미션 칸으로 내려갔다(§4.6 상단 액션 표).
 * 권한 없는 버튼은 숨기지 않고 비활성 + 버튼 아래 글자 사유(`DisabledHint`).
 */
import { useEffect, useId, useState } from "react";
import Link from "next/link";
import "./room-card.css";
import "./session-card-menu.css";
import { ConfirmDialog } from "./ConfirmDialog";
import { DisabledHint } from "./PageHead";
import { Slot, slotText } from "./Slot";
import { useCardMenu } from "./useCardMenu";
import { layerCounts } from "@/lib/room-view";
import { BLOCK_DIALOG, ROOM_HEAD, ROOM_MENU, SUMMARIZE_DIALOG } from "@/lib/wording";
import type { Room } from "@/lib/api/types";

export interface RoomHeadProps {
  room: Room;
  needs: number;
  onJumpNeed: () => void;
  onParticipants: () => void;
  /** 「이 방에서 나가기」 — S19 본인 행과 같은 경로(참여자 다이얼로그). */
  onLeave: () => void;
  busy?: boolean;
  onBlock: () => Promise<void>;
  onUnblock: () => void;
  onSummarize: (range: SummaryRange) => Promise<void>;
  /** 「직접 고르기」 → 「타임라인에서 고르기」 — 다이얼로그를 닫고 호출부(S7)가 타임라인 집기 모드로 들어간다. */
  onStartPick?: () => void;
  /** 타임라인에서 집은 범위 — `pickNonce` 가 바뀌면 다이얼로그가 「직접 고르기」로 다시 열린다. */
  picked?: PickedRange | null;
  pickNonce?: number;
  onArchive: () => void;
  onUnarchive: () => void;
  onDelete: () => void;
  /** 멈추면 중단될 진행 중 턴 · 멈추는 열린 미션 수(확인 문장의 슬롯). */
  runningTurns: number;
  openWorks: number;
  /** 「여기까지 정리」 미리보기 — 이 범위에 드는 메시지 수(다 읽은 경우만, 모르면 null). */
  countSince?: (sinceDays: number) => number | null;
}

/** 「여기까지 정리」 범위 — 최근 N일(`since`) 또는 타임라인에서 집은 두 메시지(`from_message_id`·`to_message_id`, 계약 RoomSummarize). */
export type SummaryRange = { days: number } | { from: string; to: string };
export interface PickedRange {
  from: { id: string; text: string };
  to: { id: string; text: string };
  /** 이 범위에 드는 메시지 수 — 다 읽은 경우만, 모르면 null. */
  count: number | null;
}

export function RoomHead(props: RoomHeadProps) {
  const { room } = props;
  const caps = new Set(room.my_capabilities ?? []);
  const archived = room.status === "archived";
  const c = layerCounts(room);
  const menu = useCardMenu();
  const [dialog, setDialog] = useState<"block" | "summarize" | null>(null);
  const [days, setDays] = useState<7 | 30 | "pick">(7);
  const [err, setErr] = useState<string | null>(null);
  const [working, setWorking] = useState(false);
  const blockHint = useId();
  const summarizeHint = useId();
  const archiveHint = useId();
  const deleteHint = useId();
  const leaveHint = useId();

  const blockWhy = archived ? ROOM_HEAD.archived : !caps.has("block") ? ROOM_HEAD.block_role : null;
  const summarizeWhy = archived ? ROOM_HEAD.archived : null;
  const run = async (f: () => Promise<void>) => {
    setWorking(true);
    setErr(null);
    try {
      await f();
      setDialog(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setWorking(false);
    }
  };
  const count = days === "pick" ? props.picked?.count ?? null : props.countSince?.(days) ?? null;
  // 타임라인에서 범위를 집고 돌아오면 다이얼로그가 「직접 고르기」로 다시 선다.
  useEffect(() => {
    if (!props.pickNonce) return;
    setDays("pick");
    setErr(null);
    setDialog("summarize");
  }, [props.pickNonce]);
  const picked = days === "pick" ? props.picked ?? null : null;

  return (
    <div className="room-head" data-testid="room-head">
      <div className="row" style={{ gap: 10, flexWrap: "wrap", alignItems: "flex-start" }}>
        <div className="room-head__title">
          <Link href="/rooms" className="small muted-3">← {ROOM_HEAD.back}</Link>
          <h1 style={{ margin: 0, fontSize: "var(--fs-title)" }} data-testid="room-title">{room.name}</h1>
          {room.description && <p className="muted small" style={{ margin: 0 }} data-testid="room-description">{room.description}</p>}
        </div>
        {/* 세 층 요약 — 수마다 라벨(스크린리더가 한 덩어리로 읽지 않게). 0 인 층은 생략, 미션은 「미션 없음」. */}
        <p className="room-head__layers" aria-label={ROOM_HEAD.layers_label} data-testid="room-layers">
          <span data-testid="layer-works">{c.works > 0 ? <Slot text={ROOM_HEAD.layer_works} n={c.works} /> : ROOM_HEAD.layer_works_none}</span>
          {c.lanes > 0 && <span data-testid="layer-lanes">{" · "}<Slot text={ROOM_HEAD.layer_lanes} n={c.lanes} /></span>}
          {c.tasks > 0 && <span data-testid="layer-tasks">{" · "}<Slot text={ROOM_HEAD.layer_tasks} n={c.tasks} /></span>}
        </p>
        {props.needs > 0 && (
          <button type="button" className="btn btn--sm room-head__needs" onClick={props.onJumpNeed} data-testid="needs-me" data-count={props.needs}>
            <Slot text={ROOM_HEAD.needs_me} n={props.needs} />
          </button>
        )}
        <span className="s7__spacer" />
        <div className="room-head__actions" data-testid="room-actions">
          <button type="button" className="btn btn--sm" onClick={props.onParticipants} data-testid="room-participants">{ROOM_HEAD.participants}</button>
          <Link href={`/rooms/${room.id}/settings`} className="btn btn--sm" data-testid="room-settings">{ROOM_HEAD.settings}</Link>
          {room.blocked_reason === "manual" ? (
            <span className="room-head__act">
              <button type="button" className="btn btn--sm" disabled={!caps.has("block") || props.busy} aria-describedby={!caps.has("block") ? blockHint : undefined} onClick={props.onUnblock} data-testid="room-unblock">
                {ROOM_HEAD.unblock}
              </button>
              {!caps.has("block") && <DisabledHint id={blockHint}>{ROOM_HEAD.block_role}</DisabledHint>}
            </span>
          ) : (
            <span className="room-head__act">
              <button
                type="button"
                className="btn btn--sm"
                disabled={!!blockWhy || !!room.blocked_reason || props.busy}
                aria-describedby={blockWhy ? blockHint : undefined}
                onClick={() => { setErr(null); setDialog("block"); }}
                data-testid="room-block"
              >
                {ROOM_HEAD.block}
              </button>
              {blockWhy && <DisabledHint id={blockHint}>{blockWhy}</DisabledHint>}
            </span>
          )}
          <div className="card-menu room-head__menu" ref={menu.root} onBlur={menu.onRootBlur}>
            <button
              ref={menu.button}
              type="button"
              className="btn btn--sm"
              aria-haspopup="menu"
              aria-expanded={menu.open}
              aria-label={ROOM_HEAD.more}
              onClick={menu.toggle}
              onKeyDown={menu.onButtonKey}
              data-testid="room-more"
            >
              ⋯
            </button>
            {menu.open && (
              <div className="card-menu__list" role="menu" onKeyDown={menu.onMenuKey} data-testid="room-more-menu">
                <div className="card-menu__entry">
                  <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!!summarizeWhy || undefined} aria-describedby={summarizeWhy ? summarizeHint : undefined}
                    onClick={() => { if (summarizeWhy) return; menu.close(); setErr(null); setDialog("summarize"); }} data-testid="room-menu-summarize">
                    {ROOM_HEAD.summarize}
                  </button>
                  {summarizeWhy && <DisabledHint id={summarizeHint}>{summarizeWhy}</DisabledHint>}
                </div>
                <div className="card-menu__entry">
                  <Link href={`/rooms/${room.id}/reads`} role="menuitem" className="card-menu__item" data-testid="room-menu-reads">{ROOM_HEAD.reads}</Link>
                </div>
                <div className="card-menu__entry">
                  <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!caps.has("archive") || undefined} aria-describedby={!caps.has("archive") ? archiveHint : undefined}
                    onClick={() => { if (!caps.has("archive")) return; menu.close(); if (archived) props.onUnarchive(); else props.onArchive(); }} data-testid="room-menu-archive">
                    {archived ? ROOM_HEAD.unarchive : ROOM_HEAD.archive}
                  </button>
                  {!caps.has("archive") && <DisabledHint id={archiveHint}>{ROOM_MENU.archive_role}</DisabledHint>}
                </div>
                <div className="card-menu__entry">
                  <button type="button" role="menuitem" className="card-menu__item card-menu__item--danger" aria-disabled={!caps.has("delete") || undefined} aria-describedby={!caps.has("delete") ? deleteHint : undefined}
                    onClick={() => { if (!caps.has("delete")) return; menu.close(); props.onDelete(); }} data-testid="room-menu-delete">
                    {ROOM_HEAD.delete} <span className="card-menu__tail">· {ROOM_HEAD.delete_tail}</span>
                  </button>
                  {!caps.has("delete") && <DisabledHint id={deleteHint}>{ROOM_MENU.delete_role}</DisabledHint>}
                </div>
                <div className="card-menu__entry">
                  <button type="button" role="menuitem" className="card-menu__item" aria-disabled={!room.my_room_role || undefined} aria-describedby={!room.my_room_role ? leaveHint : undefined}
                    onClick={() => { if (!room.my_room_role) return; menu.close(); props.onLeave(); }} data-testid="room-menu-leave">
                    {ROOM_HEAD.leave}
                  </button>
                  {!room.my_room_role && <DisabledHint id={leaveHint}>{ROOM_HEAD.leave_not_participant}</DisabledHint>}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
      {dialog === "block" && (
        <ConfirmDialog
          title={BLOCK_DIALOG.title}
          confirmLabel={BLOCK_DIALOG.confirm}
          busyLabel={BLOCK_DIALOG.busy}
          cancelLabel={BLOCK_DIALOG.cancel}
          busy={working}
          danger
          error={err}
          onConfirm={() => void run(props.onBlock)}
          onClose={() => setDialog(null)}
          testId="block-dialog"
        >
          <p><b data-testid="block-dialog-turns"><Slot text={BLOCK_DIALOG.turns} n={props.runningTurns} /></b></p>
          <p data-testid="block-dialog-works"><Slot text={BLOCK_DIALOG.works} n={props.openWorks} /></p>
          <p className="muted small">{BLOCK_DIALOG.resume}</p>
        </ConfirmDialog>
      )}
      {dialog === "summarize" && (
        <ConfirmDialog
          title={SUMMARIZE_DIALOG.title}
          confirmLabel={SUMMARIZE_DIALOG.confirm}
          busyLabel={SUMMARIZE_DIALOG.busy}
          cancelLabel={SUMMARIZE_DIALOG.cancel}
          busy={working}
          error={err}
          onConfirm={() => void run(() => props.onSummarize(days === "pick" ? { from: picked!.from.id, to: picked!.to.id } : { days }))}
          onClose={() => setDialog(null)}
          testId="summarize-dialog"
          confirmBlocked={days === "pick" && !picked ? SUMMARIZE_DIALOG.pick_need : null}
        >
          <fieldset className="room-head__range">
            <legend className="small muted">{SUMMARIZE_DIALOG.range}</legend>
            {([7, 30] as const).map((d) => (
              <label key={d} className="row" style={{ gap: 6 }}>
                <input type="radio" name="summarize-range" checked={days === d} onChange={() => setDays(d)} data-testid={`summarize-${d}`} />
                {d === 7 ? SUMMARIZE_DIALOG.days7 : SUMMARIZE_DIALOG.days30}
              </label>
            ))}
            {props.onStartPick && (
              <label className="row" style={{ gap: 6 }}>
                <input type="radio" name="summarize-range" checked={days === "pick"} onChange={() => setDays("pick")} data-testid="summarize-pick" />
                {SUMMARIZE_DIALOG.pick}
              </label>
            )}
          </fieldset>
          {days === "pick" && props.onStartPick && (
            <div className="room-head__picked" data-testid="summarize-picked">
              {picked && (
                <dl className="room-head__picked-dl">
                  <dt>{SUMMARIZE_DIALOG.pick_from}</dt>
                  <dd data-testid="summarize-picked-from">{picked.from.text}</dd>
                  <dt>{SUMMARIZE_DIALOG.pick_to}</dt>
                  <dd data-testid="summarize-picked-to">{picked.to.text}</dd>
                </dl>
              )}
              <button type="button" className="btn btn--sm" onClick={() => { setDialog(null); props.onStartPick!(); }} data-testid="summarize-pick-start">
                {picked ? SUMMARIZE_DIALOG.pick_again : SUMMARIZE_DIALOG.pick_start}
              </button>
            </div>
          )}
          {count != null && <p className="small" data-testid="summarize-preview" aria-label={slotText(SUMMARIZE_DIALOG.preview, count)}><Slot text={SUMMARIZE_DIALOG.preview} n={count} /></p>}
          <p className="muted small">{SUMMARIZE_DIALOG.result}</p>
        </ConfirmDialog>
      )}
    </div>
  );
}

export default RoomHead;
