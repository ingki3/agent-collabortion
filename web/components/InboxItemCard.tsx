"use client";
/**
 * Inbox Item(COMPONENTS §2.4 `T0qdqP` · SCREEN §4.6) — **항목 7종을 하나의 컴포넌트로**.
 *
 * 심각도 배지의 **글리프는 심각도**(`action_required` ! · `attention` ▲ · `info` i)이고
 * **색은 항목의 원인 상태**를 따른다(리뷰 #03 N4) — 다음 사람이 `attention` 을 빨강으로 통일하지 않도록
 * 규칙을 `TONE_BY_TYPE` 로 코드에 둔다.
 *
 * `hitl_request` 항목은 **본문을 `HitlBody` 로 그린다** — S7 타임라인 카드와 같은 하위 컴포넌트다.
 * 그래야 F2("세션을 열지 않고 인박스에서 답한다")가 성립한다(U3: 카드만 읽고 제출).
 *
 * 버튼은 **서버가 준 `actions`** 로만 정한다(계약 `InboxItem.actions` — 권한을 반영한 목록이다).
 * 화면이 버튼을 만들어 내면 403 을 누르게 된다.
 */
import Link from "next/link";
import "./inbox-item.css";
import { IsolationConfirmBody, RoomPausedBody } from "./InboxRoomBodies";
import {
  delegationLine, INBOX_V19, ISOLATION_CARD, needsDelegationLine, quoteLine, RECIPIENT_BASIS, roomNameOf, SHORTCUT_ACTIONS, shortcutsOf, workTitleOf,
  type BlockedDetail,
} from "@/lib/inbox-v19";
import { Badge } from "./Badge";
import { HitlBody, type HitlAction } from "./HitlBody";
import { dueLabel } from "./HitlBody";
import { failureLabel } from "@/lib/failure";
import { PAUSE_REASON_LABEL } from "@/lib/session-label";
import { clockTime, relativeTime } from "@/lib/time";
import type { HitlRequest, HitlResponse, InboxItem } from "@/lib/api/types";
import type { Tone } from "./badge-map";

export type InboxAction = NonNullable<InboxItem["actions"]>[number];
type ItemType = InboxItem["type"];

/** 종류 한국어 이름(COMPONENTS §2.4 `SzQ6z`). */
export const TYPE_LABEL: Record<ItemType, string> = {
  hitl_request: "응답 요청",
  lane_blocked: "에이전트 질문",
  run_failed: "작업 실패",
  runtime_offline: "컴퓨터 연결 끊김",
  mention: "멘션",
  workdir_gc_blocked: "작업 폴더 정리 막힘",
  // v0.2.0 계약(PRD v0.19) — 화면 반영은 R2. 이름은 SCREEN §4.14 초안 그대로.
  isolation_confirm: "격리 확인",
  work_proposed: "미션 제안",
  work_paused: "미션 일시정지",
  room_paused: "방 멈춤",
  work_completed: "미션 완료",
  room_invited: "방 초대",
  workdir_quota: "작업 폴더 용량 초과",
};

/**
 * 심각도 배지의 **색**(COMPONENTS §2.4 타입별 조합 표) — 심각도가 아니라 원인 상태를 따른다.
 * hitl_request `$s-wait` · lane_blocked `$s-block` · work_paused·room_paused `$s-pause` ·
 * runtime_offline·run_failed `$s-fail` · mention `$s-run` · work_completed `$s-done`.
 * (옛 session_paused·session_completed 는 v0.3.0(R4, D22)에서 계약이 지웠다 — work_paused·work_completed 가 잇는다.)
 */
export const TONE_BY_TYPE: Record<ItemType, Tone> = {
  hitl_request: "wait",
  lane_blocked: "block",
  runtime_offline: "fail",
  run_failed: "fail",
  mention: "run",
  workdir_gc_blocked: "block",
  isolation_confirm: "wait",
  work_proposed: "run",
  work_paused: "pause",
  room_paused: "pause",
  work_completed: "done",
  room_invited: "run",
  workdir_quota: "block",
};

