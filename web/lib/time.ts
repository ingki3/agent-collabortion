/** 상대 시각("3분 전") — 목록·카드 메타에 쓴다. */
export function relativeTime(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const diff = Math.max(0, now - t);
  const s = Math.floor(diff / 1000);
  if (s < 10) return "방금";
  if (s < 60) return `${s}초 전`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}분 전`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}시간 전`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}일 전`;
  return new Date(t).toLocaleDateString("ko-KR");
}

export function clockTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return iso;
  return t.toLocaleTimeString("ko-KR", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/** 경과 시간을 "1분 12초" 로. */
export function elapsed(fromIso: string, now: number = Date.now()): string {
  const t = Date.parse(fromIso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.floor((now - t) / 1000));
  const m = Math.floor(s / 60);
  return m > 0 ? `${m}분 ${s % 60}초` : `${s}초`;
}

/** lane 카드의 경과 — 끝났으면 시작~종료, 진행 중이면 시작~지금. */
export function durationSince(fromIso: string | null | undefined, toIso?: string | null, now: number = Date.now()): string {
  if (!fromIso) return "—";
  const from = Date.parse(fromIso);
  if (Number.isNaN(from)) return "—";
  const to = toIso ? Date.parse(toIso) : now;
  const s = Math.max(0, Math.floor(((Number.isNaN(to) ? now : to) - from) / 1000));
  if (s < 60) return `${s}초`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}분`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h}시간 ${m % 60}분` : `${Math.floor(h / 24)}일`;
}

/** ISO 8601 duration(`PT4H`·`PT90M`)을 사람이 읽는 문자열로. 파싱 실패면 원문. */
export function humanDuration(iso: string | null | undefined): string {
  if (!iso) return "—";
  const m = /^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$/.exec(iso);
  if (!m) return iso;
  const [, d, h, mi, s] = m;
  const parts = [d && `${d}일`, h && `${h}시간`, mi && `${mi}분`, s && `${s}초`].filter(Boolean);
  return parts.length ? parts.join(" ") : iso;
}
