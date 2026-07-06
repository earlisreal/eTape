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
		return classImmediate // indicator, orders, fills, status, scanner.*, news, sys.*
	}
}