/** 버튼 라벨(COMPONENTS §2.4 표 · SCREEN §4.6 인라인 동작). `restart` 는 "다시 지시"다 — 재시도가 아니다(리뷰 #01 C4). */
export const ACTION_LABEL: Record<InboxAction, string> = {
  answer: "답변 보내기",
  approve: "승인",
  reject: "거절",
  reply: "답글 작성",
  approve_continue: "계속 진행 승인",
  restart: "다시 지시",
  rebind: "다른 컴퓨터로 옮기기",
  open_session: "미션 열기",
  // v0.2.1 계약 — 방·미션 바로가기(SCREEN §4.14). 화면 반영은 R2.
  open_room: "방 열기",
  open_work: "미션 열기",
  open_runtimes: "연결된 컴퓨터 열기",
  // v0.2.10 계약 — 작업 폴더 정리 막힘(workdir_gc_blocked) 카드의 두 동작. 문구는 lib/inbox-v19.ts 와 같다.
  open_workdirs: "작업 폴더 열기",
  delete_workdir: "작업 폴더 지우기",
};

/**
 * 계약 enum 밖에서 서버가 주는 동작(handlers_inbox → `inbox.Actions`: `workdir_gc_blocked`·`workdir_quota` 의 `open_workdirs`).
 * 라벨이 없는 동작은 **그리지 않는다** — 이름 없는 버튼은 누를 수 없는 버튼보다 나쁘다.
 */
const EXTRA_ACTION_LABEL: Record<string, string> = { open_workdirs: INBOX_V19.open_workdirs };
export const actionLabel = (a: string): string | null => (ACTION_LABEL as Record<string, string>)[a] ?? EXTRA_ACTION_LABEL[a] ?? null;

/** `actions` 중 HITL 본문이 직접 처리하는 것(입력부와 붙어 있어야 한다). 나머지는 카드 하단 버튼이다. */
const INLINE_HITL: readonly InboxAction[] = ["answer", "approve", "reject"];

/** 부가 텍스트(COMPONENTS §2.4 `fDXjQ`, 기본 끔) — 타입별로 "더 알아야 할 한 줄". */
export function extraLine(item: InboxItem): string | null {
  switch (item.type) {
    case "hitl_request":
      // 제안 기본값은 **본문(HitlBody)이 이미 그린다** — 여기서 되풀이하면 같은 문장이 두 줄이 된다.
      // 부가 칸은 본문에 없는 것만 말한다: deputy 위임 시점(O5).
      return item.delegated ? "위임됨 · 지금부터 응답 가능" : null;
    case "run_failed":
      return item.card?.failure_kind ? `${failureLabel(item.card.failure_kind)} — 맥락을 더해 다시 지시하세요` : null;
    case "runtime_offline":
      return item.card?.grace_ends_at ? `유예 만료 ${clockTime(item.card.grace_ends_at)}` : null;
    case "work_completed":
      return item.card?.summary ?? null;
    case "work_paused":
      return item.card?.paused_reason ? (PAUSE_REASON_LABEL[item.card.paused_reason] ?? item.card.paused_reason) : null;
    default:
      return null;
  }
}

/**
 * 예산 HITL 의 **범위**(K-9 이후, Lead 확인 2026-09-07).
 *
 * `card.purpose` 가 생겨(K-9) 인박스는 HITL 상세를 다시 읽지 않는다. 다만 카드에는 `task_id` 도 `scope` 도
 * 없으므로 범위는 **세션 상태**로 가른다:
 *   · task 범위 초과(E9-01·E9-10)는 lane 만 멈추고 세션은 `active` 다 → "새 task 상한".
 *   · 세션 범위 초과(K-10)는 세션이 `paused(budget)` 다 → "새 세션 상한".
 * 범위를 섞으면 라벨이 거짓말을 하고, 세션 소진액을 task 범위의 최소로 쓰면 정상적인 $3 상향이 막힌다.
 */
