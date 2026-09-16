package scan

import (
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

type snapshotValues struct {
	price     float64
	close     float64
	volume    int64
	hasClose  bool
	hasVolume bool
	change    float64
	hasChange bool
	usable    bool
}

func normalizeSnapshot(basic *snappb.SnapshotBasicData, phase session.Phase, now time.Time) snapshotValues {
	return normalizeSnapshotWithClose(basic, phase, now, nil)
}

func normalizeSnapshotWithClose(basic *snappb.SnapshotBasicData, phase session.Phase, now time.Time, closeOverride *float64) snapshotValues {
	if basic == nil {
		return snapshotValues{}
	}
	if phase == session.Closed {
		return snapshotValues{}
	}
	ts := snapshotObservationTime(basic)
	if !ts.IsZero() && ts.Before(session.TradingCycleStart(now)) {
		return snapshotValues{}
	}
	if phase != session.RTH && ts.IsZero() {
		// Extended blocks have no per-block timestamp. Without the snapshot's
		// overall update time, a positive price cannot prove current-session
		// provenance.
		return snapshotValues{}
	}
	if phase == session.RTH {
		if ts := snapshotObservationTime(basic); !ts.IsZero() {
			schedule := session.Schedule(now)
			if schedule.TradingDay && ts.Before(schedule.Open) {
				return snapshotValues{}
			}
		}
	}
	var out snapshotValues
	if phase == session.RTH {
		if value := basic.LastClosePrice; value != nil && finitePositive(*value) {
			out.close, out.hasClose = *value, true
		}
	} else if closeOverride != nil && finitePositive(*closeOverride) {
		out.close, out.hasClose = *closeOverride, true
	}
	if phase == session.RTH {
		if value := basic.CurPrice; value != nil && finitePositive(*value) {
			out.price, out.usable = *value, true
		}
		if basic.Volume != nil && *basic.Volume >= 0 {
			out.volume, out.hasVolume = *basic.Volume, true
		}
		if out.usable && out.hasClose {
			out.change, out.hasChange = (out.price/out.close-1)*100, true
		}
		return out
	}
	var extended *qotcommon.PreAfterMarketData
	switch phase {
	case session.PostMarket:
		extended = basic.AfterMarket
	case session.Overnight:
		extended = basic.Overnight
	default:
		extended = basic.PreMarket
	}
	if extended == nil || extended.Price == nil || !finitePositive(*extended.Price) {
		return out
	}
	out.price, out.usable = *extended.Price, true
	if extended.Volume != nil && *extended.Volume >= 0 {
		out.volume, out.hasVolume = *extended.Volume, true
	}
	if extended.ChangeRate != nil && finite(*extended.ChangeRate) {
		out.change, out.hasChange = *extended.ChangeRate, true
	}
	return out
}

func finite(value float64) bool         { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePositive(value float64) bool { return value > 0 && finite(value) }

func closePricePtr(value float64) *float64 {
	if !finitePositive(value) {
		return nil
	}
	return &value
}
