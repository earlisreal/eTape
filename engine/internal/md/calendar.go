package md

import (
	"github.com/earlisreal/eTape/engine/internal/instrument"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func marketBucket(symbol string, tsMs int64, tf session.Timeframe) int64 {
	if instrument.IsPerpetual(symbol) {
		return session.ContinuousBucketStartMs(tsMs, tf)
	}
	return session.BucketStartMs(tsMs, tf)
}
