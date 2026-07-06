package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// waitFor drains ch until pred matches an event, or fails the test after 3s.
// Mirrors tradezero_test.go's helper of the same name (a different package,
// so no import cycle/collision).
func waitFor(t *testing.T, ch <-chan exec.BrokerEvent, pred func(exec.BrokerEvent) bool) exec.BrokerEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			if pred(e) {
				return e
			}
		case <-deadline:
			t.Fatal("waitFor: timed out waiting for a matching event")
			return nil
		}
	}
}

// drainNonBlocking discards whatever is currently queued without blocking.
func drainNonBlocking(ch <-chan exec.BrokerEvent) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// mockOrder is one order tracked by mockAlpacaFull, holding just enough state
// to answer POST/PATCH/DELETE/GET the way Alpaca's real REST surface would.
type mockOrder struct {
	id, clientOrderID, symbol, side, orderType string
	qty, filledQty, filledAvgPrice             float64
	limitPrice, stopPrice                      float64
	status                                     string
}

func (o mockOrder) toAUOrderJSON() []byte {
	m := map[string]any{
		"id": o.id, "client_order_id": o.clientOrderID,
		"symbol": o.symbol, "side": o.side, "order_type": o.orderType,
		"qty": fmt.Sprintf("%v", o.qty), "filled_qty": fmt.Sprintf("%v", o.filledQty),
		"filled_avg_price": fmt.Sprintf("%v", o.filledAvgPrice),
		"limit_price":      fmt.Sprintf("%v", o.limitPrice),
		"stop_price":       fmt.Sprintf("%v", o.stopPrice),
		"status":           o.status,
	}
	b, _ := json.Marshal(m)
	return b
}

// mockAlpacaFull is a combined HTTP+WS mock of Alpaca's REST + trade_updates
// surface, purpose-built to drive the Adapter through submit -> replace ->
// cancel/flatten/snapshot without touching the real endpoint. A single
// httptest.Server multiplexes both: "/stream" upgrades to the trade_updates
// WS and runs the auth->listen handshake (mirroring ws_test.go's handshake),
// everything else is the REST surface (/v2/orders, /v2/orders/{id},
// /v2/orders:by_client_order_id, /v2/account, /v2/positions).
type mockAlpacaFull struct {
	t   *testing.T
	srv *httptest.Server

	httpURL string
	wsURL   string

	mu           sync.Mutex
	conn         *websocket.Conn
	connCtx      context.Context
	nextID       int
	orders       map[string]*mockOrder // by Alpaca id
	byClientID   map[string]string     // client_order_id -> Alpaca id
	lastDeleted  string                // last DELETE /v2/orders/{id} target
	positionsRaw string                // GET /v2/positions response body
	openOrders   func() []mockOrder    // GET /v2/orders?status=open response, computed live
}

