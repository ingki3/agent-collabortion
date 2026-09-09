"use client";
/**
 * 테마 선택(COMPONENTS §8.3) — 시스템 따름 / 밝게 / 어둡게. 선택은 localStorage 에 남는다.
 *
 * 서버 렌더에는 "시스템 따름"을 그린다. 저장된 값은 브라우저에만 있으므로 서버가 알 수 없고,
 * 첫 페인트의 **색**은 layout.tsx 의 인라인 스크립트가 이미 맞춰 둔다 — 여기서는 마운트 뒤에
 * 라디오의 체크 위치만 따라잡는다(색이 아니라 컨트롤 상태라 깜빡임으로 보이지 않는다).
 */
import { useEffect, useState } from "react";
import { applyTheme, readTheme, type ThemeChoice } from "@/lib/theme";
import "./theme-select.css";

const OPTIONS: { value: ThemeChoice; label: string }[] = [
  { value: "system", label: "시스템 따름" },
  { value: "light", label: "밝게" },
  { value: "dark", label: "어둡게" },
];

export function ThemeSelect() {
  const [choice, setChoice] = useState<ThemeChoice>("system");

  useEffect(() => {
    setChoice(readTheme());
  }, []);

  function pick(next: ThemeChoice) {
    setChoice(next);
    applyTheme(next);
  }

  return (
    <div className="theme-pick" data-testid="theme-select">
      <div className="theme-pick__opts" role="radiogroup" aria-label="테마">
        {OPTIONS.map((o) => (
          <label key={o.value} className="theme-pick__opt" data-value={o.value}>
            <input
              type="radio"
              name="colab-theme"
              value={o.value}
              checked={choice === o.value}
              onChange={() => pick(o.value)}
            />
            {o.label}
          </label>
        ))}
      </div>
      <p className="theme-pick__note">시스템 따름은 운영체제의 밝기 설정을 그대로 씁니다.</p>
    </div>
  );
}
