package tradezero

import (
	"context"
	"encoding/json"
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

// mockTZFull is a combined HTTP+WS mock of TradeZero's REST + Portfolio-WS
// surface, purpose-built to drive the Adapter through a full submit ->
// replace lifecycle without ever touching the real endpoint. A single
// httptest.Server multiplexes both: "/stream" upgrades to the Portfolio WS
// and runs the 3-step handshake, everything else is the REST surface.
//
// The mock intentionally mirrors the brief: POST /order just records the
// posted clientOrderId and accepts it; a POST whose clientOrderId carries the
// "-rN" replace suffix additionally pushes a WS "New" frame for it (proving
// the resubmitted leg's inbound status still resolves to the stable domain
// id via domainID's suffix-stripping); DELETE /orders/{id} accepts the
// cancel and pushes a WS "Canceled" frame for that id (the real terminal
// confirmation ReplaceOrder awaits). Account/position/order snapshot GETs
// return empty per the brief.
type mockTZFull struct {
	t   *testing.T
	srv *httptest.Server

	httpURL string
	wsURL   string

	mu           sync.Mutex
	conn         *websocket.Conn
	connCtx      context.Context
	lastCID      string
	lastDeleteID string
}

func newMockTZFull(t *testing.T) *mockTZFull {
	t.Helper()
	m := &mockTZFull{t: t}

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

		m.mu.Lock()
		m.conn = c
		m.connCtx = ctx
		m.mu.Unlock()

		<-ctx.Done()
	})

	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientOrderID string `json:"clientOrderId"`
			Symbol        string `json:"symbol"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)

		m.mu.Lock()
		m.lastCID = body.ClientOrderID
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderStatus":"New"}`))

		if strings.Contains(body.ClientOrderID, "-r") {
			// The resubmitted leg of an emulated replace: push the WS "New"
			// confirmation the way a real Portfolio WS would.
			m.pushOrder(body.ClientOrderID, body.Symbol, "New")
		}
	})

	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/api/accounts/2TZ00001/orders/")
		m.mu.Lock()
		m.lastDeleteID = id
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clientOrderId":"` + id + `","orderStatus":"PendingCancel"}`))
		m.pushOrder(id, "AAPL", "Canceled")
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

	m.srv = httptest.NewServer(mux)
	m.httpURL = m.srv.URL
	m.wsURL = "ws" + m.srv.URL[len("http"):] + "/stream"
	return m
}

func (m *mockTZFull) Close() { m.srv.Close() }

// waitConn blocks until the WS handshake has completed (or timeout), so a
// push issued right after dial isn't silently lost to a nil connection.
func (m *mockTZFull) waitConn(timeout time.Duration) (*websocket.Conn, context.Context) {
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

func (m *mockTZFull) pushOrder(cid, symbol, status string) {
	c, ctx := m.waitConn(2 * time.Second)
	if c == nil {
		m.t.Errorf("mockTZFull: no WS connection to push %s/%s", cid, status)
		return
	}
	if symbol == "" {
		symbol = "AAPL"
	}
	frame := fmt.Sprintf(
		`{"action":"update","userOrderId":"2TZ00001:%s","symbol":"%s","orderStatus":"%s","orderQuantity":10,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`,
		cid, symbol, status,
	)
	_ = c.Write(ctx, websocket.MessageText, []byte(frame))
}

func (m *mockTZFull) lastClientOrderID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastCID
}

func (m *mockTZFull) lastDeletedID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDeleteID
}

// waitFor drains ch until pred matches an event, or fails the test after 3s.
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

func TestAdapter_EmulatedReplace_StableDomainID(t *testing.T) {
	rec := newMockTZFull(t)
	defer rec.Close()

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: rec.httpURL, WSURL: rec.wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000AA"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err != nil {
		t.Fatal(err)
	}
	// Domain sees a replace on the SAME id; no bare cancel leaks.
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		or, ok := e.(exec.OrderReplaced)
		return ok && or.OID == oid && or.NewLimit == 101
	})
	if last := rec.lastClientOrderID(); last != oid+"-r1" {
		t.Fatalf("resubmit clientOrderId = %q, want %q", last, oid+"-r1")
	}
}

