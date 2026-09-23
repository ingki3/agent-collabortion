"use client";
/**
 * 방 멈춤 배너(COMPONENTS §9.4 — Status Banner `zPJly` 의 두 번째 크기 · SCREEN §4.6) — 타임라인 위 전폭.
 *
 * 이 방의 **모든 미션과 미션 밖 할 일이 멈춘 상태**라 미션 배너와 무게가 다르다 — `role="alert"`(읽던 흐름을 끊어도 된다).
 * **몇 개가 멈췄는지 반드시 센다** — 그 수(`blocked_detail.works_stopped`)는 문장에 보간하지 않고 슬롯(`<Slot>`)이다.
 * 내가 승인 권한자가 아니면 누가 언제부터 답할 수 있는지를 적는다(FR-2A.3 위임 경로) — 다음 권한자의 이름·역할은 `next_approver`·
 * `next_approver_role`(0.2.8)에서, 없으면 「다음 권한자」로 줄인다.
 */
import "./room-banner.css";
import { Slot } from "./Slot";
import { DisabledHint } from "./PageHead";
import { clockTime } from "@/lib/time";
import { ROOM_BANNER, ROOM_HEAD } from "@/lib/wording";
import type { Room } from "@/lib/api/types";

export interface RoomBlockedBannerProps {
  room: Pick<Room, "blocked_reason" | "blocked_detail" | "my_capabilities" | "runtime">;
  me: string | null;
  /** 루프일 때 왕복한 두 에이전트의 이름. */
  agentName?: (id: string) => string;
  busy?: boolean;
  /** `manual` 해제(unblockRoom). 권한이 없으면 비활성 + 사유. */
  onUnblock?: () => void;
  /** 예산·루프 — 그 승인 요청(타임라인 카드)으로. 없으면 「받은 요청에서 승인합니다」 한 줄. */
  onApprove?: () => void;
  /** 컴퓨터 끊김 — 컴퓨터 바꾸기(S17). */
  rebindHref?: string;
}

export function RoomBlockedBanner({ room, me, agentName, busy, onUnblock, onApprove, rebindHref }: RoomBlockedBannerProps) {
  const reason = room.blocked_reason;
  if (!reason) return null;
  const d = room.blocked_detail ?? {};
  const stopped = d.works_stopped ?? 0;
  const approver = d.approver ?? null;
  const iApprove = !!me && approver?.id === me;
  const canUnblock = (room.my_capabilities ?? []).includes("block");
  // 다음 권한자(0.2.8) — 이름과 역할이 오면 누구인지 적고, 없으면 「다음 권한자」로 줄인다(#305 NN1).
  const next = d.next_approver ?? null;
  const nextText = d.next_approver_role === "room_deputy" ? ROOM_BANNER.next_room_deputy : d.next_approver_role === "workspace_owner" ? ROOM_BANNER.next_workspace_owner : ROOM_BANNER.next_plain;

  let lead: React.ReactNode;
  switch (reason) {
    case "budget":
      lead = d.budget_usd != null ? <Slot text={ROOM_BANNER.budget} n={d.budget_usd} /> : ROOM_BANNER.budget_plain;
      break;
    case "runtime_offline":
      lead = room.runtime?.name ? <Slot text={ROOM_BANNER.runtime_offline} n={room.runtime.name} /> : ROOM_BANNER.runtime_offline_plain;
      break;
    case "loop": {
      const pair = (d.loop_agents ?? []).map((id) => `@${agentName?.(id) ?? "agent"}`).join(" ↔ ");
      lead = (
        <>
          {ROOM_BANNER.loop}
          {pair && <span data-testid="room-banner-pair"> ({pair})</span>}
        </>
      );
      break;
    }
    case "manual":
      lead = d.blocked_by_user?.display_name ? <Slot text={ROOM_BANNER.manual} n={d.blocked_by_user.display_name} /> : ROOM_BANNER.manual_plain;
      break;
  }

  return (
    <section className="room-banner" role="alert" data-testid="room-banner" data-need="room-banner" data-reason={reason}>
      <p className="room-banner__body">
        <span data-testid="room-banner-lead">{lead}</span>
        {reason === "manual" && d.blocked_at && <span className="room-banner__at" data-testid="room-banner-at"> ({clockTime(d.blocked_at).slice(0, 5)})</span>}
        {" — "}
        <b data-testid="room-banner-stopped" data-count={stopped}>
          {stopped > 0 ? <Slot text={ROOM_BANNER.stopped} n={stopped} /> : ROOM_BANNER.stopped_no_works}
        </b>
      </p>
      {reason !== "manual" && approver && !iApprove && (
        <p className="room-banner__who" data-testid="room-banner-waiting">
          <Slot text={ROOM_BANNER.waiting} n={approver.display_name} />
          {d.delegate_at && (
            <>
              {" · "}
              {next ? (
                <span data-testid="room-banner-next" data-role={d.next_approver_role ?? ""}>
                  <Slot text={ROOM_BANNER.delegate_at} n={clockTime(d.delegate_at).slice(0, 5)} />
                  <Slot text={nextText} n={next.display_name} />
                </span>
              ) : (
                <Slot text={ROOM_BANNER.delegate} n={clockTime(d.delegate_at).slice(0, 5)} />
              )}
            </>
          )}
        </p>
      )}
      <div className="room-banner__actions">
        {reason === "manual" && (
          <span className="room-banner__act">
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!canUnblock || !onUnblock || busy}
              aria-describedby={!canUnblock ? "room-banner-unblock-hint" : undefined}
              onClick={onUnblock}
              data-testid="room-banner-unblock"
            >
              {ROOM_BANNER.unblock}
            </button>
            {!canUnblock && <DisabledHint id="room-banner-unblock-hint">{ROOM_HEAD.block_role}</DisabledHint>}
          </span>
        )}
        {(reason === "budget" || reason === "loop") && iApprove && (
          onApprove ? (
            <button type="button" className="btn btn--sm btn--primary" onClick={onApprove} disabled={busy} data-testid="room-banner-approve">
              {ROOM_BANNER.approve}
            </button>
          ) : (
            <span className="room-banner__hint">{ROOM_BANNER.approve_where}</span>
          )
        )}
        {reason === "runtime_offline" && iApprove && rebindHref && (
          <a className="btn btn--sm" href={rebindHref} data-testid="room-banner-rebind">{ROOM_BANNER.rebind}</a>
        )}
      </div>
    </section>
  );
}

export default RoomBlockedBanner;
