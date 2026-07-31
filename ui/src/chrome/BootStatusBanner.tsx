import { useSyncExternalStore } from "react";
import type { BootStore } from "../data/BootStore";
import { useTheme } from "./ThemeProvider";

export function BootStatusBanner({ boot }: { boot: BootStore }): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore(boot.subscribe.bind(boot), boot.getSnapshot.bind(boot));
  if (s.phase === "ready") return null;
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
      Connecting to market data…
    </div>
  );
}
