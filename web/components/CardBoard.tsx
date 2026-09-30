"use client";
/**
 * 분담표(SCREEN §4.6 (가) 「분담표」 · COMPONENTS §9.13 Card Board · PRD FR-3.8 6) — 우열 미션 칸의 둘째 탭. 그 미션의 카드 트리.
 * 두 줄 행(#397 리뷰 (5) · Lead 결정): 1줄 「C-3 · 목표」(전폭 말줄임 — 268px 에서 한 줄 열 여섯이면 목표가 2~5자로 잘려 읽을 수 없었다),
 * 2줄 「@담당 이름 · [상태 칩] · k/N · $비용」(`--fs-meta`, 결과 없으면 k/N 은 –). 담당은 이니셜 칩 대신 이름 글자(Designer·Developer 가 둘 다
 * 「D」라 구분이 안 되던 것). 하위 카드는 16px 들여쓰기 + 「└」. 행 aria-label 은 번호·목표·담당·상태·k/N(#397 NN6 — 내용을 덮지 않는다).
 * 번호순. 누르면 부른 쪽(`onOpen`)이 타임라인의 최신 판 말풍선(결과가 있으면 결과 카드)으로 스크롤·강조한다(미션 거르개 전환 포함).
 * 머리(「카드 N · 판정 대기 N」)는 탭 줄 오른쪽에 선다 — `WorkPanel` 이 `PanelTabs.aside` 로 넘긴다(`boardHead`).
 */
import "./card-board.css";
import { Badge } from "./Badge";
import { boardChip, boardTree } from "@/lib/cards";
import { TASK_CARD as L } from "@/lib/wording";
import type { CardBoard as Board, CardBoardItem } from "@/lib/api/types";

const statusText = (c: ReturnType<typeof boardChip>) => (c.kind === "card" ? L.status[c.value] : L.judge[c.value]);

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
        const met = item.met == null ? L.board_none : `${item.met}/${item.total_criteria}`;
        return (
          <li key={item.id} className="cboard__li" data-depth={depth}>
            <button
              type="button"
              className="cboard__row"
              style={depth ? { paddingLeft: 4 + depth * 16 } : undefined}
              onClick={() => onOpen(item)}
              aria-label={L.board_row_aria(item.label, item.goal, item.assignee.name, statusText(chip), met)}
              title={L.board_open(item.label)}
              data-testid="card-board-row"
              data-card-id={item.id}
              data-depth={depth}
              data-status={item.status}
            >
              {depth > 0 && <span className="cboard__tree" aria-hidden="true">└</span>}
              <span className="cboard__main">
                <span className="cboard__line1">
                  <span className="cboard__no">{item.label}</span>
                  <span className="cboard__sep" aria-hidden="true">·</span>
                  <span className="cboard__goal" title={item.goal} data-testid="card-board-goal">{item.goal}</span>
                </span>
                <span className="cboard__line2">
                  <span className="cboard__who" data-testid="card-board-who">@{item.assignee.name}</span>
                  <span className="cboard__sep" aria-hidden="true">·</span>
                  {chip.kind === "card" ? <Badge kind="card" value={chip.value} size="sm" /> : <Badge kind="card_judge" value={chip.value} size="sm" />}
                  <span className="cboard__sep" aria-hidden="true">·</span>
                  <span className="cboard__met" data-testid="card-board-met">{met}</span>
                  <span className="cboard__sep" aria-hidden="true">·</span>
                  <span className="cboard__cost" data-testid="card-board-cost">{item.cost_usd == null ? L.board_none : `$${item.cost_usd.toFixed(2)}`}</span>
                </span>
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

export default CardBoard;
