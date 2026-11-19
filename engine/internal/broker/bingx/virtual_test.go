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

func TestVirtualSubmitObserveAndCancel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		side      exec.Side
		direction string
		position  string
		reduce    bool
	}{
		{"open long", exec.SideBuy, "BUY", "[]", false},
		{"open short", exec.SideShort, "SELL", "[]", false},
		{"close long", exec.SideSell, "SELL", `[{"symbol":"BTC-USDT","positionSide":"BOTH","isolated":true,"positionAmt":"0.005","availableAmt":"0.004","leverage":5}]`, true},
		{"close short", exec.SideCover, "BUY", `[{"symbol":"BTC-USDT","positionSide":"BOTH","isolated":true,"positionAmt":"-0.005","availableAmt":"0.004","leverage":5}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const submitted = "ET01ABCD12345678901111111111"
			const exactID = "90071992547409939"
			const current = 1795041491205
			c := newVirtualAPI(creds.Pair{KeyID: "key", SecretKey: "secret"}, clock.NewFake(time.UnixMilli(current)))
			if c.base != vstURL {
				t.Fatal("virtual API uses the wrong host")
			}
			order := fmt.Sprintf(`{"orderId":%s,"clientOrderId":%q,"symbol":"BTC-USDT","side":%q,"reduceOnly":%t,"type":"LIMIT","status":"PARTIALLYFILLED","origQty":"0.0030","executedQty":"0.0020","time":%d}`,
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
					msg = `[{"asset":"VST","availableMargin":"200","equity":"210","usedMargin":"10"}]`
				case "/openApi/swap/v1/positionSide/dual":
					msg = `{"dualSidePosition":false}`
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
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				fmt.Fprintf(w, `{"code":0,"msg":"","data":%s}`, msg)
			}))
			defer server.Close()
			c.base = server.URL
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
			if err != nil || report.BrokerOrderID != exactID || report.Status != exec.StatusAccepted ||
				len(report.Fills) != 2 || report.Fills[0].Qty != 10 || report.Fills[0].ID == report.Fills[1].ID {
				t.Fatalf("unreconciled order: %+v, %v", report, err)
			}
			if err := c.Cancel(t.Context(), req); err != nil || cancels.Load() != 1 {
				t.Fatalf("cancel: %v, calls %d", err, cancels.Load())
			}
		})
	}
}

func TestVirtualRejectsRedirect(t *testing.T) {
	c := newVirtualAPI(creds.Pair{}, clock.NewFake(time.Unix(1, 0)))
	production := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("BingX Virtual followed a redirect")
	}))
	defer production.Close()
	virtual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, production.URL, http.StatusTemporaryRedirect)
	}))
	defer virtual.Close()
	c.base = virtual.URL
	if err := c.request(context.Background(), http.MethodPost, "/openApi/swap/v2/trade/order", nil, true, new(order)); err == nil {
		t.Fatal("expected redirect error")
	}
}