func newMockAlpacaFull(t *testing.T) *mockAlpacaFull {
	t.Helper()
	m := &mockAlpacaFull{
		t: t, orders: map[string]*mockOrder{}, byClientID: map[string]string{},
		positionsRaw: `[]`,
	}
	m.openOrders = func() []mockOrder {
		m.mu.Lock()
		defer m.mu.Unlock()
		var out []mockOrder
		for _, o := range m.orders {
			if o.status == "new" || o.status == "accepted" || o.status == "partially_filled" {
				out = append(out, *o)
			}
		}
		return out
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		if _, _, err := c.Read(ctx); err != nil { // auth
			return
		}
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`)); err != nil {
			return
		}
		if _, _, err := c.Read(ctx); err != nil { // listen
			return
		}
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"stream":"listening","data":{"streams":["trade_updates"]}}`)); err != nil {
			return
		}

		m.mu.Lock()
		m.conn = c
		m.connCtx = ctx
		m.mu.Unlock()

		<-ctx.Done()
	})

	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			open := m.openOrders()
			raw := make([]json.RawMessage, 0, len(open))
			for _, o := range open {
				raw = append(raw, o.toAUOrderJSON())
			}
			b, _ := json.Marshal(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)

		case http.MethodPost:
			var body struct {
				Symbol        string  `json:"symbol"`
				Qty           float64 `json:"qty"`
				Side          string  `json:"side"`
				Type          string  `json:"type"`
				ClientOrderID string  `json:"client_order_id"`
				LimitPrice    float64 `json:"limit_price"`
				StopPrice     float64 `json:"stop_price"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)

			m.mu.Lock()
			m.nextID++
			id := fmt.Sprintf("b-%d", m.nextID)
			o := &mockOrder{
				id: id, clientOrderID: body.ClientOrderID, symbol: body.Symbol,
				side: body.Side, orderType: body.Type, qty: body.Qty,
				limitPrice: body.LimitPrice, stopPrice: body.StopPrice, status: "new",
			}
			m.orders[id] = o
			m.byClientID[body.ClientOrderID] = id
			m.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(o.toAUOrderJSON())

			m.pushUpdate("new", o, "")

		case http.MethodDelete:
			// cancel-all: mark every open order canceled.
			m.mu.Lock()
			var canceled []*mockOrder
			for _, o := range m.orders {
				if o.status == "new" || o.status == "accepted" || o.status == "partially_filled" {
					o.status = "canceled"
					canceled = append(canceled, o)
				}
			}
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			for _, o := range canceled {
				m.pushUpdate("canceled", o, "")
			}

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v2/orders/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v2/orders/")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Qty        float64 `json:"qty"`
				LimitPrice float64 `json:"limit_price"`
				StopPrice  float64 `json:"stop_price"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)

			m.mu.Lock()
			o, ok := m.orders[id]
			if ok {
				if body.Qty > 0 {
					o.qty = body.Qty
				}
				if body.LimitPrice > 0 {
					o.limitPrice = body.LimitPrice
				}
				if body.StopPrice > 0 {
					o.stopPrice = body.StopPrice
				}
				o.status = "replaced"
			}
			m.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(o.toAUOrderJSON())
			// Alpaca's native replace: the WS "replaced" event echoes the
			// SAME client_order_id with the updated qty/price (per the
			// design this adapter is built against; see testdata/replaced.json).
			m.pushUpdate("replaced", o, "")

		case http.MethodDelete:
			m.mu.Lock()
			m.lastDeleted = id
			o, ok := m.orders[id]
			if ok {
				o.status = "canceled"
			}
			m.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(o.toAUOrderJSON())
			m.pushUpdate("canceled", o, "")

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("client_order_id")
		m.mu.Lock()
		id, ok := m.byClientID[cid]
		var o *mockOrder
		if ok {
			o = m.orders[id]
		}
		m.mu.Unlock()
		if !ok || o == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(o.toAUOrderJSON())
	})

	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})

	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		m.mu.Lock()
		body := m.positionsRaw
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	m.srv = httptest.NewServer(mux)
	m.httpURL = m.srv.URL
	m.wsURL = "ws" + m.srv.URL[len("http"):] + "/stream"
	return m
}

func (m *mockAlpacaFull) Close() { m.srv.Close() }

// waitConn blocks until the WS handshake has completed (or timeout), so a
// push issued right after dial isn't silently lost to a nil connection.
func (m *mockAlpacaFull) waitConn(timeout time.Duration) (*websocket.Conn, context.Context) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		c, ctx := m.conn, m.connCtx
		m.mu.Unlock()
		if c != nil {
			return c, ctx
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, nil
}

func (m *mockAlpacaFull) pushUpdate(event string, o *mockOrder, executionID string) {
	c, ctx := m.waitConn(2 * time.Second)
	if c == nil {
		m.t.Errorf("mockAlpacaFull: no WS connection to push %s for %s", event, o.clientOrderID)
		return
	}
	env := map[string]any{
		"stream": "trade_updates",
		"data": map[string]any{
			"event":        event,
			"execution_id": executionID,
			"price":        fmt.Sprintf("%v", o.filledAvgPrice),
			"qty":          fmt.Sprintf("%v", o.filledQty),
			"position_qty": fmt.Sprintf("%v", o.filledQty),
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"order": map[string]any{
				"id": o.id, "client_order_id": o.clientOrderID,
				"symbol": o.symbol, "side": o.side, "order_type": o.orderType,
				"qty": fmt.Sprintf("%v", o.qty), "filled_qty": fmt.Sprintf("%v", o.filledQty),
				"filled_avg_price": fmt.Sprintf("%v", o.filledAvgPrice),
				"limit_price":      fmt.Sprintf("%v", o.limitPrice),
				"stop_price":       fmt.Sprintf("%v", o.stopPrice),
				"status":           o.status,
			},
		},
	}
	b, _ := json.Marshal(env)
	_ = c.Write(ctx, websocket.MessageText, b)
}

func (m *mockAlpacaFull) lastDeletedID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDeleted
}

