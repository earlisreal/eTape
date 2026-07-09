package tradezero

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func serveFile(t *testing.T, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func TestSubmit_HTTP200Rejected_R114(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", serveFile(t, "order_reject_r114.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, reason, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-dup",
	}, "ET-dup", "SMART")
	if err != nil {
		t.Fatalf("submit returned transport err: %v", err)
	}
	if accepted {
		t.Fatal("HTTP 200 with orderStatus Rejected must NOT be treated as accepted")
	}
	if reason == "" || reason[:4] != "R114" {
		t.Fatalf("reason should carry the R-code, got %q", reason)
	}
}

func TestSubmit_HTTP200Rejected_R78(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", serveFile(t, "order_reject_r78.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, reason, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
		ClientOrderID: "ET-1",
	}, "ET-1", "SMART")
	if err != nil {
		t.Fatalf("submit returned transport err: %v", err)
	}
	if accepted {
		t.Fatal("R78 rejection must not be treated as accepted")
	}
	if reason == "" || reason[:3] != "R78" {
		t.Fatalf("reason should carry the R-code, got %q", reason)
	}
}

func TestSubmit_Accept(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", serveFile(t, "order_accept.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, reason, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-2",
	}, "ET-2", "SMART")
	if err != nil {
		t.Fatalf("submit returned transport err: %v", err)
	}
	if !accepted {
		t.Fatalf("expected accepted, reason=%q", reason)
	}
}

// hijackOnceThenServe fails the transport on the FIRST request (by hijacking
// the connection and closing it without writing a response, which surfaces
// to the client as a transport error — no HTTP response at all) and serves
// the named fixture on every subsequent request. This is how we simulate
// TradeZero's "connection dropped, unknown whether the order landed" case
// without ever touching a real network endpoint.
func hijackOnceThenServe(t *testing.T, name string) (http.HandlerFunc, *int32) {
	var calls int32
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("ResponseWriter does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		serveFile(t, name)(w, r)
	}, &calls
}

func alwaysHijack(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
}

func TestSubmit_TransportFail_RetrySameID_R114ProbeMeansAccepted(t *testing.T) {
	mux := http.NewServeMux()
	handler, calls := hijackOnceThenServe(t, "order_reject_r114.json")
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, reason, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-3",
	}, "ET-3", "SMART")
	if err != nil {
		t.Fatalf("submit returned err: %v", err)
	}
	if !accepted {
		t.Fatalf("R114 on retry means the ORIGINAL landed — must be treated as accepted; reason=%q", reason)
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Fatalf("expected exactly 2 attempts (retry-once), got %d", *calls)
	}
}

func TestSubmit_TransportFail_RetryCleanAccept(t *testing.T) {
	mux := http.NewServeMux()
	handler, calls := hijackOnceThenServe(t, "order_accept.json")
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-4",
	}, "ET-4", "SMART")
	if err != nil {
		t.Fatalf("submit returned err: %v", err)
	}
	if !accepted {
		t.Fatal("a clean accept on retry must be treated as accepted")
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Fatalf("expected exactly 2 attempts (retry-once), got %d", *calls)
	}
}

func TestSubmit_TransportFail_BothAttemptsFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", alwaysHijack(t))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-5",
	}, "ET-5", "SMART")
	if err == nil {
		t.Fatal("expected an error when both transport attempts fail")
	}
}

// TestSubmit_HTTPErrorStatus_NeverSilentlyAccepted covers the critical fix:
// a non-200, non-400 status (401/403/5xx — auth failure or a TZ outage) must
// never fall through to a default-accept just because the body doesn't parse
// into the expected orderStatus shape. It must surface as a hard error.
func TestSubmit_HTTPErrorStatus_NeverSilentlyAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"500 with HTML error page", http.StatusInternalServerError, "<html><body>Internal Server Error</body></html>"},
		{"401 with unexpected JSON shape", http.StatusUnauthorized, `{"message":"invalid API key"}`},
		{"503 with empty body", http.StatusServiceUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
			accepted, _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
				Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
				ClientOrderID: "ET-err",
			}, "ET-err", "SMART")
			if err == nil {
				t.Fatalf("expected an error for HTTP %d, got accepted=%v", tc.status, accepted)
			}
			if accepted {
				t.Fatalf("HTTP %d must never be silently treated as accepted", tc.status)
			}
		})
	}
}

