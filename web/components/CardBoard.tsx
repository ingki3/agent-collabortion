"use client";
/**
 * 분담표(SCREEN §4.6 (가) 「분담표」 · COMPONENTS §9.13 Card Board · PRD FR-3.8 6) — 우열 미션 칸의 둘째 탭. 그 미션의 카드 트리.
 * 행 36px: 번호(등폭) · 담당 칩 · 목표 한 줄(말줄임) · 상태 칩 · 기준 충족 수(k/N, 결과 없으면 –) · 비용. 하위 카드는 16px 들여쓰기 + 「└」.
 * 번호순. 누르면 부른 쪽(`onOpen`)이 타임라인의 최신 판 말풍선(결과가 있으면 결과 카드)으로 스크롤·강조한다(미션 거르개 전환 포함).
 * 머리(「카드 N · 판정 대기 N」)는 탭 줄 오른쪽에 선다 — `WorkPanel` 이 `PanelTabs.aside` 로 넘긴다(`boardHead`).
 */
import "./card-board.css";
import { Badge } from "./Badge";
import { boardChip, boardTree } from "@/lib/cards";
import { TASK_CARD as L } from "@/lib/wording";
import type { CardBoard as Board, CardBoardItem } from "@/lib/api/types";

export function boardHead(board: Board): string {
  return L.board_head(board.total, board.pending_judgement);
}

export function CardBoard({ board, onOpen }: { board: Board | null; onOpen: (item: CardBoardItem) => void }) {
  if (!board) return <p className="aside__quiet" data-testid="card-board-loading">{L.board_loading}</p>;
  const rows = boardTree(board.items);
  return (
    <ul className="cboard" data-testid="card-board" aria-label={boardHead(board)}>
      {rows.map(({ item, depth }) => {
        const chip = boardChip(item.status);
        return (
          <li key={item.id} className="cboard__li" data-depth={depth}>
            <button
              type="button"
              className="cboard__row"
              style={depth ? { paddingLeft: 4 + depth * 16 } : undefined}
              onClick={() => onOpen(item)}
              aria-label={L.board_open(item.label)}
              data-testid="card-board-row"
              data-card-id={item.id}
              data-depth={depth}
              data-status={item.status}
            >
              {depth > 0 && <span className="cboard__tree" aria-hidden="true">└</span>}
              <span className="cboard__no">{item.label}</span>
              <span className="cboard__who" title={`@${item.assignee.name}`} aria-hidden="true">{item.assignee.name.slice(0, 1).toUpperCase()}</span>
              <span className="cboard__goal" title={item.goal}>{item.goal}</span>
              {chip.kind === "card" ? <Badge kind="card" value={chip.value} size="sm" /> : <Badge kind="card_judge" value={chip.value} size="sm" />}
              <span className="cboard__met" data-testid="card-board-met">{item.met == null ? L.board_none : `${item.met}/${item.total_criteria}`}</span>
              <span className="cboard__cost" data-testid="card-board-cost">{item.cost_usd == null ? L.board_none : `$${item.cost_usd.toFixed(2)}`}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

export default CardBoard;
