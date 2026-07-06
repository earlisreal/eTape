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
)

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

func TestSubmit_Accept(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]any
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = jsonDecode(t, r, &gotBody)
		_, _ = w.Write([]byte(`{"id":"b-42","client_order_id":"ET-2","symbol":"AAPL","side":"buy","order_type":"limit","qty":"10","filled_qty":"0","status":"new"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
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
	err := rc.replaceOrder(context.Background(), "b-1", exec.ReplaceRequest{Qty: 20, LimitPrice: 190.5049})
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

func TestReplaceOrder_ErrorStatusNeverSilentlyAccepted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders/b-1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"order not replaceable"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if err := rc.replaceOrder(context.Background(), "b-1", exec.ReplaceRequest{Qty: 20}); err == nil {
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
