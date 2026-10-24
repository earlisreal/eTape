// First-run nudge shown while no REAL (non-sim) broker venue is configured
// (Task 3, venues/creds redesign; re-keyed off "no real venue" once a paper
// sim practice venue started auto-seeding on first run — see
// config.SeedDefaultIfMissing). AppShell owns the show/hide decision
// (execStatus.venues, the "don't show again" localStorage flag, and the
// current-session dismiss flag) — this component is a dumb, controlled
// prompt: it only renders its own checkbox state and reports that state back
// on either action. Always mounted only while AppShell wants it shown (no
// internal `open` prop), same lifetime contract as TVDialog.
import { useState } from "react";
import { useTheme } from "./ThemeProvider";
import { Dialog } from "./controls/Dialog";
import { Button } from "./controls/Button";

const BROKER_CHIPS = ["TradeZero", "Alpaca", "moomoo"];

export function VenueSetupPrompt({ onConfigure, onDismiss, onTryDemo }: {
  onConfigure: (dontShowAgain: boolean) => void;
  onDismiss: (dontShowAgain: boolean) => void;
  // Task 6 (U4): same onTryDemo callback threaded into EmptyState — this
  // prompt only ever shows while sessionMode.mode isn't "demo" (see
  // AppShell's showVenueSetup), so unlike EmptyState there's no separate
  // gating boolean needed here; the button is always offered.
  onTryDemo: () => void;
}): JSX.Element {
  const { palette } = useTheme();
  const [dontShowAgain, setDontShowAgain] = useState(false);

  return (
    <Dialog title="Add a broker to trade live" onClose={() => onDismiss(dontShowAgain)} width={500} className="venue-setup-prompt">
        <p style={{ fontSize: 12, color: palette.textMuted, lineHeight: 1.5, margin: "0 0 14px" }}>
          You already have a paper Sim practice venue, so you can place orders right away.
          To trade real money, fill in one of the broker cards — moomoo configures itself from
          OpenD, Alpaca and TradeZero just need an API key. A paper Alpaca card also unlocks
          deeper 1-minute chart history (~20 days, quota-free) instead of moomoo's limited
          fallback.
        </p>
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 16 }}>
          {BROKER_CHIPS.map((b) => <span key={b} className="chip chip-set">{b}</span>)}
        </div>
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: palette.textMuted, marginBottom: 18, cursor: "pointer" }}>
          <input type="checkbox" checked={dontShowAgain} onChange={(e) => setDontShowAgain(e.target.checked)} />
          Don&rsquo;t show this again
        </label>
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <Button aria-label="I'll do it later" onClick={() => onDismiss(dontShowAgain)}>I&rsquo;ll do it later</Button>
          {/* Same palette.demo tint as EmptyState's "Try demo" CTA (not the
              primary treatment) — a lower-commitment alternative to this
              modal's actual primary action (Configure venues), tinted to
              preview where it leads without competing with it. */}
          <Button aria-label="Try demo" onClick={onTryDemo} style={{ color: palette.demo, borderColor: palette.demo }}>Try demo</Button>
          <Button variant="primary" aria-label="Configure venues" onClick={() => onConfigure(dontShowAgain)}>Configure venues</Button>
        </div>
    </Dialog>
  );
}
