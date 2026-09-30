/**
 * useFlash — 걸어 둔 타이머를 언마운트 때 치운다(#400 CI web: 설정 저장 뒤 1.5초 타이머가 테스트 환경 해체 뒤에 터져
 * `window is not defined`). 회귀 주입: useFlash 의 cleanup effect 를 빼면 (언마운트) FAIL; 새로 걸 때 앞 타이머를 안 치우면 (마지막만) FAIL.
 */
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useFlash } from "./use-flash";

let fire: () => void = () => {};
function Probe({ hit }: { hit: () => void }) {
  const flash = useFlash(1500);
  fire = () => flash(hit);
  return null;
}

describe("useFlash", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("1.5초 뒤 한 번 끈다", () => {
    const hit = vi.fn();
    render(<Probe hit={hit} />);
    act(() => fire());
    act(() => vi.advanceTimersByTime(1499));
    expect(hit).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(hit).toHaveBeenCalledTimes(1);
  });

  it("(언마운트) 걸린 타이머는 언마운트 때 치운다 — 사라진 컴포넌트의 setState 가 없다", () => {
    const hit = vi.fn();
    const r = render(<Probe hit={hit} />);
    act(() => fire());
    r.unmount();
    expect(vi.getTimerCount()).toBe(0);
    vi.advanceTimersByTime(5000);
    expect(hit).not.toHaveBeenCalled();
  });

  it("(마지막만) 다시 걸면 앞의 것을 치운다", () => {
    const hit = vi.fn();
    render(<Probe hit={hit} />);
    act(() => fire());
    act(() => vi.advanceTimersByTime(1000));
    act(() => fire());
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(1000));
    expect(hit).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(500));
    expect(hit).toHaveBeenCalledTimes(1);
  });
});
