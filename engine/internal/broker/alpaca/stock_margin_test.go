package alpaca

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestStockMarginRefreshSharesResultAndRetainsValueAfterFailure(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/assets", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"symbol":"AAPL","margin_requirement_long":"30","maintenance_margin_requirement":30}]`)
	})
	mux.HandleFunc("/v2/assets/AAPL", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) > 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"symbol":"AAPL","margin_requirement_long":"50.25","margin_requirement_short":"120"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	clk := clock.NewFake(time.Unix(1, 0))
	a, err := New(Config{Venue: "alpaca-paper", RESTBase: server.URL, Clock: clk, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.LoadActiveAssets(ctx); err != nil {
		t.Fatal(err)
	}
	seed := a.StockMargin(ctx, "US.AAPL", true)
	if seed.Long == nil || *seed.Long != "30" || seed.Short != nil || calls.Load() != 0 {
		t.Fatalf("unexpected seed: %+v, lookups: %d", seed, calls.Load())
	}
	clk.Advance(stockMarginTTL)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			value := a.StockMargin(ctx, "US.AAPL", true)
			if value.Long == nil || *value.Long != "50.25" || value.Short == nil || *value.Short != "120" || value.Stale {
				t.Errorf("unexpected refreshed margin: %+v", value)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("lookups: %d, want 1", calls.Load())
	}
	fresh := a.StockMargin(ctx, "US.AAPL", false)
	clk.Advance(stockMarginTTL)
	stale := a.StockMargin(ctx, "US.AAPL", true)
	if !stale.Stale || stale.Long != fresh.Long || stale.Short != fresh.Short || !stale.FetchedAt.Equal(fresh.FetchedAt) {
		t.Fatalf("refresh failure changed the last value: %+v", stale)
	}
	a.StockMargin(ctx, "US.AAPL", true)
	if calls.Load() != 2 {
		t.Fatalf("lookups during backoff: %d, want 2", calls.Load())
	}
}
