package alpaca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// etTime builds a weekday ET time for extended-hours tests below.
func etTime(hour, min int) time.Time {
	return time.Date(2026, 7, 6, hour, min, 0, 0, session.Loc()) // Monday
}

// TestSubmit_StructuredError is the brief's Step 1 test: a 422 with Alpaca's
// documented {code,message} error body must surface as a Go error, never be
// silently treated as a successful submit.
func TestSubmit_StructuredError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"sub-penny increment"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 1.001, ClientOrderID: "ET-x",
	}, "ET-x"); err == nil {
		t.Fatal("422 structured error must surface as an error")
	}
}

// TestAccount_DayPnLFromEquityDelta is the brief's Step 1 test: DayPnL is
// derived as equity - last_equity, not read from a dedicated pnl field
// (Alpaca has none on /v2/account, unlike TradeZero's separate /pnl
// endpoint).
func TestAccount_DayPnLFromEquityDelta(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100050","last_equity":"100000","buying_power":"200000","cash":"50000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	acct, _, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.DayPnL != 50 { // 100050 - 100000
		t.Fatalf("DayPnL = %v want 50", acct.DayPnL)
	}
	if acct.BuyingPower != 200000 || acct.AvailableCash != 50000 || acct.Leverage != 4 || acct.SodEquity != 100000 {
		t.Fatalf("account fields not parsed: %+v", acct)
	}
}

// TestPing_Success verifies ping treats a normal GET /v2/clock 200 as
// reachable — the happy path for the RTT probe (Adapter.ProbeRTT).
func TestPing_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/clock", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"timestamp":"2026-07-09T09:30:00Z","is_open":true,"next_open":"","next_close":""}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.ping(context.Background()); err != nil {
		t.Fatalf("ping should succeed on 200: %v", err)
	}
}

// TestPing_StructuredError verifies a >=400 response surfaces as a real
// error rather than a false "reachable" nil.
func TestPing_StructuredError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/clock", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"code":50000000,"message":"internal error"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.ping(context.Background()); err == nil {
		t.Fatal("500 must surface as an error, not a false-reachable nil")
	}
}

func TestSubmit_Accept(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-42","client_order_id":"ET-2","symbol":"AAPL","side":"buy","order_type":"limit","qty":"10","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Pinned to RTH (10:00 ET) so this test's assertions stay focused on the
	// non-session-dependent fields; extended_hours behavior has its own tests.
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(etTime(10, 0)))
	brokerID, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 190.5049,
	}, "ET-2")
	if err != nil {
		t.Fatal(err)
	}
	if brokerID != "b-42" {
		t.Fatalf("brokerID = %q, want b-42", brokerID)
	}
	if gotBody["limit_price"] != 190.50 {
		t.Fatalf("limit_price not rounded: %v", gotBody["limit_price"])
	}
	if gotBody["client_order_id"] != "ET-2" {
		t.Fatalf("client_order_id = %v", gotBody["client_order_id"])
	}
	if _, has := gotBody["extended_hours"]; has {
		t.Fatalf("RTH order must not set extended_hours: %v", gotBody["extended_hours"])
	}
}

// TestSubmit_StripsUSPrefixFromSymbol proves the domain's "US.AAPL" convention
// never leaks onto the wire: Alpaca's API rejects a "US."-prefixed symbol as
// unknown, so the outbound payload must carry the bare ticker.
func TestSubmit_StripsUSPrefixFromSymbol(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"market","qty":"1","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(etTime(10, 0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 1,
	}, "ET-1"); err != nil {
		t.Fatal(err)
	}
	if gotBody["symbol"] != "AAPL" {
		t.Fatalf("symbol = %v, want bare AAPL (not US.AAPL)", gotBody["symbol"])
	}
}