// TestAdapter_EmulatedReplace_NoBareCancelLeaksToDomain drains every event the
// adapter emits across the whole submit->replace flow and asserts that no
// OrderCanceled ever appears for oid — the old TZ leg's terminal Canceled must
// be fully swallowed by onCanceled while the replace is in flight, not just
// "eventually superseded" by a later OrderReplaced.
func TestAdapter_EmulatedReplace_NoBareCancelLeaksToDomain(t *testing.T) {
	rec := newMockTZFull(t)
	defer rec.Close()

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: rec.httpURL, WSURL: rec.wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000BB"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 102}); err != nil {
		t.Fatal(err)
	}
	replaced := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		or, ok := e.(exec.OrderReplaced)
		return ok && or.OID == oid
	})
	_ = replaced

	// Drain whatever else has queued up (e.g. the async WS "New" push for the
	// new leg) with a short grace window, asserting none of it is a bare
	// OrderCanceled for oid.
	grace := time.After(300 * time.Millisecond)
	for {
		select {
		case e := <-a.Events():
			if oc, ok := e.(exec.OrderCanceled); ok && oc.OID == oid {
				t.Fatalf("bare OrderCanceled leaked to the domain during an emulated replace: %+v", oc)
			}
		case <-grace:
			return
		}
	}
}

// TestAdapter_ReplaceOrder_TimesOutIfCancelNeverConfirmed covers the abort
// path: TZ acknowledges the cancel over HTTP (200) but the mock deliberately
// never pushes the terminal WS "Canceled" frame ReplaceOrder awaits. The
// replace must time out, report an error, and never resubmit — the domain
// order is left exactly as it was (still working under its original TZ id).
func TestAdapter_ReplaceOrder_TimesOutIfCancelNeverConfirmed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		if _, auth, err := c.Read(ctx); err != nil || !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
		<-ctx.Done()
	})
	var submitCount int
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, _ *http.Request) {
		submitCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderStatus":"New"}`))
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Accept the cancel over HTTP but never push the WS confirmation.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderStatus":"PendingCancel"}`))
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
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):] + "/stream"

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, WSURL: wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.replaceCancelTimeout = 300 * time.Millisecond // keep the test fast
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000CC"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err == nil {
		t.Fatal("expected ReplaceOrder to error when the cancel is never confirmed")
	}
	if submitCount != 1 {
		t.Fatalf("resubmit must never happen when the cancel wasn't confirmed; POST /order called %d times", submitCount)
	}

	// A second replace attempt must be possible (the replacing flag was
	// cleared on abort, not left stuck).
	a.replaceCancelTimeout = 300 * time.Millisecond
	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 103}); err == nil {
		t.Fatal("expected the second replace attempt to also time out (mock never confirms cancels)")
	}
}

func TestAdapter_Capabilities_And_FlattenUnsupported(t *testing.T) {
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	caps := a.Capabilities()
	if caps.NativeReplace || caps.FlattenAll || caps.OvernightSession || caps.ResetBalance {
		t.Fatalf("capabilities = %+v, want all false", caps)
	}
	if err := a.Flatten(context.Background()); err == nil {
		t.Fatal("Flatten must return an unsupported error (Capabilities.FlattenAll is false)")
	}
	if err := a.ResetBalance(context.Background(), 100_000); err == nil {
		t.Fatal("ResetBalance must return an unsupported error (Capabilities.ResetBalance is false)")
	}
}

func TestAdapter_New_Defaults(t *testing.T) {
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.rest.base != defaultRESTBase {
		t.Fatalf("REST base = %q, want default %q", a.rest.base, defaultRESTBase)
	}
	if a.route != defaultRoute {
		t.Fatalf("route = %q, want default %q", a.route, defaultRoute)
	}
}

func TestAdapter_New_RequiresVenueAndAccount(t *testing.T) {
	if _, err := New(Config{AccountID: "2TZ00001"}); err == nil {
		t.Fatal("expected an error for a missing venue")
	}
	if _, err := New(Config{Venue: "tz"}); err == nil {
		t.Fatal("expected an error for a missing accountID")
	}
}

// TestOnCanceled_GenuineCancelEmitsOrderCanceled proves the OTHER half of the
// swallow-during-replace behavior that TestNormalizeOrder_PendingCancelDoesNotEmitCancelEvent
// (Task 7) does not exercise: when NO replace is in flight for the domain
// order, a real terminal Canceled from TZ must surface as a genuine
// exec.OrderCanceled, not be silently dropped.
func TestOnCanceled_GenuineCancelEmitsOrderCanceled(t *testing.T) {
	a := &Adapter{venue: "tz", seenExecuted: map[string]float64{}}
	evs := a.onCanceled("tz", "ET1", "ET1", 123)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(evs), evs)
	}
	oc, ok := evs[0].(exec.OrderCanceled)
	if !ok || oc.OID != "ET1" || oc.Ts != 123 {
		t.Fatalf("event = %+v", evs[0])
	}
}

