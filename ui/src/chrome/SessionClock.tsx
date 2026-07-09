import { useEffect, useState } from "react";
import { sessionAt, type Session } from "../render/chart/sessions";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

// Module-scope Intl.DateTimeFormat (built once, not per tick) — same idiom as
// render/chart/chartTheme.ts's ET_TICK formatters. hour12:false + timeZone handles
// EST/EDT (DST) automatically.
const ET_CLOCK = new Intl.DateTimeFormat("en-US", {
  hour12: false, timeZone: "America/New_York",
  hour: "2-digit", minute: "2-digit", second: "2-digit",
});

const SESSION_LABEL: Record<Session, string> = { pre: "PRE", rth: "RTH", post: "POST", closed: "CLOSED" };

// sessionPre/Rth/Post/Closed in the palette are low-alpha chart-shading fills
// (sessionRth is "usually transparent") — not visible as a status dot, so this
// maps to the same visible status tokens LatencyReadout uses.
const sessionColor = (s: Session, p: Palette): string =>
  s === "rth" ? p.ok : s === "pre" ? p.accent : s === "post" ? p.warn : p.textMuted;

// A 1Hz React projection of the wall clock. Interval (not rAF) keeps this
// deterministic under fake timers, matching the rule in exec/useThrottledQuote.ts.
function useEtClock(): number {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

// Center-of-top-bar ET clock + session badge (PRE/RTH/POST/CLOSED). Client-derived —
// no store, no props. sessionAt() is a client-side wall-clock classifier only (no
// holiday awareness); acceptable here since this is a glance indicator, not the
// order-gate source of truth (preChecks.ts has its own sessionAt call for that).
export function SessionClock(): JSX.Element {
  const { palette } = useTheme();
  const now = useEtClock();
  const session = sessionAt(now);
  return (
    <div
      data-testid="session-clock"
      style={{ display: "inline-flex", alignItems: "center", gap: 6, whiteSpace: "nowrap" }}
      title="Current time (US/Eastern)"
    >
      <span
        style={{ width: 7, height: 7, borderRadius: "50%", background: sessionColor(session, palette) }}
      />
      <span className="mono" style={{ fontSize: 12, color: palette.text }}>
        {ET_CLOCK.format(now)}
      </span>
      <span
        className="serif"
        style={{ fontSize: 9, letterSpacing: ".06em", textTransform: "uppercase", color: palette.textMuted }}
      >
        ET · {SESSION_LABEL[session]}
      </span>
    </div>
  );
}