// TestSubmit_ExtendedHours_LimitOutsideRTH_SetsFlag covers the three sessions
// where Alpaca requires extended_hours=true to work a day/gtc limit order
// immediately instead of queuing it for the next RTH open: pre-market,
// post-market, and (uniquely to Alpaca, via Blue Ocean ATS) overnight.
func TestSubmit_ExtendedHours_LimitOutsideRTH_SetsFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    time.Time
	}{
		{"pre-market", etTime(8, 0)},
		{"post-market", etTime(18, 0)},
		{"overnight", etTime(22, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var gotBody map[string]any
			mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
				_ = jsonDecode(t, r, &gotBody)
				_, _ = w.Write([]byte(`{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","filled_qty":"0","status":"new"}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(tc.t))
			if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
				Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100,
			}, "ET-1"); err != nil {
				t.Fatal(err)
			}
			if gotBody["extended_hours"] != true {
				t.Fatalf("extended_hours = %v, want true", gotBody["extended_hours"])
			}
		})
	}
}

// TestSubmit_RTH_LimitDoesNotSetFlag confirms the flag is omitted (Alpaca
// defaults it to false) once the session is back in RTH.
func TestSubmit_RTH_LimitDoesNotSetFlag(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(etTime(10, 0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100,
	}, "ET-1"); err != nil {
		t.Fatal(err)
	}
	if _, has := gotBody["extended_hours"]; has {
		t.Fatalf("RTH order must not set extended_hours: %v", gotBody["extended_hours"])
	}
}

// TestSubmit_ExtendedHours_MarketDoesNotSetFlag guards the order-type gate:
// Alpaca rejects extended_hours on market orders, so it must never be set
// even during pre/post/overnight sessions.
func TestSubmit_ExtendedHours_MarketDoesNotSetFlag(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"market","qty":"1","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(etTime(8, 0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 1,
	}, "ET-1"); err != nil {
		t.Fatal(err)
	}
	if _, has := gotBody["extended_hours"]; has {
		t.Fatalf("market order must never set extended_hours: %v", gotBody["extended_hours"])
	}
}

// TestSubmit_ExplicitSession_OverridesClock covers the trader's explicit
// session choice taking priority over the clock-inferred (SessionAuto)
// default: RTH forces extended_hours off even during a pre-market clock, and
// Extended forces it on even during an RTH clock.
func TestSubmit_ExplicitSession_OverridesClock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		t       time.Time
		session exec.OrderSession
		want    bool // extended_hours expected value; false means "absent"
	}{
		{"RTH override during pre-market clock", etTime(8, 0), exec.SessionRTH, false},
		{"Extended override during RTH clock", etTime(10, 0), exec.SessionExtended, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var gotBody map[string]any
			mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
				_ = jsonDecode(t, r, &gotBody)
				_, _ = w.Write([]byte(`{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","filled_qty":"0","status":"new"}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(tc.t))
			if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
				Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
				Session: tc.session, Qty: 1, LimitPrice: 100,
			}, "ET-1"); err != nil {
				t.Fatal(err)
			}
			got, has := gotBody["extended_hours"]
			if tc.want && got != true {
				t.Fatalf("extended_hours = %v (has=%v), want true", got, has)
			}
			if !tc.want && has {
				t.Fatalf("extended_hours must be absent, got %v", got)
			}
		})
	}
}

