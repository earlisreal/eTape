package md

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type lastEligibilityMode uint8

const (
	lastNever lastEligibilityMode = iota
	lastAlways
	lastFirstBusinessDay
	lastCutoff
)

type conditionPolicy struct {
	rangeEligible  bool
	lastMode       lastEligibilityMode
	volumeEligible bool
	extendedOnly   bool
}

func conditionPolicyFor(c feed.TradeReportCondition) conditionPolicy {
	switch c {
	case feed.TradeConditionAutomaticMatch,
		feed.TradeConditionIntermarketSweep,
		feed.TradeConditionAuction,
		feed.TradeConditionBunchedTrade,
		feed.TradeConditionRule127Or155,
		feed.TradeConditionMarketCenterOpening,
		feed.TradeConditionReopeningPrice,
		feed.TradeConditionClosingPrice:
		return conditionPolicy{rangeEligible: true, lastMode: lastAlways, volumeEligible: true}
	case feed.TradeConditionOddLot, feed.TradeConditionOddLotIntermarketSweep,
		feed.TradeConditionCashSale, feed.TradeConditionPriceVariation,
		feed.TradeConditionNextDaySettlement, feed.TradeConditionSeller,
		feed.TradeConditionContingent, feed.TradeConditionAveragePrice:
		return conditionPolicy{volumeEligible: true}
	case feed.TradeConditionBunchedSold, feed.TradeConditionPriorReferencePrice,
		feed.TradeConditionOTCSold, feed.TradeConditionDerivativelyPriced:
		return conditionPolicy{rangeEligible: true, lastMode: lastFirstBusinessDay, volumeEligible: true}
	case feed.TradeConditionDelayed:
		return conditionPolicy{rangeEligible: true, lastMode: lastCutoff, volumeEligible: true}
	case feed.TradeConditionMarketCenterOfficialClose, feed.TradeConditionMarketCenterOfficialOpen,
		feed.TradeConditionNonAutomaticMatch, feed.TradeConditionSameBrokerAutomaticMatch,
		feed.TradeConditionSameBrokerNonAutomaticMatch, feed.TradeConditionOverseas,
		feed.TradeConditionUnknown:
		return conditionPolicy{}
	case feed.TradeConditionCorrectedComprehensiveLatePrice:
		return conditionPolicy{rangeEligible: true, lastMode: lastAlways}
	case feed.TradeConditionLate, feed.TradeConditionFormT, feed.TradeConditionExtendedHours:
		return conditionPolicy{rangeEligible: true, lastMode: lastAlways, volumeEligible: true, extendedOnly: true}
	default:
		return conditionPolicy{}
	}
}

// eligibilityState is intentionally bounded to one symbol/day. The core is
// the only writer, so replay and live delivery use the same state transition.
type eligibilityState struct {
	day           int64
	lastSeenToday bool
}

func (s *eligibilityState) stamp(t feed.Tick) feed.Tick {
	day := session.DayMs(t.TsMs)
	if day != s.day {
		s.day = day
		s.lastSeenToday = false
	}

	p := conditionPolicyFor(t.Condition)
	extended := isExtendedSession(t.TsMs)
	priceAllowed := !p.extendedOnly || extended
	t.RangeEligible = p.rangeEligible && priceAllowed && t.Price > 0
	t.VolumeEligible = p.volumeEligible
	switch p.lastMode {
	case lastAlways:
		t.LastEligible = priceAllowed && t.Price > 0
	case lastFirstBusinessDay:
		t.LastEligible = !s.lastSeenToday && session.IsTradingDay(time.UnixMilli(t.TsMs)) && t.Price > 0
	case lastCutoff:
		t.LastEligible = t.Price > 0 && beforeLastSaleCutoff(t.TsMs)
	default:
		t.LastEligible = false
	}
	if t.LastEligible {
		s.lastSeenToday = true
	}
	return t
}

func isExtendedSession(tsMs int64) bool {
	switch session.PhaseAt(time.UnixMilli(tsMs)) {
	case session.PreMarket, session.PostMarket, session.Overnight:
		return true
	default:
		return false
	}
}

// UTP's last-sale control message follows the scheduled consolidated close;
// the repository calendar supplies the early-close-aware 90-second control
// window. Reports after it may remain visible and volume eligible, but cannot
// move consolidated last.
func beforeLastSaleCutoff(tsMs int64) bool {
	et := time.UnixMilli(tsMs).In(session.Loc())
	s := session.Schedule(et)
	return s.TradingDay && !et.After(s.Close.Add(90*time.Second))
}
