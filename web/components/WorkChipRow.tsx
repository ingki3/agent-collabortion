"use client";
/**
 * 미션 칩 줄(COMPONENTS §9.1 Work Chip · SCREEN §4.6 상단) — **거르개이자 선택자**. 누르면 가운데 타임라인·보드가 거르고 우열 미션 칸이
 * 그 미션으로 바뀐다(연동은 화면이 `ChipSel` 하나로 한다).
 *
 * 규칙 4(§9.1): ① 선택은 색만으로 표시하지 않는다 — 굵은 테두리 + 앞의 `✓`, 프로그램적으로는 `aria-pressed`. ② 누를 수 있는 쪽만 커서·포커스 링
 * (카드 안 라벨은 `WorkLabel` — 버튼이 아니다). ③ 칩 줄은 한 그룹이고 이름은 「미션 거르개」. ④ 누르면 두 곳이 바뀌므로 `aria-live="polite"`.
 * 접기 기준은 미션 칩만 센다(§8.7 Q8) — 판정은 `lib/room-view.ts` `chipRow`.
 */
import { useState } from "react";
import "./work-chip.css";
import { Slot } from "./Slot";
import { DisabledHint } from "./PageHead";
import { chipGlyph, chipRow, type ChipSel } from "@/lib/room-view";
import { WORK_CHIPS, WORK_PAUSE_LABEL } from "@/lib/wording";
import type { WorkListItem } from "@/lib/api/types";

export interface WorkChipRowProps {
  works: WorkListItem[];
  sel: ChipSel;
  onSelect: (sel: ChipSel) => void;
  /** 「+ 새 미션」(S21 — W3). 없으면 버튼을 그리지 않는다. */
  onNewWork?: () => void;
  /** 「+ 새 미션」 비활성 사유(보관된 방 등) — 버튼 아래 글자. */
  newWorkDisabled?: string | null;
  /** 칩을 누른 뒤의 조용한 안내 — 화면이 거른 결과(서브 미션 N개 · 메시지 N개)를 넣는다. */
  announce: string;
}

/** 칩 하나 — 상태 글리프 + 이름(말줄임 12자). ⏳ 는 파생이라 aria-label 로 「사람 대기」를 준다. */
function ChipFace({ w }: { w: Pick<WorkListItem, "title" | "status" | "waiting_human"> }) {
  const g = chipGlyph(w);
  return (
    <>
      <span className="work-chip__glyph" aria-hidden={g.waiting ? undefined : true} aria-label={g.waiting ? WORK_CHIPS.waiting : undefined} role={g.waiting ? "img" : undefined}>
        {g.glyph}
      </span>
      <span className="work-chip__name">{w.title}</span>
    </>
  );
}

/** 펼침 목록(「지난 미션 ▾」·「일시정지 N ▾」·「미션 N개 ▾」) — 한 번에 하나만 열린다. */
type Pop = "past" | "paused" | "overflow" | null;