// TestSubmit_HTTPErrorStatus_NeverSilentlyAccepted is the critical
// fail-closed property called out in the task brief, mirroring TradeZero's
// equivalent test: a non-200 status with a body that does NOT match the
// documented {code,message} shape (an HTML error page, an unrelated JSON
// shape, or an empty body) must still surface as a hard error, never fall
// through to a default-accept.
func TestSubmit_HTTPErrorStatus_NeverSilentlyAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"500 with HTML error page", http.StatusInternalServerError, "<html><body>Internal Server Error</body></html>"},
		{"401 with unexpected JSON shape", http.StatusUnauthorized, `{"message":"invalid API key"}`},
		{"503 with empty body", http.StatusServiceUnavailable, ""},
		{"429 rate limited", http.StatusTooManyRequests, `{"code":42910000,"message":"too many requests"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
			brokerID, err := rc.submitOrder(context.Background(), exec.OrderRequest{
				Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
			}, "ET-err")
			if err == nil {
				t.Fatalf("expected an error for HTTP %d, got brokerID=%q", tc.status, brokerID)
			}
			if brokerID != "" {
				t.Fatalf("HTTP %d must never be silently treated as accepted, got brokerID=%q", tc.status, brokerID)
			}
		})
	}
}

// TestSubmit_200ButUnparseableBody covers the other half of "never falls
// through to a default-accept": even a 200 status must not be treated as
// success if the body doesn't actually carry an order id.
func TestSubmit_200ButUnparseableBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	brokerID, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
	}, "ET-shape")
	if err == nil {
		t.Fatalf("expected an error when the 200 body carries no order id, got brokerID=%q", brokerID)
	}
}

func TestReplaceOrder_Success(t *testing.T) {
	mux := http.NewServeMux()
	var gotMethod, gotPath string
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-1r","client_order_id":"ET-1-r1","status":"pending_replace"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	err := rc.replaceOrder(context.Background(), "b-1", "ET-1", exec.ReplaceRequest{Qty: 20, LimitPrice: 190.5049})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/v2/orders/b-1" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody["qty"] != 20.0 || gotBody["limit_price"] != 190.50 {
		t.Fatalf("replace body = %+v", gotBody)
	}
	if _, hasStop := gotBody["stop_price"]; hasStop {
		t.Fatalf("unset stop_price must be omitted, got %+v", gotBody)
	}
}

// TestReplaceOrder_IncludesOriginalClientOrderID guards against Alpaca's
// documented PATCH behavior of auto-generating a brand-new client_order_id
// when the field is omitted from the replace request: this adapter's whole
// id-stability design (brokerIDByClientID, WS "replaced" correlation,
// reconcile) assumes client_order_id never changes across a replace, so the
// PATCH body must always explicitly re-send the ORIGINAL one.
func TestReplaceOrder_IncludesOriginalClientOrderID(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-1r","client_order_id":"ET-original","status":"pending_replace"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.replaceOrder(context.Background(), "b-1", "ET-original", exec.ReplaceRequest{Qty: 15}); err != nil {
		t.Fatal(err)
	}
	if got := gotBody["client_order_id"]; got != "ET-original" {
		t.Fatalf("PATCH body client_order_id = %v, want %q (must be sent explicitly or Alpaca auto-generates a new one)", got, "ET-original")
	}
}

func TestReplaceOrder_ErrorStatusNeverSilentlyAccepted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"order not replaceable"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.replaceOrder(context.Background(), "b-1", "ET-1", exec.ReplaceRequest{Qty: 20}); err == nil {
		t.Fatal("expected a 422 to surface as an error")
	}
}

func TestCancelOrder_Success(t *testing.T) {
	mux := http.NewServeMux()
	var gotMethod string
	mux.HandleFunc("/v2/orders/b-9", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "b-9"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s", gotMethod)
	}
}

func TestCancelOrder_ErrorStatusNeverSilentlyAccepted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders/b-9", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":40410000,"message":"order not found"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelOrder(context.Background(), "b-9"); err == nil {
		t.Fatal("expected a 404 to surface as an error")
	}
}

func TestCancelAll_NoSymbol_SingleDeleteCall(t *testing.T) {
	mux := http.NewServeMux()
	var calls int
	var gotMethod string
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod = r.Method
		_, _ = w.Write([]byte(`[{"id":"b-1","status":200}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 call for account-wide cancel-all, got %d", calls)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", gotMethod)
	}
}

func TestCancelAll_ErrorStatusNeverSilentlyAccepted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<html>gateway error</html>"))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err == nil {
		t.Fatal("expected a 500 to surface as an error")
	}
}

