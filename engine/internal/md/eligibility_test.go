package md

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func conditionTick(seq int64, offMs int64, price float64, volume int64, condition feed.TradeReportCondition, dir feed.Direction) feed.Tick {
	return feed.Tick{
		Symbol: "US.AAPL", Seq: seq, TsMs: t0Ms + offMs, Price: price, Volume: volume, Dir: dir,
		Type: feed.TransactionExcluded, Condition: condition, Delivery: feed.DeliveryRealtime,
	}
}

func tapeFrom(updates []Update) []feed.Tick {
	for _, update := range updates {
		if tape, ok := update.(TapeUpdate); ok {
			return tape.Ticks
		}
	}
	return nil
}

func tenSecondFinal(updates []Update) (Bar, bool) {
	for _, update := range updates {
		if bar, ok := update.(BarUpdate); ok && bar.Bar.TF == session.TF10s && !bar.Bar.InProgress {
			return bar.Bar, true
		}
	}
	return Bar{}, false
}

func TestTradeConditionPolicyTable(t *testing.T) {
	always := conditionPolicy{rangeEligible: true, lastMode: lastAlways, volumeEligible: true}
	volumeOnly := conditionPolicy{volumeEligible: true}
	conditional := conditionPolicy{rangeEligible: true, lastMode: lastFirstBusinessDay, volumeEligible: true}
	cutoff := conditionPolicy{rangeEligible: true, lastMode: lastCutoff, volumeEligible: true}
	extended := conditionPolicy{rangeEligible: true, lastMode: lastAlways, volumeEligible: true, extendedOnly: true}
	cases := []struct {
		name string
		got  conditionPolicy
		want conditionPolicy
	}{
		{"automatic", conditionPolicyFor(feed.TradeConditionAutomaticMatch), always},
		{"sweep", conditionPolicyFor(feed.TradeConditionIntermarketSweep), always},
		{"auction", conditionPolicyFor(feed.TradeConditionAuction), always},
		{"bunched", conditionPolicyFor(feed.TradeConditionBunchedTrade), always},
		{"rule", conditionPolicyFor(feed.TradeConditionRule127Or155), always},
		{"opening", conditionPolicyFor(feed.TradeConditionMarketCenterOpening), always},
		{"reopening", conditionPolicyFor(feed.TradeConditionReopeningPrice), always},
		{"closing", conditionPolicyFor(feed.TradeConditionClosingPrice), always},
		{"odd lot", conditionPolicyFor(feed.TradeConditionOddLot), volumeOnly},
		{"odd lot sweep", conditionPolicyFor(feed.TradeConditionOddLotIntermarketSweep), volumeOnly},
		{"cash", conditionPolicyFor(feed.TradeConditionCashSale), volumeOnly},
		{"price variation", conditionPolicyFor(feed.TradeConditionPriceVariation), volumeOnly},
		{"next day", conditionPolicyFor(feed.TradeConditionNextDaySettlement), volumeOnly},
		{"seller", conditionPolicyFor(feed.TradeConditionSeller), volumeOnly},
		{"contingent", conditionPolicyFor(feed.TradeConditionContingent), volumeOnly},
		{"average price", conditionPolicyFor(feed.TradeConditionAveragePrice), volumeOnly},
		{"bunched sold", conditionPolicyFor(feed.TradeConditionBunchedSold), conditional},
		{"prior reference", conditionPolicyFor(feed.TradeConditionPriorReferencePrice), conditional},
		{"otc sold", conditionPolicyFor(feed.TradeConditionOTCSold), conditional},
		{"derivatively priced", conditionPolicyFor(feed.TradeConditionDerivativelyPriced), conditional},
		{"delayed", conditionPolicyFor(feed.TradeConditionDelayed), cutoff},
		{"official close", conditionPolicyFor(feed.TradeConditionMarketCenterOfficialClose), conditionPolicy{}},
		{"official open", conditionPolicyFor(feed.TradeConditionMarketCenterOfficialOpen), conditionPolicy{}},
		{"corrected late", conditionPolicyFor(feed.TradeConditionCorrectedComprehensiveLatePrice), conditionPolicy{rangeEligible: true, lastMode: lastAlways}},
		{"extended", conditionPolicyFor(feed.TradeConditionExtendedHours), extended},
		{"late", conditionPolicyFor(feed.TradeConditionLate), extended},
		{"form T", conditionPolicyFor(feed.TradeConditionFormT), extended},
		{"non automatic", conditionPolicyFor(feed.TradeConditionNonAutomaticMatch), conditionPolicy{}},
		{"same broker automatic", conditionPolicyFor(feed.TradeConditionSameBrokerAutomaticMatch), conditionPolicy{}},
		{"same broker non automatic", conditionPolicyFor(feed.TradeConditionSameBrokerNonAutomaticMatch), conditionPolicy{}},
		{"overseas", conditionPolicyFor(feed.TradeConditionOverseas), conditionPolicy{}},
		{"unknown", conditionPolicyFor(feed.TradeConditionUnknown), conditionPolicy{}},
		{"unrecognized", conditionPolicyFor(feed.TradeReportCondition(99)), conditionPolicy{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("policy = %+v, want %+v", tc.got, tc.want)
			}
		})
	}
}

