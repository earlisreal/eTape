// First-run nudge shown while no REAL (non-sim) broker venue is configured
// (Task 3, venues/creds redesign; re-keyed off "no real venue" once a paper
// sim practice venue started auto-seeding on first run — see
// config.SeedDefaultIfMissing). AppShell owns the show/hide decision
// (execStatus.venues, the "don't show again" localStorage flag, and the
// current-session dismiss flag) — this component is a dumb, controlled
// prompt: it only renders its own checkbox state and reports that state back
// on either action. Always mounted only while AppShell wants it shown (no
// internal `open` prop), same lifetime contract as TVDialog.
import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import { modalTracker } from "./modalTracker";

const BROKER_CHIPS = ["TradeZero", "Alpaca", "moomoo"];

export function VenueSetupPrompt({ onConfigure, onDismiss, onTryDemo }: {
  onConfigure: (dontShowAgain: boolean) => void;
  onDismiss: (dontShowAgain: boolean) => void;
  // Task 6 (U4): same onTryDemo callback threaded into EmptyState — this
  // prompt only ever shows while sessionMode.mode isn't "replay"/"demo" (see
  // AppShell's showVenueSetup), so unlike EmptyState there's no separate
  // gating boolean needed here; the button is always offered.
  onTryDemo: () => void;
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
      <div onClick={(e) => e.stopPropagation()} className="venue-setup-prompt" role="dialog" aria-modal="true" aria-label="Add a broker to trade live"
        style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 460, padding: 20, boxSizing: "border-box" }}>
        <div className="serif" style={{ fontSize: 16, fontWeight: 600, color: palette.text, marginBottom: 8 }}>
          Add a broker to trade live
        </div>
        <p style={{ fontSize: 12, color: palette.textMuted, lineHeight: 1.5, margin: "0 0 14px" }}>
          You already have a paper Sim practice venue, so you can place orders right away.
          To trade real money, add a broker — an environment and its API keys. A paper Alpaca
          venue also unlocks deeper 1-minute chart history (~20 days, quota-free) instead of
          moomoo's limited fallback.
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
          {/* Same palette.demo tint as EmptyState's "Try demo" CTA (not a
              full btn-primary treatment) — a lower-commitment alternative to
              this modal's actual primary action (Configure venues), tinted
              to preview where it leads without competing with it. */}
          <button className="btn" aria-label="Try demo" onClick={onTryDemo} style={{ color: palette.demo, borderColor: palette.demo }}>Try demo</button>
          <button className="btn btn-primary" aria-label="Configure venues" onClick={() => onConfigure(dontShowAgain)}>Configure venues</button>
        </div>
      </div>
    </div>
  );
}
