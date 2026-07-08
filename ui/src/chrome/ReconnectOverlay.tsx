import { useEffect, useState, type ReactNode } from "react";
import type { ConnState } from "../wire/WsClient";
import { useTheme } from "./ThemeProvider";

// A brief, healthy reconnect (sub-second, e.g. tens of ms on localhost) is common
// during busy market activity and must stay invisible to the user.
const GRACE_MS = 600;

// Honesty policy: while not "open" for longer than the grace period, dim the
// surfaces and say so — never present stale canvases as live.
export function ReconnectOverlay({ state, children }: { state: ConnState; children: ReactNode }): JSX.Element {
  const { palette } = useTheme();
  const [showOverlay, setShowOverlay] = useState(false);
  const isOpen = state === "open";

  // Keyed on the open/non-open boundary (not the raw state) so churn between
  // non-open values (e.g. "reconnecting" -> "connecting" during a retry) does
  // not reset an in-flight grace timer. Only a genuine return to "open" — or
  // the initial open->non-open transition — should (re)schedule this effect.
  useEffect(() => {
    if (isOpen) {
      setShowOverlay(false);
      return;
    }
    const handle = setTimeout(() => setShowOverlay(true), GRACE_MS);
    return () => clearTimeout(handle);
  }, [isOpen]);

  return (
    <div style={{ position: "relative", height: "100%" }}>
      <div style={{ height: "100%", opacity: showOverlay ? 0.4 : 1, transition: "opacity 120ms" }}>
        {children}
      </div>
      {showOverlay && (
        <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center",
          background: palette.bg, color: palette.textMuted, pointerEvents: "none" }}>
          {state === "connecting" ? "connecting…" : "reconnecting…"}
        </div>
      )}
    </div>
  );
}
