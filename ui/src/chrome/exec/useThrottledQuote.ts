import { useEffect, useState } from "react";
import type { Quote } from "../../wire/contract";
import type { QuoteStore } from "../../data/QuoteStore";

// A capped-rate React projection of QuoteStore for the ticket's bid/ask display.
// The hard rule forbids raw per-tick React updates; this polls at ≤ hz (default 6),
// only re-rendering when the store's revision advanced. Interval (not rAF) keeps it
// deterministic under fake timers.
export function useThrottledQuote(quotes: QuoteStore, symbol: string, hz = 6): Quote | undefined {
  const [q, setQ] = useState<Quote | undefined>(() => quotes.get(symbol));
  useEffect(() => {
    setQ(quotes.get(symbol));
    let lastRev = quotes.getRev();
    const id = setInterval(() => {
      const rev = quotes.getRev();
      if (rev !== lastRev) { lastRev = rev; setQ(quotes.get(symbol)); }
    }, Math.max(1, Math.floor(1000 / hz)));
    return () => clearInterval(id);
  }, [quotes, symbol, hz]);
  return q;
}
