package uihubtest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e2e.db"), Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// dialWS opens a client, subscribes to topics, and returns a read helper.
func dialWS(t *testing.T, ctx context.Context, url string, topics ...wsmsg.Topic) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range topics {
		b, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: tp})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// waitFrame reads until a frame satisfies pred or the deadline passes.
func waitFrame(t *testing.T, ctx context.Context, c *websocket.Conn, pred func(m map[string]any) bool) map[string]any {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && pred(m) {
			return m
		}
	}
}

func TestE2EExecLifecycleOverWS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	clk := clock.System{}

	simBroker := sim.New("sim", clk, 100_000, sim.Options{})
	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1e9, MaxSymbolPositionValue: 1e9, MaxSymbolPositionShares: 1e9},
			Venue:  map[exec.VenueID]exec.VenueLimits{"sim": {MaxOrderValue: 1e9, MaxPositionValue: 1e9, MaxPositionShares: 1e9, MaxOpenOrders: 100}},
		},
		Store: st, Brokers: map[exec.VenueID]exec.Broker{"sim": simBroker},
		Clock: clk, IDGen: exec.NewOrderIDGen(clk, deterministicReader()),
	})
	if err := execCore.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clk, uihub.Config{
		Venues: []uihub.VenueMeta{{ID: "sim", Broker: "alpaca", Gate: uihub.GateLimits{MaxOrderValue: 1e9}}},
		Global: uihub.GlobalLimits{MaxDayLoss: 1e9},
		MD:     20 * time.Millisecond, Account: 50 * time.Millisecond, Position: 30 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore, nil, nil, nil, nil, nil, nil)
	go func() { _ = hub.Run(ctx) }()
	go forwardExec(ctx, execCore, hub)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialWS(t, ctx, url, wsmsg.TopicExecOrders, wsmsg.TopicExecStatus)
	defer c.Close(websocket.StatusNormalClosure, "")

	// arm master so the gate lets orders through
	sendCommand(t, ctx, c, "Arm", map[string]any{})

	// Give the sim a mark so a market order is marketable at the broker level,
	// AND feed exec.Core's own mark table directly: the gate's order-value
	// check (exec/gate.go orderValue) reads exec.Core.marks, which is only
	// populated via Core.FeedMark — in production that's the markBridge
	// goroutine copying md.Core.Marks() into execCore.FeedMark (see
	// cmd/etape/main.go). This test has no live md feed for US.AAPL, so it
	// feeds the mark directly, exactly like markBridge would. Sim fills are
	// book-priced (Task 2), so the market order below also needs a book —
	// SetMark alone no longer fills it.
	simBroker.SetMark("US.AAPL", 3.50)
	simBroker.SetBook("US.AAPL", feed.Book{
		Symbol: "US.AAPL",
		Bids:   []feed.BookLevel{{Price: 3.50, Volume: 1_000_000}},
		Asks:   []feed.BookLevel{{Price: 3.50, Volume: 1_000_000}},
	})
	execCore.FeedMark(exec.Mark{Symbol: "US.AAPL", Price: 3.50, TsMs: clk.Now().UnixMilli()})
	corr := sendCommand(t, ctx, c, "SubmitOrder", map[string]any{
		"venue": "sim", "symbol": "US.AAPL", "side": "BUY", "type": "MARKET", "tif": "DAY", "qty": 10,
	})

	// ack accepted with an orderId
	ack := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "ack" && m["corrId"] == corr })
	if ack["status"] != "accepted" || ack["orderId"] == "" {
		t.Fatalf("submit should be accepted with an orderId: %v", ack)
	}

	// an exec.orders delta with status FILLED must arrive
	filled := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.orders" {
			return false
		}
		o, _ := m["payload"].(map[string]any)
		return o != nil && o["status"] == "FILLED"
	})
	o := filled["payload"].(map[string]any)
	if o["symbol"] != "US.AAPL" || o["executedQty"] != float64(10) {
		t.Fatalf("filled order wrong: %v", o)
	}

	// QueryFills returns the fill (persisted via exec AppendExecEvent -> store)
	qcorr := sendQuery(t, ctx, c, "QueryFills", map[string]any{"symbol": "US.AAPL", "fromMs": 0, "toMs": time.Now().Add(time.Hour).UnixMilli()})
	res := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "result" && m["corrId"] == qcorr })
	fills, _ := res["payload"].([]any)
	if len(fills) == 0 {
		t.Fatalf("QueryFills should return the fill, got %v", res["payload"])
	}
	fill, _ := fills[0].(map[string]any)
	if fill == nil || fill["symbol"] != "US.AAPL" || fill["qty"] != float64(10) {
		t.Fatalf("QueryFills fill contents wrong: %v", fills[0])
	}
}

