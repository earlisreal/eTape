package exec

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type HeldRoute string

const (
	RouteNative     HeldRoute = "NATIVE"
	RouteEngineHeld HeldRoute = "ENGINE_HELD"
)

// ResolveStopLimitRoute is the authoritative custody decision. AUTO becomes
// EXTENDED in pre/post; only DAY extended-hours orders are held locally.
func ResolveStopLimitRoute(now time.Time, tif TIF, requested OrderSession) (HeldRoute, OrderSession, time.Time) {
	effective := requested
	phase := session.PhaseAt(now)
	if effective == SessionAuto && (phase == session.PreMarket || phase == session.PostMarket) {
		effective = SessionExtended
	}
	if tif != TIFDay || effective != SessionExtended || (phase != session.PreMarket && phase != session.PostMarket) {
		return RouteNative, effective, time.Time{}
	}
	schedule := session.Schedule(now)
	if phase == session.PreMarket {
		return RouteEngineHeld, effective, schedule.Open
	}
	return RouteEngineHeld, effective, schedule.DataClose
}

func StopLimitTriggered(side Side, price, stop float64) bool {
	switch side {
	case SideBuy, SideCover:
		return price >= stop
	case SideSell, SideShort:
		return price <= stop
	default:
		return false
	}
}
