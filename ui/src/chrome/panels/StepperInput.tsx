import type { CSSProperties } from "react";
import { QUOTE_DECIMALS } from "../../render/format";

// ±10¢ price stepper. Wraps a native type="number" input (keyboard
// ArrowUp/ArrowDown already steps by STEP) with a compact ▲/▼ button column,
// styled to sit inside the shared .ctl control box (see global.css).
const STEP = 0.1;

type Props = {
  testid: string;
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  placeholder?: string;
  style?: CSSProperties;
};

export function StepperInput({ testid, value, onChange, disabled, placeholder, style }: Props): JSX.Element {
  const nudge = (dir: 1 | -1) => {
    const next = Math.max(0, (Number(value) || 0) + dir * STEP);
    onChange(next.toFixed(QUOTE_DECIMALS));
  };
  return (
    <div className="ctl stepper" style={style}>
      <input
        type="number" inputMode="decimal" step={STEP} min={0}
        className="numfield mono" data-testid={testid} value={value}
        onChange={(e) => onChange(e.target.value)} disabled={disabled} placeholder={placeholder}
      />
      <div className="stepper-btns">
        <button type="button" tabIndex={-1} data-testid={`${testid}-up`}
          aria-label={`Increase ${placeholder ?? testid} by 10 cents`} disabled={disabled}
          onClick={() => nudge(1)}>▲</button>
        <button type="button" tabIndex={-1} data-testid={`${testid}-down`}
          aria-label={`Decrease ${placeholder ?? testid} by 10 cents`} disabled={disabled}
          onClick={() => nudge(-1)}>▼</button>
      </div>
    </div>
  );
}
