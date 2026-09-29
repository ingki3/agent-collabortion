/**
 * 탭 패턴(#305 NN3 — WAI-ARIA tabs)의 키 이동 한 규칙. 방 화면 좁은 탭(S7)과 패널 안 탭(`components/PanelTabs`)이 같이 쓴다.
 * ←→ 는 한 칸씩(끝에서 돌아간다) · Home 은 처음 · End 는 끝. 다른 키면 -1(기본 동작 그대로).
 */
export function tabKeyTarget(key: string, index: number, count: number): number {
  if (count <= 0) return -1;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
  return step ? (index + step + count) % count : -1;
}