export function budgetScopeOf(item: InboxItem): "task" | "session" {
  // W-7·K-12: 세션이 **예산 때문에** 멈춘 것만 세션 범위다. 다른 사유(사람 확인·컴퓨터 연결 끊김)로 paused 인 동안
  // task 범위 예산 HITL 이 열리면 그것은 여전히 "새 task 상한"이다 — `paused_reason` 까지 본다(PR #166 리뷰 NN2).
  // 세션의 사유는 `card.paused_reason` 에 실린다(서버 handlers_inbox.go 가 모든 항목에 세션의 paused_reason 을 조인한다).
  return item.session?.status === "paused" && item.card?.paused_reason === "budget" ? "session" : "task";
}

export interface InboxItemCardProps {
  item: InboxItem;
  /**
   * (선택) 이 항목이 가리키는 HITL 요청. **더는 필요하지 않다** — `card.purpose`(K-9)로 예산 HITL 을
   * 알아보고 범위는 `budgetScopeOf` 가 세션 상태로 가른다. 넘기면 `budget_override_usd`(지금 상한)만
   * 조금 더 정확해지므로, S7 처럼 이미 상세를 들고 있는 자리에서만 넘긴다.
   */
  hitl?: HitlRequest | null;
  /** 인라인 HITL 응답(F2). `hitl_request` 에서만 쓰인다. */
  onRespond?: (item: InboxItem, body: HitlResponse) => Promise<void> | void;
  /** 그 밖의 인라인 동작 — 세션 열기·답글·다시 지시·계속 승인·Runtimes. */
  onAction?: (item: InboxItem, action: InboxAction) => void;
  onMarkRead?: (item: InboxItem) => void;
  busy?: boolean;
  now?: number;
  /**
   * v0.19(§4.14) 카드가 목록 밖에서 더 아는 것 — 인박스 페이지가 채운다. 전부 선택이고, 없으면 그 줄을 그리지 않는다(지어내지 않는다).
   *   · `roomNames` — 방 이름(0.2.9 `InboxItem.room` 을 서버가 아직 안 채울 때의 목록 폴백)
   *   · `blocked`   — `room_paused` 의 방 `blocked_detail`(getRoom, 카드당 1회 — Lead Q5)
   *   · `ownerName` — 위임 줄의 「방장 〈민호〉」
   *   · `respondFrom` — 위임받은 사람이 답할 수 있게 되는 시각(`HitlRequest.can_respond_from` · `blocked_detail.delegate_at`)
   *   · `options`   — `isolation_confirm` 의 저장소 고르기 선택지(getHitlRequest, Lead Q6)
   */
  roomNames?: ReadonlyMap<string, string>;
  blocked?: BlockedDetail | null;
  ownerName?: string | null;
  respondFrom?: string | null;
  options?: string[] | null;
  /**
   * 서버가 `recipient_basis` 를 비워 보낼 때의 대신(인박스 페이지가 방의 방장과 나를 대조해 정한다). 서버 값이 있으면 쓰지 않는다.
   * 실서버(dev c5afee6)의 room_paused·isolation_confirm 행이 근거 없이 들어간다(roomgate·router insert — W4a 보고).
   */
  basisFallback?: InboxItem["recipient_basis"];
}

