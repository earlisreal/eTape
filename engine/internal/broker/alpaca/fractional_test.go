package alpaca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestFractionalStockOrderRoutes(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 59, 59, 0, time.UTC))
	yes, no := true, false
	a := &Adapter{clk: clk, assetsBySymbol: map[string]AssetStatus{
		"AAPL": {Tradable: &yes, Fractionable: &yes, FractionalEHEnabled: &no},
	}}
	req := exec.OrderRequest{Symbol: "US.AAPL", Side: exec.SideBuy, Qty: .375, Type: exec.TypeLimit, TIF: exec.TIFDay}
	if reason := a.FractionalStockOrderReason(req); reason != "" {
		t.Fatal(reason)
	}
	clk.Advance(time.Second)
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted after-hours without fractional extended-hours eligibility")
	}
	a.assetsBySymbol["AAPL"] = AssetStatus{Tradable: &yes, Fractionable: &yes, FractionalEHEnabled: &yes}
	if reason := a.FractionalStockOrderReason(req); reason != "" {
		t.Fatal(reason)
	}
	req.Side = exec.SideShort
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted fractional short")
	}
	req.Side = exec.SideBuy
	clk.Advance(4 * time.Hour)
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted overnight")
	}
}

func TestAdapter_SubmitFractionalOrdersOnPaperAndLive(t *testing.T) {
	for _, env := range []string{"paper", "live"} {
		t.Run(env, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v2/assets", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[ {"symbol":"AAPL","tradable":true,"fractionable":true,"attributes":["fractional_eh_enabled"]} ]`))
			})
			mux.HandleFunc("POST /v2/orders", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Qty           string `json:"qty"`
					Type          string `json:"type"`
					Side          string `json:"side"`
					TIF           string `json:"time_in_force"`
					ExtendedHours bool   `json:"extended_hours"`
					ClientID      string `json:"client_order_id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "invalid order", http.StatusBadRequest)
					return
				}
				if req.Qty != "0.375" || req.TIF != "day" || req.Type != "limit" ||
					req.ExtendedHours != (req.ClientID == "after-hours") ||
					req.Side != "buy" {
					t.Errorf("fractional order payload = %+v", req)
				}
				_, _ = w.Write([]byte(`{"id":"fixture-order"}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			clk := clock.NewFake(time.Date(2026, 11, 20, 20, 59, 59, 0, time.UTC))
			a, err := New(Config{Venue: exec.VenueID("alpaca-" + env), Env: env, RESTBase: srv.URL, Clock: clk})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.LoadActiveAssets(t.Context()); err != nil {
				t.Fatal(err)
			}
			metadata, found, err := a.VenueInstrumentEligibility(t.Context(), "US.AAPL")
			if err != nil || !found || !metadata.FractionalStockOrders {
				t.Fatalf("fractional capability: %+v, found=%v, err=%v", metadata, found, err)
			}
			req := exec.OrderRequest{Symbol: "US.AAPL", Venue: exec.VenueID("alpaca-" + env), Side: exec.SideBuy,
				Qty: .375, Type: exec.TypeLimit, LimitPrice: 200, TIF: exec.TIFDay, Session: exec.SessionRTH, ClientOrderID: "regular-hours"}
			assertAccepted := func(req exec.OrderRequest) {
				t.Helper()
				if ack, err := a.SubmitOrder(t.Context(), req); err != nil || !ack.Accepted {
					t.Fatalf("submit %s: ack=%+v, err=%v", req.ClientOrderID, ack, err)
				}
			}
			assertAccepted(req)
			clk.Advance(time.Second)
			req.Session, req.ClientOrderID = exec.SessionExtended, "after-hours"
			assertAccepted(req)
		})
	}
}
