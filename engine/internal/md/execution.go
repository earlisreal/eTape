package md

import (
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type ExecutionBook struct {
	ReceiptMs        int64
	BidTsMs, AskTsMs *int64
	Coverage         uint64
	Symbol           string
	Bid, Ask         float64
	Generation       uint64
}
type ExecutionCandle struct {
	Symbol, Timeframe string
	StartMs, EndMs    int64
	Low, High         float64
	Verified          bool
}
type ExecutionEvent struct {
	Print  *EligiblePrint
	Book   *ExecutionBook
	Candle *ExecutionCandle
}
type executionRange struct {
	start     int64
	low, high float64
}
type executionSymbol struct {
	minuteCompletedThrough int64
	minuteCoverage         feed.Coverage
	coverage               feed.Coverage
	since                  int64
	ten                    executionRange
	minutes                map[int64]feed.Bar
	sent                   map[string]int64
}
type executionState struct {
	generation                   uint64
	sourceConnection, sourceLoss uint64
	symbols                      map[string]*executionSymbol
}

func (c *Core) executionSource(ev feed.Event, at time.Time) {
	if c.execution == nil {
		return
	}
	r := feed.EventSource(ev)
	if r.Connection == 0 {
		return
	}
	if r.Connection != c.executionState.sourceConnection || r.Loss != c.executionState.sourceLoss {
		c.executionGap(at)
		c.executionState.sourceConnection, c.executionState.sourceLoss = r.Connection, r.Loss
	}
}

func (c *Core) executionEmit(e ExecutionEvent) {
	if c.execution == nil {
		return
	}
	select {
	case c.execution <- e:
	case <-c.executionCtx.Done():
	}
}
func (c *Core) executionGap(at time.Time) {
	if c.execution == nil {
		return
	}
	c.executionState.generation++
	c.executionState.symbols = make(map[string]*executionSymbol)
	c.executionEmit(ExecutionEvent{Print: &EligiblePrint{Gap: true, RecvTsMs: at.UnixMilli()}})
}
func (c *Core) executionSymbol(symbol string, at int64) *executionSymbol {
	if c.executionState.symbols == nil {
		c.executionState.symbols = make(map[string]*executionSymbol)
	}
	s := c.executionState.symbols[symbol]
	if s == nil {
		s = &executionSymbol{since: at, minutes: make(map[int64]feed.Bar), sent: make(map[string]int64)}
		c.executionState.symbols[symbol] = s
	}
	return s
}
func (c *Core) executionBook(e feed.BookEvent) {
	if c.execution == nil || !e.ExecutionVerified {
		return
	}
	if c.executionState.generation == 0 {
		c.executionState.generation = 1
	}
	b := ExecutionBook{Symbol: e.Book.Symbol, Generation: c.executionState.generation, Coverage: e.ExecutionRevision, ReceiptMs: e.Book.Source.ReceiptMs, BidTsMs: e.Book.BidTsMs, AskTsMs: e.Book.AskTsMs}
	if len(e.Book.Bids) > 0 {
		b.Bid = e.Book.Bids[0].Price
	}
	if len(e.Book.Asks) > 0 {
		b.Ask = e.Book.Asks[0].Price
	}
	c.executionEmit(ExecutionEvent{Book: &b})
}
func (c *Core) executionMinutes(e feed.Bars1mEvent) {
	if c.execution == nil || e.Seed || !e.Execution.Verified {
		return
	}
	for _, b := range e.Bars {
		s := c.executionState.symbols[b.Symbol]
		if s != nil && s.minuteCoverage.Verified && s.minuteCoverage.Revision != e.Execution.Revision {
			c.executionGap(c.currentTime())
			continue
		}
		if s != nil {
			s.minuteCoverage = e.Execution
		}
		if s == nil || b.BucketMs < s.since || b.BucketMs < e.Execution.SinceMs || b.Volume <= 0 || !executionPrice(b.L) || !executionPrice(b.H) || b.L > b.H {
			continue
		}
		s.minutes[b.BucketMs] = b
		if b.BucketMs > s.minuteCompletedThrough {
			s.minuteCompletedThrough = b.BucketMs
		}
	}
}
func executionPrice(p float64) bool { return p > 0 && !math.IsNaN(p) && !math.IsInf(p, 0) }
func (c *Core) executionTick(t feed.Tick, coverage feed.Coverage) {
	if c.execution == nil || t.Delivery != feed.DeliveryRealtime || t.Seq <= 0 || !executionPrice(t.Price) {
		return
	}
	s := c.executionSymbol(t.Symbol, t.TsMs)
	if s.coverage.Revision != coverage.Revision {
		if s.coverage.Verified {
			c.executionGap(c.currentTime())
			s = c.executionSymbol(t.Symbol, t.TsMs)
		}
		s.coverage = coverage
		if coverage.SinceMs > s.since {
			s.since = coverage.SinceMs
		}
	}
	// Only live, continuously observed real ranges can become execution candles.
	start := session.BucketStartMs(t.TsMs, session.Timeframe("10s"))
	if s.ten.start != 0 && start > s.ten.start {
		if s.ten.start >= s.since && s.ten.low > 0 {
			c.executionEmit(ExecutionEvent{Candle: &ExecutionCandle{Symbol: t.Symbol, Timeframe: "10s", StartMs: s.ten.start, EndMs: s.ten.start + 10000, Low: s.ten.low, High: s.ten.high, Verified: true}})
		}
		s.ten = executionRange{}
	}
	if s.ten.start == 0 {
		s.ten.start = start
	}
	if t.RangeEligible && start == s.ten.start {
		if s.ten.low == 0 || t.Price < s.ten.low {
			s.ten.low = t.Price
		}
		s.ten.high = math.Max(s.ten.high, t.Price)
	}
	for _, tf := range []string{"1m", "5m", "15m", "30m", "60m"} {
		span, _ := session.IntradaySpanSecs(session.Timeframe(tf))
		end := session.BucketStartMs(t.TsMs, session.Timeframe(tf))
		begin := end - span*1000
		if begin < s.since || begin <= s.sent[tf] || end > s.minuteCompletedThrough {
			continue
		}
		low, high := 0.0, 0.0
		complete := true
		for at := begin; at < end; at += 60000 {
			b, ok := s.minutes[at]
			if !ok {
				complete = false
				break
			}
			if low == 0 || b.L < low {
				low = b.L
			}
			high = math.Max(high, b.H)
		}
		if complete {
			s.sent[tf] = begin
			c.executionEmit(ExecutionEvent{Candle: &ExecutionCandle{Symbol: t.Symbol, Timeframe: tf, StartMs: begin, EndMs: end, Low: low, High: high, Verified: true}})
		}
	}
	for at := range s.minutes {
		if at < t.TsMs-2*60*60*1000 {
			delete(s.minutes, at)
		}
	}
	if t.LastEligible {
		c.executionEmit(ExecutionEvent{Print: &EligiblePrint{Symbol: t.Symbol, Price: t.Price, TsMs: t.TsMs, RecvTsMs: t.RecvTsMs, ReceiptMs: t.SourceRef().ReceiptMs, Seq: t.Seq}})
	}
}