// TestCancelAll_NoSymbol_207PartialFailureIsReported is the critical review
// finding's regression test: Alpaca answers a partially-failed account-wide
// cancel-all with HTTP 207 (an outer status < 400) and a per-item array
// where individual entries carry their own >=400 status. The prior code only
// checked the outer status and returned nil on any <400 response, silently
// reporting a clean success even though one order failed to cancel.
func TestCancelAll_NoSymbol_207PartialFailureIsReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"id":"b-1","status":200},{"id":"b-2","status":500,"body":{"message":"internal error"}}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err == nil {
		t.Fatal("expected a 207 mixed-result cancel-all to surface the failed item as an error")
	}
}

// TestCancelAll_NoSymbol_207AllSuccessIsNotAnError confirms the fix
// discriminates rather than always erroring on 207: an all-success 207 (or
// plain 200) with every item's own status < 400 must still return nil.
func TestCancelAll_NoSymbol_207AllSuccessIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"id":"b-1","status":200},{"id":"b-2","status":200}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err != nil {
		t.Fatalf("expected an all-success 207 to return nil, got %v", err)
	}
}

// TestFlatten_207PartialFailureIsReported mirrors the cancel-all case for
// flatten: flatten is eTape's documented emergency kill-switch, so a
// partial-failure 207 (some positions closed, some not) must surface as an
// error rather than a silent nil that could make an operator believe the
// account is flat during a live incident.
func TestFlatten_207PartialFailureIsReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200},{"symbol":"TSLA","status":422,"body":{"message":"position not found"}}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err == nil {
		t.Fatal("expected a 207 mixed-result flatten to surface the failed item as an error")
	}
}

// TestCancelAll_NoSymbol_UndecodableBodyIsError confirms the call site
// (not just checkBatchItems in isolation) fails closed when the
// account-wide cancel-all's 207/200 body is present but undecodable, per
// the task-13 reviewer's finding.
func TestCancelAll_NoSymbol_UndecodableBodyIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"orders":[{"id":"b-1","status":500}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err == nil {
		t.Fatal("expected an undecodable cancel-all batch body to surface as an error, not silent success")
	}
}

// TestCancelAll_NoSymbol_MissingStatusKeyIsError is the fourth reviewer-found
// gap applied at the cancelAll call site: an item with the `status` key
// entirely omitted must not be mistaken for a genuine success just because
// it decodes cleanly.
func TestCancelAll_NoSymbol_MissingStatusKeyIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"id":"b-1","status":200},{"id":"b-2"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err == nil {
		t.Fatal("expected a cancel-all item with a missing status key to surface as an error, not silent success")
	}
}

// TestCancelAll_NoSymbol_NullStatusIsError is the same gap via explicit
// JSON null.
func TestCancelAll_NoSymbol_NullStatusIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"id":"b-1","status":200},{"id":"b-2","status":null}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), ""); err == nil {
		t.Fatal("expected a cancel-all item with status:null to surface as an error, not silent success")
	}
}

// TestFlatten_207AllSuccessIsNotAnError confirms flatten also discriminates:
// an all-success 207 (or plain 200) must still return nil.
func TestFlatten_207AllSuccessIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200},{"symbol":"TSLA","status":200}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err != nil {
		t.Fatalf("expected an all-success 207 to return nil, got %v", err)
	}
}

// TestCheckBatchItems_EmptyBodyIsSuccess confirms the genuinely-empty cases
// -- no bytes, whitespace-only, the literal `[]`, and the literal `null` --
// all still return nil. This is Alpaca's documented shape for "nothing to
// cancel/flatten" and must not be turned into a false error by the fix
// below.
func TestCheckBatchItems_EmptyBodyIsSuccess(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(""), []byte("   \n\t "), []byte("[]"), []byte("null")} {
		if err := checkBatchItems(body); err != nil {
			t.Fatalf("checkBatchItems(%q) = %v, want nil", body, err)
		}
	}
}

