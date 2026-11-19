package binance

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
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/linearperp"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type demoFixture struct {
	rules linearperp.DemoRules
}

func (d demoFixture) Refresh(context.Context) error { return nil }

func (d demoFixture) DemoRules(symbol string) (linearperp.DemoRules, bool) {
	return d.rules, symbol == demoSymbol("BTCUSDT")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDemoSubmitAndReconcile(t *testing.T) {
	clk := clock.System{}
	api := newDemoAPI(creds.Pair{KeyID: "demo-key", SecretKey: "demo-secret"}, clk)
	rules := instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.0001", PriceTick: "0.1",
		Minimum: 1, Maximum: 1000, QuantityStep: 1, MinLeverage: 1, MaxLeverage: 125, TakerFee: "0", TradingEnabled: true}
	api.catalog = demoFixture{rules: linearperp.DemoRules{
		ContractRules: rules, Market: rules, MinNotional: 50, MinPrice: 260, MaxPrice: 800000,
	}}
	symbol := demoSymbol("BTCUSDT")
	api.quotes.Store(symbol, feed.Quote{Symbol: symbol, Mark: 60000, TsMs: clk.Now().UnixMilli()})

	const id = "ET01KAB5T0PC010001200300400"
	const orderID = 1000000000000000123
	const orderPath = "/fapi/v1/order"
	req := exec.OrderRequest{Venue: "binance-demo", Symbol: symbol, Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10,
		TIF: exec.TIFGTC, Session: exec.SessionAuto, LimitPrice: 60000, ClientOrderID: id,
		Perpetual: &exec.PerpetualOrder{Multiplier: 0.0001, Leverage: 20}}
	var held string
	var direction = "BUY"
	var reduce bool
	state, executed := "PARTIALLY_FILLED", "0.0004"
	fillQty := "0.0004"
	postCount := 0
	api.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		query, signature, _ := strings.Cut(r.URL.RawQuery, "&signature=")
		mac := hmac.New(sha256.New, []byte("demo-secret"))
		_, _ = mac.Write([]byte(query))
		if r.URL.Scheme != "https" || r.URL.Host != "demo-fapi.binance.com" ||
			r.Header.Get("X-MBX-APIKEY") != "demo-key" || signature != hex.EncodeToString(mac.Sum(nil)) {
			t.Fatal("signed request escaped the Demo route or had an invalid signature")
		}
		q := r.URL.Query()
		body := ""
		order := func() string {
			return fmt.Sprintf(`{"orderId":%d,"clientOrderId":"%s","symbol":"BTCUSDT","side":%q,"positionSide":"BOTH",
				"reduceOnly":%t,"type":"LIMIT","status":%q,"origQty":"0.0010","executedQty":%q}`, orderID, id, direction, reduce, state, executed)
		}
		switch r.URL.Path {
		case "/fapi/v1/accountConfig":
			body = `{"dualSidePosition":false,"multiAssetsMargin":false,"canTrade":true}`
		case "/fapi/v3/account":
			body = `{"totalMarginBalance":"5000.00","availableBalance":"5000.00","totalInitialMargin":"0.00"}`
		case "/fapi/v3/positionRisk":
			body = "[]"
			if held != "" {
				body = fmt.Sprintf(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":%q,
					"entryPrice":"60000","positionInitialMargin":"3.00"}]`, held)
			}
		case "/fapi/v1/symbolConfig":
			body = `[{"symbol":"BTCUSDT","marginType":"ISOLATED","leverage":20}]`
		case "/fapi/v1/openOrders":
			body = "[]"
		case orderPath:
			switch r.Method {
			case http.MethodPost:
				postCount++
				if q.Get("newClientOrderId") != id || q.Get("quantity") != "0.0010" || q.Get("positionSide") != "BOTH" ||
					q.Get("reduceOnly") != fmt.Sprint(reduce) || q.Get("side") != direction || q.Get("timeInForce") != "GTC" {
					t.Fatal("incorrect Binance order parameters")
				}
				if postCount == 1 {
					return nil, errors.New("transport timeout with unknown outcome")
				}
			case http.MethodGet:
				if q.Get("origClientOrderId") != id {
					t.Fatal("order lookup used a different client ID")
				}
			case http.MethodDelete:
				if q.Get("orderId") != fmt.Sprint(orderID) {
					t.Fatal("cancellation did not identify the exchange order")
				}
				state = "CANCELED"
			default:
				t.Fatalf("unexpected order method %s", r.Method)
			}
			body = order()
		case "/fapi/v1/userTrades":
			if q.Get("orderId") != fmt.Sprint(orderID) || q.Get("symbol") != "BTCUSDT" {
				t.Fatal("trade lookup did not identify the exchange order")
			}
			body = fmt.Sprintf(`[{"id":42,"orderId":%d,"symbol":"BTCUSDT","positionSide":"BOTH",
				"side":"BUY","qty":%q,"price":"60000","time":1795060621004}]`, orderID, fillQty)
		default:
			t.Fatalf("unexpected Demo path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	submit, err := api.Prepare(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if postCount != 0 {
		t.Fatal("preparation sent an order before journaling")
	}
	if err := submit(t.Context()); err == nil {
		t.Fatal("expected ambiguous POST failure")
	}
	report, err := api.Observe(t.Context(), req)
	if err != nil || report.Status != exec.StatusPartiallyFilled || len(report.Fills) != 1 || report.Fills[0].Qty != 4 ||
		report.BrokerOrderID != fmt.Sprint(orderID) || report.Fills[0].ID != "BTCUSDT:42" {
		t.Fatalf("unexpected partial fill %+v, %v", report, err)
	}
	if err := api.Cancel(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	report, err = api.Observe(t.Context(), req)
	if err != nil || !report.Terminal() || report.Status != exec.StatusCanceled || postCount != 1 {
		t.Fatalf("unexpected cancellation %+v, %v", report, err)
	}
	fillQty = "0.0003"
	if _, err := api.Observe(t.Context(), req); err == nil {
		t.Fatal("cancellation retired an order with missing fills")
	}

	req.Side, held, direction, reduce = exec.SideSell, "0.0010", "SELL", true
	submit, err = api.Prepare(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := submit(t.Context()); err != nil {
		t.Fatal(err)
	}
	req.Side = exec.SideCover
	if _, err := api.Prepare(t.Context(), req); err == nil {
		t.Fatal("cover admitted against a long")
	}
	account, positions, _, err := api.Snapshot(t.Context())
	if err != nil || account.Currency != "USDT" || len(positions) != 1 ||
		positions[0].Qty != 10 || positions[0].Multiplier != 0.0001 {
		t.Fatalf("unexpected account or positions: %+v, %+v, %v", account, positions, err)
	}
}

func TestDemoFairPriceRequiresDemoSymbol(t *testing.T) {
	api := newDemoAPI(creds.Pair{}, clock.System{})
	demo := Demo{api: api}
	demo.SetFairQuote(feed.Quote{Symbol: "PERP.BINANCE.BTCUSDT", Mark: 60000, TsMs: time.Now().UnixMilli()})
	if _, ok := api.FairPrice("PERP.BINANCE.BTCUSDT"); ok {
		t.Fatal("production mark admitted to Demo execution")
	}
	if _, ok := api.FairPrice(demoSymbol("BTCUSDT")); ok {
		t.Fatal("production price populated the Demo symbol")
	}
}

func TestDemoReconciliationQuantityFormat(t *testing.T) {
	if got, ok := linearperp.DemoLots(json.Number("-0.00100"), "0.0001"); !ok || got != -10 {
		t.Fatalf("quantity converted to %d lots, valid %t", got, ok)
	}
	if _, ok := linearperp.DemoLots(json.Number("0.00009"), "0.0001"); ok {
		t.Fatal("fractional lot was accepted")
	}
}
