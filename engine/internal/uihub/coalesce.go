package uihub

import "github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

// dedupOf computes the keep-latest coalescing key for a staged md/account frame.
func dedupOf(s staged) string {
	switch p := s.Payload.(type) {
	case wsmsg.Quote:
		return "q|" + p.Symbol
	case wsmsg.Book:
		return "b|" + p.Symbol
	case wsmsg.Bar:
		return "bar|" + p.Symbol + "|" + p.Timeframe + "|" + p.BucketStart
	case wsmsg.AccountRow:
		return "acct|" + p.Venue
	default:
		return string(s.Topic) + "|" + s.Key
	}
}

// coalesceClass buckets a staged frame into how the hub should flush it.
type coalesceClass int

const (
	classMDKeep    coalesceClass = iota // quote/book/bars -> md ticker, keep-latest by dedup
	classTape                           // md.tape -> md ticker, batch-append
	classAccount                        // exec.account -> account ticker, keep-latest by venue
	classPositions                      // exec.positions -> position ticker, full-replace
	classImmediate                      // everything else -> broadcast now
)

func classify(topic wsmsg.Topic) coalesceClass {
	switch topic {
	case wsmsg.TopicQuote, wsmsg.TopicBook, wsmsg.TopicBars:
		return classMDKeep
	case wsmsg.TopicTape:
		return classTape
	case wsmsg.TopicExecAccount:
		return classAccount
	case wsmsg.TopicExecPositions:
		return classPositions
	default:
		return classImmediate // indicator, orders, fills, status, scanner.*, stock.detail, news, sys.*, trades
	}
}

// outboundCoalesceKey decides, per staged frame, whether a *specific slow
// client* may shed this frame by superseding an older queued value of the same
// key ("" => never; non-empty => coalesce under that key). It is the per-conn
// outbound counterpart to classify()/dedupOf, and the two axes are orthogonal:
// classify controls *when the Hub broadcasts* (ingest-side batching, shared by
// all clients); outboundCoalesceKey controls *what a single client's outbox
// does if that one client can't keep up*. A topic can therefore be
// classImmediate for ingest (broadcast the instant it changes) yet still be
// coalesceable outbound (scanner.rank, sys.health below) -- an immediate
// broadcast and a slow client's shedding are independent concerns.
func outboundCoalesceKey(s staged, snap bool) string {
	// Every snapshot, of every topic, is lossless -- it seeds a topic's
	// client-side store, so superseding or reordering it behind a delta would
	// leave the client applying deltas onto a missing/stale base.
	if snap {
		return ""
	}
	if s.Batch {
		return "" // bars batch-prepend is lossless/ordered; never shed
	}
	switch s.Topic {
	// Latest-wins market data: a slow client only needs the newest value per
	// symbol (quote/book), per (symbol,timeframe,bucket) bar, or per venue
	// (account). Reuse dedupOf -- the same key granularity the ingest side
	// keep-latest coalesces on -- namespaced with a "d|" prefix so an outbound
	// key can never collide with anything else.
	case wsmsg.TopicQuote, wsmsg.TopicBook, wsmsg.TopicBars, wsmsg.TopicExecAccount:
		return "d|" + dedupOf(s)
	// Positions is a single full-replace slot (the whole position table each
	// time), so one fixed key coalesces it.
	case wsmsg.TopicExecPositions:
		return "d|exec.positions"
	// scanner.rank is a full ranked-table replace per scan session; a slow
	// client only needs the newest ranking, so coalesce per session key. (See
	// the orthogonality note above: classify puts this in classImmediate, but
	// that only governs broadcast timing, not per-client shedding.)
	case wsmsg.TopicScannerRank:
		return "d|scanner.rank|" + s.Key
	// stock.detail is a latest-value-per-symbol replace (fundamentals refresh
	// periodically); a slow client only needs the newest value per symbol, so
	// coalesce per symbol key. (Same orthogonality note as scanner.rank.)
	case wsmsg.TopicStockDetail:
		return "d|stock.detail|" + s.Key
	// sys.health is a single full-replace snapshot of every link's latency; one
	// fixed key coalesces it. (Same orthogonality note as scanner.rank.)
	case wsmsg.TopicSysHealth:
		return "d|sys.health"
	default:
		// Lossless/ordered event lane: md.tape, exec.orders/fills/status/trades,
		// sys.events, news.item, scanner.hit, config, md.indicator. These are
		// never dropped by load-shedding -- only a hard-cap overflow drops them
		// (and the whole connection with them).
		return ""
	}
}
