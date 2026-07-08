// Package session is the pure ET (America/New_York) trading calendar: session
// phases (pre 04:00–09:30, RTH 09:30–16:00, post 16:00–20:00) and
// session-anchored bar bucketing. It is DST-correct by construction and must
// stay in exact agreement with the UI's test-mirror
// (ui/src/render/chart/barBucket.ts) — both use wall-clock-seconds arithmetic
// from ET midnight, which is self-consistent for market hours (no US session
// straddles the 02:00 DST transition).
package session

import (
	"time"
	_ "time/tzdata" // embed IANA zones: correctness must not depend on host zoneinfo
)

// Timeframe is a chart timeframe key. Values match the UI contract exactly.
type Timeframe string

const (
	TF10s   Timeframe = "10s"
	TF1m    Timeframe = "1m"
	TF5m    Timeframe = "5m"
	TF15m   Timeframe = "15m"
	TF30m   Timeframe = "30m"
	TF60m   Timeframe = "60m"
	TFDay   Timeframe = "D"
	TFWeek  Timeframe = "W"
	TFMonth Timeframe = "M"
)

// Intraday lists the intraday timeframes in ascending span order.
var Intraday = []Timeframe{TF10s, TF1m, TF5m, TF15m, TF30m, TF60m}

// IntradaySpanSecs returns the bucket span for intraday timeframes and
// ok=false for calendar timeframes (D/W/M).
func IntradaySpanSecs(tf Timeframe) (int64, bool) {
	switch tf {
	case TF10s:
		return 10, true
	case TF1m:
		return 60, true
	case TF5m:
		return 5 * 60, true
	case TF15m:
		return 15 * 60, true
	case TF30m:
		return 30 * 60, true
	case TF60m:
		return 60 * 60, true
	}
	return 0, false
}

// AnchorSecsDefault is the default intraday bucket anchor: 09:30 ET.
const AnchorSecsDefault int64 = 9*3600 + 30*60

var loc = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic("session: America/New_York missing from embedded tzdata: " + err.Error())
	}
	return l
}()

// Loc returns the America/New_York location.
func Loc() *time.Location { return loc }

// Phase is a point-in-time session classification.
type Phase int

const (
	Closed Phase = iota
	PreMarket
	RTH
	PostMarket
	Overnight
)

func (p Phase) String() string {
	switch p {
	case PreMarket:
		return "pre"
	case RTH:
		return "rth"
	case PostMarket:
		return "post"
	case Overnight:
		return "overnight"
	}
	return "closed"
}

// PhaseAt classifies t. Weekends are Closed; US market holidays are NOT
// modeled in v1 (a holiday reads as a normal weekday with no data).
func PhaseAt(t time.Time) Phase {
	et := t.In(loc)
	if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return Closed
	}
	s := wallSecs(et)
	switch {
	case s >= 4*3600 && s < AnchorSecsDefault:
		return PreMarket
	case s >= AnchorSecsDefault && s < 16*3600:
		return RTH
	case s >= 16*3600 && s < 20*3600:
		return PostMarket
	case s >= 20*3600 || s < 4*3600:
		return Overnight
	}
	return Closed
}

func wallSecs(et time.Time) int64 {
	return int64(et.Hour())*3600 + int64(et.Minute())*60 + int64(et.Second())
}

// dayMidnightMs mirrors the UI's etMidnightMs: subtract the ET wall-clock
// seconds from ts. Self-consistent within a trading day even across DST
// regimes (documented caveat shared with the UI mirror).
func dayMidnightMs(tsMs int64) int64 {
	et := time.UnixMilli(tsMs).In(loc)
	return tsMs - wallSecs(et)*1000 - int64(et.Nanosecond()/1e6)
}

// DayMs returns the D bucket (ET wall-midnight) containing tsMs.
func DayMs(tsMs int64) int64 { return dayMidnightMs(tsMs) }

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// BucketStartMs buckets tsMs into tf with the default 09:30 ET anchor.
func BucketStartMs(tsMs int64, tf Timeframe) int64 {
	return BucketStartMsAnchored(tsMs, tf, AnchorSecsDefault)
}

// BucketStartMsAnchored buckets tsMs into tf. 10s/1m align to the minute grid;
// 5m–60m floor against anchorSecs (pre-market offsets are negative — floored
// division, matching the UI mirror); D/W/M are calendar buckets.
func BucketStartMsAnchored(tsMs int64, tf Timeframe, anchorSecs int64) int64 {
	midnight := dayMidnightMs(tsMs)
	secsIntoDay := (tsMs - midnight) / 1000
	floorTo := func(span, anchor int64) int64 {
		rel := secsIntoDay - anchor
		return midnight + (anchor+floorDiv(rel, span)*span)*1000
	}
	switch tf {
	case TF10s:
		return floorTo(10, 0)
	case TF1m:
		return floorTo(60, 0)
	case TF5m, TF15m, TF30m, TF60m:
		span, _ := IntradaySpanSecs(tf)
		return floorTo(span, anchorSecs)
	case TFDay:
		return midnight
	case TFWeek:
		et := time.UnixMilli(tsMs).In(loc)
		daysFromMonday := (int64(et.Weekday()) + 6) % 7
		return midnight - daysFromMonday*86_400_000
	case TFMonth:
		et := time.UnixMilli(tsMs).In(loc)
		return midnight - int64(et.Day()-1)*86_400_000
	}
	return midnight
}