// TestSubmit_ExplicitSession_OverridesClock covers the trader's explicit
// session choice taking priority over the clock-inferred (SessionAuto)
// default: RTH forces the base "Day" TIF even during a pre-market clock, and
// Extended forces "Day_Plus" even during an RTH clock.
func TestSubmit_ExplicitSession_OverridesClock(t *testing.T) {
	preMarket := time.Date(2026, 7, 6, 8, 0, 0, 0, mustET(t))
	rth := time.Date(2026, 7, 6, 10, 0, 0, 0, mustET(t))
	for _, tc := range []struct {
		name    string
		t       time.Time
		session exec.OrderSession
		want    string
	}{
		{"RTH override during pre-market clock", preMarket, exec.SessionRTH, "Day"},
		{"Extended override during RTH clock", rth, exec.SessionExtended, "Day_Plus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var gotBody map[string]any
			mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &gotBody)
				serveFile(t, "order_accept.json")(w, r)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(tc.t))
			if _, _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
				Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
				Session: tc.session, Qty: 10, LimitPrice: 100, ClientOrderID: "ET-sess",
			}, "ET-sess", "SMART"); err != nil {
				t.Fatalf("submit returned err: %v", err)
			}
			if gotBody["timeInForce"] != tc.want {
				t.Fatalf("timeInForce = %v, want %q", gotBody["timeInForce"], tc.want)
			}
		})
	}
}

func TestSnapshot_ParsesAccountsPositions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", serveFile(t, "accounts.json"))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"dayPnl":-25.5,"realized":10}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", serveFile(t, "positions.json"))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	acct, pos, orders, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.DayPnL != -25.5 {
		t.Fatalf("dayPnL = %v", acct.DayPnL)
	}
	if acct.Realized != 10 {
		t.Fatalf("realized = %v", acct.Realized)
	}
	if acct.Equity != 100000 || acct.BuyingPower != 200000 || acct.SodEquity != 99000 || acct.Leverage != 2 {
		t.Fatalf("account details not parsed: %+v", acct)
	}
	if len(pos) != 1 || pos[0].Qty != 100 || pos[0].Symbol != "US.AAPL" {
		t.Fatalf("positions = %+v", pos)
	}
	if len(orders) != 0 {
		t.Fatalf("orders = %+v", orders)
	}
}

