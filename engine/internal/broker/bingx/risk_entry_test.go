package bingx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestChartRiskEntryLeverageAndPositionClose(t *testing.T) {
	for _, env := range []environment{virtual, live} {
		t.Run(env.label, func(t *testing.T) {
			const symbol, positionID, closeOrderID, entryOrderID = "PERP.BINGX.BTC-USDT", "90071992547409955", "90071992547409957", "90071992547409959"
			const now = 1795041491205
			var positionState, closeWrites, entryWrites atomic.Int32
			var warmCheck, dispatchCheck, closeCheck atomic.Bool
			var riskReads atomic.Int32
			allRiskReads := make(chan struct{})
			journal := filepath.Join(t.TempDir(), "journal.json")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if !strings.Contains(r.URL.Path, "/quote/") && (r.Header.Get("X-BX-APIKEY") != "fixture" || q.Get("signature") == "") {
					t.Error("account check or trade request was unsigned")
				}
				var msg string
				switch r.URL.Path {
				case "/openApi/swap/v2/trade/marginType", leveragePath, "/openApi/swap/v1/maintMarginRatio":
					if warmCheck.Load() && r.Method == http.MethodGet {
						if riskReads.Add(1) == 3 {
							close(allRiskReads)
						}
						select {
						case <-allRiskReads:
						case <-r.Context().Done():
							return
						}
					}
				}
				if dispatchCheck.Load() && (r.Method != http.MethodPost || r.URL.Path != "/openApi/swap/v2/trade/order") {
					t.Errorf("protected limit dispatch made an extra request: %s %s", r.Method, r.URL.Path)
				}
				if closeCheck.Load() && (r.Method != http.MethodPost || r.URL.Path != closePositionPath) {
					t.Errorf("position-bound close made a preflight request: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/openApi/swap/v2/quote/contracts":
					msg = `[{"symbol":"BTC-USDT","asset":"BTC","currency":"USDT","quantityPrecision":4,"pricePrecision":1,"tradeMinQuantity":0.0001,"tradeMinUSDT":2,"takerFeeRate":0.0005,"status":1,"apiStateOpen":"true","apiStateClose":"true"}]`
				case "/openApi/swap/v3/user/balance":
					msg = fmt.Sprintf(`[{"asset":%q,"availableMargin":"1000","equity":"1010","usedMargin":"10"}]`, env.currency)
				case "/openApi/swap/v1/positionSide/dual":
					msg = `{"dualSidePosition":false}`
				case "/openApi/swap/v2/user/positions":
					id := positionID
					if positionState.Load() == 2 {
						id = "90071992547409956"
					}
					msg = "[]"
					if positionState.Load() != 0 {
						msg = fmt.Sprintf(`[{"symbol":"BTC-USDT","positionId":%s,"positionSide":"BOTH","isolated":true,"positionAmt":"0.0004","availableAmt":"0.0004","avgPrice":"100000","initialMargin":"6","margin":"7","liquidationPrice":"87500","leverage":5}]`, id)
					}
				case "/openApi/swap/v2/trade/openOrders":
					msg = `{"orders":[]}`
				case "/openApi/swap/v2/trade/marginType":
					msg = `{"marginType":"ISOLATED"}`
				case leveragePath:
					if r.Method != http.MethodGet {
						t.Error("protected limit changed exchange leverage")
					}
					msg = `{"longLeverage":5,"shortLeverage":5,"maxLongLeverage":125,"maxShortLeverage":100,"availableLongVol":"0.0008","availableShortVol":"0.0008"}`
				case "/openApi/swap/v1/maintMarginRatio":
					msg = `[{"symbol":"BTC-USDT","minPositionVal":"0","maxPositionVal":"10000","maintMarginRatio":"0.005","maintAmount":"0"}]`
				case closePositionPath:
					closeWrites.Add(1)
					if r.Method != http.MethodPost || q.Get("positionId") != positionID || q.Has("quantity") || q.Has("clientOrderId") || q.Has("symbol") {
						t.Error("whole close must address only the exact original position ID")
					}
					data, err := os.ReadFile(journal)
					if err != nil || !strings.Contains(string(data), "held-stop") {
						t.Error("whole-position close was not journaled before sending")
					}
					msg = fmt.Sprintf(`{"orderId":%s,"positionId":%q}`, closeOrderID, positionID)
				case "/openApi/swap/v2/trade/order":
					if q.Get("clientOrderId") == "held-entry" && r.Method == http.MethodPost {
						if q.Get("timeInForce") != "GTC" || q.Get("price") != "100100" || q.Get("type") != "LIMIT" {
							t.Errorf("expected a regular limit order: %v", q)
						}
						entryWrites.Add(1)
						msg = fmt.Sprintf(`{"orderId":%s,"clientOrderId":"held-entry","symbol":"BTC-USDT"}`, entryOrderID)
						break
					}
					if q.Get("clientOrderId") == "held-entry" && r.Method == http.MethodGet {
						msg = fmt.Sprintf(`{"orderId":%s,"clientOrderId":"held-entry","symbol":"BTC-USDT","side":"BUY","type":"LIMIT","status":"FILLED","origQty":"0.0002","executedQty":"0.0002","time":%d}`, entryOrderID, now-100)
						break
					}
					if r.Method != http.MethodGet || q.Get("orderId") != closeOrderID || q.Get("clientOrderId") != "" {
						t.Error("whole close lookup must use the returned exchange order ID")
					}
					msg = fmt.Sprintf(`{"order":{"orderId":%s,"positionID":%s,"symbol":"BTC-USDT","side":"SELL","type":"MARKET","status":"FILLED","origQty":"0.0003","executedQty":"0.0003","time":%d}}`, closeOrderID, positionID, now-100)
				case "/openApi/swap/v2/trade/allOrders":
					msg = fmt.Sprintf(`{"orders":[{"orderId":%s,"positionId":"90071992547409950","clientOrderId":"different","symbol":"BTC-USDT","side":"BUY","type":"LIMIT"},{"orderId":%s,"positionId":%s,"clientOrderId":"held-entry","symbol":"BTC-USDT","side":"BUY","type":"LIMIT"}]}`,
						closeOrderID, entryOrderID, positionID)
				case "/openApi/swap/v2/trade/allFillOrders":
					if q.Get("orderId") == entryOrderID {
						msg = fmt.Sprintf(`{"fill_orders":[{"orderId":%s,"symbol":"BTC-USDT","side":"BUY","volume":"0.0002","price":"100000","filledTime":%q}]}`,
							entryOrderID, time.UnixMilli(now-90).UTC().Format(time.RFC3339Nano))
						break
					}
					if q.Get("orderId") != closeOrderID {
						t.Error("fill lookup lost the whole close order ID")
					}
					msg = fmt.Sprintf(`{"fill_orders":[{"orderId":%s,"symbol":"BTC-USDT","side":"SELL","volume":"0.0003","price":"99200","filledTime":%q}]}`,
						closeOrderID, time.UnixMilli(now-90).UTC().Format(time.RFC3339Nano))
					if env == live {
						msg = fmt.Sprintf(`[{"orderId":%q,"tradeId":"42","symbol":"BTC-USDT","side":"SELL","qty":"0.0003","price":"99200","time":%d}]`,
							closeOrderID, now-90)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprintf(w, `{"code":0,"data":%s}`, msg)
			}))
			defer server.Close()
			clk := clock.NewFake(time.UnixMilli(now))
			a, err := newAdapter(env, "venue", creds.Pair{KeyID: "fixture", SecretKey: "secret"}, clk, journal)
			if err != nil {
				t.Fatal(err)
			}
			a.api.env.base = server.URL
			ctx := t.Context()
			if _, _, _, err := a.Snapshot(ctx); err != nil {
				t.Fatal(err)
			}
			a.SetFairQuote(feed.Quote{Symbol: symbol, TsMs: now, Mark: 100_000})
			warmCheck.Store(true)
			terms, err := a.OpeningTerms(ctx, symbol)
			if err != nil || terms.Long.Leverage != 5 || terms.Long.MaxContracts != 8 {
				t.Fatalf("background refresh did not load the signed opening settings: %+v, %v", terms, err)
			}
			warmCheck.Store(false)
			positionState.Store(1)
			if _, _, _, err := a.Snapshot(ctx); err != nil {
				t.Fatal(err)
			}
			entry := exec.OrderRequest{Venue: "venue", Symbol: symbol, ClientOrderID: "held-entry", Qty: 2, Side: exec.SideBuy,
				Type: exec.TypeLimit, TIF: exec.TIFGTC, LimitPrice: 100_100,
				Perpetual: &exec.PerpetualOrder{Leverage: 5, Multiplier: 0.0001, ProtectionPrice: 98_000, ProtectionID: "held-stop",
					PositionID: positionID, RiskBudget: 2, WholePosition: true, LocalProtection: true, AutoLeverage: true}}
			dispatchCheck.Store(true)
			send, err := a.api.Prepare(ctx, entry)
			if err != nil {
				t.Fatalf("protected limit could not use cached settings and account data: %v", err)
			}
			if err := send(ctx); err != nil || entryWrites.Load() != 1 {
				t.Fatalf("protected limit was not sent: %v", err)
			}
			dispatchCheck.Store(false)
			if report, err := a.api.Observe(ctx, entry); err != nil || report.PositionID != positionID || report.Status != exec.StatusFilled {
				t.Fatalf("signed order history did not identify the limit order's position: %+v, %v", report, err)
			}

			close := exec.OrderRequest{Venue: "venue", Symbol: symbol, ClientOrderID: "held-stop", Side: exec.SideSell, Type: exec.TypeMarket, Qty: 2,
				Perpetual: &exec.PerpetualOrder{Multiplier: 0.0001, PositionID: "90071992547409954", WholePosition: true, LocalProtection: true}}
			if _, err := a.SubmitOrder(ctx, close); err == nil || closeWrites.Load() != 0 {
				t.Fatal("attempted to close a different position")
			}
			close.Perpetual.PositionID = positionID
			clk.Advance(positionCloseSnapshotMaxAge)
			positionState.Store(2)
			if _, err := a.SubmitOrder(ctx, close); err == nil || closeWrites.Load() != 0 {
				t.Fatal("stale evidence allowed a close after the position was replaced")
			}
			positionState.Store(1)
			if _, _, _, err := a.Snapshot(ctx); err != nil {
				t.Fatal(err)
			}
			closeCheck.Store(true)
			if ack, err := a.SubmitOrder(ctx, close); err != nil || !ack.Accepted || ack.BrokerOrderID != closeOrderID || closeWrites.Load() != 1 {
				t.Fatalf("protected position close was not sent: %+v, %v", ack, err)
			}
			closeCheck.Store(false)
			data, err := os.ReadFile(journal)
			if err != nil || !strings.Contains(string(data), closeOrderID) {
				t.Fatalf("exchange-generated ID was not persisted: %v", err)
			}
			report, err := a.api.ObservePositionClose(ctx, close, closeOrderID)
			if err != nil || report.PositionID != positionID || report.OrderQty != 3 || report.Status != exec.StatusFilled ||
				len(report.Fills) != 1 || report.Fills[0].Qty != 3 {
				t.Fatalf("did not reconcile the actual whole-position close: %+v, %v", report, err)
			}
			if report, err = a.api.ObservePositionClose(ctx, close, ""); err == nil || report.Terminal() {
				t.Fatal("a lost reply was treated as terminal while the original position remained")
			}
			positionState.Store(2)
			if report, err = a.api.ObservePositionClose(ctx, close, ""); err != nil || !report.PositionGone || len(report.Fills) != 0 || closeWrites.Load() != 1 {
				t.Fatalf("a replacement position restarted the close or was attributed a fill: %+v, %v", report, err)
			}
		})
	}
}