func TestOddLotIsVisibleVolumeOnlyAndCannotMovePriceOrMark(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 222.95}})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, 1_000, 222.94, 100, feed.TradeConditionAutomaticMatch, feed.Buy),
		conditionTick(2, 3_000, 223.123, 10, feed.TradeConditionOddLot, feed.Sell),
		conditionTick(3, 11_000, 222.96, 5, feed.TradeConditionAutomaticMatch, feed.Buy),
	}})
	updates := drain()
	ticks := tapeFrom(updates)
	if len(ticks) != 3 || ticks[1].Condition != feed.TradeConditionOddLot || ticks[1].RangeEligible || ticks[1].LastEligible || !ticks[1].VolumeEligible {
		t.Fatalf("stamped tape = %+v", ticks)
	}
	bar, ok := tenSecondFinal(updates)
	if !ok || bar.O != 222.94 || bar.H != 222.94 || bar.L != 222.94 || bar.C != 222.94 || bar.V != 110 || bar.Ticks != 2 {
		t.Fatalf("final bar = %+v, want odd lot volume without price effect", bar)
	}
	select {
	case mark := <-c.Marks():
		if mark.Price != 222.96 {
			t.Fatalf("mark = %+v, want last eligible price", mark)
		}
	default:
		t.Fatal("expected a mark from the last eligible print")
	}
}

func TestUnknownConditionIsVisibleButHasNoStatisticalEffects(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 100}})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, 1_000, 100, 20, feed.TradeConditionAutomaticMatch, feed.Buy),
		conditionTick(2, 2_000, 999, 50, feed.TradeConditionUnknown, feed.Sell),
		conditionTick(3, 11_000, 101, 1, feed.TradeConditionUnknown, feed.Neutral),
	}})
	updates := drain()
	ticks := tapeFrom(updates)
	if len(ticks) != 3 || ticks[1].RangeEligible || ticks[1].LastEligible || ticks[1].VolumeEligible {
		t.Fatalf("unknown stamp = %+v", ticks)
	}
	bar, ok := tenSecondFinal(updates)
	if !ok || bar.H != 100 || bar.L != 100 || bar.C != 100 || bar.V != 20 || bar.Ticks != 1 {
		t.Fatalf("unknown affected bar = %+v", bar)
	}
	select {
	case mark := <-c.Marks():
		if mark.Price != 100 {
			t.Fatalf("unknown moved mark = %+v", mark)
		}
	default:
		t.Fatal("expected the regular mark")
	}
}

