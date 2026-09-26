"use client";
/**
 * S5 방 카드의 「…」 메뉴(SCREEN §4.3 「카드 「…」 메뉴」, T-R2-W1) — 「이름 바꾸기」(v0.19.5) · 「보관」(보관된 방은 「보관 해제」) · 「삭제」.
 *
 * 「이름 바꾸기」는 **권한자에게만 있다**(PRD FR-2.1.2 「그 밖에게는 … 편집 수단이 보이지 않는다」) — 보관·삭제처럼 비활성 + 사유로 두지 않는다.
 * 누르면 카드의 이름 줄이 S7 머리와 같은 편집 칸(`InlineTitleEdit`)으로 바뀐다(호출부 RoomCard 가 한다).
 *
 * 옛 세션 카드 메뉴(T-W13, R1.5b 에서 삭제)와 같은 자리·같은 키보드(`useCardMenu`)다. 항목은 **숨기지 않고** 비활성 + 사유(`DisabledHint`,
 * §5 권한 비활성 버튼) — 사유는 `archiveGate`·`deleteRoomGate`(lib/wording.ts)가 고르고 서버가 409·403 으로 다시 검사한다.
 * 삭제 항목에는 「되돌릴 수 없음」을 붙인다(FR-2.4 [V19-C] — 보관과 삭제를 나란히 두되 무게가 다르다는 것을 항목이 말한다).
 */
import { useId } from "react";
import { Icon } from "./Icon";
import { DisabledHint } from "./PageHead";
import { Slot } from "./Slot";
import { useCardMenu } from "./useCardMenu";
import { ROOM_MENU, ROOM_RENAME, type RoomGate } from "@/lib/wording";
import "./session-card-menu.css";

export interface RoomCardMenuProps {
  archived: boolean;
  archiveGate: RoomGate;
  deleteGate: RoomGate;
  /** 「보관」 — 확인 다이얼로그를 여는 것은 호출부의 몫. 「보관 해제」 는 되돌릴 수 있는 쪽이라 확인 없이 호출부가 바로 부른다. */
  onArchive: () => void;
  onUnarchive: () => void;
  onDelete: () => void;
  /** 「이름 바꾸기」 — 주지 않으면(권한 없음) 항목이 없다. */
  onRename?: () => void;
  testId?: string;
}

function GateHint({ id, gate }: { id: string; gate: RoomGate }) {
  if (gate.ok) return null;
  return <DisabledHint id={id}>{gate.count !== undefined ? <Slot text={gate.reason} n={gate.count} /> : gate.reason}</DisabledHint>;
}

export function RoomCardMenu({ archived, archiveGate, deleteGate, onArchive, onUnarchive, onDelete, onRename, testId }: RoomCardMenuProps) {
  const { open, root, button, close, toggle, onRootBlur, onButtonKey, onMenuKey } = useCardMenu();
  const base = useId();
  const archiveHint = `${base}-archive-hint`;
  const deleteHint = `${base}-delete-hint`;
  const run = (gate: RoomGate, f: () => void) => () => {
    if (!gate.ok) return;
    close();
    f();
  };

  return (
    <div className="card-menu" ref={root} onBlur={onRootBlur} data-testid={testId ? `room-menu-${testId}` : "room-menu"}>
      <button
        ref={button}
        type="button"
        className="card-menu__btn"
        aria-label={ROOM_MENU.button}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={toggle}
        onKeyDown={onButtonKey}
        data-testid="room-menu-button"
      >
        <Icon name="more" />
      </button>
      {open && (
        <div className="card-menu__list" role="menu" aria-label={ROOM_MENU.button} onKeyDown={onMenuKey} data-testid="room-menu-list">
          {onRename && (
            <button type="button" role="menuitem" className="card-menu__item" onClick={() => { close(); onRename(); }} data-testid="room-menu-rename">
              {ROOM_RENAME.menu_item}
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            className="card-menu__item"
            aria-disabled={!archiveGate.ok || undefined}
            aria-describedby={!archiveGate.ok ? archiveHint : undefined}
            onClick={run(archiveGate, archived ? onUnarchive : onArchive)}
            data-testid={archived ? "room-menu-unarchive" : "room-menu-archive"}
          >
            {archived ? ROOM_MENU.unarchive : ROOM_MENU.archive}
          </button>
          <GateHint id={archiveHint} gate={archiveGate} />
          <button
            type="button"
            role="menuitem"
            className="card-menu__item card-menu__item--danger"
            aria-disabled={!deleteGate.ok || undefined}
            aria-describedby={!deleteGate.ok ? deleteHint : undefined}
            onClick={run(deleteGate, onDelete)}
            data-testid="room-menu-delete"
          >
            {ROOM_MENU.delete} <span className="card-menu__tail">· {ROOM_MENU.delete_tail}</span>
          </button>
          <GateHint id={deleteHint} gate={deleteGate} />
        </div>
      )}
    </div>
  );
}

export default RoomCardMenu;
