// First-run nudge shown once no venue is configured yet (Task 3, venues/creds
// redesign). AppShell owns the show/hide decision (execStatus.venues.length,
// the "don't show again" localStorage flag, and the current-session dismiss
// flag) — this component is a dumb, controlled prompt: it only renders its own
// checkbox state and reports that state back on either action. Always mounted
// only while AppShell wants it shown (no internal `open` prop), same lifetime
// contract as TVDialog.
import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import { modalTracker } from "./modalTracker";

const BROKER_CHIPS = ["TradeZero", "Alpaca", "moomoo", "Sim"];

export function VenueSetupPrompt({ onConfigure, onDismiss }: {
  onConfigure: (dontShowAgain: boolean) => void;
  onDismiss: (dontShowAgain: boolean) => void;
}): JSX.Element {
  const { palette } = useTheme();
  const [dontShowAgain, setDontShowAgain] = useState(false);

  // Mirrors TVDialog's pattern exactly: this component only ever exists in the
  // tree while the prompt is showing, so mount == open and unmount == closed —
  // no `open` prop to gate on, so the tracker call can't get stuck true.
  useEffect(() => {
    modalTracker.setOpen(true);
    return () => modalTracker.setOpen(false);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onDismiss(dontShowAgain); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onDismiss, dontShowAgain]);

  return (
    <div onClick={() => onDismiss(dontShowAgain)}
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div onClick={(e) => e.stopPropagation()} className="venue-setup-prompt" role="dialog" aria-modal="true" aria-label="Set up a venue to trade"
        style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 460, padding: 20, boxSizing: "border-box" }}>
        <div className="serif" style={{ fontSize: 16, fontWeight: 600, color: palette.text, marginBottom: 8 }}>
          Set up a venue to trade
        </div>
        <p style={{ fontSize: 12, color: palette.textMuted, lineHeight: 1.5, margin: "0 0 14px" }}>
          Charts and the tape work without a venue. To place orders, add one — a broker, an
          environment, and its API keys.
        </p>
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 16 }}>
          {BROKER_CHIPS.map((b) => <span key={b} className="chip chip-set">{b}</span>)}
        </div>
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: palette.textMuted, marginBottom: 18, cursor: "pointer" }}>
          <input type="checkbox" checked={dontShowAgain} onChange={(e) => setDontShowAgain(e.target.checked)} />
          Don&rsquo;t show this again
        </label>
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button className="btn" aria-label="I'll do it later" onClick={() => onDismiss(dontShowAgain)}>I&rsquo;ll do it later</button>
          <button className="btn btn-primary" aria-label="Configure venues" onClick={() => onConfigure(dontShowAgain)}>Configure venues</button>
        </div>
      </div>
    </div>
  );
}
