import { useSyncExternalStore } from "react";
import type { HealthStore } from "../data/HealthStore";
import type { ConnState } from "../wire/WsClient";
import { useTheme } from "./ThemeProvider";
import { HoverButton } from "./controls/HoverButton";

// Slim, unmissable notice strip shown under the top bar whenever the
// engine-moomoo link (OpenD's RTT probe) is reported down while the UI's own
// WebSocket to the engine is genuinely open. If the WS itself isn't open, the
// app already shows a full-screen ReconnectOverlay for that outage — this
// banner must stay hidden then to avoid a confusing double-message.
export function FeedStatusBanner(
  { health, engineState, onOpenConnection }:
  { health: HealthStore; engineState: ConnState; onOpenConnection: () => void },
): JSX.Element | null {
  const { palette } = useTheme();
  const state = useSyncExternalStore(health.subscribe.bind(health), health.getSnapshot.bind(health));

  if (engineState !== "open") return null;
  const moomoo = state.links.find((l) => l.link === "engine-moomoo");
  if (!moomoo || moomoo.status !== "down") return null;

  return (
    <div
      data-testid="feed-status-banner"
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 10,
        padding: "5px 12px",
        background: "rgba(168,30,48,.10)",
        borderBottom: `1px solid ${palette.danger}`,
      }}
    >
      <span className="serif" style={{ fontSize: 12, color: palette.danger, display: "flex", alignItems: "center", gap: 6 }}>
        <span aria-hidden="true">⚠</span>
        moomoo OpenD disconnected — live market data unavailable. Reconnecting…
      </span>
      <HoverButton
        data-testid="feed-banner-open-connection"
        className="btn"
        onClick={onOpenConnection}
        style={{ fontSize: 11, color: palette.danger, borderColor: palette.danger }}
      >
        Connection ▸
      </HoverButton>
    </div>
  );
}
