// Already-qualified symbols ("HK.00700", "US.NVDA") carry their own market
// prefix; a bare ticker gets the project-wide default market (US.) so it
// matches the `US.<TICKER>` convention every store/fixture keys by.
//
// This must be an explicit allow-list, not an open-ended `/^[A-Z]+\./` (or
// even a fixed-length `/^[A-Z]{2}\./`) pattern: real US tickers contain a
// dot as part of the ticker itself (BRK.B, BRK.A, BF.B, BF.A), and any
// leading-letters-then-dot rule misclassifies them as already market-
// qualified, leaving them unprefixed.
const MARKET_PREFIXES = ["US.", "HK."];

/** Uppercase; prefix bare tickers with `US.` unless already market-qualified.
 * Allow-list (not a regex) so dotted US tickers like BRK.B aren't misread. */
export function normalizeSymbol(raw: string): string {
  const upper = raw.toUpperCase();
  return MARKET_PREFIXES.some((p) => upper.startsWith(p)) ? upper : `US.${upper}`;
}
