import { useTheme } from "./ThemeProvider";
import { HoverButton } from "./controls/HoverButton";

// Non-blocking hint shown when at least one venue is configured but none of
// them is Alpaca. A paper Alpaca venue is the quota-free deep 1-minute
// history provider (~20 trading days via SIP, engine/cmd/etape/main.go's
// intraday chain); without it, deep 1m backfill falls back to moomoo's
// quota-guarded request_history_kline (1 of 100 slots per rolling 30 days
// per symbol) and degrades to ~1,000 cached bars (≈1 trading day) once that
// quota is exhausted — see
// docs/superpowers/specs/2026-07-10-history-bars-providers-design.md.
// AppShell owns show/hide + the "don't show again" persistence
// (etape.alpacaHintHidden); this is a dumb, controlled banner, same contract
// as VenueSetupPrompt (not the self-gating FeedStatusBanner).
export function AlpacaBackfillBanner(
  { onSetup, onDismiss }: { onSetup: () => void; onDismiss: () => void },
): JSX.Element {
  const { palette } = useTheme();
  return (
    <div
      data-testid="alpaca-backfill-banner"
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 10,
        padding: "5px 12px",
        background: "rgba(154,106,27,.08)",
        borderBottom: `1px solid ${palette.accent}`,
      }}
    >
      <span className="serif" style={{ fontSize: 12, color: palette.accent, display: "flex", alignItems: "center", gap: 6 }}>
        <span aria-hidden="true">ⓘ</span>
        Add a paper Alpaca venue for deeper 1-minute history — ~20 days, quota-free.
      </span>
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <HoverButton
          data-testid="alpaca-banner-setup"
          className="btn"
          onClick={onSetup}
          style={{ fontSize: 11, color: palette.accent, borderColor: palette.accent }}
        >
          Set up Alpaca ▸
        </HoverButton>
        <HoverButton
          data-testid="alpaca-banner-dismiss"
          aria-label="Dismiss"
          className="btn"
          onClick={onDismiss}
          style={{ fontSize: 11, color: palette.textMuted }}
        >
          ✕
        </HoverButton>
      </div>
    </div>
  );
}
