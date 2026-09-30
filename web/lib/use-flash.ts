"use client";
// useFlash — 「저장됨」 같은 잠깐 켜졌다 꺼지는 표시의 타이머(#400 CI web: `window is not defined`).
// 예전에는 `setTimeout(() => setSaved(false), 1500)` 를 그 자리에서 걸었는데, 컴포넌트가 1.5초 안에 사라지면
// (화면 이동 · 테스트 환경 해체) 타이머가 남아 언마운트된 컴포넌트의 setState 를 부르거나, jsdom 이 걷힌 뒤에 터졌다.
// 이 훅은 걸어 둔 타이머를 기억하고 언마운트 때 모두 치운다 — 새로 걸면 앞의 것도 치운다(마지막 표시만 끈다).
import { useCallback, useEffect, useRef } from "react";

export function useFlash(ms = 1500): (off: () => void) => void {
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = null;
    },
    [],
  );
  return useCallback(
    (off: () => void) => {
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = setTimeout(() => {
        timer.current = null;
        off();
      }, ms);
    },
    [ms],
  );
}