func TestVolumeOnlyBarUsesTrustedPriorCloseAndDedupsBeforeEligibility(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 222.95}})
	odd := conditionTick(1, 1_000, 223.123, 10, feed.TradeConditionOddLot, feed.Buy)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{odd, odd, conditionTick(2, 11_000, 223.2, 0, feed.TradeConditionUnknown, feed.Neutral)}})
	updates := drain()
	bar, ok := tenSecondFinal(updates)
	if !ok || !bar.VolumeOnly || bar.O != 222.95 || bar.H != 222.95 || bar.L != 222.95 || bar.C != 222.95 ||
		bar.V != 10 || bar.BuyV != 10 || bar.SellV != 0 || bar.Ticks != 1 {
		t.Fatalf("volume-only bar = %+v", bar)
	}
	if len(tapeFrom(updates)) != 2 {
		t.Fatalf("deduplicated tape = %+v", tapeFrom(updates))
	}
	select {
	case mark := <-c.Marks():
		t.Fatalf("volume-only print moved mark: %+v", mark)
	default:
	}
}

func TestRangeOnlyPrintExtendsRangeWithoutChangingLast(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 100}})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, 1_000, 100, 10, feed.TradeConditionAutomaticMatch, feed.Buy),
		conditionTick(2, 2_000, 105, 4, feed.TradeConditionPriorReferencePrice, feed.Sell),
		conditionTick(3, 11_000, 101, 1, feed.TradeConditionUnknown, feed.Neutral),
	}})
	updates := drain()
	bar, ok := tenSecondFinal(updates)
	if !ok || bar.O != 100 || bar.C != 100 || bar.H != 105 || bar.L != 100 || bar.V != 14 || bar.Ticks != 2 {
		t.Fatalf("range-only bar = %+v", bar)
	}
	for _, tick := range tapeFrom(updates) {
		if tick.Condition == feed.TradeConditionPriorReferencePrice && tick.LastEligible {
			t.Fatalf("prior-reference report unexpectedly moved last: %+v", tick)
		}
	}
}

func TestExtendedSessionPolicyUsesExchangeTimeAndKeepsOddLotsPriceIneligible(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, -2*time.Hour.Milliseconds(), 101, 10, feed.TradeConditionFormT, feed.Buy),
		conditionTick(2, -time.Hour.Milliseconds(), 999, 10, feed.TradeConditionOddLot, feed.Sell),
	}})
	ticks := tapeFrom(drain())
	if len(ticks) != 2 || !ticks[0].RangeEligible || !ticks[0].LastEligible || ticks[1].RangeEligible || ticks[1].LastEligible || !ticks[1].VolumeEligible {
		t.Fatalf("extended eligibility = %+v", ticks)
	}
}

func TestRegularSmallPrintRemainsPriceForming(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, 1_000, 105, 1, feed.TradeConditionAutomaticMatch, feed.Buy),
		conditionTick(2, 11_000, 105, 0, feed.TradeConditionUnknown, feed.Neutral),
	}})
	updates := drain()
	bar, ok := tenSecondFinal(updates)
	if !ok || bar.H != 105 || bar.L != 105 || bar.C != 105 || bar.V != 1 || bar.Ticks != 1 {
		t.Fatalf("small regular print was filtered: %+v", bar)
	}
}

func TestDeliverySourceDoesNotChangeEligibilityOutputs(t *testing.T) {
	for _, source := range []feed.DeliverySource{feed.DeliveryRealtime, feed.DeliveryDisconnectBackfill, feed.DeliveryCache} {
		t.Run(source.String(), func(t *testing.T) {
			c, drain := runCore(t)
			first := conditionTick(1, 1_000, 100, 10, feed.TradeConditionAutomaticMatch, feed.Buy)
			first.Delivery = source
			second := conditionTick(2, 11_000, 101, 0, feed.TradeConditionUnknown, feed.Neutral)
			second.Delivery = source
			c.Feed(feed.TicksEvent{Ticks: []feed.Tick{first, second}})
			bar, ok := tenSecondFinal(drain())
			if !ok || bar.O != 100 || bar.H != 100 || bar.L != 100 || bar.C != 100 || bar.V != 10 || bar.Ticks != 1 {
				t.Fatalf("source %s changed bar eligibility: %+v", source, bar)
			}
		})
	}
}

