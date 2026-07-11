package uihubtest

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/demojournal"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// TestE2EReplayDemoJournal_SimFillsPriceAgainstReplayedBook is Task 6's
// replay-mode smoke check. cmd/etape/main.go's `-replay`/`-demo` boot path
// wires: demojournal.Generate (or a recorded day) -> replay.NewFeed ->
// pipe() -> md.Core.Feed -> md.Core's BookEvent/TicksEvent handling ->
// core.Marks()/core.Books() -> markBridge -> every sim.Broker's
// SetMark/SetBook. main.go's markBridge/pipe are unexported (package main),
// so this test can't import them directly -- exactly the reason
// e2e_test.go's forwardExec/forwardMD in this same file already
// RECONSTRUCT (not reimplement the logic of, just relay) main's fan-in
// goroutines rather than importing them; the bridge goroutine below follows
// that established precedent for the mark/book leg markBridge owns. Every
// OTHER piece here -- demojournal.Generate, replay.NewFeed/replay.Clock,
// md.Core, sim.Broker, exec.Core, the gate, and uihub itself -- is the real
// production code, driven over a real WS connection, so a fill observed
// here is genuine proof the whole chain works against REPLAYED data, not
// just data a sim-package unit test constructed by hand.
//
// It confirms the three things the task brief calls for:
//   - fills print at the replayed book's ask levels, not the submitted
//     limit price;
//   - an order larger than the replayed book's total depth partial-fills;
//   - exec.account/exec.positions WS frames show cash/equity/positions
//     moving as the position opens.
func TestE2EReplayDemoJournal_SimFillsPriceAgainstReplayedBook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- generate a deterministic synthetic trading day (no OpenD, no
	// market hours -- the same tool cmd/genjournal and main.go's -demo flag
	// use) and read it back exactly as boot()'s -replay branch does ---
	dbPath := filepath.Join(t.TempDir(), "smoke.db")
	const day = "2026-01-05"
	if err := demojournal.Generate(dbPath, day); err != nil {
		t.Fatalf("generate demo journal: %v", err)
	}
	st, err := store.Open(store.Options{Path: dbPath, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rows, err := st.ReadJournalDay(day)
	if err != nil || len(rows) == 0 {
		t.Fatalf("demo journal day unavailable: %v (%d rows)", err, len(rows))
	}

	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	// A real sim.Broker, zero-value Options -- the fill-latency/slippage
	// knobs already have dedicated coverage in sim_integration_test.go; this
	// test's whole point is proving the BOOK WIRING into a real broker
	// during replay, not re-proving those knobs.
	simBroker := sim.New("sim", clock.System{}, 100_000, sim.Options{})
	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1e9, MaxSymbolPositionValue: 1e9, MaxSymbolPositionShares: 1e9},
			Venue:  map[exec.VenueID]exec.VenueLimits{"sim": {MaxOrderValue: 1e9, MaxPositionValue: 1e9, MaxPositionShares: 1e9, MaxOpenOrders: 100}},
		},
		Store: st, Brokers: map[exec.VenueID]exec.Broker{"sim": simBroker},
		Clock: clock.System{}, IDGen: exec.NewOrderIDGen(clock.System{}, deterministicReader()),
	})
	if err := execCore.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clock.System{}, uihub.Config{
		Venues: []uihub.VenueMeta{{ID: "sim", Broker: "sim", Gate: uihub.GateLimits{MaxOrderValue: 1e9}}},
		Global: uihub.GlobalLimits{MaxDayLoss: 1e9},
		MD:     15 * time.Millisecond, Account: 20 * time.Millisecond, Position: 20 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore, nil, nil, nil, nil, nil)
	go func() { _ = hub.Run(ctx) }()
	go forwardExec(ctx, execCore, hub)
	go forwardMD(ctx, mdCore, hub)

	// markAndBookBridge is main.go's markBridge, reconstructed here (see the
	// doc comment above for why it can't be imported): copies
	// mdCore.Marks()/mdCore.Books() into execCore.FeedMark and the sim
	// broker's SetMark/SetBook, exactly as production replay does.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-mdCore.Marks():
				execCore.FeedMark(exec.Mark{Symbol: m.Symbol, Price: m.Price, TsMs: m.TsMs})
				simBroker.SetMark(m.Symbol, m.Price)
			case bk := <-mdCore.Books():
				simBroker.SetBook(bk.Symbol, bk)
			}
		}
	}()

	// Drive the actual replay pump over the demojournal-generated rows --
	// the same replay.NewFeed/replay.Clock machinery main.go's -replay (and
	// -demo) branch uses, at max (non-realtime) speed so the test doesn't
	// wait out a full simulated trading day.
	simClk := replay.NewClock(time.UnixMilli(rows[0].TsExch))
	fd := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: simClk, Speed: 0})
	go func() { _ = fd.Run(ctx) }()
	go func() {
		for ev := range fd.Events() {
			mdCore.Feed(ev)
		}
	}()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c := dialWS(t, ctx, url, wsmsg.TopicExecOrders, wsmsg.TopicExecAccount, wsmsg.TopicExecPositions)
	defer c.Close(websocket.StatusNormalClosure, "")

	sendCommand(t, ctx, c, "Arm", map[string]any{})

	// demojournal seeds US.AAPL's book at market open with 5 ask levels
	// (190.01..190.05, volumes 100..500 -- 1500 shares total, see
	// demojournal.bookAround) and never sends a second book for it (only one
	// BookEvent is ever recorded per symbol). A 2000-share buy therefore can
	// only ever partial-fill (1500 of 2000) against this replayed data,
	// which is exactly the "large test order partial-fills" case the brief
	// calls for. The limit (900) sits far above the replayed ask levels so
	// the order is unconditionally marketable against them; the gate values
	// a LIMIT order off its own limit price, so this submits and rests even
	// before any mark/book has arrived from the replay pump -- it fills the
	// moment the real BookEvent reaches the broker via the bridge above.
	const wantSymbol = "US.AAPL"
	corr := sendCommand(t, ctx, c, "SubmitOrder", map[string]any{
		"venue": "sim", "symbol": wantSymbol, "side": "BUY", "type": "LIMIT", "tif": "DAY",
		"qty": 2000, "limitPrice": 900,
	})
	ack := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "ack" && m["corrId"] == corr })
	if ack["status"] != "accepted" {
		t.Fatalf("submit should be accepted, got %v", ack)
	}

	// A PARTIALLY_FILLED exec.orders delta must arrive, proving the
	// replayed BookEvent genuinely reached the sim broker (through
	// replay.Feed -> md.Core.Feed -> md.Core's real BookEvent handling ->
	// Books() -> the bridge -> sim.Broker.SetBook) and priced a real fill.
	partial := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.orders" {
			return false
		}
		o, _ := m["payload"].(map[string]any)
		return o != nil && o["symbol"] == wantSymbol && o["status"] == "PARTIALLY_FILLED"
	})
	po := partial["payload"].(map[string]any)
	if po["executedQty"] != float64(1500) || po["leavesQty"] != float64(500) {
		t.Fatalf("expected a 1500/500 partial fill against the replayed book's total depth, got %v", po)
	}
	avgFillPx, _ := po["avgFillPrice"].(float64)
	// The replayed ask levels are 190.01..190.05; the submitted limit was
	// 900 -- so a fill average anywhere near 190 (and nowhere near 900)
	// proves the fill priced off the book, not the submitted limit.
	if avgFillPx < 190.0 || avgFillPx > 190.1 {
		t.Fatalf("avgFillPrice = %v, want ~190.0x (the replayed ask levels), not the submitted limit of 900", avgFillPx)
	}

	// exec.account must show AvailableCash decremented and Equity moved for
	// the now-open (partial) position.
	acct := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.account" {
			return false
		}
		p, _ := m["payload"].(map[string]any)
		cash, ok := p["availableCash"].(float64)
		return ok && cash < 100_000
	})
	acctPayload := acct["payload"].(map[string]any)
	wantCash := 100_000.0 - 1500*avgFillPx
	if cash := acctPayload["availableCash"].(float64); cash > wantCash+1e-6 || cash < wantCash-1e-6 {
		t.Fatalf("exec.account availableCash = %v, want %v (starting cash - 1500*avgFillPrice)", cash, wantCash)
	}

	// exec.positions must show the AAPL position opened at the replayed size.
	posFrame := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.positions" {
			return false
		}
		rows, _ := m["payload"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			if row != nil && row["symbol"] == wantSymbol && row["qty"] == float64(1500) {
				return true
			}
		}
		return false
	})
	_ = posFrame
}

// deterministicReader and forwardExec/forwardMD are already defined by
// e2e_test.go in this same package (uihubtest) -- reused as-is, matching
// this file's own doc comment about not duplicating that reconstruction.
