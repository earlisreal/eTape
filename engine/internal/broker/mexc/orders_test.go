package mexc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const (
	fixtureTime       = "1795041491205"
	fixtureOrderID    = "750330744886358528"
	fixturePositionID = "750330744886358529"
	fixtureFillID     = "750330744886358530"
)

func fixture(t *testing.T, handler func(http.ResponseWriter, *http.Request, []byte)) client {
	t.Helper()
	cr := creds.Pair{KeyID: "fixture-key", SecretKey: "fixture-secret"}
	clk := clock.NewFake(time.UnixMilli(1795041491205))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		signed := r.URL.RawQuery
		if r.Method == http.MethodPost {
			signed = string(body)
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("invalid post content type %s", r.Header.Get("Content-Type"))
			}
		}
		mac := hmac.New(sha256.New, []byte(cr.SecretKey))
		_, _ = mac.Write([]byte(cr.KeyID + fixtureTime + signed))
		if r.Header.Get("ApiKey") != cr.KeyID || r.Header.Get("Request-Time") != fixtureTime ||
			r.Header.Get("Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("unexpected MEXC signature on %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r, body)
	}))
	t.Cleanup(server.Close)
	c := newClient(cr, clk)
	c.base = server.URL
	return c
}

func sendResult(w http.ResponseWriter, jsonData string) {
	_, _ = fmt.Fprintf(w, `{"success":true,"code":0,"data":%s}`, jsonData)
}

func snapshotFixture(w http.ResponseWriter, r *http.Request, p string, posMode int) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/private/account/assets":
		sendResult(w, `[{"currency":"USDT","availableBalance":100,"equity":120,"positionMargin":20}]`)
	case "/api/v1/private/position/position_mode":
		sendResult(w, fmt.Sprint(posMode))
	case "/api/v1/private/position/open_positions":
		sendResult(w, p)
	case "/api/v1/private/stoporder/open_orders", "/api/v1/private/order/list/open_orders":
		sendResult(w, "[]")
	case "/api/v1/private/account/risk_limit":
		sendResult(w, `{"BTC_USDT":[{"positionType":1,"openType":1,"maxVol":20,"leverage":5,"maxLeverage":10},
			{"positionType":2,"openType":1,"maxVol":20,"leverage":5,"maxLeverage":10}]}`)
	default:
		return false
	}
	return true
}

func tradingRules() instrument.ContractRules {
	return instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.0001", PriceTick: "0.1", QuantityStep: 1,
		Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 10, TakerFee: "0.0005", TradingEnabled: true}
}

func TestPlaceOrderSetsMEXCOneWaySideAndProtection(t *testing.T) {
	for _, route := range []struct {
		name               string
		side               exec.Side
		code, positionSide int
		reduce             bool
		stop               float64
	}{
		{"open long", exec.SideBuy, 1, longPosition, false, 99_000},
		{"close short", exec.SideCover, 2, shortPosition, true, 0},
		{"open short", exec.SideShort, 3, shortPosition, false, 101_000},
		{"close long", exec.SideSell, 4, longPosition, true, 0},
	} {
		t.Run(route.name, func(t *testing.T) {
			positions := "[]"
			if route.reduce {
				positions = fmt.Sprintf(`[{"symbol":"BTC_USDT","positionId":%s,"positionType":%d,"openType":1,"holdVol":10,"frozenVol":1}]`,
					fixturePositionID, route.positionSide)
			}
			var writes atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
				if snapshotFixture(w, r, positions, oneWay) {
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/private/order/create" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writes.Add(1)
				var posted orderSubmission
				if err := json.Unmarshal(body, &posted); err != nil {
					t.Error(err)
				}
				if posted.Side != route.code || posted.MarginMode != isolated || posted.PositionMode != oneWay ||
					posted.ReduceOnly != route.reduce || posted.Price != "100000" || posted.Volume != "9" || posted.Type != 3 ||
					posted.ClientID != "ETfixture" || posted.Symbol != "BTC_USDT" {
					t.Errorf("incorrect order payload %+v", posted)
				}
				if route.reduce && (posted.PositionID.String() != fixturePositionID || posted.StopLoss != "" || posted.Leverage != 0) ||
					!route.reduce && (posted.PositionID != "" || posted.StopLoss != decimal(route.stop) || posted.LossTrend != fairTrigger || posted.Leverage != 5) {
					t.Errorf("incorrect leverage or protective stop payload %+v", posted)
				}
				sendResult(w, `{"orderId":"`+fixtureOrderID+`","ts":`+fixtureTime+`}`)
			})
			req := exec.OrderRequest{Symbol: "PERP.MEXC.BTC_USDT", ClientOrderID: "ETfixture", Side: route.side,
				Type: exec.TypeLimit, TIF: exec.TIFIOC, LimitPrice: 100_000, Qty: 9,
				Perpetual: &exec.PerpetualOrder{ProtectionPrice: route.stop, Leverage: 5, Multiplier: .0001}}
			got, err := c.placeOrder(context.Background(), req, tradingRules(), 100_000)
			if err != nil || got.ID.String() != fixtureOrderID || writes.Load() != 1 {
				t.Fatalf("place resulted in %+v, %v, %d posts", got, err, writes.Load())
			}
		})
	}
}