// TestCheckBatchItems_TruncatedArrayIsError is the task-13 reviewer's first
// demonstrated gap: a truncated/malformed JSON array (e.g. a network glitch
// or proxy cutting the response short) previously fell through the blanket
// "any unmarshal error means success" fallback, silently hiding whatever
// failed items were in the part that got cut off. It must now surface as a
// real error.
func TestCheckBatchItems_TruncatedArrayIsError(t *testing.T) {
	body := []byte(`[{"id":"b-1","status":200},{"id":"b-2","status":5`)
	if err := checkBatchItems(body); err == nil {
		t.Fatal("expected a truncated batch array to surface as an error, got nil")
	}
}

// TestCheckBatchItems_WrappedEnvelopeIsError is the reviewer's second
// demonstrated gap: an unexpected envelope shape (e.g. `{"orders":[...]}`
// instead of a bare array) is API version drift, not a "nothing happened"
// signal, and must not be silently treated as success even though it
// contains a failed item's status.
func TestCheckBatchItems_WrappedEnvelopeIsError(t *testing.T) {
	body := []byte(`{"orders":[{"id":"b-1","status":500}]}`)
	if err := checkBatchItems(body); err == nil {
		t.Fatal("expected a wrapped/unexpected envelope shape to surface as an error, got nil")
	}
}

// TestCheckBatchItems_PerItemTypeDriftIsError is the reviewer's third
// demonstrated gap: a single item's status field arriving as a JSON string
// instead of a number fails the whole array's unmarshal due to the type
// mismatch, which previously hid whatever real failures were elsewhere in
// the array. It must now surface as a real error rather than silent success.
func TestCheckBatchItems_PerItemTypeDriftIsError(t *testing.T) {
	body := []byte(`[{"id":"b-1","status":200},{"id":"b-2","status":"500"}]`)
	if err := checkBatchItems(body); err == nil {
		t.Fatal("expected a per-item status type mismatch to surface as an error, got nil")
	}
}

// TestCheckBatchItems_MissingStatusKeyIsError is the fourth reviewer-found
// gap on this same chain: a per-item object with the `status` key omitted
// entirely decodes cleanly (json.Unmarshal leaves a plain int at its Go
// zero value, 0), which is not >= 400 and was previously indistinguishable
// from a genuine status:200 success. It must now surface as a real error --
// a missing status means "cannot confirm success," not "assume success."
func TestCheckBatchItems_MissingStatusKeyIsError(t *testing.T) {
	body := []byte(`[{"id":"b-1","status":200},{"id":"b-2"}]`)
	if err := checkBatchItems(body); err == nil {
		t.Fatal("expected a per-item body with a missing status key to surface as an error, got nil")
	}
}

// TestCheckBatchItems_NullStatusIsError is the same gap via explicit JSON
// null rather than a wholly-absent key: `"status":null` also decodes
// cleanly into a plain int's zero value and must fail closed rather than
// pass through as success.
func TestCheckBatchItems_NullStatusIsError(t *testing.T) {
	body := []byte(`[{"id":"b-1","status":200},{"id":"b-2","status":null}]`)
	if err := checkBatchItems(body); err == nil {
		t.Fatal("expected a per-item body with status:null to surface as an error, got nil")
	}
}