func TestE2EReplayMarketDataOverWS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)

	// record a couple of feed events, then read the day back to replay them
	base := time.Date(2026, 7, 6, 13, 31, 0, 0, time.UTC)
	day := base.Format("2006-01-02")
	st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: base.UnixMilli()}}, base.UnixMilli())
	st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.50, TsMs: base.Add(time.Second).UnixMilli()}}, base.Add(time.Second).UnixMilli())
	st.Flush()
	rows, err := st.ReadJournalDay(day)
	if err != nil || len(rows) < 2 {
		t.Fatalf("recorded rows unavailable: %v (%d rows)", err, len(rows))
	}

	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	// exec core with no venues (md-only test) still constructs a valid hub
	execCore := exec.NewCore(exec.CoreConfig{Store: st, Brokers: map[exec.VenueID]exec.Broker{}, Clock: clock.System{}, IDGen: exec.NewOrderIDGen(clock.System{}, deterministicReader())})
	_ = execCore.Recover(ctx)
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clock.System{}, uihub.Config{
		MD: 15 * time.Millisecond, Account: 50 * time.Millisecond, Position: 30 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore, nil, nil, nil, nil, nil, nil)
	go func() { _ = hub.Run(ctx) }()
	go forwardMD(ctx, mdCore, hub)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// replay the recorded day into md.Core
	simClk := replay.NewClock(time.UnixMilli(rows[0].TsExch))
	fd := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: simClk, Speed: 0})
	go func() { _ = fd.Run(ctx) }()
	go func() {
		for ev := range fd.Events() {
			mdCore.Feed(ev)
		}
	}()

	c := dialWS(t, ctx, url, wsmsg.TopicQuote)
	defer c.Close(websocket.StatusNormalClosure, "")

	// a md.quote frame for US.AAPL must arrive (snapshot or delta)
	q := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["topic"] != "md.quote" {
			return false
		}
		p, _ := m["payload"].(map[string]any)
		return p != nil && p["symbol"] == "US.AAPL"
	})
	p := q["payload"].(map[string]any)
	last, ok := p["last"]
	if !ok {
		t.Fatalf("md.quote payload missing last: %v", p)
	}
	if last != 3.47 && last != 3.50 {
		t.Fatalf("md.quote last should be one of the replayed prices, got %v: %v", last, p)
	}
}

// forwardExec/forwardMD mirror main's fan-in goroutines (the capstone reconstructs
// the wiring main does, since it can't import package main).
func forwardExec(ctx context.Context, execCore *exec.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-execCore.Updates():
			hub.PublishExec(u)
		}
	}
}

func forwardMD(ctx context.Context, mdCore *md.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-mdCore.Updates():
			hub.PublishMD(u)
		}
	}
}

// helpers: sendCommand/sendQuery write a frame and return its corrId.
func sendCommand(t *testing.T, ctx context.Context, c *websocket.Conn, name string, args map[string]any) string {
	t.Helper()
	corr := "c-" + name
	raw, _ := json.Marshal(args)
	b, _ := json.Marshal(wsmsg.CommandMsg{Kind: "command", CorrID: corr, Name: name, Args: raw})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return corr
}

func sendQuery(t *testing.T, ctx context.Context, c *websocket.Conn, name string, args map[string]any) string {
	t.Helper()
	corr := "q-" + name
	raw, _ := json.Marshal(args)
	b, _ := json.Marshal(wsmsg.QueryMsg{Kind: "query", CorrID: corr, Name: name, Args: raw})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return corr
}

// deterministicReader supplies the exec OrderIDGen with a non-crypto seed for tests.
func deterministicReader() *strings.Reader {
	return strings.NewReader(strings.Repeat("etape-seed-0123456789", 64))
}
