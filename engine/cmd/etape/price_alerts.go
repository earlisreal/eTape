package main

import (
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/pricealert"
)

type engineAlertDemand struct{ feed demandFeeder }

func (d engineAlertDemand) Ensure(symbol string) {
	d.feed.Ensure(feed.Demand{ID: "price-alert/" + symbol, Symbol: symbol, Subs: []feed.SubType{feed.SubTicker}, Background: true, BackgroundSeed: true})
}
func (d engineAlertDemand) Release(symbol string) { d.feed.Release("price-alert/" + symbol) }
func (d engineAlertDemand) Availability(symbol string) pricealert.Availability {
	if f, ok := d.feed.(interface{ TickerAvailability(string) (bool, bool) }); ok {
		active, capacity := f.TickerAvailability(symbol)
		if active {
			return pricealert.Available
		}
		if capacity {
			return pricealert.CapacityUnavailable
		}
		return pricealert.Unavailable
	}
	return pricealert.Available
}
