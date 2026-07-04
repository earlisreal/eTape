import type { ReactNode } from "react";
import type { ConnState } from "../wire/WsClient";

// Honesty policy: while not "open", dim the surfaces and say so — never present
// stale canvases as live.
export function ReconnectOverlay({ state, children }: { state: ConnState; children: ReactNode }): JSX.Element {
  return (
    <div style={{ position: "relative", height: "100%" }}>
      <div style={{ height: "100%", opacity: state === "open" ? 1 : 0.4, transition: "opacity 120ms" }}>
        {children}
      </div>
      {state !== "open" && (
        <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center",
          background: "rgba(15,17,21,0.35)", color: "#cbd5e1", pointerEvents: "none" }}>
          {state === "connecting" ? "connecting…" : "reconnecting…"}
        </div>
      )}
    </div>
  );
}