// TestOnCanceled_SwallowedDuringReplaceSignalsChannel proves the swallow half:
// while a.replacing["ET1"] is set (a replace is mid-flight for that domain
// order), the terminal Canceled for the OLD TZ leg must produce zero domain
// events AND signal the waiting ReplaceOrder goroutine via rs.confirmed —
// this is the exact mechanism ReplaceOrder blocks on.
func TestOnCanceled_SwallowedDuringReplaceSignalsChannel(t *testing.T) {
	rs := &replaceState{oldTZID: "ET1", confirmed: make(chan struct{}, 1)}
	a := &Adapter{
		venue:        "tz",
		seenExecuted: map[string]float64{},
		replacing:    map[string]*replaceState{"ET1": rs},
	}
	evs := a.onCanceled("tz", "ET1", "ET1", 123)
	if len(evs) != 0 {
		t.Fatalf("swallowed cancel must emit zero domain events, got %+v", evs)
	}
	select {
	case <-rs.confirmed:
	default:
		t.Fatal("swallowed cancel must signal rs.confirmed")
	}
}

// TestAdapter_CancelOrder_UsesCurrentTZID covers both halves of CancelOrder's
// id resolution: before any replace, the domain id IS the TZ id; the test
// also drives a replace first so the DELETE must target the "-r1" leg, not
// the original id (which TZ would 404 on — it's already terminally canceled).
func TestAdapter_CancelOrder_UsesCurrentTZID(t *testing.T) {
	rec := newMockTZFull(t)
	defer rec.Close()

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: rec.httpURL, WSURL: rec.wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000DD"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})
	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 105}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		or, ok := e.(exec.OrderReplaced)
		return ok && or.OID == oid
	})

	if err := a.CancelOrder(ctx, oid); err != nil {
		t.Fatal(err)
	}
	if got := rec.lastDeletedID(); got != oid+"-r1" {
		t.Fatalf("CancelOrder DELETE targeted %q, want the current leg %q", got, oid+"-r1")
	}
}

func TestAdapter_CancelAll_DelegatesToREST(t *testing.T) {
	var gotMethod, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/orders", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelAll(context.Background(), "AAPL"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/api/accounts/orders" {
		t.Fatalf("method=%s path=%s", gotMethod, gotPath)
	}
}