func TestConditionalLastModesUseBusinessDayAndCutoff(t *testing.T) {
	day := time.Date(2026, 7, 6, 9, 31, 0, 0, session.Loc())
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 1, TsMs: day.UnixMilli(), Price: 100, Volume: 5, Dir: feed.Buy, Condition: feed.TradeConditionPriorReferencePrice},
		{Symbol: "US.AAPL", Seq: 2, TsMs: day.Add(time.Minute).UnixMilli(), Price: 101, Volume: 5, Dir: feed.Buy, Condition: feed.TradeConditionPriorReferencePrice},
	}})
	prior := tapeFrom(drain())
	if len(prior) != 2 || !prior[0].LastEligible || prior[1].LastEligible {
		t.Fatalf("first-of-business-day last eligibility = %+v", prior)
	}

	c2, drain2 := runCore(t)
	beforeClose := time.Date(2026, 7, 6, 15, 59, 0, 0, session.Loc())
	afterClose := time.Date(2026, 7, 6, 16, 2, 0, 0, session.Loc())
	c2.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 1, TsMs: beforeClose.UnixMilli(), Price: 100, Volume: 5, Dir: feed.Buy, Condition: feed.TradeConditionDelayed},
		{Symbol: "US.AAPL", Seq: 2, TsMs: afterClose.UnixMilli(), Price: 101, Volume: 5, Dir: feed.Buy, Condition: feed.TradeConditionDelayed},
	}})
	delayed := tapeFrom(drain2())
	if len(delayed) != 2 || !delayed[0].LastEligible || delayed[1].LastEligible {
		t.Fatalf("cutoff last eligibility = %+v", delayed)
	}
}

func TestConditionalCutoffUsesEarlyCloseAndHolidayCalendar(t *testing.T) {
	close := time.Date(2026, 11, 27, 13, 0, 0, 0, session.Loc())
	if !beforeLastSaleCutoff(close.Add(time.Minute).UnixMilli()) || beforeLastSaleCutoff(close.Add(2*time.Minute).UnixMilli()) {
		t.Fatal("early-close cutoff did not use the calendar close plus control window")
	}
	holiday := time.Date(2026, 11, 26, 12, 0, 0, 0, session.Loc())
	if beforeLastSaleCutoff(holiday.UnixMilli()) {
		t.Fatal("holiday report should not be last-sale eligible")
	}
}

func TestUnanchoredRangeOnlyBucketWaitsForTrustedClose(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	first := feed.Tick{Symbol: "US.AAPL", TsMs: t0Ms + 1_000, Price: 105, Volume: 4, RangeEligible: true, VolumeEligible: true}
	if got := a.addTick(first, false); len(got) != 0 {
		t.Fatalf("unanchored range-only bucket emitted %+v", got)
	}
	second := feed.Tick{Symbol: "US.AAPL", TsMs: t0Ms + 11_000, Price: 106, Volume: 0}
	if got := a.addTick(second, false); len(got) != 0 {
		t.Fatalf("unanchored bucket finalized without anchor: %+v", got)
	}
	a.seedAnchor(100, 0)
	third := feed.Tick{Symbol: "US.AAPL", TsMs: t0Ms + 21_000, Price: 107, Volume: 0}
	got := a.addTick(third, false)
	if len(got) == 0 || got[0].O != 100 || got[0].H != 105 || got[0].L != 100 || got[0].C != 100 {
		t.Fatalf("anchored held bucket = %+v", got)
	}
}

func TestOlderHistoryCannotReplaceCurrentTrustedClose(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{
		Symbol: "US.AAPL", TsMs: t0Ms + 120_000, Last: 200,
	}})
	_ = drain()
	c.SeedHistory1m("US.AAPL", []feed.Bar{{
		Symbol: "US.AAPL", BucketMs: t0Ms - 60_000, O: 99, H: 101, L: 98, C: 100,
	}})
	_ = drain()
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		conditionTick(1, 1_000, 223.123, 10, feed.TradeConditionOddLot, feed.Buy),
		conditionTick(2, 11_000, 223.2, 0, feed.TradeConditionUnknown, feed.Neutral),
	}})
	bar, ok := tenSecondFinal(drain())
	if !ok || !bar.VolumeOnly || bar.O != 200 || bar.H != 200 || bar.L != 200 || bar.C != 200 {
		t.Fatalf("older history replaced current anchor: %+v", bar)
	}
}