export function WorkChipRow({ works, sel, onSelect, onNewWork, newWorkDisabled, announce }: WorkChipRowProps) {
  const row = chipRow(works, sel);
  const [pop, setPop] = useState<Pop>(null);
  const pressed = (s: ChipSel) => (s.kind === sel.kind && (s.kind !== "work" || (sel.kind === "work" && sel.id === s.id)));
  const pick = (s: ChipSel) => {
    setPop(null);
    onSelect(s);
  };
  const newBtn = onNewWork && (
    <span className="work-chips__new">
      <button
        type="button"
        className="btn btn--sm"
        onClick={onNewWork}
        disabled={!!newWorkDisabled}
        aria-describedby={newWorkDisabled ? "new-work-hint" : undefined}
        data-testid="new-work"
      >
        {WORK_CHIPS.new_work}
      </button>
      {newWorkDisabled && <DisabledHint id="new-work-hint">{newWorkDisabled}</DisabledHint>}
    </span>
  );
  // 미션이 하나도 없으면 칩 줄 자체를 그리지 않고 「+ 새 미션」만 남긴다(§4.6).
  if (!row.show) return <div className="work-chips work-chips--empty" data-testid="work-chips-empty">{newBtn}</div>;

  const chip = (s: ChipSel, face: React.ReactNode, testId: string, extra?: Record<string, string>) => {
    const on = pressed(s);
    return (
      <button type="button" className="work-chip" aria-pressed={on} onClick={() => pick(s)} data-testid={testId} {...extra}>
        {on && <span className="work-chip__check" aria-hidden="true">✓</span>}
        {face}
      </button>
    );
  };
  const listRow = (w: WorkListItem, testId: string, detail?: React.ReactNode) => (
    <li key={w.id}>
      <button type="button" className="work-chips__item" onClick={() => pick({ kind: "work", id: w.id })} data-testid={testId} data-work-id={w.id} aria-pressed={pressed({ kind: "work", id: w.id })}>
        <ChipFace w={w} />
        {detail}
      </button>
    </li>
  );
  const toggle = (p: Exclude<Pop, null>) => setPop((cur) => (cur === p ? null : p));

  return (
    <div className="work-chips" data-testid="work-chips">
      <div className="work-chips__row" role="group" aria-label={WORK_CHIPS.group}>
        {chip({ kind: "all" }, <span className="work-chip__name">{WORK_CHIPS.all}</span>, "chip-all")}
        {chip({ kind: "none" }, <span className="work-chip__name">{WORK_CHIPS.none}</span>, "chip-none")}
        {row.chips.map((w) => (
          <span key={w.id}>{chip({ kind: "work", id: w.id }, <ChipFace w={w} />, "work-chip", { "data-work-id": w.id, "data-status": w.status })}</span>
        ))}
        {row.overflow.length > 0 && (
          <button type="button" className="work-chip work-chip--more" aria-expanded={pop === "overflow"} onClick={() => toggle("overflow")} data-testid="chip-overflow">
            <Slot text={WORK_CHIPS.overflow} n={row.overflow.length} /> ▾
          </button>
        )}
        {row.past.length > 0 && (
          <button type="button" className="work-chip work-chip--more" aria-expanded={pop === "past"} onClick={() => toggle("past")} data-testid="chip-past">
            <Slot text={WORK_CHIPS.past} n={row.past.length} /> ▾
          </button>
        )}
        {row.paused.length > 0 && (
          <button type="button" className="work-chip work-chip--more" aria-expanded={pop === "paused"} onClick={() => toggle("paused")} data-testid="chip-paused">
            <Slot text={WORK_CHIPS.paused} n={row.paused.length} /> ▾
          </button>
        )}
        {newBtn}
      </div>
      {pop === "overflow" && <ul className="work-chips__pop" data-testid="chip-overflow-list">{row.overflow.map((w) => listRow(w, "chip-overflow-item"))}</ul>}
      {pop === "past" && <ul className="work-chips__pop" data-testid="chip-past-list">{row.past.map((w) => listRow(w, "chip-past-item"))}</ul>}
      {pop === "paused" && (
        <ul className="work-chips__pop" data-testid="chip-paused-list">
          {row.paused.map((w) =>
            listRow(
              w,
              "chip-paused-item",
              <span className="work-chips__detail">
                {w.paused_reason ? ` · ${WORK_PAUSE_LABEL[w.paused_reason]}` : ""}
                {" · "}
                {WORK_CHIPS.approver}
                {w.director.display_name}
              </span>,
            ),
          )}
        </ul>
      )}
      <p className="sr-only" aria-live="polite" data-testid="chip-announce">{announce}</p>
    </div>
  );
}

/**
 * 카드 안의 미션 라벨(서브 미션 카드 · 메시지 카드) — 칩과 같은 모양이지만 **누를 수 없다**(§9.1 규칙 2: 커서·포커스 링 없음).
 * `(전체)` 보기에서만 보이고, 칩이 골라져 있으면 감춘다(양방향 규칙) — 그 판정은 부르는 쪽.
 */
export function WorkLabel({ text, testId = "work-label" }: { text: string; testId?: string }) {
  return (
    <span className="work-chip work-chip--label" data-testid={testId}>
      {text}
    </span>
  );
}

export default WorkChipRow;
