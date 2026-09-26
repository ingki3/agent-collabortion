/**
 * 수 자리가 있는 문장 — `앞{수}뒤`(COMPONENTS §8.5 v0.19 "수를 문장에 보간하지 않는다"). 문장 두 토막은 `lib/wording.ts` 의 `Slotted` 이고
 * 문구 자물쇠가 토막을 그대로 잰다. 수는 `data-slot` 칸에 선다 — 화면 테스트가 수만 따로 읽을 수 있다.
 */
import type { Slotted } from "@/lib/wording";

export function Slot({ text, n }: { text: Slotted; n: number | string }) {
  return (
    <>
      {text[0]}
      <span className="slot" data-slot>{n}</span>
      {text[1]}
    </>
  );
}

/** 같은 문장을 속성(`aria-label`)에 넣을 때 — 속성은 문자열이어야 한다. */
export function slotText(text: Slotted, n: number | string): string {
  return text[0] + String(n) + text[1];
}