func TestPlaceOrderBlocksUnsupportedAccountBeforePOST(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if !snapshotFixture(w, r, "[]", 1) {
			t.Errorf("unexpected request after hedge-mode preflight: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	req := exec.OrderRequest{Symbol: "PERP.MEXC.BTC_USDT", ClientOrderID: "ETfixture", Side: exec.SideBuy,
		Type: exec.TypeLimit, TIF: exec.TIFIOC, LimitPrice: 100_000, Qty: 9,
		Perpetual: &exec.PerpetualOrder{Leverage: 5, Multiplier: .0001}}
	_, err := c.placeOrder(context.Background(), req, tradingRules(), 100_000)
	if err == nil || !strings.Contains(err.Error(), "one-way") {
		t.Fatalf("expected one-way preflight error, got %v", err)
	}
}

func TestMEXCOpeningTermsAndDefinitiveRejection(t *testing.T) {
	var posts atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/private/account/risk_limit":
			if r.Method != http.MethodGet || r.URL.Query().Get("symbol") != "BTC_USDT" {
				t.Errorf("unexpected risk tier request: %s %s", r.Method, r.URL)
			}
			sendResult(w, `{"BTC_USDT":[{"positionType":1,"openType":1,"maxVol":20,"leverage":5,"maxLeverage":10},
				{"positionType":2,"openType":2,"maxVol":20,"leverage":3,"maxLeverage":10}]}`)
		case "/api/v1/private/order/create":
			posts.Add(1)
			_, _ = io.WriteString(w, `{"success":false,"code":6002,"data":null}`)
		default:
			if !snapshotFixture(w, r, "[]", oneWay) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			}
		}
	})
	terms, err := c.openingTerms(t.Context(), "BTC_USDT")
	if err != nil || terms.Long != (exec.PerpetualOpeningLimit{Leverage: 5, MaxContracts: 20}) || terms.Short.Leverage != 0 {
		t.Fatalf("wrong isolated terms: %+v, %v", terms, err)
	}
	req := exec.OrderRequest{Symbol: "PERP.MEXC.BTC_USDT", ClientOrderID: "ETfixture", Side: exec.SideBuy,
		Type: exec.TypeLimit, TIF: exec.TIFIOC, LimitPrice: 100_000, Qty: 7,
		Perpetual: &exec.PerpetualOrder{Leverage: 1, Multiplier: .0001}}
	if _, err := c.placeOrder(t.Context(), req, tradingRules(), 100_000); err == nil || !strings.Contains(err.Error(), "isolated leverage is 5×") || posts.Load() != 0 {
		t.Fatalf("leverage mismatch reached submit or lacked a specific reason: %v", err)
	}
	req.Perpetual.Leverage = 5
	var declined exec.PerpetualSubmitDeclined
	if _, err := c.placeOrder(t.Context(), req, tradingRules(), 100_000); !errors.As(err, &declined) || !strings.Contains(err.Error(), "6002") || posts.Load() != 1 {
		t.Fatalf("expected definitive 6002 after one post, got %v after %d", err, posts.Load())
	}
}