func TestSnapshot_ShortPositionIsNegativeQty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"GME","side":"Short","shares":50,"priceAvg":20.5}]`))
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, pos, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0].Qty != -50 {
		t.Fatalf("short position must have negative Qty: %+v", pos)
	}
}

func TestSnapshot_AccountEndpoint404IsNonFatal(t *testing.T) {
	// TZ docs: an empty/404 account endpoint is a "platform asleep" state,
	// never a fatal auth failure. Snapshot must still succeed using
	// whatever pnl/positions/orders return.
	mux := http.NewServeMux()
	// No handler registered for the account-details path -> 404 from ServeMux.
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"dayPnl":1.5}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	acct, _, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatalf("account-details 404 must not fail the whole snapshot: %v", err)
	}
	if acct.DayPnL != 1.5 {
		t.Fatalf("dayPnL = %v", acct.DayPnL)
	}
}

func TestCancelOrder_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/ET-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"clientOrderId":"ET-1","orderStatus":"Canceled"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "ET-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCancelOrder_404ThenTerminalInPoll_ResolvesClean(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/ET-2", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"clientOrderId":"ET-2","orderStatus":"Filled"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "ET-2"); err != nil {
		t.Fatalf("a 404 resolved by the poll to a terminal state must not error: %v", err)
	}
}

func TestCancelOrder_404ThenStillWorkingInPoll_RetriesAndSucceeds(t *testing.T) {
	mux := http.NewServeMux()
	var cancelCalls int32
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/ET-3", func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&cancelCalls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"clientOrderId":"ET-3","orderStatus":"Canceled"}`))
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		// The order has now registered but is still working (New) — the
		// documented "cancel immediately after place" race.
		_, _ = w.Write([]byte(`[{"clientOrderId":"ET-3","orderStatus":"New"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "ET-3"); err != nil {
		t.Fatalf("cancel should retry once the poll shows the order registered and working: %v", err)
	}
	if atomic.LoadInt32(&cancelCalls) != 2 {
		t.Fatalf("expected 2 DELETE attempts, got %d", cancelCalls)
	}
}

func TestCancelOrder_404AndNotFoundInPoll_Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/ET-4", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "ET-4"); err == nil {
		t.Fatal("expected an error when the order is nowhere in the truth poll")
	}
}

func TestCancelAll_SendsAccountFormAndSymbolQuery(t *testing.T) {
	mux := http.NewServeMux()
	var gotAccount, gotSymbol, gotMethod string
	mux.HandleFunc("/v1/api/accounts/orders", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotSymbol = r.URL.Query().Get("symbol")
		// r.ParseForm doesn't read the body for DELETE (only POST/PUT/PATCH),
		// so decode the url-encoded form body by hand.
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(b))
		if err != nil {
			t.Fatal(err)
		}
		gotAccount = form.Get("account")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), "AAPL"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotAccount != "2TZ00001" {
		t.Fatalf("account form field = %q", gotAccount)
	}
	if gotSymbol != "AAPL" {
		t.Fatalf("symbol query = %q", gotSymbol)
	}
}

func TestCancelAll_NoSymbolOmitsQuery(t *testing.T) {
	mux := http.NewServeMux()
	var gotRawQuery string
	mux.HandleFunc("/v1/api/accounts/orders", func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if gotRawQuery != "" {
		t.Fatalf("expected no query string, got %q", gotRawQuery)
	}
}

func TestFetchRoutes_PickRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/routes", serveFile(t, "routes.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	routes, err := rc.fetchRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].name() != "SMART" {
		t.Fatalf("routes = %+v", routes)
	}
	if got := rc.pickRoute("Stock"); got != "SMART" {
		t.Fatalf("pickRoute(Stock) = %q, want SMART", got)
	}
	if got := rc.pickRoute("Option"); got != "" {
		t.Fatalf("pickRoute(Option) = %q, want empty (no route supports Option in fixture)", got)
	}
}

func TestFetchRoutes_WrappedShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/routes", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"routes":[{"routeName":"SIM","securityTypes":["Stock"],"orderTypes":["Market"],"timesInForce":["Day"]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	routes, err := rc.fetchRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].name() != "SIM" {
		t.Fatalf("routes = %+v", routes)
	}
	if got := rc.pickRoute("Stock"); got != "SIM" {
		t.Fatalf("pickRoute(Stock) = %q, want SIM (no SMART route exists, falls back)", got)
	}
}

func TestDo_SetsAuthHeaders(t *testing.T) {
	mux := http.NewServeMux()
	var gotKeyID, gotSecret string
	mux.HandleFunc("/v1/api/accounts/2TZ00001/routes", func(w http.ResponseWriter, r *http.Request) {
		gotKeyID = r.Header.Get("TZ-API-KEY-ID")
		gotSecret = r.Header.Get("TZ-API-SECRET-KEY")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "the-key-id", "the-secret", clock.NewFake(time.UnixMilli(0)))
	if _, err := rc.fetchRoutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotKeyID != "the-key-id" || gotSecret != "the-secret" {
		t.Fatalf("auth headers = %q / %q", gotKeyID, gotSecret)
	}
}

// TestSubmitOrder_StripsUSPrefixFromSymbol mirrors the Alpaca fix: eTape's
// domain Symbol always carries the "US." prefix, but TradeZero's asset
// symbols never do. submitOrder must strip it before the request reaches TZ.
func TestSubmitOrder_StripsUSPrefixFromSymbol(t *testing.T) {
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"orderStatus":"New","userOrderId":"2TZ00001:ET-x"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
		ClientOrderID: "ET-x",
	}, "ET-x", "SMART"); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["symbol"].(string); got != "AAPL" {
		t.Fatalf("symbol sent to TradeZero = %q, want %q (US. prefix must be stripped)", got, "AAPL")
	}
}

// TestCancelAll_WithSymbol_StripsUSPrefix mirrors the submit-side fix for the
// symbol-scoped cancel-all query.
func TestCancelAll_WithSymbol_StripsUSPrefix(t *testing.T) {
	mux := http.NewServeMux()
	var gotSymbol string
	mux.HandleFunc("/v1/api/accounts/orders", func(w http.ResponseWriter, r *http.Request) {
		gotSymbol = r.URL.Query().Get("symbol")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), "US.AAPL"); err != nil {
		t.Fatal(err)
	}
	if gotSymbol != "AAPL" {
		t.Fatalf("symbol query sent to TradeZero = %q, want %q", gotSymbol, "AAPL")
	}
}

// TestSnapshot_AddsUSPrefixToPositionAndOrderSymbols proves the inbound half:
// TZ's REST responses carry bare symbols, but every domain Position/Order
// eTape keys state by must carry the "US." prefix.
func TestSnapshot_AddsUSPrefixToPositionAndOrderSymbols(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","side":"Long","shares":10,"priceAvg":100}]`))
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"clientOrderId":"ET1","symbol":"AAPL","orderStatus":"New","orderQuantity":10}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, positions, orders, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].Symbol != "US.AAPL" {
		t.Fatalf("position symbol = %+v, want US.AAPL", positions)
	}
	if len(orders) != 1 || orders[0].Symbol != "US.AAPL" {
		t.Fatalf("order symbol = %+v, want US.AAPL", orders)
	}
}
