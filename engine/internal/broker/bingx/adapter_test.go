package bingx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestSubmitObserveAndCancel(t *testing.T) {
	t.Run("Virtual", func(t *testing.T) {
		testSubmitObserveAndCancel(t, virtual, "https://open-api-vst.bingx.com", "VST")
	})
	t.Run("Live", func(t *testing.T) {
		testSubmitObserveAndCancel(t, live, "https://open-api.bingx.com", "USDT")
	})
}

func testSubmitObserveAndCancel(t *testing.T, env environment, host, currency string) {
	t.Helper()
	for _, tc := range []struct {
		name      string
		side      exec.Side
		direction string
		position  string
		reduce    bool
	}{
		{"open long", exec.SideBuy, "BUY", "[]", false},
		{"open short", exec.SideShort, "SELL", "[]", false},
		{"close long", exec.SideSell, "SELL", `[{"symbol":"BTC-USDT","positionId":"90071992547409955","positionSide":"BOTH","isolated":true,"positionAmt":"0.005","availableAmt":"0.004","avgPrice":"92000","initialMargin":"92","leverage":5}]`, true},
		{"close short", exec.SideCover, "BUY", `[{"symbol":"BTC-USDT","positionId":90071992547409955,"positionSide":"BOTH","isolated":true,"positionAmt":"-0.005","availableAmt":"0.004","avgPrice":"92000","initialMargin":"92","leverage":5}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const submitted = "ET01ABCD12345678901111111111"
			const exactID = "90071992547409939"
			const current = 1795041491205
			c := newAPI(env, creds.Pair{KeyID: "key", SecretKey: "secret"}, clock.NewFake(time.UnixMilli(current)))
			if c.env.base != host {
				t.Fatalf("%s uses the wrong host: %s", env.label, c.env.base)
			}
			order := fmt.Sprintf(`{"orderId":%s,"positionID":90071992547409955,"clientOrderId":%q,"symbol":"BTC-USDT","side":%q,"reduceOnly":%t,"type":"LIMIT","status":"PARTIALLYFILLED","origQty":"0.0030","executedQty":"0.0020","time":%d}`,
				exactID, strings.ToLower(submitted), tc.direction, tc.reduce, current-100)
			var posts, cancels atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				msg := ""
				q := r.URL.Query()
				if !strings.Contains(r.URL.Path, "/quote/") {
					raw, sig, ok := strings.Cut(r.URL.RawQuery, "&signature=")
					mac := hmac.New(sha256.New, []byte("secret"))
					mac.Write([]byte(raw))
					if !ok || r.Header.Get("X-BX-APIKEY") != "key" || sig != hex.EncodeToString(mac.Sum(nil)) ||
						q.Get("timestamp") != fmt.Sprint(current) || q.Get("recvWindow") != "5000" {
						t.Errorf("invalid signed %s request for %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}
				switch r.URL.Path {
				case "/openApi/swap/v2/quote/contracts":
					msg = `[{"symbol":"BTC-USDT","asset":"BTC","currency":"USDT","quantityPrecision":4,"pricePrecision":1,"tradeMinQuantity":0.0001,"tradeMinUSDT":2,"takerFeeRate":0.0005,"status":1,"apiStateOpen":"true","apiStateClose":"true"}]`
				case "/openApi/swap/v3/user/balance":
					msg = fmt.Sprintf(`[{"asset":"%s","availableMargin":"200","equity":"210","usedMargin":"10"}]`, currency)
				case "/openApi/swap/v1/positionSide/dual":
					msg = `{"dualSidePosition":false}`
					if env == live {
						msg = `{"dualSidePosition":"false"}`
					}
				case "/openApi/swap/v2/user/positions":
					msg = tc.position
				case "/openApi/swap/v2/trade/openOrders":
					msg = `{"orders":[]}`
				case "/openApi/swap/v2/trade/marginType":
					msg = `{"marginType":"ISOLATED"}`
				case "/openApi/swap/v2/trade/leverage":
					msg = `{"longLeverage":5,"shortLeverage":5,"maxLongLeverage":125,"maxShortLeverage":125,"availableLongVol":"1","availableShortVol":"1"}`
				case "/openApi/swap/v2/quote/premiumIndex":
					msg = `{"symbol":"BTC-USDT","markPrice":"92300.1"}`
				case "/openApi/swap/v2/trade/order":
					msg = `{"order":` + order + `}`
					switch r.Method {
					case http.MethodPost:
						posts.Add(1)
						if q.Get("side") != tc.direction || q.Get("positionSide") != "BOTH" || q.Get("reduceOnly") != fmt.Sprint(tc.reduce) ||
							q.Get("quantity") != "0.0030" || q.Get("price") != "92000" || q.Get("type") != "LIMIT" || q.Get("timeInForce") != "IOC" ||
							q.Get("clientOrderId") != strings.ToLower(submitted) {
							t.Errorf("incorrect order terms: %v", q)
						}
					case http.MethodGet:
						if q.Get("clientOrderId") != strings.ToLower(submitted) {
							t.Error("lookup did not use the original client ID")
						}
					case http.MethodDelete:
						cancels.Add(1)
						if q.Get("orderId") != exactID {
							t.Error("cancel did not use the exact exchange ID")
						}
					}
				case "/openApi/swap/v2/trade/allFillOrders":
					if q.Get("orderId") != exactID || q.Get("tradingUnit") != "COIN" {
						t.Error("fill query did not use the exact ID and base units")
					}
					fill := fmt.Sprintf(`{"orderId":%q,"symbol":"BTC-USDT","side":%q,"volume":"0.001","price":"92000","filledTime":%q}`,
						exactID, tc.direction, time.UnixMilli(current-50).UTC().Format(time.RFC3339Nano))
					msg = `{"fill_orders":[` + fill + "," + fill + `]}`
					if currency == "USDT" && !tc.reduce {
						fixture := `{"tradeId":%s,"orderId":%q,"symbol":"BTC-USDT","side":%q,"qty":"0.001","price":"92000","time":%d}`
						msg = fmt.Sprintf("["+fixture+","+fixture+"]",
							"90071992547409941", exactID, tc.direction, current-50,
							"90071992547409942", exactID, tc.direction, current-50)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprintf(w, `{"code":0,"msg":"","data":%s}`, msg)
			}))
			defer server.Close()
			c.env.base = server.URL
			account, err := c.account(t.Context())
			if err != nil || account.Account.Currency != currency || account.Account.AvailableCash != 200 {
				t.Fatalf("unexpected collateral: %+v, %v", account.Account, err)
			}
			if tc.reduce {
				_, positions, _, err := c.Snapshot(t.Context())
				if err != nil || len(positions) != 1 || positions[0].BrokerPositionID != "90071992547409955" {
					t.Fatalf("snapshot did not preserve position identity: %+v, %v", positions, err)
				}
			}
			req := exec.OrderRequest{Symbol: "PERP.BINGX.BTC-USDT", ClientOrderID: submitted,
				Side: tc.side, Type: exec.TypeLimit, TIF: exec.TIFIOC, Qty: 30, LimitPrice: 92000,
				Perpetual: &exec.PerpetualOrder{Multiplier: 0.0001, Leverage: 5}}
			send, err := c.Prepare(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 0 {
				t.Fatal("preflight posted an order")
			}
			if err := send(t.Context()); err != nil || posts.Load() != 1 {
				t.Fatalf("submit: %v, calls %d", err, posts.Load())
			}
			report, err := c.Observe(t.Context(), req)
			if err != nil || report.BrokerOrderID != exactID || report.PositionID != "90071992547409955" || report.Status != exec.StatusAccepted ||
				len(report.Fills) != 2 || report.Fills[0].Qty != 10 || report.Fills[0].ID == report.Fills[1].ID {
				t.Fatalf("unreconciled order: %+v, %v", report, err)
			}
			if currency == "USDT" && !tc.reduce && report.Fills[0].ID != "90071992547409941" {
				t.Fatalf("expected exact live fill ID, got %s", report.Fills[0].ID)
			}
			if err := c.Cancel(t.Context(), req); err != nil || cancels.Load() != 1 {
				t.Fatalf("cancel: %v, calls %d", err, cancels.Load())
			}
		})
	}
}

func TestPositionBoundCloseCannotFallBackToSymbol(t *testing.T) {
	c := newAPI(virtual, creds.Pair{}, clock.NewFake(time.UnixMilli(1700000000000)))
	send, err := c.Prepare(t.Context(), exec.OrderRequest{Symbol: "PERP.BINGX.BTC-USDT",
		Side: exec.SideSell, Type: exec.TypeMarket, Qty: 1,
		Perpetual: &exec.PerpetualOrder{PositionID: "90071992547409955"}})
	if send != nil || err == nil || !strings.Contains(err.Error(), "position-bound quantity-limited") {
		t.Fatalf("position-bound close must fail before exchange I/O: %v", err)
	}
}

func TestAccountRejectsQuotedHedgeMode(t *testing.T) {
	c := newAPI(live, creds.Pair{KeyID: "key", SecretKey: "secret"}, clock.NewFake(time.UnixMilli(1700000000000)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/openApi/swap/v1/positionSide/dual" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"dualSidePosition":"true"}}`)
	}))
	defer server.Close()
	c.env.base = server.URL
	if _, err := c.account(t.Context()); err == nil || err.Error() != "set BingX Live to one-way position mode" {
		t.Fatalf("account check in hedge mode = %v", err)
	}
}

func TestRejectsEnvironmentRedirect(t *testing.T) {
	for _, env := range []environment{virtual, live} {
		t.Run(env.label, func(t *testing.T) {
			c := newAPI(env, creds.Pair{}, clock.NewFake(time.Unix(1, 0)))
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("BingX followed a redirect")
			}))
			defer other.Close()
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
			}))
			defer host.Close()
			c.env.base = host.URL
			if err := c.request(context.Background(), http.MethodPost, "/openApi/swap/v2/trade/order", nil, true, new(order)); err == nil {
				t.Fatal("expected redirect error")
			}
		})
	}
}
