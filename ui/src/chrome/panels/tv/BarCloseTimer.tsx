// ui/src/chrome/panels/tv/BarCloseTimer.tsx
import { useEffect, useState } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import { remainingToBarCloseMs, formatCountdown } from "../../../render/chart/barClose";
import type { Timeframe } from "../../../render/chart/barBucket";

export interface BarCloseTimerProps {
  chrome: TvChrome;
  timeframe: string;
  price: string;
  lastPriceY: number;
  rightAxisWidth: number;
  paneBottom: number;
  up: boolean;
}

// Rendered line height of each row (price + countdown) at the font sizes below —
// used for both the price row's vertical centering and the pill's total height.
const ROW_HEIGHT = 15;
const PRICE_FONT = 12;
// Vertical padding above the price row and below the countdown row.
const PAD_V = 2;
const PILL_HEIGHT = PAD_V * 2 + ROW_HEIGHT * 2;

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

// TradingView-style merged price+countdown badge: a single pill in the price-axis
// column showing the live price on top and the time-to-bar-close beneath it, with
// no seam between the two rows. This REPLACES LWC's own last-value tag while it's
// visible (ChartPanel calls ChartController.setLastValueVisible(false) for the
// duration) — the price row's vertical center lines up with lastPriceY, the same
// coordinate LWC's tag would have centered on, so LWC's dotted price line (still
// drawn — only the tag is suppressed) meets the badge exactly where the price row
// sits. Plain position:absolute DOM over the canvas (same overlay layer as
// TVLegend) — pointerEvents:none so it never steals clicks/drag from the
// interactive chart underneath.
export function BarCloseTimer({ chrome, timeframe, price, lastPriceY, rightAxisWidth, paneBottom, up }: BarCloseTimerProps): JSX.Element {
  const now = useNowTick();
  const text = formatCountdown(remainingToBarCloseMs(timeframe as Timeframe, now));
  const tint = up ? chrome.up : chrome.down;
  const top = Math.min(lastPriceY - PAD_V - ROW_HEIGHT / 2, paneBottom - PILL_HEIGHT);

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
        fontVariantNumeric: "tabular-nums",
        background: tint,
        color: "#FFFFFF",
        borderRadius: TV_GEOM.radius,
        padding: `${PAD_V}px 0`,
      }}
    >
      <div data-testid="bar-close-timer-price" style={{ font: `700 ${PRICE_FONT}px ${TV_FONT}`, lineHeight: `${ROW_HEIGHT}px` }}>
        {price}
      </div>
      <div data-testid="bar-close-timer-countdown" style={{ font: `600 ${TV_GEOM.axisFont}px ${TV_FONT}`, lineHeight: `${ROW_HEIGHT}px` }}>
        {text}
      </div>
    </div>
  );
}
