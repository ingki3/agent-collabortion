"use client";
/**
 * 우열 비용 줄(T-BUDGETCAP, SCREEN §4.6 「비용 줄」) — 미션 칸과 방 전체 칸이 같이 쓴다.
 *
 * - 상한이 있으면 「$3.20 / $20 (16%)」.
 * - 없으면 「$92.77 · 상한 없음 — [상한 걸기]」. 미션은 자기 상한이 없고 방 상한이 있으면 「방 상한 $20 을 따릅니다」
 *   (계약 WorkLimits 「비우면 방 한도를 따른다」 — 없다고 말하면 거짓이다).
 * - [상한 걸기]는 **그 자리의 작은 입력**이다. 권한이 없으면 버튼을 그리지 않는다(읽기 전용 줄) — 권한은 부르는 쪽이 정한다
 *   (방: `configure` 능력 · 미션: 그 미션의 Director).
 * 기본값은 바꾸지 않는다 — 상한 없음이 기본이고, 이 줄은 그것을 **보이게** 할 뿐이다(Director 2026-09-25).
 */
import { useState } from "react";
import { Slot } from "./Slot";
import { errorMessage } from "@/lib/api/client";
import { BUDGET_CAP } from "@/lib/wording";

export interface BudgetCapLineProps {
  /** 줄 앞말(「이 미션 」) — 없으면 금액부터. */
  prefix?: string;
  cost: number;
  limit: number | null;
  /** 미션 줄만 — 자기 상한이 없을 때 따르는 방 상한. */
  roomLimit?: number | null;
  estimated?: boolean;
  estimatedLabel?: string;
  /** 권한자만. 없으면 [상한 걸기]를 그리지 않는다. */
  onSet?: (usd: number) => Promise<void>;
  busy?: boolean;
  /** 입력 아래 한 줄 — 넘으면 무엇이 멈추는지. */
  note: string;
  testId: string;
}

export function BudgetCapLine(props: BudgetCapLineProps) {
  const { cost, limit } = props;
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const pct = limit ? Math.round((cost / limit) * 100) : null;
  const follows = limit == null && props.roomLimit != null ? props.roomLimit : null;

  async function save() {
    const n = Number(value);
    if (!value.trim() || !Number.isFinite(n) || n <= 0) return setErr(BUDGET_CAP.invalid);
    if (n <= cost) return setErr(BUDGET_CAP.too_low);
    setSaving(true);
    setErr(null);
    try {
      await props.onSet!(n);
      setEditing(false);
      setValue("");
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <p className="aside__cost" data-testid={props.testId} data-capped={limit != null ? "true" : "false"}>
        {props.prefix}${cost.toFixed(2)}
        {limit != null ? ` / $${limit}` : ""}
        {pct != null ? ` (${pct}%)` : ""}
        {props.estimated && props.estimatedLabel && <span className="aside__badge">{props.estimatedLabel}</span>}
        {limit == null && (
          <span className="budget-cap__none" data-testid={`${props.testId}-none`}>
            {" · "}
            {follows != null ? <Slot text={BUDGET_CAP.follows_room} n={follows} /> : BUDGET_CAP.none}
            {props.onSet && !editing && (
              <>
                {" — "}
                <button type="button" className="aside__link budget-cap__set" onClick={() => setEditing(true)} disabled={props.busy} data-testid={`${props.testId}-set`}>
                  {BUDGET_CAP.set}
                </button>
              </>
            )}
          </span>
        )}
      </p>
      {editing && (
        <form
          className="budget-cap__form"
          data-testid={`${props.testId}-form`}
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label className="budget-cap__row">
            <span className="aside__quiet">{BUDGET_CAP.input_label}</span>
            <input
              className="input input--num"
              type="number"
              min={0}
              step="1"
              inputMode="decimal"
              autoFocus
              value={value}
              onChange={(e) => (setValue(e.target.value), setErr(null))}
              aria-invalid={err ? true : undefined}
              data-testid={`${props.testId}-input`}
            />
          </label>
          <span className="budget-cap__row">
            <button type="submit" className="btn btn--sm btn--primary" disabled={saving || props.busy} data-testid={`${props.testId}-save`}>{BUDGET_CAP.save}</button>
            <button type="button" className="btn btn--sm btn--ghost" onClick={() => (setEditing(false), setErr(null), setValue(""))} data-testid={`${props.testId}-cancel`}>{BUDGET_CAP.cancel}</button>
          </span>
          {err ? <span className="aside__warn" role="alert" data-testid={`${props.testId}-err`}>{err}</span> : <span className="aside__quiet">{props.note}</span>}
        </form>
      )}
    </>
  );
}

export default BudgetCapLine;
