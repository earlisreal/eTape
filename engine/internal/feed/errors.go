package feed

import "errors"

// ErrUnknownSymbol means the market-data source reports no such symbol.
// Callers reject the load with a "unknown symbol" reason. Negative results
// are never cached (an intraday listing must not be locked out).
var ErrUnknownSymbol = errors.New("feed: unknown symbol")

// ErrFeedUnavailable means the source could not answer (transport error,
// timeout, decode failure, or an ambiguous server error). Callers reject the
// load with a "feed unavailable" reason and the user retries.
var ErrFeedUnavailable = errors.New("feed: unavailable")
