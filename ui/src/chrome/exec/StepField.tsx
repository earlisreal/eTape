// A text-input stepper for the Orders & hotkeys template editor. Visually
// identical to the order-ticket's StepperInput (same .ctl/.stepper-btns CSS,
// same ▲/▼ glyphs) but built on a plain text <input> rather than
// type="number". That's deliberate, not an oversight: offset/size cells are
// edited through a `rawEdits` staging map (see OrderSettingsSection) so a
// value like "0.05" can be typed keystroke-by-keystroke ("0" -> "0." ->
// "0.0" -> "0.05") without the numeric model collapsing the trailing ".".
// type="number" sanitizes "0." to "" immediately and would break that flow
// (and its regression tests) — so this component knows nothing about
// numbers at all; every step/clamp/format decision lives in the caller.
import type { CSSProperties } from "react";

type Props = {
  ariaLabel: string;
  testid: string;          // buttons expose `${testid}-up` / `${testid}-down`
  value: string;
  onType: (v: string) => void;
  onStep: (dir: 1 | -1) => void;
  onBlur?: () => void;
  disabled?: boolean;
  style?: CSSProperties;
};

export function StepField({ ariaLabel, testid, value, onType, onStep, onBlur, disabled, style }: Props): JSX.Element {
  return (
    <div className="ctl stepper" style={style}>
      <input
        aria-label={ariaLabel}
        className="numfield mono"
        data-testid={testid}
        value={value}
        inputMode="decimal"
        disabled={disabled}
        onChange={(e) => onType(e.target.value)}
        onBlur={onBlur}
      />
      <div className="stepper-btns">
        <button type="button" tabIndex={-1} data-testid={`${testid}-up`} aria-label={`Increase ${ariaLabel}`} disabled={disabled} onClick={() => onStep(1)}>▲</button>
        <button type="button" tabIndex={-1} data-testid={`${testid}-down`} aria-label={`Decrease ${ariaLabel}`} disabled={disabled} onClick={() => onStep(-1)}>▼</button>
      </div>
    </div>
  );
}