// TestFlatten_UndecodableBodyIsError confirms flatten's call site fails
// closed when the emergency kill-switch's response body is present but
// undecodable (e.g. truncated), per the task-13 reviewer's finding --
// silent success here could make an operator believe the account is flat
// during a live incident when it never was checked at all.
func TestFlatten_UndecodableBodyIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200},{"symbol":"TSLA","status":4`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err == nil {
		t.Fatal("expected an undecodable flatten batch body to surface as an error, not silent success")
	}
}

// TestFlatten_MissingStatusKeyIsError is the fourth reviewer-found gap
// applied at the flatten call site (eTape's documented emergency
// kill-switch): a position-close item with the `status` key entirely
// omitted must fail closed, not be mistaken for a genuine close because it
// happens to decode cleanly.
func TestFlatten_MissingStatusKeyIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200},{"symbol":"TSLA"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err == nil {
		t.Fatal("expected a flatten item with a missing status key to surface as an error, not silent success")
	}
}

// TestFlatten_NullStatusIsError is the same gap via explicit JSON null.
func TestFlatten_NullStatusIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200},{"symbol":"TSLA","status":null}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err == nil {
		t.Fatal("expected a flatten item with status:null to surface as an error, not silent success")
	}
}

// TestCancelAll_WithSymbol_ListsThenCancelsEach verifies the symbol-scoped
// path per the brief: list open orders filtered by symbol, then cancel each
// individually (Alpaca's cancel-all has no symbol filter of its own).
func TestCancelAll_WithSymbol_ListsThenCancelsEach(t *testing.T) {
	mux := http.NewServeMux()
	var gotQuery string
	var canceled []string
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","status":"new"},{"id":"b-2","client_order_id":"ET-2","symbol":"AAPL","status":"new"}]`))
	})
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, r *http.Request) {
		canceled = append(canceled, "b-1")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v2/orders/b-2", func(w http.ResponseWriter, r *http.Request) {
		canceled = append(canceled, "b-2")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), "AAPL"); err != nil {
		t.Fatal(err)
	}
	if gotQuery == "" {
		t.Fatal("expected a query string scoping the list to the symbol")
	}
	if len(canceled) != 2 {
		t.Fatalf("expected both listed orders canceled, got %v", canceled)
	}
}

func TestCancelAll_WithSymbol_PerOrderFailureIsReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","status":"new"}]`))
	})
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"already filled"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), "AAPL"); err == nil {
		t.Fatal("expected the per-order cancel failure to be reported")
	}
}

func TestFlatten_Success(t *testing.T) {
	mux := http.NewServeMux()
	var gotMethod, gotPath string
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","status":200}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v2/positions" {
		t.Fatalf("method/path = %s %s", gotMethod, gotPath)
	}
}

func TestFlatten_ErrorStatusNeverSilentlyAccepted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.flatten(context.Background()); err == nil {
		t.Fatal("expected a 503 with an empty body to surface as an error")
	}
}

func TestSnapshot_ShortPositionSignedQtyRespected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"GME","qty":"-50","side":"short","avg_entry_price":"20.5"}]`))
	})
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, pos, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0].Qty != -50 {
		t.Fatalf("short position must have negative Qty (already signed on the wire): %+v", pos)
	}
}

func TestSnapshot_ShortPositionPositiveQtyWithSideFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"GME","qty":"50","side":"short","avg_entry_price":"20.5"}]`))
	})
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, pos, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0].Qty != -50 {
		t.Fatalf("a positive qty paired with side:short must be negated: %+v", pos)
	}
}

func TestSnapshot_ErrorStatusOnAnyLegFailsTheWholeSnapshot(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":40110000,"message":"invalid credentials"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, _, _, err := rc.snapshot(context.Background()); err == nil {
		t.Fatal("expected a 401 on /v2/account to fail the whole snapshot")
	}
}

