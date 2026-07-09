package tradezero

import (
	"context"
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
	"github.com/earlisreal/eTape/engine/internal/creds"
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
	if len(pos) != 1 || pos[0].Qty != 100 || pos[0].Symbol != "AAPL" {
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

// TestListAccounts_ParsesAccountTypeAndID covers the happy path FetchAccounts
// (tradezero.go) relies on: the list-all endpoint's bare-array response
// decodes into tzAccount rows carrying both accountId and accountType, so a
// caller can auto-fill env + account id without the user typing either.
func TestListAccounts_ParsesAccountTypeAndID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts", serveFile(t, "accounts.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accounts, err := rc.listAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %+v, want 1 row", accounts)
	}
	if accounts[0].AccountID != "2TZ00001" || accounts[0].AccountType != "Live" {
		t.Fatalf("account = %+v, want accountId=2TZ00001 accountType=Live", accounts[0])
	}
}

// TestListAccounts_EmptyArray_ReturnsEmptySliceNoError covers TradeZero's
// documented "platform asleep" shape (docs/2026-07-03-tradezero-api.md):
// GET /v1/api/accounts can return HTTP 200 with an empty array even for
// valid keys. This must not be an error, and must not return a nil slice
// that would behave differently from an empty one under len() == 0 checks.
func TestListAccounts_EmptyArray_ReturnsEmptySliceNoError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accounts, err := rc.listAccounts(context.Background())
	if err != nil {
		t.Fatalf("empty accounts array must not be an error: %v", err)
	}
	if accounts == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %+v, want empty", accounts)
	}
}

// TestListAccounts_ErrorStatus covers the "wrong/expired key" half: a >=400
// response is a real error, never a silent empty-success.
func TestListAccounts_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid API key"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accounts, err := rc.listAccounts(context.Background())
	if err == nil {
		t.Fatalf("expected a 401 to surface as an error, got accounts=%+v", accounts)
	}
	if accounts != nil {
		t.Fatalf("a 401 must never return non-nil accounts, got %+v", accounts)
	}
}

// TestListAccounts_NoAccountIDInPath asserts listAccounts never puts an
// account id in the request path — the whole point of the list-all endpoint
// is that it needs none. A stray "/v1/api/accounts/" (empty segment) or any
// other path would 404 against the mux below, which only registers the bare
// "/v1/api/accounts".
func TestListAccounts_NoAccountIDInPath(t *testing.T) {
	mux := http.NewServeMux()
	var gotPath string
	mux.HandleFunc("/v1/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "", "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, err := rc.listAccounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/api/accounts" {
		t.Fatalf("path = %q, want /v1/api/accounts", gotPath)
	}
}

// TestFetchAccounts_ReturnsPromptly_NoAdapterNoGoroutine is the one
// network-free property FetchAccounts (tradezero.go) can be checked against
// without a RESTBase override: like Alpaca's VerifyCredentials, its mandated
// signature (ctx, cr, clk — no base-URL parameter) always targets TZ's real
// production host, so it genuinely cannot be pointed at an httptest server
// here; listAccounts above already covers the actual HTTP decode/error
// behavior directly against a mock server. What this test guards is that
// FetchAccounts is a bare, synchronous REST call — never going through New
// (which would panic/error over the missing AccountID this call
// deliberately omits) and never leaking a goroutine that ignores ctx — by
// checking it returns promptly on an already-canceled context.
func TestFetchAccounts_ReturnsPromptly_NoAdapterNoGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := FetchAccounts(ctx, creds.Pair{KeyID: "K", SecretKey: "S"}, clock.NewFake(time.UnixMilli(0)))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a canceled context to surface as an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchAccounts did not return promptly on a canceled context")
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
