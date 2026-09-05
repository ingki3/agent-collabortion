import { Badge } from "@/components/Badge";
import {
  BADGE_ENTRY_COUNT,
  BADGE_KINDS,
  badgeSpec,
  badgeValues,
  toneTokens,
  type BadgeKind,
  type Tone,
  type Variant,
} from "@/components/badge-map";

const VARIANTS: readonly Variant[] = ["soft", "solid"];
const TONES: readonly Tone[] = ["run", "wait", "block", "pause", "done", "fail", "neutral"];

export const metadata = { title: "Badge 조합표 — Colab dev" };

/**
 * /dev/badges — 모든 kind × value × variant 격자. agent-browser 시각 회귀 대상.
 * 셀 수 = badge-map 항목 수 × 2. 각 셀: data-testid="badge-<kind>-<value>-<variant>".
 */
export default function BadgesPage() {
  const total = BADGE_ENTRY_COUNT * VARIANTS.length;
  return (
    <main className="page" style={{ maxWidth: 960 }}>
      <h1 className="h1">Badge 조합표</h1>
      <p className="muted">
        COMPONENTS.md · SCREEN.md §5 — 항목 <b data-testid="badge-entry-count">{BADGE_ENTRY_COUNT}</b> × variant{" "}
        {VARIANTS.length} = 셀 <b data-testid="badge-total">{total}</b>
      </p>

      {BADGE_KINDS.map((kind) => (
        <KindTable key={kind} kind={kind} />
      ))}

      <section style={{ marginTop: 32 }}>
        <h2 className="h1" style={{ fontSize: 16 }}>
          inbox × 원인 상태 tone
        </h2>
        <p className="muted-3">심각도 배지의 색은 항목의 원인 상태를 따른다(SCREEN §5, 리뷰 #03 N4). 격자 셀 수에 들어가지 않는다.</p>
        <table className="grid">
          <thead>
            <tr>
              <th>severity</th>
              {TONES.map((t) => (
                <th key={t}>{t}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {badgeValues("inbox").map((value) => (
              <tr key={value}>
                <th>{value}</th>
                {TONES.map((tone) => (
                  <td key={tone} data-testid={`inbox-tone-${value}-${tone}`}>
                    <Badge kind="inbox" value={value} tone={tone} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section style={{ marginTop: 32 }}>
        <h2 className="h1" style={{ fontSize: 16 }}>
          size
        </h2>
        <p>
          <Badge kind="lane" value="running" size="md" /> <Badge kind="lane" value="running" size="sm" />{" "}
          <Badge kind="lane" value="paused" size="sm" /> <Badge kind="lane" value="failed" size="sm" />
        </p>
      </section>

      <style>{`
        .grid { border-collapse: collapse; margin-top: 12px; font-size: 12px; }
        .grid th, .grid td { border: 1px solid var(--line); padding: 8px 12px; text-align: left; vertical-align: middle; }
        .grid th { background: var(--surface); color: var(--ink-2); font-weight: 500; }
        .grid td.meta { color: var(--ink-3); font-family: ui-monospace, monospace; font-size: 11px; }
      `}</style>
    </main>
  );
}

function KindTable({ kind }: { kind: BadgeKind }) {
  return (
    <section style={{ marginTop: 24 }} data-testid={`badge-kind-${kind}`}>
      <h2 className="h1" style={{ fontSize: 16 }}>
        {kind} <span className="muted-3">({badgeValues(kind).length})</span>
      </h2>
      <table className="grid">
        <thead>
          <tr>
            <th>value</th>
            {VARIANTS.map((v) => (
              <th key={v}>{v}</th>
            ))}
            <th>glyph</th>
            <th>tone</th>
            <th>default</th>
          </tr>
        </thead>
        <tbody>
          {badgeValues(kind).map((value) => {
            const spec = badgeSpec(kind, value);
            const tokens = toneTokens(spec.tone);
            return (
              <tr key={value}>
                <th>{value}</th>
                {VARIANTS.map((variant) => (
                  <td key={variant} data-testid={`badge-${kind}-${value}-${variant}`}>
                    <Badge kind={kind} value={value} variant={variant} />
                  </td>
                ))}
                <td className="meta">{spec.glyph}</td>
                <td className="meta">
                  {tokens.solid} / {tokens.text}
                </td>
                <td className="meta">{spec.variant}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
}