func TestOrderByClientID_Found(t *testing.T) {
	mux := http.NewServeMux()
	var gotQuery string
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("client_order_id")
		_, _ = w.Write([]byte(`{"id":"b-7","client_order_id":"ET-7","symbol":"AAPL","side":"buy","order_type":"limit","qty":"5","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	ord, found, err := rc.orderByClientID(context.Background(), "ET-7")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if ord.ID != "b-7" || gotQuery != "ET-7" {
		t.Fatalf("ord=%+v query=%q", ord, gotQuery)
	}
}

func TestOrderByClientID_NotFoundIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, found, err := rc.orderByClientID(context.Background(), "ET-missing")
	if err != nil {
		t.Fatalf("a 404 (order doesn't exist) must not be an error: %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestOrderByClientID_OtherErrorStatusNeverSilentlyFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>error</html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	_, found, err := rc.orderByClientID(context.Background(), "ET-x")
	if err == nil {
		t.Fatal("expected a 500 to surface as an error")
	}
	if found {
		t.Fatal("a 500 must never be reported as found")
	}
}

func TestDo_SetsAuthHeaders(t *testing.T) {
	mux := http.NewServeMux()
	var gotKeyID, gotSecret string
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, r *http.Request) {
		gotKeyID = r.Header.Get("APCA-API-KEY-ID")
		gotSecret = r.Header.Get("APCA-API-SECRET-KEY")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "the-key-id", "the-secret", clock.NewFake(time.UnixMilli(0)))
	if _, _, _, err := rc.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotKeyID != "the-key-id" || gotSecret != "the-secret" {
		t.Fatalf("auth headers = %q / %q", gotKeyID, gotSecret)
	}
}

// TestTokenBucket_SharedAcrossAllEndpoints proves the brief's requirement of
// a SINGLE pooled 200/min bucket across every endpoint (unlike TradeZero's
// per-endpoint buckets): draining the bucket via direct Allow() calls must
// throttle a call to a completely different endpoint, because they share one
// bucket field rather than each having their own.
func TestTokenBucket_SharedAcrossAllEndpoints(t *testing.T) {
	rc := newRESTClient("http://127.0.0.1:0", "K", "S", clock.System{})
	drained := 0
	for rc.bucket.Allow() {
		drained++
		if drained > 100 {
			t.Fatal("bucket did not exhaust its burst")
		}
	}
	if drained != 5 {
		t.Fatalf("expected burst of 5, drained %d tokens", drained)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	// cancelOrder targets a totally different endpoint than whatever drained
	// the bucket above; if it had its own independent bucket it would
	// proceed immediately (and fail on a connection error to the bogus base
	// URL). Instead it must block on the SHARED bucket until the context
	// deadline, because refilling 1 token at ~3.33/s takes ~300ms.
	if err := rc.cancelOrder(ctx, "whatever"); err == nil {
		t.Fatal("expected the shared bucket to block this call until the context deadline")
	}
}

func jsonDecode(t *testing.T, r *http.Request, v *map[string]any) error {
	t.Helper()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
}

// TestSubmitOrder_StripsUSPrefixFromSymbol reproduces a real live rejection
// (docs: "asset \"US.VRAX\" not found") — eTape's domain Symbol always carries
// the "US." prefix (the moomoo-derived convention used throughout the engine
// and UI), but Alpaca's asset symbols never do. submitOrder must strip it
// before the request ever reaches Alpaca's API.
func TestSubmitOrder_StripsUSPrefixFromSymbol(t *testing.T) {
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		if err := jsonDecode(t, r, &body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"b-1"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 1, ClientOrderID: "ET-x",
	}, "ET-x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["symbol"].(string); got != "AAPL" {
		t.Fatalf("symbol sent to Alpaca = %q, want %q (US. prefix must be stripped)", got, "AAPL")
	}
}

// TestCancelAll_WithSymbol_StripsUSPrefix mirrors the submit-side fix for the
// symbol-scoped cancel-all path, which also sends the symbol straight to
// Alpaca's /v2/orders?symbols= query.
func TestCancelAll_WithSymbol_StripsUSPrefix(t *testing.T) {
	var gotSymbols string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		gotSymbols = r.URL.Query().Get("symbols")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.cancelAll(context.Background(), "US.AAPL"); err != nil {
		t.Fatal(err)
	}
	if gotSymbols != "AAPL" {
		t.Fatalf("symbols filter sent to Alpaca = %q, want %q", gotSymbols, "AAPL")
	}
}

// TestSnapshot_AddsUSPrefixToPositionAndOrderSymbols proves the inbound half
// of the same fix: Alpaca's REST responses carry bare symbols, but every
// domain Position/Order eTape keys state by must carry the "US." prefix to
// match the rest of the engine (positions/orders would otherwise never
// reconcile against gate/state lookups keyed by the domain symbol).
func TestSnapshot_AddsUSPrefixToPositionAndOrderSymbols(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100","last_equity":"100","buying_power":"100","cash":"100","multiplier":"1"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","qty":"10","side":"long","avg_entry_price":"190"}]`))
	})
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"b-1","client_order_id":"ET-1","symbol":"AAPL","side":"buy","order_type":"limit","status":"new"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
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
