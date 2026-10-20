package exec

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type TrailCandle struct {
	afterSeq  int64
	Symbol    string  `json:"symbol"`
	Timeframe string  `json:"timeframe"`
	StartMs   int64   `json:"startMs"`
	EndMs     int64   `json:"endMs"`
	Low       float64 `json:"low"`
	High      float64 `json:"high"`
	Verified  bool    `json:"verified"`
}

func (c *Core) FeedTrailCandle(ctx context.Context, candle TrailCandle) {
	done := make(chan struct{})
	select {
	case c.printCh <- executionInput{Candle: &candle, Done: done}:
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (c *Core) handleTrailCandle(b TrailCandle) {
	span, ok := session.IntradaySpanSecs(session.Timeframe(b.Timeframe))
	if !ok || !b.Verified || !finitePositive(b.Low) || !finitePositive(b.High) || b.Low > b.High ||
		b.EndMs != b.StartMs+span*1000 || b.EndMs > c.now() || b.StartMs != session.BucketStartMs(b.StartMs, session.Timeframe(b.Timeframe)) {
		return
	}
	if c.trailCandles == nil {
		c.trailCandles = make(map[string]TrailCandle)
	}
	key := b.Symbol + "/" + b.Timeframe
	b.afterSeq = c.lastEligible[b.Symbol].Seq
	if old, exists := c.trailCandles[key]; !exists || b.StartMs >= old.StartMs {
		c.trailCandles[key] = b
	}
}

func (s TrailSessions) windowStart(at time.Time) int64 {
	sched := session.Schedule(at)
	date := at.In(session.Loc())
	pre := time.Date(date.Year(), date.Month(), date.Day(), 4, 0, 0, 0, session.Loc()).UnixMilli()
	switch session.PhaseAt(at) {
	case session.PreMarket:
		if s.Pre {
			return pre
		}
	case session.RTH:
		if s.RTH {
			if s.Pre {
				return pre
			}
			return sched.Open.UnixMilli()
		}
	case session.PostMarket:
		if s.Post {
			if !s.RTH {
				return sched.Close.UnixMilli()
			}
			if s.Pre {
				return pre
			}
			return sched.Open.UnixMilli()
		}
	}
	return 0
}

func (c *Core) candleTrailCandidate(symbol string, side Side, t *TrailState) float64 {
	window := t.Config.Sessions.windowStart(c.clk.Now())
	if window == 0 {
		return t.Stop
	}
	b, ok := c.trailCandles[symbol+"/"+t.Config.Timeframe]
	used, hasUsed := t.References[t.Config.Timeframe]
	if !hasUsed && t.Reference != nil && t.Reference.Timeframe == t.Config.Timeframe {
		used, hasUsed = *t.Reference, true
	}
	if ok && b.StartMs >= window && b.EndMs <= c.now() && (!hasUsed || b.StartMs > used.StartMs) {
		used, hasUsed = b, true
		references := make(map[string]TrailCandle, len(t.References)+1)
		for tf, reference := range t.References {
			references[tf] = reference
		}
		references[t.Config.Timeframe] = used
		t.References = references
	}
	t.Reference = nil
	if hasUsed {
		reference := used
		t.Reference = &reference
	}
	if t.Reference == nil || t.Reference.StartMs < window {
		return t.Stop
	}
	base := t.Reference.Low
	if side == SideCover {
		base = t.Reference.High
	}
	distance := t.Config.Buffer.Value
	if t.Config.Buffer.Unit == "%" {
		distance = base * distance / 100
	}
	if side == SideCover {
		return base + distance
	}
	return base - distance
}
