// ui/src/chrome/panels/tv/BarCloseTimer.tsx
import { useEffect, useState } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import { remainingToBarCloseMs, formatCountdown } from "../../../render/chart/barClose";
import type { Timeframe } from "../../../render/chart/barBucket";

export interface BarCloseTimerProps {
  chrome: TvChrome;
  timeframe: string;
  lastPriceY: number;
  rightAxisWidth: number;
  paneBottom: number;
  up: boolean;
}

// Gap below LWC's built-in last-price tag (tag itself is ~18-20px tall at
// axisFont size) so the badge reads as sitting directly underneath it, not
// floating separately in the axis column.
const LABEL_OFFSET = 20;
// Approximate rendered height of the pill (font line + vertical padding) —
// used only to keep the clamp math simple; doesn't need to be exact since it
// just biases the badge a few px earlier than a hard overflow.
const BADGE_HEIGHT = 18;

// A 1Hz React projection of the wall clock, local to this component — same
// idiom as SessionClock's useEtClock. Interval (not rAF) keeps this
// deterministic under fake timers; this is low-rate chrome state, not the
// high-frequency canvas path, so a React re-render per tick is fine.
function useNowTick(): number {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

// TradingView-style "time to bar close" badge, positioned directly below LWC's
// built-in last-price tag in the price-axis column. Plain position:absolute DOM
// over the canvas (same overlay layer as TVLegend) — pointerEvents:none so it
// never steals clicks/drag from the interactive chart underneath.
export function BarCloseTimer({ chrome, timeframe, lastPriceY, rightAxisWidth, paneBottom, up }: BarCloseTimerProps): JSX.Element {
  const now = useNowTick();
  const text = formatCountdown(remainingToBarCloseMs(timeframe as Timeframe, now));
  const tint = up ? chrome.up : chrome.down;
  const top = Math.min(lastPriceY + LABEL_OFFSET, paneBottom - BADGE_HEIGHT);

  return (
    <div
      data-testid="bar-close-timer"
      style={{
        position: "absolute",
        right: 0,
        top,
        width: rightAxisWidth,
        zIndex: 5,
        pointerEvents: "none",
        textAlign: "center",
        font: `600 ${TV_GEOM.axisFont}px ${TV_FONT}`,
        fontVariantNumeric: "tabular-nums",
        background: tint,
        color: "#FFFFFF",
        borderRadius: TV_GEOM.radius,
        padding: "1px 0",
      }}
    >
      {text}
    </div>
  );
}
