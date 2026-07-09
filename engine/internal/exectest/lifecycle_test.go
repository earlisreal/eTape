// Package exectest is the Task 16 integration capstone: it wires the REAL
// exec.Core (Plan 4) against the REAL tradezero and alpaca adapters (Tasks
// 6-15) — never the adapters' own unit tests in isolation — and proves a
// scripted order lifecycle (arm -> submit -> replace -> cancel -> kill) works
// end to end through the actual coordinator.
//
// Both venues in the mock-server test below talk to in-process httptest/WS
// mocks defined in this file, never a real broker endpoint. TradeZero has NO
// real-integration test anywhere in this plan (only LIVE keys exist for it;
// see CLAUDE.md's safety rule) — this package's mock leg is TZ's only
// exec.Core-level coverage. Alpaca additionally gets an opt-in real-paper
// test (alpacapaper_test.go), gated behind ETAPE_ALPACA_PAPER=1 and never run
// automatically.
package exectest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// ---------------------------------------------------------------------------
// Mock servers.
//
// These are intentionally NOT imported from the tradezero/alpaca packages'
// own _test.go files (mockTZFull, mockAlpacaFull): those are unexported test
// helpers private to package-internal tests, and exporting them would mean
// carving new tztest/altest packages out of already-reviewed, already-merged
// Task 10/15 code just to save ~150 lines here. Duplicating a trimmed subset
// — scoped to exactly what this capstone drives (TZ: handshake + snapshot
// only, since the script never submits a TZ order; Alpaca: the full
// submit/replace/cancel/cancel-all surface) — is the less invasive path the
// brief explicitly allows ("duplicate the minimal handlers in exectest —
// either is fine as long as no real endpoint is contacted").
// ---------------------------------------------------------------------------

// tzStub is a minimal in-process mock of TradeZero's REST + Portfolio-WS
// surface. This capstone test never submits a TZ order (the scripted
// lifecycle runs entirely on the Alpaca leg per the brief), so tzStub only
// needs to satisfy: the WS 3-step handshake (Run must not block/error) and
// the REST snapshot endpoints Core.Recover's Snapshot call and every
// reconnect's reconcile() hit. It never contacts webapi.tradezero.com.
type tzStub struct {
	srv     *httptest.Server
	HTTPURL string
	WSURL   string
}

func startTZStub(t *testing.T) *tzStub {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`)); err != nil {
			return
		}
		if _, auth, err := c.Read(ctx); err != nil || !json.Valid(auth) {
			_ = c.Close(websocket.StatusInternalError, "bad auth")
			return
		}
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`)); err != nil {
			return
		}
		if _, _, err := c.Read(ctx); err != nil { // subscribe payload
			return
		}
		<-ctx.Done()
	})

	empty := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/v1/api/account/2TZ00001", empty(`{}`))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", empty(`{}`))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", empty(`[]`))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", empty(`[]`))
	mux.HandleFunc("/v1/api/accounts/orders", empty(`{"message":"ok"}`)) // kill-switch CancelAll

	srv := httptest.NewServer(mux)
	return &tzStub{srv: srv, HTTPURL: srv.URL, WSURL: "ws" + srv.URL[len("http"):] + "/stream"}
}

func (m *tzStub) Close() { m.srv.Close() }

// alMockOrder is one order tracked by alStub, holding just enough state to
// answer POST/PATCH/DELETE/GET the way Alpaca's real REST surface would.
type alMockOrder struct {
	id, clientOrderID, symbol, side, orderType string
	qty, filledQty, filledAvgPrice             float64
	limitPrice, stopPrice                      float64
	status                                     string
}

