/**
 * 테마 선택(COMPONENTS §8.3) — 시스템 따름 / 밝게 / 어둡게.
 *
 * 저장은 localStorage 한 칸이고, 실제 전환은 `<html data-theme>` 한 속성이 한다.
 * "시스템 따름"은 속성을 **지우는 것**이다 — tokens.css 의
 * `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) }` 가 그때 산다.
 *
 * 첫 페인트 깜빡임은 layout.tsx 가 <head> 에 심는 THEME_INIT 이 막는다(하이드레이션 전에 실행된다).
 * 여기와 THEME_INIT 은 **같은 키·같은 값**을 써야 한다 — 둘이 갈라지면 첫 페인트만 틀린다.
 */
export const THEME_KEY = "colab.theme";

export type ThemeChoice = "system" | "light" | "dark";

export function isThemeChoice(v: unknown): v is ThemeChoice {
  return v === "system" || v === "light" || v === "dark";
}

/** 첫 페인트 전에 실행되는 인라인 스크립트. localStorage 가 막힌 브라우저에서도 조용히 넘어간다. */
export const THEME_INIT = `(function(){try{var t=localStorage.getItem(${JSON.stringify(THEME_KEY)});if(t==="dark"||t==="light"){document.documentElement.setAttribute("data-theme",t)}}catch(e){}})();`;

export function readTheme(): ThemeChoice {
  try {
    const v = window.localStorage.getItem(THEME_KEY);
    return isThemeChoice(v) ? v : "system";
  } catch {
    return "system";
  }
}

/** 속성 반영과 저장을 함께 한다 — 한쪽만 하면 새로고침에서 되돌아간다. */
export function applyTheme(choice: ThemeChoice): void {
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
  try {
    if (choice === "system") window.localStorage.removeItem(THEME_KEY);
    else window.localStorage.setItem(THEME_KEY, choice);
  } catch {
    /* 저장이 막혀도 이번 세션의 전환은 살린다 */
  }
}