export function InboxItemCard({ item, hitl: detail, onRespond, onAction, onMarkRead, busy, now, roomNames, blocked, ownerName, respondFrom, options, basisFallback }: InboxItemCardProps) {
  const hitl = item.type === "hitl_request" ? item.card : null;
  const actions = (item.actions ?? []) as InboxAction[];
  const inline = actions.filter((a): a is HitlAction => (INLINE_HITL as readonly string[]).includes(a));
  // 방 층 두 타입은 본문이 결과 이름의 버튼을 그린다 — 여기서 `approve_continue` 를 한 번 더 그리지 않는다.
  const roomLevel = item.type === "isolation_confirm" || item.type === "room_paused";
  const links = shortcutsOf(item);
  // 바로가기 링크가 있으면 같은 이동(세션·방·미션 열기)을 버튼으로 한 번 더 그리지 않는다.
  const rest = actions.filter((a) =>
    !(INLINE_HITL as readonly string[]).includes(a) && !(links.room && SHORTCUT_ACTIONS.has(a)) && !(roomLevel && a === "approve_continue") && actionLabel(a) != null);
  const canRespondRoom = item.type === "isolation_confirm" ? actions.includes("approve") : item.type === "room_paused" ? actions.includes("approve_continue") : false;
  const roomName = roomNameOf(item, roomNames);
  const workTitle = workTitleOf(item);
  const quote = quoteLine(item);
  const basis = item.recipient_basis ?? basisFallback ?? null;
  // 근거가 없는 옛 항목(0.2.0 전)은 위임 줄 대신 부가 칸(`extraLine`)이 「위임됨 · 지금부터 응답 가능」을 말한다.
  const delegation = basis && (needsDelegationLine(item) || basis === "deputy")
    ? delegationLine({ ...item, recipient_basis: basis }, { canRespond: roomLevel ? canRespondRoom : inline.length > 0, from: respondFrom ?? blocked?.delegate_at ?? null, ownerName, now })
    : null;
  const basisText = basis
    ? item.type === "isolation_confirm" && basis === "room_deputy" && ownerName && delegation && !delegation.locked
      ? ISOLATION_CARD.basis_delegated(ownerName)
      : RECIPIENT_BASIS[basis]
    : null;
  const overdue = item.overdue === true;
  // 옛 `session_paused` 카드의 「새 상한」 입력(resumeSession)은 v0.3.0(R4, D22)에서 타입·op 과 함께 지웠다 —
  // 예산 멈춤은 `hitl_request(purpose: budget)` 카드가 받는다(아래 `hitlBudget`).
  /**
   * HITL 쪽 예산 상향 입력(W-6). **조건은 `card.purpose` 하나**다(K-9) — 항목 타입(옛 `session_paused`, R4 삭제)은
   * 보지 않는다. task 범위 초과(E9-01·E9-10)는 lane 만 멈추고 세션은 `active` 라서 항목이 `hitl_request` 로
   * 오는데, 그때도 Director 는 카드 안에서 금액을 정할 수 있어야 한다(U7-1).
   *
   * **여기서 HITL 상세를 읽지 않는다** — K-9 전에는 항목마다 `getHitlRequest` 를 한 번 더 불렀고(N+1),
   * 그 왕복이 카드가 그려진 뒤에 끝나 입력이 뒤늦게 붙었다.
   */
  const budgetHitl = item.type === "hitl_request" && item.card?.purpose === "budget";
  const hitlBudget = budgetHitl
    ? { scope: budgetScopeOf(item), current: detail?.budget_override_usd ?? null, spent: null }
    : null;

  return (
    <article
      className={`inbox-item inbox-item--${item.severity}${item.read_at ? "" : " inbox-item--unread"}`}
      data-testid="inbox-item"
      data-item-id={item.id}
      data-type={item.type}
      data-severity={item.severity}
      data-overdue={overdue ? "true" : "false"}
      data-delegated={item.delegated ? "true" : "false"}
    >
      <div className={`inbox-item__head${roomNames || item.room || item.room_id ? " inbox-item__head--ctx" : ""}`}>
        <Badge kind="inbox" value={item.severity} size="sm" tone={TONE_BY_TYPE[item.type]} />
        <span className="inbox-item__type" data-testid="inbox-type">{TYPE_LABEL[item.type]}</span>
        {/* 맥락 한 줄(§4.14) — 방 이름은 줄이지 않는다. 말줄임은 미션 제목부터(CSS: __work 가 먼저 줄어든다). */}
        {(roomNames || item.room || item.room_id) ? (
          <span className="inbox-item__room" data-testid="inbox-room" aria-label={INBOX_V19.room_aria(roomName)}>· {roomName}</span>
        ) : null}
        {workTitle ? (
          <span className="inbox-item__work" data-testid="inbox-session" title={workTitle}>· {workTitle}</span>
        ) : !(roomNames || item.room || item.room_id) && item.session?.title ? (
          <span className="inbox-item__session" data-testid="inbox-session">· {item.session.title}</span>
        ) : null}
        <span className="inbox-item__spacer" />
        <span
          className={`inbox-item__due${overdue ? " inbox-item__due--overdue" : ""}`}
          data-testid="inbox-due"
        >
          {item.due_at ? dueLabel(item.due_at, overdue, now) : relativeTime(item.created_at, now)}
        </span>
        {!item.read_at && onMarkRead && (
          <button type="button" className="msg__link" onClick={() => onMarkRead(item)} data-testid="inbox-read">
            읽음
          </button>
        )}
      </div>

      {quote && <p className="inbox-item__quote" data-testid="inbox-quote" title={quote}>{quote}</p>}
      {(basisText || delegation) && (
        <p className="inbox-item__basis" data-testid="inbox-basis" data-basis={basis ?? ""} data-locked={delegation?.locked ? "true" : "false"}>
          {basisText && <b>{basisText}</b>}
          {basisText && delegation && " · "}
          {delegation && <span data-testid="inbox-delegation">{delegation.text}</span>}
        </p>
      )}

      {item.type === "isolation_confirm" ? (
        <IsolationConfirmBody
          question={item.card?.title}
          computer={item.card?.runtime_name ?? null}
          hitlType={item.card?.hitl_type ?? null}
          options={options}
          proposedDefault={item.card?.proposed_default ?? null}
          canRespond={canRespondRoom && !delegation?.locked}
          busy={busy}
          settingsHref={links.room ? `${links.room}/settings` : null}
          onRespond={onRespond && ((body) => onRespond(item, body))}
        />
      ) : item.type === "room_paused" ? (
        <RoomPausedBody
          question={item.card?.body}
          detail={blocked}
          purpose={item.card?.purpose ?? null}
          offline={item.card?.paused_reason === "runtime_offline" || blocked?.reason === "runtime_offline"}
          canRespond={canRespondRoom && !delegation?.locked}
          busy={busy}
          onRespond={onRespond && ((body) => onRespond(item, body))}
        />
      ) : hitl && hitl.hitl_type ? (
        // 세션을 열지 않고 답한다(F2) — 타임라인 카드와 **같은 하위 컴포넌트**.
        <HitlBody
          type={hitl.hitl_type}
          status="open"
          question={hitl.title ?? "응답이 필요합니다"}
          context={hitl.body}
          proposedDefault={hitl.proposed_default}
          dueAt={item.due_at}
          overdue={overdue}
          canRespond={inline.length > 0}
          canRespondFrom={respondFrom ?? null}
          hideGate={!!delegation}
          actions={inline}
          budgetOverride={hitlBudget}
          onRespond={onRespond && ((body) => onRespond(item, body))}
          busy={busy}
          dense
        />
      ) : (
        <div className="inbox-item__body">
          {item.card?.title && <p className="inbox-item__title" data-testid="inbox-title">{item.card.title}</p>}
          {item.card?.body && <p className="inbox-item__text" data-testid="inbox-body">{item.card.body}</p>}
          {item.card?.agent_name && <p className="inbox-item__text">에이전트 @{item.card.agent_name}</p>}
        </div>
      )}

      {extraLine(item) && !(delegation && item.type === "hitl_request") && (
        <p className="inbox-item__extra" data-testid="inbox-extra">{extraLine(item)}</p>
      )}

      {rest.length > 0 && (
        <div className="inbox-item__actions" data-testid="inbox-actions">
          {rest.map((a, i) => (
            <button
              key={a}
              type="button"
              className={`btn btn--sm${i === 0 && a !== "open_session" ? " btn--primary" : ""}`}
              disabled={busy || !onAction}
              onClick={() => onAction?.(item, a)}
              data-testid={`inbox-action-${a}`}
            >
              {actionLabel(a)}
            </button>
          ))}
        </div>
      )}

      {/* 두 바로가기(PRD §6) — 미션이 없는 항목은 방 하나만. */}
      {(links.room || links.work) && (
        <div className="inbox-item__links" data-testid="inbox-links">
          {links.room && <Link href={links.room} className="msg__link" data-testid="inbox-open-room">{INBOX_V19.open_room}</Link>}
          {links.work && <Link href={links.work} className="msg__link" data-testid="inbox-open-work">{INBOX_V19.open_work}</Link>}
        </div>
      )}
    </article>
  );
}

export default InboxItemCard;
