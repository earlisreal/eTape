import { useSyncExternalStore } from "react";
import type { BootStore } from "../data/BootStore";
import { useTheme } from "./ThemeProvider";

// Neutral, self-gating strip shown during the pre-feed boot window (journal seal
// + connect). Deliberately NOT the red danger tone of FeedStatusBanner: this is
// expected daily maintenance, not a failure. Hidden once the engine is ready.
export function BootStatusBanner({ boot }: { boot: BootStore }): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore(boot.subscribe.bind(boot), boot.getSnapshot.bind(boot));
  if (s.phase === "ready") return null;

  let text: string;
  if (s.phase === "sealing") {
    const n = s.daysTotal ?? 1;
    text = n > 1
      ? `Preparing journal — compressing ${n} days…`
      : `Preparing journal — compressing 1 day (~15 s)…`;
  } else {
    text = "Connecting to market data…";
  }

  return (
    <div
      data-testid="boot-status-banner"
      className="serif"
      style={{
        display: "flex", alignItems: "center", gap: 6,
        padding: "5px 12px", fontSize: 12, color: palette.textMuted,
        background: palette.surface,
        borderBottom: `1px solid ${palette.border}`,
      }}
    >
      <span aria-hidden="true">⏳</span>
      {text}
    </div>
  );
}
