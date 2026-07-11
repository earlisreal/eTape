import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { SessionStore } from "../data/SessionStore";
import type { ConnState } from "../wire/WsClient";
import { useTheme } from "./ThemeProvider";

// Persistent, unmissable strip shown whenever the engine reports replay mode
// (sys.session). Safety-critical: practice/replay trading must never be
// visually confusable with live trading (see standing safety rule), so this
// banner is the primary UI signal for "you are not looking at live data."
//
// "Return to live" mirrors VenuesSection.tsx's restartEngine/sawDropRef
// pattern: GoLive triggers an engine restart back into live mode, so the ack
// arrives ~200ms before the socket actually drops. Naively checking "is
// engineState open again yet" right after the ack would report success
// immediately (it's still the pre-drop "open"). Instead this waits for a
// genuine open -> non-open -> open cycle before clearing the in-flight
// "Returning to live…" state. The banner itself un-mounts naturally once
// sys.session reports mode: "live" after reconnect — sawDropRef/returning
// only smooth over the button label during the drop-then-reconnect gap.
export function ReplayBanner({ session, engineState, onGoLive }: {
  session: SessionStore; engineState: ConnState | undefined; onGoLive: () => Promise<void>;
}): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore((cb) => session.subscribe(cb), () => session.getSnapshot());
  const [returning, setReturning] = useState(false);
  const sawDropRef = useRef(false);

  useEffect(() => {
    if (!returning) { sawDropRef.current = false; return; }
    if (engineState !== "open") { sawDropRef.current = true; return; }
    if (sawDropRef.current) setReturning(false); // banner clears itself once sys.session reports "live" again
  }, [engineState, returning]);

  if (s.mode !== "replay") return null;
  const speed = s.speed && s.speed > 0 ? `${s.speed}×` : "max";
  return (
    <div data-testid="replay-banner" style={{
      display: "flex", alignItems: "center", justifyContent: "center", gap: 12,
      padding: "4px 12px", background: palette.warn, color: "#fff", fontWeight: 600,
    }}>
      <span>REPLAY — {s.day} @ {speed} · practice orders only</span>
      <button data-testid="return-to-live" disabled={returning} onClick={() => {
        setReturning(true);
        onGoLive().catch(() => setReturning(false));
      }}
        style={{ padding: "2px 10px", borderRadius: 4, border: "1px solid #fff", background: "transparent", color: "#fff", cursor: "pointer" }}>
        {returning ? "Returning to live…" : "Return to live"}
      </button>
    </div>
  );
}
