package main

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type marketCatalogFixture struct {
	perpetualAPI
	exchange string
}

func (f marketCatalogFixture) PerpetualMarkets(context.Context) ([]instrument.PerpetualMarket, error) {
	return []instrument.PerpetualMarket{{ID: instrument.Perpetual{Exchange: f.exchange, Native: "BTCUSDT"}}}, nil
}

func TestPerpetualMarketCatalogScopesExchange(t *testing.T) {
	router := perpetualFeedRouter{sources: []perpetualSource{
		{exchange: "BINANCE", api: marketCatalogFixture{exchange: "BINANCE"}},
		{exchange: instrument.BinanceDemo, api: marketCatalogFixture{exchange: instrument.BinanceDemo}},
	}}
	markets, err := router.PerpetualMarkets(t.Context(), instrument.BinanceDemo)
	if err != nil || len(markets) != 1 || markets[0].ID.String() != "PERP.BINANCE-DEMO.BTCUSDT" {
		t.Fatalf("demo catalog: %+v, %v", markets, err)
	}
	markets, err = router.PerpetualMarkets(t.Context(), "")
	if err != nil || len(markets) != 2 {
		t.Fatalf("unscoped catalog: %+v, %v", markets, err)
	}
	if _, err := router.PerpetualMarkets(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown exchange did not fail")
	}
}