func TestPositionBoundCloseRejectsDifferentMEXCPositionBeforePOST(t *testing.T) {
	p := `[{"symbol":"BTC_USDT","positionId":` + fixturePositionID + `,"positionType":1,"openType":1,"holdVol":4,"frozenVol":0}]`
	c := fixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if !snapshotFixture(w, r, p, oneWay) {
			t.Errorf("unexpected request for stale position: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	req := exec.OrderRequest{Symbol: "PERP.MEXC.BTC_USDT", ClientOrderID: "ETclose", Side: exec.SideSell,
		Type: exec.TypeMarket, TIF: exec.TIFIOC, Qty: 2,
		Perpetual: &exec.PerpetualOrder{Multiplier: .0001, PositionID: "99"}}
	if _, err := c.placeOrder(t.Context(), req, tradingRules(), 100_000); err == nil || !strings.Contains(err.Error(), "position changed") {
		t.Fatalf("expected a position-cycle rejection, got %v", err)
	}
}

func TestAmbiguousMEXCSubmitReconcilesPartialIOCWithProtection(t *testing.T) {
	var posts atomic.Int32
	var confirmed atomic.Bool
	c := fixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/private/order/create":
			posts.Add(1)
			w.WriteHeader(http.StatusGatewayTimeout)
		case "/api/v1/private/order/external/BTC_USDT/ETpartial":
			sendResult(w, `{"orderId":`+fixtureOrderID+`,"positionId":`+fixturePositionID+`,
				"externalOid":"ETpartial","symbol":"BTC_USDT","side":1,"state":4,"vol":9,"dealVol":4}`)
		case "/api/v1/private/order/deal_details/" + fixtureOrderID:
			sendResult(w, `[{"id":"`+fixtureFillID+`","orderId":"`+fixtureOrderID+`","symbol":"BTC_USDT","side":1,
				"vol":"4.0","price":100000,"timestamp":`+fixtureTime+`}]`)
		case "/api/v1/private/stoporder/open_orders":
			if r.URL.Query().Get("symbol") == "" {
				sendResult(w, "[]")
				return
			}
			explicitMarket := ""
			if confirmed.Load() {
				explicitMarket = `"stopLossType":0,`
			}
			sendResult(w, `[{`+explicitMarket+`"id":99,"orderId":"`+fixtureOrderID+`","positionId":"`+fixturePositionID+`","symbol":"BTC_USDT",
				"positionType":1,"lossTrend":2,"state":1,"isFinished":0,"stopLossReverse":2,"volType":1,
				"profitLossVolType":"SEPARATE","vol":9,"stopLossVol":4,"stopLossPrice":99000}]`)
		default:
			if !snapshotFixture(w, r, "[]", oneWay) {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
			}
		}
	})
	ctx := context.Background()
	req := exec.OrderRequest{Symbol: "PERP.MEXC.BTC_USDT", ClientOrderID: "ETpartial", Side: exec.SideBuy,
		Type: exec.TypeLimit, TIF: exec.TIFIOC, LimitPrice: 100_000, Qty: 9,
		Perpetual: &exec.PerpetualOrder{ProtectionPrice: 99_000, Leverage: 5, Multiplier: .0001}}
	if _, err := c.placeOrder(ctx, req, tradingRules(), 100_000); err == nil {
		t.Fatal("a gateway timeout cannot confirm the submit")
	}
	key := orderIdentity{Symbol: "BTC_USDT", ClientID: req.ClientOrderID}
	uncertain, err := c.reconcileOrder(ctx, key, 99_000)
	if err != nil || len(uncertain.Fills) != 1 || uncertain.Fills[0].ID.String() != fixtureFillID || uncertain.ProtectedContracts != 0 {
		t.Fatalf("unconfirmed protection state %+v, %v", uncertain, err)
	}
	confirmed.Store(true)
	got, err := c.reconcileOrder(ctx, key, 99_000)
	if err != nil || got.ProtectedContracts != 4 || got.Order.State != 4 || got.Order.ID.String() != fixtureOrderID || posts.Load() != 1 {
		t.Fatalf("partial fill reconciliation %+v, %v, %d posts", got, err, posts.Load())
	}
}

func TestCancelUsesPerOrderErrorAndExactExternalID(t *testing.T) {
	identity := orderIdentity{Symbol: "BTC_USDT", ClientID: "ETcancel"}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch r.URL.Path {
		case "/api/v1/private/order/external/BTC_USDT/ETcancel":
			sendResult(w, `{"orderId":`+fixtureOrderID+`,"externalOid":"ETcancel","symbol":"BTC_USDT","side":1,"state":2}`)
		case "/api/v1/private/order/cancel_with_external":
			var keys []orderIdentity
			if err := json.Unmarshal(body, &keys); err != nil || len(keys) != 1 || keys[0] != identity {
				t.Errorf("cancel payload %s, %v", body, err)
			}
			sendResult(w, `[{"orderId":"`+fixtureOrderID+`","errorCode":2041}]`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	var failure apiError
	if err := c.cancelOrder(context.Background(), identity); !errors.As(err, &failure) || failure.Code != 2041 {
		t.Fatalf("per-order cancel response lost: %v", err)
	}
}
