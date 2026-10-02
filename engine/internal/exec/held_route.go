package exec

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type HeldRoute string

const (
	RouteNative      HeldRoute = "NATIVE"
	RouteEngineHeld  HeldRoute = "ENGINE_HELD"
	RouteUnsupported HeldRoute = "UNSUPPORTED"
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

// ResolveDeferredStopSellRoute keeps percentage-sized exits in engine custody
// until their trigger supplies a concrete share quantity.
func ResolveDeferredStopSellRoute(now time.Time, tif TIF, requested OrderSession) (HeldRoute, OrderSession, time.Time, string) {
	if tif != TIFDay {
		return RouteUnsupported, requested, time.Time{}, "deferred position sizing requires a DAY order"
	}
	phase := session.PhaseAt(now)
	effective := requested
	if effective == SessionAuto {
		switch phase {
		case session.PreMarket, session.PostMarket:
			effective = SessionExtended
		case session.RTH:
			effective = SessionRTH
		}
	}
	switch phase {
	case session.PreMarket, session.PostMarket:
		if effective == SessionRTH {
			return RouteUnsupported, effective, time.Time{}, "deferred RTH orders can only be placed during RTH"
		}
	}
	schedule := session.Schedule(now)
	switch phase {
	case session.PreMarket:
		if effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.Open, ""
		}
	case session.RTH:
		if effective == SessionRTH || effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.Close, ""
		}
	case session.PostMarket:
		if effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.DataClose, ""
		}
	}
	return RouteUnsupported, effective, time.Time{}, "deferred stop-sell is unavailable outside market sessions"
}

func ResolveLimitIfTouchedRoute(now time.Time, tif TIF, requested OrderSession) (HeldRoute, OrderSession, time.Time, string) {
	if tif != TIFDay {
		return RouteUnsupported, requested, time.Time{}, "limit-if-touched order requires a DAY order"
	}
	phase := session.PhaseAt(now)
	effective := requested
	if effective == SessionAuto {
		switch phase {
		case session.PreMarket, session.PostMarket:
			effective = SessionExtended
		case session.RTH:
			effective = SessionRTH
		}
	}
	if (phase == session.PreMarket || phase == session.PostMarket) && effective == SessionRTH {
		return RouteUnsupported, effective, time.Time{}, "limit-if-touched order RTH orders can only be placed during RTH"
	}
	schedule := session.Schedule(now)
	switch phase {
	case session.PreMarket:
		if effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.Open, ""
		}
	case session.RTH:
		if effective == SessionRTH || effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.Close, ""
		}
	case session.PostMarket:
		if effective == SessionExtended {
			return RouteEngineHeld, effective, schedule.DataClose, ""
		}
	}
	return RouteUnsupported, effective, time.Time{}, "limit-if-touched order is unavailable outside market sessions"
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

func HeldOrderTriggered(typ OrderType, side Side, price, trigger float64) bool {
	if typ == TypeLimitIfTouched {
		switch side {
		case SideBuy, SideCover:
			return price <= trigger
		case SideSell, SideShort:
			return price >= trigger
		default:
			return false
		}
	}
	return typ == TypeStopLimit && StopLimitTriggered(side, price, trigger)
}