// TestAdapter_NativeReplaceEmitsOrderReplaced is the brief's Step 1 test
// verbatim: submit -> WS "new" -> OrderAccepted; PATCH replace -> WS
// "replaced" -> OrderReplaced, under the SAME domain order id.
func TestAdapter_NativeReplaceEmitsOrderReplaced(t *testing.T) {
	rec := newMockAlpacaFull(t)
	defer rec.Close()
	a, err := New(Config{Venue: "alpaca", Env: "paper", RESTBase: rec.httpURL, WSURL: rec.wsURL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000CC"
	_, _ = a.SubmitOrder(ctx, exec.OrderRequest{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100, ClientOrderID: oid})
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { or, ok := e.(exec.OrderReplaced); return ok && or.OID == oid })
	or := ev.(exec.OrderReplaced)
	if or.NewLimit != 101 {
		t.Fatalf("OrderReplaced.NewLimit = %v, want 101", or.NewLimit)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a, _ := New(Config{Venue: "alpaca", Env: "paper", RESTBase: "http://x", WSURL: "ws://x", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	c := a.Capabilities()
	if !c.NativeReplace || !c.FlattenAll {
		t.Fatalf("caps = %+v", c)
	}
	if !c.OvernightSession {
		t.Fatalf("caps.OvernightSession = false, want true (Blue Ocean ATS)")
	}
}

func TestAdapter_New_RequiresVenue(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected an error for a missing venue")
	}
}

func TestAdapter_New_DefaultsPaperAndLive(t *testing.T) {
	a, err := New(Config{Venue: "alpaca"})
	if err != nil {
		t.Fatal(err)
	}
	if a.rest.base != defaultPaperRESTBase {
		t.Fatalf("default REST base = %q, want paper %q", a.rest.base, defaultPaperRESTBase)
	}
	if a.ws.url != defaultPaperWSURL {
		t.Fatalf("default WS url = %q, want paper %q", a.ws.url, defaultPaperWSURL)
	}

	live, err := New(Config{Venue: "alpaca", Env: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if live.rest.base != defaultLiveRESTBase {
		t.Fatalf("live REST base = %q, want %q", live.rest.base, defaultLiveRESTBase)
	}
	if live.ws.url != defaultLiveWSURL {
		t.Fatalf("live WS url = %q, want %q", live.ws.url, defaultLiveWSURL)
	}
}

// TestAdapter_CancelOrder_TargetsBrokerID proves CancelOrder resolves the
// domain id to Alpaca's OWN order id (not the client_order_id) before
// issuing the DELETE -- Alpaca's DELETE /v2/orders/{id} takes its own id.
func TestAdapter_CancelOrder_TargetsBrokerID(t *testing.T) {
	rec := newMockAlpacaFull(t)
	defer rec.Close()
	a, err := New(Config{Venue: "alpaca", RESTBase: rec.httpURL, WSURL: rec.wsURL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000AA"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100, ClientOrderID: oid}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })

	if err := a.CancelOrder(ctx, oid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oc, ok := e.(exec.OrderCanceled); return ok && oc.OID == oid })

	if got := rec.lastDeletedID(); got != "b-1" {
		t.Fatalf("DELETE targeted %q, want Alpaca's own id %q (not the client_order_id %q)", got, "b-1", oid)
	}
}

// TestAdapter_Flatten_DelegatesToPositionsDelete proves Flatten hits
// Alpaca's native DELETE /v2/positions directly (unlike TradeZero, which has
// no flatten primitive at all).
func TestAdapter_Flatten_DelegatesToPositionsDelete(t *testing.T) {
	var gotMethod, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Flatten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v2/positions" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
}

// TestAdapter_CancelAll_DelegatesToOrdersDelete covers the account-wide
// cancel-all path.
func TestAdapter_CancelAll_DelegatesToOrdersDelete(t *testing.T) {
	var gotMethod, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v2/orders" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
}

// TestAdapter_Snapshot_StampsVenueNoLegStripping proves Snapshot stamps
// venue on every returned struct and passes client_order_id through
// UNCHANGED -- unlike TradeZero's Snapshot, there is no replace-suffix
// stripping because Alpaca's client_order_id never changes across a
// replace.
func TestAdapter_Snapshot_StampsVenueNoLegStripping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","qty":"10","side":"long","avg_entry_price":"190.00"}]`))
	})
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"b-1","client_order_id":"ET1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"10","filled_qty":"0","limit_price":"100","status":"new"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	acct, positions, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.Venue != "alpaca" {
		t.Fatalf("account venue = %q", acct.Venue)
	}
	if len(positions) != 1 || positions[0].Venue != "alpaca" {
		t.Fatalf("positions = %+v", positions)
	}
	if len(orders) != 1 || orders[0].ID != "ET1" || orders[0].Venue != "alpaca" {
		t.Fatalf("orders = %+v, want ID unchanged (ET1)", orders)
	}
}

// TestAdapter_SubmitOrder_RejectedEmitsOrderRejected covers the definitive
// (non-ambiguous) rejection path: a structured >=400 from POST /v2/orders
// means the order never landed and no trade_updates event will EVER follow
// for it, so SubmitOrder itself must be the one place that reports it -- a
// non-error OrderAck{Accepted:false} plus a synchronous OrderRejected,
// mirroring TradeZero's semantic-reject convention.
func TestAdapter_SubmitOrder_RejectedEmitsOrderRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"sub-penny increment"}`))
	})
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	oid := "ET-reject-1"
	ack, err := a.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 1.005, ClientOrderID: oid})
	if err != nil {
		t.Fatalf("expected a non-error Ack for a semantic reject, got err=%v", err)
	}
	if ack.Accepted {
		t.Fatalf("ack = %+v, want Accepted=false", ack)
	}
	ev := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { r, ok := e.(exec.OrderRejected); return ok && r.OID == oid })
	if r := ev.(exec.OrderRejected); r.Reason == "" {
		t.Fatal("expected a non-empty rejection reason")
	}
}

// flakyOnceTransport forwards the first POST to targetPath through, but
// discards the real response and returns a synthetic transport error instead
// -- simulating a lost response AFTER the order actually landed at the
// broker (the exact ambiguity orderByClientID exists to resolve). Every
// other request (the probe GET, subsequent calls) passes through untouched.
type flakyOnceTransport struct {
	mu         sync.Mutex
	targetPath string
	tripped    bool
}

func (f *flakyOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	fire := !f.tripped && req.Method == http.MethodPost && req.URL.Path == f.targetPath
	if fire {
		f.tripped = true
	}
	f.mu.Unlock()
	if !fire {
		return http.DefaultTransport.RoundTrip(req)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
	return nil, errors.New("simulated transport failure: response lost after the order landed")
}

// TestAdapter_SubmitOrder_AmbiguousTransportErrorResolvedByLookup covers the
// OTHER submit-error case: a genuine transport failure loses the HTTP
// response after Alpaca already created the order. SubmitOrder must resolve
// this via orderByClientID rather than either silently reporting failure (the
// order is actually live) or silently reporting success without recording
// the real Alpaca order id.
func TestAdapter_SubmitOrder_AmbiguousTransportErrorResolvedByLookup(t *testing.T) {
	rec := newMockAlpacaFull(t)
	defer rec.Close()
	a, err := New(Config{Venue: "alpaca", RESTBase: rec.httpURL, WSURL: rec.wsURL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	a.rest.hc = &http.Client{Transport: &flakyOnceTransport{targetPath: "/v2/orders"}}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET-ambiguous-1"
	ack, err := a.SubmitOrder(ctx, exec.OrderRequest{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 5, LimitPrice: 100, ClientOrderID: oid})
	if err != nil {
		t.Fatalf("expected the ambiguity to resolve to accepted, got err=%v", err)
	}
	if !ack.Accepted {
		t.Fatalf("ack = %+v, want Accepted=true (the order landed despite the lost response)", ack)
	}

	a.mu.Lock()
	brokerID, tracked := a.brokerIDByClientID[oid]
	a.mu.Unlock()
	if !tracked || brokerID == "" {
		t.Fatalf("brokerIDByClientID[%q] = %q, tracked=%v, want a real Alpaca id recorded via the lookup", oid, brokerID, tracked)
	}

	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })
}

// TestAdapter_Reconcile_NoDuplicateOnUnchangedSnapshot drives handleConn
// directly (bypassing the WS handshake, same pattern as
// tradezero_test.go's reconcile tests) across repeated "connects" against a
// REST mock whose /v2/orders?status=open response is fully controlled by the
// test, proving: (1) the first connect seeds state with no StreamGap, (2) a
// reconnect with an unchanged open-orders snapshot produces ONLY
// BrokerAccount + BrokerPositions + StreamGap -- no spurious fill/cancel --
// and (3) a reconnect where an open order's filled_qty genuinely advanced
// synthesizes exactly one catch-up OrderFilled, which a LATER unchanged
// reconnect does not re-fire.
func TestAdapter_Reconcile_NoDuplicateOnUnchangedSnapshot(t *testing.T) {
	var mu sync.Mutex
	ordersBody := `[{"id":"b-1","client_order_id":"ET1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"10","filled_qty":"0","limit_price":"100","status":"new"}]`
	setOrders := func(s string) { mu.Lock(); ordersBody = s; mu.Unlock() }
	getOrders := func() string { mu.Lock(); defer mu.Unlock(); return ordersBody }

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(getOrders())) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// First connect: seeds state, no StreamGap.
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	select {
	case e := <-a.Events():
		if _, ok := e.(exec.StreamGap); ok {
			t.Fatal("StreamGap fired on the very first connect")
		}
	case <-time.After(150 * time.Millisecond):
	}
	drainNonBlocking(a.Events())

	// Reconnect with an UNCHANGED snapshot: only the reconcile boilerplate,
	// no fill/cancel.
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		t.Fatalf("unexpected event on an unchanged reconcile: %+v", e)
	case <-time.After(150 * time.Millisecond):
	}

	// The order partially filled while "disconnected".
	setOrders(`[{"id":"b-1","client_order_id":"ET1","symbol":"AAPL","side":"buy","order_type":"limit","qty":"10","filled_qty":"4","filled_avg_price":"100.5","status":"partially_filled"}]`)
	a.handleConn(false)
	a.handleConn(true)
	fillEv := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.OrderFilled); return ok })
	fill := fillEv.(exec.OrderFilled)
	if fill.F.OrderID != "ET1" || fill.F.Qty != 4 || fill.CumQty != 4 {
		t.Fatalf("synthesized catch-up fill = %+v", fill)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	drainNonBlocking(a.Events())

	// Another reconnect with the SAME (now-unchanged) snapshot: the fill
	// must not re-fire.
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		if _, ok := e.(exec.OrderFilled); ok {
			t.Fatalf("fill re-emitted on an unchanged reconcile snapshot: %+v", e)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// TestAdapter_Reconcile_MissingOrderResolvesTerminalStatus covers the other
// reconcile half: an order that was tracked as Working() before a reconnect
// but is ABSENT from the fresh status=open list (Alpaca's open-orders
// endpoint omits terminal orders entirely, unlike TradeZero's unfiltered
// blotter) must have its real terminal state resolved via
// orderByClientID, and a LATER reconnect must not re-resolve/re-emit it
// (lastKnownStatus is no longer Working() once resolved).
func TestAdapter_Reconcile_MissingOrderResolvesTerminalStatus(t *testing.T) {
	lookupCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }) // always empty: nothing open
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		lookupCount++
		_, _ = w.Write([]byte(`{"id":"b-9","client_order_id":"ET9","symbol":"TSLA","side":"sell","order_type":"limit","qty":"25","filled_qty":"0","limit_price":"250","status":"canceled"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// Simulate: this adapter previously tracked ET9 as Accepted (e.g. via a
	// prior SubmitOrder/handleUpdate), and this is a reconnect.
	a.mu.Lock()
	a.lastKnownStatus["ET9"] = exec.StatusAccepted
	a.lastKnownFilledQty["ET9"] = 0
	a.connectedOnce = true
	a.mu.Unlock()

	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oc, ok := e.(exec.OrderCanceled); return ok && oc.OID == "ET9" })
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	drainNonBlocking(a.Events())

	// A second reconnect must NOT re-resolve (lastKnownStatus["ET9"] is now
	// Canceled, no longer Working()) -- no second lookup, no second event.
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		t.Fatalf("unexpected event on the second reconnect: %+v", e)
	case <-time.After(150 * time.Millisecond):
	}
	if lookupCount != 1 {
		t.Fatalf("orderByClientID called %d times, want exactly 1 (no re-resolution of an already-terminal order)", lookupCount)
	}
}

// TestAdapter_Reconcile_MissingOrderAfterReplaceStillResolvesTerminalStatus
// mirrors TestAdapter_Reconcile_MissingOrderResolvesTerminalStatus but seeds
// lastKnownStatus as exec.StatusReplaced (the state a just-replaced order is
// tracked under, per handleUpdate's restOrderStatusDomain("replaced") ->
// StatusReplaced) instead of StatusAccepted. Before the isWorkingStatus fix,
// StatusReplaced was excluded from the "still working" set, so an order that
// replaced then went terminal (filled/canceled/rejected) while disconnected
// would never be checked against the open-orders list on reconnect -- its
// disappearance would be silently ignored forever, AND lastKnownStatus would
// stay stuck at StatusReplaced permanently, excluding it from every FUTURE
// reconcile too. This proves the fix: the missing, just-replaced order IS
// detected and resolved to its real terminal status.
func TestAdapter_Reconcile_MissingOrderAfterReplaceStillResolvesTerminalStatus(t *testing.T) {
	lookupCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }) // always empty: nothing open
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		lookupCount++
		// Filled at the broker while disconnected, after having replaced
		// earlier in its life.
		_, _ = w.Write([]byte(`{"id":"b-9","client_order_id":"ET9","symbol":"TSLA","side":"sell","order_type":"limit","qty":"25","filled_qty":"25","filled_avg_price":"251.5","status":"filled"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// Simulate: a prior WS "replaced" event set lastKnownStatus["ET9"] to
	// StatusReplaced (handleUpdate's normal bookkeeping), and the connection
	// then dropped before any further event -- the order went on to fill at
	// the broker while disconnected.
	a.mu.Lock()
	a.lastKnownStatus["ET9"] = exec.StatusReplaced
	a.lastKnownFilledQty["ET9"] = 0
	a.connectedOnce = true
	a.mu.Unlock()

	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	fillEv := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.OrderFilled); return ok })
	fill := fillEv.(exec.OrderFilled)
	if fill.F.OrderID != "ET9" || fill.CumQty != 25 {
		t.Fatalf("resolved catch-up fill = %+v, want the fill that happened while disconnected after the replace", fill)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	drainNonBlocking(a.Events())

	if lookupCount != 1 {
		t.Fatalf("orderByClientID called %d times, want exactly 1", lookupCount)
	}

	a.mu.Lock()
	st := a.lastKnownStatus["ET9"]
	a.mu.Unlock()
	if st != exec.StatusFilled {
		t.Fatalf("lastKnownStatus[ET9] = %v, want StatusFilled -- must not stay stuck at StatusReplaced", st)
	}

	// A second reconnect must NOT re-resolve (now genuinely terminal) -- no
	// second lookup, no second event.
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		t.Fatalf("unexpected event on the second reconnect: %+v", e)
	case <-time.After(150 * time.Millisecond):
	}
	if lookupCount != 1 {
		t.Fatalf("orderByClientID called %d times after the second reconnect, want still exactly 1", lookupCount)
	}
}

