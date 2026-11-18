package session

import "time"

// ContinuousBucketStartMs aligns a 24/7 market to UTC, including weekends.
func ContinuousBucketStartMs(tsMs int64, tf Timeframe) int64 {
	if span, ok := IntradaySpanSecs(tf); ok {
		return time.UnixMilli(tsMs).Truncate(time.Duration(span) * time.Second).UnixMilli()
	}
	t := time.UnixMilli(tsMs).UTC()
	year, month, day := t.Date()
	switch tf {
	case TFWeek:
		day -= (int(t.Weekday()) + 6) % 7
	case TFMonth:
		day = 1
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).UnixMilli()
}