func (o alMockOrder) toJSON() []byte {
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

// alStub is a minimal in-process mock of Alpaca's REST + trade_updates
// surface, trimmed from the pattern in alpaca_test.go's mockAlpacaFull to
// what this capstone's submit -> replace -> cancel -> kill script drives.
// Never contacts a real Alpaca endpoint.
type alStub struct {
	t   *testing.T
	srv *httptest.Server

	HTTPURL string
	WSURL   string

	mu         sync.Mutex
	conn       *websocket.Conn
	connCtx    context.Context
	nextID     int
	orders     map[string]*alMockOrder // by Alpaca broker id
	byClientID map[string]string       // client_order_id -> broker id
}

func startAlStub(t *testing.T) *alStub {
	t.Helper()
	m := &alStub{t: t, orders: map[string]*alMockOrder{}, byClientID: map[string]string{}}

	openOrders := func() []alMockOrder {
		m.mu.Lock()
		defer m.mu.Unlock()
		var out []alMockOrder
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
		m.conn, m.connCtx = c, ctx
		m.mu.Unlock()
		<-ctx.Done()
	})

	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			open := openOrders()
			raw := make([]json.RawMessage, 0, len(open))
			for _, o := range open {
				raw = append(raw, o.toJSON())
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
			o := &alMockOrder{
				id: id, clientOrderID: body.ClientOrderID, symbol: body.Symbol,
				side: body.Side, orderType: body.Type, qty: body.Qty,
				limitPrice: body.LimitPrice, stopPrice: body.StopPrice, status: "new",
			}
			m.orders[id] = o
			m.byClientID[body.ClientOrderID] = id
			m.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(o.toJSON())
			m.pushUpdate("new", o)

		case http.MethodDelete: // cancel-all (kill switch)
			m.mu.Lock()
			var canceled []*alMockOrder
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
				m.pushUpdate("canceled", o)
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
			_, _ = w.Write(o.toJSON())
			m.pushUpdate("replaced", o)

		case http.MethodDelete:
			m.mu.Lock()
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
			_, _ = w.Write(o.toJSON())
			m.pushUpdate("canceled", o)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v2/orders:by_client_order_id", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("client_order_id")
		m.mu.Lock()
		id, ok := m.byClientID[cid]
		var o *alMockOrder
		if ok {
			o = m.orders[id]
		}
		m.mu.Unlock()
		if !ok || o == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(o.toJSON())
	})

	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"equity":"100000","last_equity":"99000","buying_power":"400000","cash":"100000","multiplier":"4"}`))
	})

	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	m.srv = httptest.NewServer(mux)
	m.HTTPURL = m.srv.URL
	m.WSURL = "ws" + m.srv.URL[len("http"):] + "/stream"
	return m
}

func (m *alStub) Close() { m.srv.Close() }

// waitConn blocks until the WS handshake has completed (or timeout), so a
// push issued right after dial isn't silently lost to a nil connection.
func (m *alStub) waitConn(timeout time.Duration) (*websocket.Conn, context.Context) {
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

func (m *alStub) pushUpdate(event string, o *alMockOrder) {
	c, ctx := m.waitConn(2 * time.Second)
	if c == nil {
		m.t.Errorf("alStub: no WS connection to push %s for %s", event, o.clientOrderID)
		return
	}
	env := map[string]any{
		"stream": "trade_updates",
		"data": map[string]any{
			"event":        event,
			"execution_id": "",
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

// waitUpdate drains c.Updates() until pred matches, or fails the test after a
// timeout.
func waitUpdate(t *testing.T, c *exec.Core, pred func(exec.Update) bool) exec.Update {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			if pred(u) {
				return u
			}
		case <-deadline:
			t.Fatal("waitUpdate: timed out waiting for a matching update")
			return nil
		}
	}
}

// TestLifecycle_LimitReplaceCancelKill_ThroughCore is the plan's final
// verification gate: a real exec.Core, wired with a real store.Open'd SQLite
// DB and real tradezero/alpaca adapters (both talking only to the in-process
// mocks above), driven through arm -> submit a limit order -> replace ->
// cancel -> kill switch, asserting on Core.Updates() at every step. Neither
// adapter ever contacts a real broker endpoint.
func TestLifecycle_LimitReplaceCancelKill_ThroughCore(t *testing.T) {
	clk := clock.System{} // adapters use real WS timing; store uses it too
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "life.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tzMock := startTZStub(t)
	defer tzMock.Close()
	alMock := startAlStub(t)
	defer alMock.Close()

	tz, err := tradezero.New(tradezero.Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: tzMock.HTTPURL, WSURL: tzMock.WSURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	al, err := alpaca.New(alpaca.Config{
		Venue: "alpaca", Env: "paper", RESTBase: alMock.HTTPURL, WSURL: alMock.WSURL,
		Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go tz.Run(ctx)
	go al.Run(ctx)

	c := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"tz", "alpaca"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxSymbolPositionShares: 10_000},
			Venue: map[exec.VenueID]exec.VenueLimits{
				"tz":     {MaxOrderValue: 1_000_000, MaxOpenOrders: 50},
				"alpaca": {MaxOrderValue: 1_000_000, MaxOpenOrders: 50},
			},
		},
		Store: st, Brokers: map[exec.VenueID]exec.Broker{"tz": tz, "alpaca": al},
		Clock: clk, IDGen: exec.NewOrderIDGen(clk, rand.New(rand.NewSource(7))),
	})
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()

	c.Do(exec.Arm{})

	// far-from-market limit on Alpaca (mock keeps it working)
	ack := c.Do(exec.SubmitOrder{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 1})
	if !ack.Accepted {
		t.Fatalf("submit blocked: %q", ack.Reason)
	}
	oid := ack.OrderID
	waitUpdate(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == oid && o.Order.Status == exec.StatusAccepted
	})

	c.Do(exec.ReplaceOrder{Venue: "alpaca", OrderID: oid, Qty: 10, LimitPrice: 2})
	waitUpdate(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == oid && o.Order.LimitPrice == 2
	})

	c.Do(exec.CancelOrder{Venue: "alpaca", OrderID: oid})
	waitUpdate(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == oid && o.Order.Status == exec.StatusCanceled
	})

	// kill switch: master disarm + cancel-all on both venues
	c.Do(exec.KillSwitch{})
	if c.Do(exec.SubmitOrder{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 1}).Accepted {
		t.Fatal("submit after kill must be blocked (master disarmed)")
	}
}