// TestAdapter_Snapshot_StripsReplaceSuffixAndStampsVenue proves Snapshot (the
// exec.Broker interface method, distinct from restClient.snapshot) recovers
// the stable domain id for a replaced order and stamps venue on every
// returned struct.
func TestAdapter_Snapshot_StripsReplaceSuffixAndStampsVenue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"symbol":"AAPL","side":"Long","shares":10,"priceAvg":100}]`))
	})
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"clientOrderId":"ET1-r1","symbol":"AAPL","orderStatus":"New","orderQuantity":10}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	acct, positions, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.Venue != "tz" {
		t.Fatalf("account venue = %q", acct.Venue)
	}
	if len(positions) != 1 || positions[0].Venue != "tz" {
		t.Fatalf("positions = %+v", positions)
	}
	if len(orders) != 1 || orders[0].ID != "ET1" || orders[0].Venue != "tz" {
		t.Fatalf("orders = %+v, want ID stripped to ET1", orders)
	}
}

// TestAdapter_HandlePosition_EmitsFullSignedSnapshot drives the wsClient
// onPosition callback directly and checks the resulting BrokerPositions:
// short positions get a negative Qty (un-enriching TZ's side field) and the
// event carries the full accumulated position map, not a bare delta.
func TestAdapter_HandlePosition_EmitsFullSignedSnapshot(t *testing.T) {
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.handlePosition(tzPosition{Symbol: "AAPL", Side: "Long", Shares: 10, PriceAvg: 100})
	ev := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerPositions); return ok })
	bp := ev.(exec.BrokerPositions)
	if len(bp.Positions) != 1 || bp.Positions[0].Qty != 10 || bp.Positions[0].Venue != "tz" || bp.Positions[0].Symbol != "US.AAPL" {
		t.Fatalf("positions after first push = %+v", bp.Positions)
	}

	a.handlePosition(tzPosition{Symbol: "GME", Side: "Short", Shares: 50, PriceAvg: 20.5})
	ev = waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		bp, ok := e.(exec.BrokerPositions)
		return ok && len(bp.Positions) == 2
	})
	bp = ev.(exec.BrokerPositions)
	var gme *exec.Position
	for i := range bp.Positions {
		if bp.Positions[i].Symbol == "US.GME" {
			gme = &bp.Positions[i]
		}
	}
	if gme == nil || gme.Qty != -50 {
		t.Fatalf("short position must carry a negative Qty, keyed by the domain-prefixed symbol: %+v", bp.Positions)
	}
}

// TestAdapter_HandlePosition_AddsUSPrefixToSymbol proves the Portfolio-WS
// position push (TZ's bare symbol, e.g. "GME") is tagged with eTape's domain
// "US." prefix, both as the BrokerPositions Symbol field and as the internal
// map key — otherwise a second push for the same symbol would fail to
// dedup/replace the first (keyed by the untagged symbol) and reconcile()'s
// domain-symbol-keyed lookups would never find it.
func TestAdapter_HandlePosition_AddsUSPrefixToSymbol(t *testing.T) {
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.handlePosition(tzPosition{Symbol: "GME", Side: "Short", Shares: 50, PriceAvg: 20.5})
	ev := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerPositions); return ok })
	bp := ev.(exec.BrokerPositions)
	if len(bp.Positions) != 1 || bp.Positions[0].Symbol != "US.GME" {
		t.Fatalf("position symbol = %+v, want US.GME", bp.Positions)
	}
}

// TestAdapter_Reconcile_ReconnectSynthesizesFillAndStreamGap drives handleConn
// directly (bypassing the WS handshake) across two "connects" against a REST
// mock whose /orders snapshot changes between calls, proving: (1) the first
// connect seeds state with no StreamGap, (2) a second connect ("reconnect")
// that shows an order's cumulative executed qty advanced while disconnected
// synthesizes the missed OrderFilled AND a StreamGap, and (3) the synthesized
// fill respects the same seenExecuted dedup counter normalizeOrder uses (a
// third connect with an unchanged snapshot must not re-emit the fill).
func TestAdapter_Reconcile_ReconnectSynthesizesFillAndStreamGap(t *testing.T) {
	var ordersBody atomicString
	ordersBody.set(`[{"clientOrderId":"ET1","symbol":"AAPL","orderStatus":"New","orderQuantity":10,"executed":0}]`)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(ordersBody.get()))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// First connect: seeds state, no StreamGap (nothing to have missed yet).
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	drainNonBlocking(a.Events())

	// The order filled while we were "disconnected".
	ordersBody.set(`[{"clientOrderId":"ET1","symbol":"AAPL","orderStatus":"Filled","orderQuantity":10,"executed":10,"priceAvg":101}]`)
	a.handleConn(false)
	a.handleConn(true)

	fillEv := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.OrderFilled); return ok })
	fill := fillEv.(exec.OrderFilled)
	if fill.F.OrderID != "ET1" || fill.F.Qty != 10 || fill.CumQty != 10 {
		t.Fatalf("synthesized fill = %+v", fill)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	drainNonBlocking(a.Events())

	// Third connect with an unchanged snapshot: must not re-emit the fill
	// (seenExecuted dedup) even though it's again a "reconnect".
	a.handleConn(false)
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })
	select {
	case e := <-a.Events():
		if _, ok := e.(exec.OrderFilled); ok {
			t.Fatalf("fill re-emitted on an unchanged reconcile snapshot: %+v", e)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// TestAdapter_Reconcile_CatchesUpFillOnUnchangedStatus reproduces the final
// whole-branch review finding: an order already tracked as PartiallyFilled
// gains MORE executed quantity while the WS connection is down, but its
// overall status is still PartiallyFilled on reconnect (no status
// transition). Before the fix, the fill-catch-up branch was gated on
// prevStatus != o.Status, so a same-status-more-fills gap synthesized no
// OrderFilled at all, yet seenExecuted was still bumped unconditionally to
// the new cumulative executed qty — permanently losing those fills (a later
// live frame reporting the same cumulative executed would be deduped away as
// "already seen"). This test asserts the catch-up fill IS synthesized for
// the delta, and that seenExecuted only advances in step with it.
func TestAdapter_Reconcile_CatchesUpFillOnUnchangedStatus(t *testing.T) {
	var ordersBody atomicString
	ordersBody.set(`[{"clientOrderId":"ET1","symbol":"AAPL","orderStatus":"PartiallyFilled","orderQuantity":10,"executed":4,"priceAvg":100}]`)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(ordersBody.get()))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// First connect: seeds state at PartiallyFilled/executed=4, no StreamGap.
	a.handleConn(true)
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	drainNonBlocking(a.Events())

	a.mu.Lock()
	if got := a.seenExecuted["ET1"]; got != 4 {
		a.mu.Unlock()
		t.Fatalf("seenExecuted after first connect = %v, want 4", got)
	}
	a.mu.Unlock()

	// More partial fills happen at the broker while disconnected; status is
	// STILL PartiallyFilled on reconnect -- no status transition at all.
	ordersBody.set(`[{"clientOrderId":"ET1","symbol":"AAPL","orderStatus":"PartiallyFilled","orderQuantity":10,"executed":7,"priceAvg":100.5}]`)
	a.handleConn(false)
	a.handleConn(true)

	fillEv := waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.OrderFilled); return ok })
	fill := fillEv.(exec.OrderFilled)
	if fill.F.OrderID != "ET1" || fill.F.Qty != 3 || fill.CumQty != 7 {
		t.Fatalf("catch-up fill = %+v, want a delta of 3 (7-4) with CumQty 7", fill)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })

	a.mu.Lock()
	got := a.seenExecuted["ET1"]
	a.mu.Unlock()
	if got != 7 {
		t.Fatalf("seenExecuted after catch-up = %v, want 7 (advanced exactly in step with the emitted fill, not silently past it)", got)
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

// atomicString is a tiny mutex-guarded string, used only so the mock server's
// handler (running on its own goroutine per request) can read a value the
// test goroutine updates between two handleConn(true) calls.
type atomicString struct {
	mu sync.Mutex
	v  string
}

func (a *atomicString) set(v string) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicString) get() string  { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// TestAdapter_Reconcile_SkipsSupersededLegAfterReplace reproduces the
// Critical reconcile leg-conflation bug: TZ's GET .../orders returns EVERY
// leg submitted today, unfiltered to working orders, so a domain order that
// was replaced earlier in the session shows up as TWO rows in the same
// snapshot — its dead old leg (Canceled) and its live new leg (Accepted) —
// both stripping to the same domain id. Before the fix, reconcile() keyed
// snapshot rows only by the stripped domain id, so it would diff the dead
// old leg's Canceled status against lastKnownStatus (which reflects the live
// new leg) and synthesize a spurious, permanent exec.OrderCanceled for a
// domain order that is actually still working. The mock returns the new leg
// FIRST and the old leg SECOND specifically so that, without the fix, the
// old leg's diff both fires the spurious event AND clobbers
// lastKnownStatus[oid] back to Canceled — this test's two assertions catch
// both halves of that corruption regardless of a fix that only prevents one.
func TestAdapter_Reconcile_SkipsSupersededLegAfterReplace(t *testing.T) {
	oid := "ET1"
	newLeg := oid + "-r1"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"clientOrderId":"` + newLeg + `","symbol":"AAPL","orderStatus":"New","orderQuantity":10},
			{"clientOrderId":"` + oid + `","symbol":"AAPL","orderStatus":"Canceled","orderQuantity":10}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.runCtx = context.Background()

	// Simulate: the order was submitted, then replaced once, so the new leg
	// is now the authoritative one and the domain last saw it as Accepted.
	a.tzIDByDomain[oid] = newLeg
	a.lastKnownStatus[oid] = exec.StatusAccepted
	a.connectedOnce = true // this reconcile() call is a reconnect, not the first connect

	a.handleConn(true) // BrokerConnUp -> reconcile -> (gap events) -> StreamGap
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerAccount); return ok })
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { _, ok := e.(exec.StreamGap); return ok })

	grace := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case e := <-a.Events():
			if oc, ok := e.(exec.OrderCanceled); ok && oc.OID == oid {
				t.Fatalf("spurious OrderCanceled synthesized for a replaced order's dead old leg: %+v", oc)
			}
		case <-grace:
			break loop
		}
	}

	a.mu.Lock()
	got := a.lastKnownStatus[oid]
	a.mu.Unlock()
	if got != exec.StatusAccepted {
		t.Fatalf("lastKnownStatus[%q] = %v, want StatusAccepted (must reflect the current leg, not the superseded dead leg)", oid, got)
	}
}

// TestAdapter_Snapshot_ReturnsOnlyCurrentLegAfterReplace reproduces the
// Critical Snapshot leg-conflation bug: with both a replaced order's dead old
// leg and live new leg present in TZ's unfiltered orders blotter, Snapshot
// must return exactly ONE exec.Order for the domain id — the live one — not
// two colliding entries under the same stripped id.
func TestAdapter_Snapshot_ReturnsOnlyCurrentLegAfterReplace(t *testing.T) {
	oid := "ET1"
	newLeg := oid + "-r1"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"clientOrderId":"` + oid + `","symbol":"AAPL","orderStatus":"Canceled","orderQuantity":10},
			{"clientOrderId":"` + newLeg + `","symbol":"AAPL","orderStatus":"New","orderQuantity":10}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	a.tzIDByDomain[oid] = newLeg

	_, _, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("Snapshot returned %d orders for domain id %q, want exactly 1 (the live leg): %+v", len(orders), oid, orders)
	}
	if orders[0].ID != oid {
		t.Fatalf("orders[0].ID = %q, want %q", orders[0].ID, oid)
	}
	if orders[0].Status != exec.StatusAccepted {
		t.Fatalf("orders[0].Status = %v, want StatusAccepted (the live leg's status, not the dead old leg's Canceled)", orders[0].Status)
	}
}

// TestAdapter_Snapshot_PicksHighestReplaceSuffixOnColdStart covers the other
// half of the Critical fix: a freshly-constructed Adapter (as after a real
// process restart, where tzIDByDomain starts out empty with no memory of any
// replace that happened in a prior process) must still resolve the ambiguity
// between a dead old leg and a live new leg deterministically -- by taking
// the row with the highest "-rN" replace-suffix number, since that suffix is
// embedded in the id itself and requires no in-memory state to interpret.
func TestAdapter_Snapshot_PicksHighestReplaceSuffixOnColdStart(t *testing.T) {
	oid := "ET1"
	newLeg := oid + "-r1"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"clientOrderId":"` + oid + `","symbol":"AAPL","orderStatus":"Canceled","orderQuantity":10},
			{"clientOrderId":"` + newLeg + `","symbol":"AAPL","orderStatus":"New","orderQuantity":10}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Deliberately do NOT populate tzIDByDomain -- this is the fresh,
	// no-memory-of-any-prior-replace state a real process restart leaves
	// behind.
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}

	_, _, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("Snapshot returned %d orders for domain id %q with no in-memory replace state, want exactly 1: %+v", len(orders), oid, orders)
	}
	if orders[0].ID != oid || orders[0].Status != exec.StatusAccepted {
		t.Fatalf("orders[0] = %+v, want the live leg (ID %q, StatusAccepted) picked via the highest replace suffix", orders[0], oid)
	}
}

// TestAdapter_Snapshot_ColdStartPrefersCanceledOverHigherSuffixRejected covers
// the task-reviewer finding on top of the Critical fix above: when a
// replace's cancel confirms but the resubmit is then semantically rejected
// (see TestAdapter_ReplaceOrder_ResubmitFailsAfterCancelConfirmed), the old
// leg is genuinely Canceled and the new "-r1" leg is genuinely Rejected — both
// terminal (not the Critical bug's live-vs-dead severity), but only the
// Canceled row is the truth Core.Recover()'s ReconcileOpenOrders should
// reinstate on a real process restart (tzIDByDomain empty, no memory of which
// leg is "current"). A pure highest-suffix tie-break would wrongly pick the
// Rejected "-r1" row just because its suffix is higher. pickColdStartLeg's
// legTier instead ranks Rejected below any other status (mirroring
// SubmitOrder's own invariant that a rejected order never becomes a
// registered leg), so the Canceled row wins regardless of suffix.
func TestAdapter_Snapshot_ColdStartPrefersCanceledOverHigherSuffixRejected(t *testing.T) {
	oid := "ET1"
	newLeg := oid + "-r1"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/account/2TZ00001", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"clientOrderId":"` + oid + `","symbol":"AAPL","orderStatus":"Canceled","orderQuantity":10},
			{"clientOrderId":"` + newLeg + `","symbol":"AAPL","orderStatus":"Rejected","orderQuantity":10}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Deliberately do NOT populate tzIDByDomain -- simulating a fresh process
	// restart with no memory of the replace that produced these two rows.
	a, err := New(Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}})
	if err != nil {
		t.Fatal(err)
	}

	_, _, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("Snapshot returned %d orders for domain id %q, want exactly 1: %+v", len(orders), oid, orders)
	}
	if orders[0].ID != oid {
		t.Fatalf("orders[0].ID = %q, want %q", orders[0].ID, oid)
	}
	if orders[0].Status != exec.StatusCanceled {
		t.Fatalf("orders[0].Status = %v, want StatusCanceled (the truth: the higher-suffix -r1 row is only Rejected, not the current leg)", orders[0].Status)
	}
}

// TestAdapter_ReplaceOrder_ResubmitFailsAfterCancelConfirmed covers the path
// where the old leg's cancel is genuinely confirmed over the Portfolio WS but
// the subsequent resubmit POST is then semantically rejected by TZ. This
// design was reviewed and accepted: the old leg is truly dead (its cancel
// can't be undone) and the new leg never got accepted, so the domain order
// must be reported as really canceled rather than silently left "still
// working" under an id that no longer exists at the broker.
func TestAdapter_ReplaceOrder_ResubmitFailsAfterCancelConfirmed(t *testing.T) {
	var mu sync.Mutex
	var conn *websocket.Conn
	var connCtx context.Context

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
			return
		}
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`)); err != nil {
			return
		}
		if _, _, err := c.Read(ctx); err != nil { // subscribe payload
			return
		}
		mu.Lock()
		conn, connCtx = c, ctx
		mu.Unlock()
		<-ctx.Done()
	})

	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientOrderID string `json:"clientOrderId"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.ClientOrderID, "-r") {
			// The resubmitted (replacement) leg: TZ semantically rejects it
			// (HTTP 200, orderStatus Rejected — TZ's documented shape).
			_, _ = w.Write([]byte(`{"orderStatus":"Rejected","text":"insufficient buying power"}`))
			return
		}
		_, _ = w.Write([]byte(`{"orderStatus":"New"}`))
	})

	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/api/accounts/2TZ00001/orders/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"clientOrderId":"` + id + `","orderStatus":"PendingCancel"}`))

		// Push the real terminal Canceled confirmation over the Portfolio WS,
		// the way a real TZ cancel would — this is what ReplaceOrder awaits
		// before resubmitting.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			c, ctx := conn, connCtx
			mu.Unlock()
			if c != nil {
				frame := fmt.Sprintf(
					`{"action":"update","userOrderId":"2TZ00001:%s","symbol":"AAPL","orderStatus":"Canceled","orderQuantity":10,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`,
					id,
				)
				_ = c.Write(ctx, websocket.MessageText, []byte(frame))
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
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

	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):] + "/stream"

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: srv.URL, WSURL: wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000EE"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err == nil {
		t.Fatal("expected ReplaceOrder to return an error when the resubmit is rejected after the cancel is confirmed")
	}

	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oc, ok := e.(exec.OrderCanceled)
		return ok && oc.OID == oid
	})
}

// TestOnCanceled_SwallowedDuringReplace_MismatchedLegDoesNotSignal guards the
// leg-identity check: a stale/duplicate Canceled frame for a TZ id that is
// NOT the one the in-flight replace is currently canceling must still be
// swallowed (the domain order is mid-replace either way) but must NOT
// falsely signal rs.confirmed for a leg it doesn't belong to.
func TestOnCanceled_SwallowedDuringReplace_MismatchedLegDoesNotSignal(t *testing.T) {
	rs := &replaceState{oldTZID: "ET1-r1", confirmed: make(chan struct{}, 1)}
	a := &Adapter{
		venue:        "tz",
		seenExecuted: map[string]float64{},
		replacing:    map[string]*replaceState{"ET1": rs},
	}
	// A late duplicate Canceled for the FIRST leg's raw id ("ET1"), arriving
	// after a second replace already started canceling "ET1-r1".
	evs := a.onCanceled("tz", "ET1", "ET1", 123)
	if len(evs) != 0 {
		t.Fatalf("still-replacing order must swallow any cancel, got %+v", evs)
	}
	select {
	case <-rs.confirmed:
		t.Fatal("a mismatched leg's cancel must not signal the current replace's confirmation")
	default:
	}
}