// TestAdapter_Reconcile_MissingOrderStillMidReplaceIsRetriedNotDropped covers
// resolveMissingOrder's own StatusReplaced case: orderByClientID itself can
// answer with an order still showing status "replaced"/"pending_replace" (a
// genuinely non-terminal, transitional wire state, not yet the order's real
// fate). That must not be silently dropped either -- no event is emitted for
// it, but the id stays "working" (isWorkingStatus(StatusReplaced) == true)
// so the NEXT reconcile retries the lookup instead of abandoning the order.
func TestAdapter_Reconcile_MissingOrderStillMidReplaceIsRetriedNotDropped(t *testing.T) {
	lookupCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }) // always empty: nothing open
	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		lookupCount++
		_, _ = w.Write([]byte(`{"id":"b-9","client_order_id":"ET9","symbol":"TSLA","side":"sell","order_type":"limit","qty":"25","filled_qty":"0","limit_price":"250","status":"pending_replace"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "alpaca", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	a.mu.Lock()
	a.lastKnownStatus["ET9"] = exec.StatusAccepted
	a.lastKnownFilledQty["ET9"] = 0
	a.connectedOnce = true
	a.mu.Unlock()

	// First reconnect: orderByClientID answers "pending_replace" -- no
	// terminal event, but the id must remain trackable.
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		t.Fatalf("unexpected terminal event while still mid-replace: %+v", e)
	case <-time.After(150 * time.Millisecond):
	}
	if lookupCount != 1 {
		t.Fatalf("orderByClientID called %d times after first reconnect, want 1", lookupCount)
	}

	// Second reconnect: the id must still be re-examined (not permanently
	// dropped) -- a second lookup call proves it wasn't abandoned.
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	if lookupCount != 2 {
		t.Fatalf("orderByClientID called %d times after second reconnect, want 2 (still working, must be retried)", lookupCount)
	}
}
