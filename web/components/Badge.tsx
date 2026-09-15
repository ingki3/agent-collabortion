import "./badge.css";
import {
  BADGE_MAP,
  type BadgeKind,
  type BadgeValue,
  type Tone,
  type Variant,
  type BadgeSpec,
} from "./badge-map";

type KindValue = { [K in BadgeKind]: { kind: K; value: BadgeValue<K> } }[BadgeKind];

export type BadgeProps = KindValue & {
  /** 기본은 조합표: failed·error·offline solid, 나머지 soft */
  variant?: Variant;
  /** 기본은 상태의 한국어 라벨 (SCREEN §4.3 등) */
  label?: string;
  size?: "sm" | "md";
  /**
   * 색 토큰 덮어쓰기. 인박스 심각도 배지는 색이 원인 상태를 따르므로(SCREEN §5) 호출부가 넘긴다.
   * 예: session_paused → "pause", runtime_offline → "fail".
   */
  tone?: Tone;
  className?: string;
};

export function Badge(props: BadgeProps) {
  const { kind, value, size = "md", className } = props;
  const spec = (BADGE_MAP[kind] as Record<string, BadgeSpec>)[value];
  const variant = props.variant ?? spec.variant;
  const label = props.label ?? spec.label;
  const tone = props.tone ?? spec.tone;
  const classes = ["badge", `badge--${variant}`, `badge--${size}`, className].filter(Boolean).join(" ");

  return (
    <span
      className={classes}
      role="img"
      aria-label={label}
      data-kind={kind}
      data-value={value}
      data-variant={variant}
      data-tone={tone}
    >
      <span className="badge__glyph" aria-hidden="true">
        {spec.glyph}
      </span>
      <span className="badge__label">{label}</span>
    </span>
  );
}

export default Badge;
