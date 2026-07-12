package uihubtest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/backfill"
	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/synth"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
)

// synthDemoSeed pins the fictional universe/day for every test in this file
// (determinism -- see the plan's Global Constraints). synthDemoLargeCapSlot
// relies on DrawUniverse's own documented, stable personality-to-sorted-slot
// assignment (universe.go: 2 runner slots, then 5 large-cap, then 5 mid-cap;
// "the personality assignment... is fixed" regardless of seed) to pick a
// large-cap symbol without this package needing white-box access to
// SymbolSpec.Pers -- large caps never halt (price.go's detectHalt is only
// ever invoked for PersRunner), so anchoring both tests below on this slot
// sidesteps a runner's halt-freeze as a source of flakiness.
const (
	synthDemoSeed         = int64(4242)
	synthDemoLargeCapSlot = 2
)

// TestSynthDemoBoot_EnsureSymbolWarmHistoryAndMoversConsistent is Task 11's
// boot-integration check for the -demo path's warm-history and movers wiring
// (cmd/etape/main.go's *demo branch: synth.New -> gen.Seed(st, now) ->
// synth.NewFeed/synth.NewRequester), mirroring replay_smoke_test.go's own
// precedent of reconstructing (not reimplementing) main's fan-in wiring
// against real components rather than importing package main.
//
// It drives two of the three checks the task brief asks for over a single
// boot:
//
//   - EnsureSymbol (a real WS command, exactly as the UI would send it for a
//     newly-opened chart) triggers a real backfill.Orchestrator.Backfill call
//     (chain-less, matching main.go's -demo wiring exactly: daily/intraday
//     chains and the tail fetcher are all nil, so Backfill only performs its
//     warmStart step) that reads back exactly what gen.Seed archived, and a
//     resulting md.bars snapshot on the WS wire proves 1m and daily history
//     both survived the whole real path (store -> backfill.Orchestrator ->
//     md.Core -> uihub mirror -> WS frame), not just the store round trip in
//     isolation.
//   - The pre-market-rank protocol (3410, what scan.go's fetchPreMarket polls
//     for the movers board in production) answers, from the SAME live
//     Generator instance, with rows whose price/%-change/volume are exactly
//     what Generator.QuoteOf independently reports for each symbol --
//     verifying the requester and the quote-producing side of the generator
//     never disagree with each other once wired together.
//
// A fixed clock.Fake instant (not clock.System{}) is used for New/Seed/the
// backfill Orchestrator specifically so the archived bar counts asserted
// below are exactly reproducible across runs, not dependent on wall-clock
// jitter; no live ticking (Feed.Run) is needed for either check, so there is
// no ticker/fake-clock interaction to reason about here (see
// TestSynthDemoBoot_SimFillsPriceAgainstSyntheticBook for the one check that
// does need live ticking, and why that one uses clock.System{} instead,
// matching main.go's own -demo wiring exactly).
func TestSynthDemoBoot_EnsureSymbolWarmHistoryAndMoversConsistent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := openStore(t)
	clk := clock.NewFake(time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC))
	nowMs := clk.Now().UnixMilli()

	gen := synth.New(synthDemoSeed, clk)
	syms := gen.Symbols()
	wantSymbol := syms[synthDemoLargeCapSlot]

	// gen.Seed is exactly cmd/etape/main.go's -demo boot step: fast-runs the
	// model in logical time up to nowMs and archives the result (~1y dailies,
	// ~3d 1m, ~2h ticks journaled) through st, leaving the generator
	// positioned at nowMs with realistic (non-opening-print) state.
	gen.Seed(st, nowMs)
	st.Flush()

	// --- movers/rank check: the SAME gen the Requester answers from, cross
	// checked against QuoteOf directly (no scan.Poller wiring needed -- the
	// poller's own float/OTC-resolution machinery is orthogonal to what this
	// check is verifying: that the wire response and the generator's quotes
	// never disagree). ---
	req := synth.NewRequester(gen)
	fr, err := req.Request(ctx, opend.ProtoQotGetUSPreMarketRank, &rankpb.Request{})
	if err != nil {
		t.Fatalf("Requester.Request(3410): %v", err)
	}
	var rankResp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &rankResp); err != nil {
		t.Fatalf("unmarshal pre-market rank response: %v", err)
	}
	rows := rankResp.GetS2C().GetDataList()
	if len(rows) != len(syms) {
		t.Fatalf("pre-market rank returned %d rows, want %d (one per universe symbol)", len(rows), len(syms))
	}
	for _, row := range rows {
		// codeFromRankRow mirrors scan.go's symbolOf: "US." + wire Security.Code.
		code := "US." + row.GetSecurity().GetCode()
		q, ok := gen.QuoteOf(code)
		if !ok {
			t.Fatalf("rank row for non-universe code %q", code)
		}
		var wantPct float64
		if q.PrevClose != 0 {
			wantPct = (q.Last - q.PrevClose) / q.PrevClose * 100
		}
		if got := row.GetPreMarketChangeRatio(); got != wantPct {
			t.Errorf("%s: PreMarketChangeRatio = %v, want %v (from QuoteOf)", code, got, wantPct)
		}
		if got := row.GetPreMarketPrice(); got != q.Last {
			t.Errorf("%s: PreMarketPrice = %v, want %v (QuoteOf.Last)", code, got, q.Last)
		}
		if got := row.GetPreMarketVolume(); got != q.Volume {
			t.Errorf("%s: PreMarketVolume = %v, want %v (QuoteOf.Volume)", code, got, q.Volume)
		}
	}

	// --- EnsureSymbol -> warm BarSnapshot check: the real uihub/md/backfill
	// wiring, chain-less exactly like main.go's -demo branch (no
	// daily/intraday providers, no tail fetcher -- Backfill only warm-starts
	// from what gen.Seed already archived in st). ---
	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Store: st, Brokers: map[exec.VenueID]exec.Broker{}, Clock: clock.System{},
		IDGen: exec.NewOrderIDGen(clock.System{}, deterministicReader()),
	})
	if err := execCore.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clock.System{}, uihub.Config{
		MD: 15 * time.Millisecond, Account: 50 * time.Millisecond, Position: 30 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore, nil, nil, nil, nil, nil)
	go func() { _ = hub.Run(ctx) }()
	go forwardMD(ctx, mdCore, hub)

	sf := synth.NewFeed(gen, st, clk)
	hub.SetFeed(sf) // enables EnsureSymbol's existence probe (f.Validate -> gen.Has)

	orch := backfill.New(nil, nil, nil, mdCore, st, clk, backfill.Config{})
	hub.SetBackfill(func(sym string, done func(ok bool)) {
		go func() {
			err := orch.Backfill(ctx, sym)
			if done != nil {
				done(err == nil)
			}
		}()
	})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c := dialWS(t, ctx, url, wsmsg.TopicBars)
	defer c.Close(websocket.StatusNormalClosure, "")
	// ~1y of daily bars + ~3 trailing days of 1m bars serialize well past
	// coder/websocket's default 32KiB read limit (dialWS, e2e_test.go, uses
	// the library default) -- raise it for this connection only, matching
	// what a real chart-history-sized frame needs in production too.
	c.SetReadLimit(16 << 20)

	corr := sendCommand(t, ctx, c, "EnsureSymbol", map[string]any{
		"demandId": "chart1", "symbol": wantSymbol, "profile": "focused",
	})
	ack := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "ack" && m["corrId"] == corr })
	if ack["status"] != "accepted" {
		t.Fatalf("EnsureSymbol should be accepted, got %v", ack)
	}

	got := waitBarSnapshots(t, ctx, c, wantSymbol, []string{"1m", "D"})

	// Cross-check against the archive directly (what warmStart itself read)
	// rather than a fixed magic number, so this assertion tracks whatever
	// gen.Seed actually wrote, not a guess at it.
	wantDaily, err := st.ReadDailyBars(wantSymbol)
	if err != nil {
		t.Fatalf("ReadDailyBars: %v", err)
	}
	wantIntraday, err := st.ReadBars1m(wantSymbol, 0, nowMs)
	if err != nil {
		t.Fatalf("ReadBars1m: %v", err)
	}

	if got1m := got["1m"]; len(got1m) != len(wantIntraday) {
		t.Errorf("md.bars 1m snapshot has %d bars, want %d (matching the store archive)", len(got1m), len(wantIntraday))
	}
	// seedTrailingDays=3 (synth/seeder.go) plus "today so far": comfortably
	// more than a single trading day's worth of 1-minute bars, proving real
	// multi-day depth rather than a handful of bars from just now.
	if len(wantIntraday) < 1000 {
		t.Errorf("only %d 1m bars archived, want >1000 (>~1 day) for a 3-trailing-day warm seed", len(wantIntraday))
	}

	if gotD := got["D"]; len(gotD) != len(wantDaily) {
		t.Errorf("md.bars daily snapshot has %d bars, want %d (matching the store archive)", len(gotD), len(wantDaily))
	}
	// seedHistoryDays=365 (synth/seeder.go): expect the large majority of a
	// year's worth of daily bars (weekends/holidays aren't modeled, so every
	// calendar day gets one, unlike a real trading calendar).
	if len(wantDaily) < 300 {
		t.Errorf("only %d daily bars archived, want >300 (~1y warm seed)", len(wantDaily))
	}
}

// waitBarSnapshots reads WS frames until it has seen a "md.bars" snapshot
// for symbol at every timeframe in want (or ctx/the deadline runs out),
// returning each timeframe's bar payloads keyed by timeframe string. It
// exists because a plain waitFrame call for one timeframe would silently
// discard a same-connection snapshot for the OTHER requested timeframe if it
// happens to arrive first -- SeedDaily and SeedHistory1m (md/core.go) each
// emit their own independent BarSnapshot, in no guaranteed relative order.
func waitBarSnapshots(t *testing.T, ctx context.Context, c *websocket.Conn, symbol string, want []string) map[string][]map[string]any {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	got := map[string][]map[string]any{}
	pending := map[string]bool{}
	for _, tf := range want {
		pending[tf] = true
	}

	for len(pending) > 0 {
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read frame (still waiting on timeframes %v for %s): %v", keysOf(pending), symbol, err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m["kind"] != "snapshot" || m["topic"] != string(wsmsg.TopicBars) {
			continue
		}
		bars, _ := m["payload"].([]any)
		if len(bars) == 0 {
			continue
		}
		first, _ := bars[0].(map[string]any)
		if first == nil || first["symbol"] != symbol {
			continue
		}
		tf, _ := first["timeframe"].(string)
		if !pending[tf] {
			continue
		}
		out := make([]map[string]any, 0, len(bars))
		for _, b := range bars {
			if bm, ok := b.(map[string]any); ok {
				out = append(out, bm)
			}
		}
		got[tf] = out
		delete(pending, tf)
	}
	return got
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSynthDemoBoot_SimFillsPriceAgainstSyntheticBook is Task 11's third
// checklist item: a sim order fills against the LIVE synthetic book via the
// book-walk path, mirroring replay_smoke_test.go's own
// TestE2EReplayDemoJournal_SimFillsPriceAgainstReplayedBook structure
// (including reconstructing the markBridge goroutine inline, per that test's
// documented precedent for why uihubtest can't import it from package main)
// -- substituted here with synth.New/synth.NewFeed driving md.Core instead of
// a replayed journal.
//
// Unlike the boot/warm-history test above, this one needs the generator
// actually ticking live (Feed.Run's StepTo/Drain loop) so a fresh BookEvent
// reaches the sim broker through the real chain (Feed -> md.Core.Feed ->
// md.Core's BookEvent handling -> Books() -> the bridge below ->
// sim.Broker.SetBook), so it uses clock.System{} throughout -- exactly what
// cmd/etape/main.go's -demo branch itself uses for synth.New/synth.NewFeed
// (verified: main.go never passes a Fake clock to either). A clock.Fake
// would need a background goroutine explicitly pumping Advance() in lockstep
// with Feed.Run's ticker to make any real-time progress at all, which adds
// ticker-buffering subtlety (clock.Fake's channel is capacity-1, dropping a
// tick if the consumer hasn't read the previous one) for no benefit here:
// this test's correctness doesn't depend on bit-exact reproducibility --
// only on the wiring genuinely delivering a book-priced fill -- and
// Tasks 1/2/6 already own the package's dedicated determinism tests.
func TestSynthDemoBoot_SimFillsPriceAgainstSyntheticBook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := openStore(t)
	clk := clock.System{}

	gen := synth.New(synthDemoSeed, clk)
	wantSymbol := gen.Symbols()[synthDemoLargeCapSlot]

	gen.Seed(st, clk.Now().UnixMilli())
	st.Flush()

	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	simBroker := sim.New("sim", clk, 100_000, sim.Options{})
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
		Venues: []uihub.VenueMeta{{ID: "sim", Broker: "sim", Gate: uihub.GateLimits{MaxOrderValue: 1e9}}},
		Global: uihub.GlobalLimits{MaxDayLoss: 1e9},
		MD:     15 * time.Millisecond, Account: 20 * time.Millisecond, Position: 20 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore, nil, nil, nil, nil, nil)
	go func() { _ = hub.Run(ctx) }()
	go forwardExec(ctx, execCore, hub)
	go forwardMD(ctx, mdCore, hub)

	// markAndBookBridge: main.go's markBridge, reconstructed here exactly as
	// replay_smoke_test.go's own copy does (see that file's doc comment) --
	// copies mdCore.Marks()/mdCore.Books() into execCore.FeedMark and the sim
	// broker's SetMark/SetBook, feeding from the synth Feed instead of a
	// replayed journal.
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

	// Drive the actual synthetic feed -- the same synth.NewFeed machinery
	// main.go's -demo branch uses -- into md.Core, exactly like main.go's
	// pipe() (unexported, package main; reconstructed here per this
	// package's established precedent, same as fd.Events() is consumed in
	// replay_smoke_test.go/e2e_test.go).
	sf := synth.NewFeed(gen, st, clk)
	go func() { _ = sf.Run(ctx) }()
	go func() {
		for ev := range sf.Events() {
			mdCore.Feed(ev)
		}
	}()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c := dialWS(t, ctx, url, wsmsg.TopicExecOrders, wsmsg.TopicExecAccount, wsmsg.TopicExecPositions)
	defer c.Close(websocket.StatusNormalClosure, "")

	sendCommand(t, ctx, c, "Arm", map[string]any{})

	// A small, unconditionally-marketable BUY: the limit (100000) sits
	// nowhere near any plausible large-cap price this universe draws
	// (SymbolSpec.Open in [80,500], universe.go), so this is guaranteed
	// marketable the instant a real BookEvent reaches the broker -- the
	// point of this test is proving the fill prices off THAT book, not off
	// the submitted limit.
	const wantQty = 50.0
	const submittedLimit = 100000.0
	corr := sendCommand(t, ctx, c, "SubmitOrder", map[string]any{
		"venue": "sim", "symbol": wantSymbol, "side": "BUY", "type": "LIMIT", "tif": "DAY",
		"qty": wantQty, "limitPrice": submittedLimit,
	})
	ack := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "ack" && m["corrId"] == corr })
	if ack["status"] != "accepted" {
		t.Fatalf("submit should be accepted, got %v", ack)
	}

	filled := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.orders" {
			return false
		}
		o, _ := m["payload"].(map[string]any)
		return o != nil && o["symbol"] == wantSymbol && o["status"] == "FILLED"
	})
	fo := filled["payload"].(map[string]any)
	if fo["executedQty"] != wantQty {
		t.Fatalf("expected a full %v-share fill against the synthetic book's touch, got %v", wantQty, fo)
	}
	avgFillPx, _ := fo["avgFillPrice"].(float64)

	// (1) Book-priced, not limit-priced: the synthetic large-cap price walk
	// never remotely approaches the submitted limit.
	if avgFillPx <= 0 || avgFillPx > submittedLimit/10 {
		t.Fatalf("avgFillPrice = %v: expected a real book price, nowhere near the submitted limit of %v", avgFillPx, submittedLimit)
	}

	// (2) Plausible price: within a generous band of the pre-run quote
	// (PrevClose) -- large-cap Vol (universe.go: 0.005-0.015) can't move the
	// price anywhere near this band's edges in the few live ticks this test
	// waits out, so this is a real, meaningful sanity bound, not a rubber
	// stamp.
	q, ok := gen.QuoteOf(wantSymbol)
	if !ok {
		t.Fatalf("QuoteOf(%s): symbol not found", wantSymbol)
	}
	if lo, hi := q.PrevClose*0.5, q.PrevClose*2; avgFillPx < lo || avgFillPx > hi {
		t.Fatalf("avgFillPrice = %v outside a sane band [%v,%v] of PrevClose %v", avgFillPx, lo, hi, q.PrevClose)
	}

	// (3) Book-walk specifically: consistent with the CURRENT book the same
	// live Generator reports right after the fill (a couple of percent of
	// slack for the live price walk's continued movement between the fill
	// and this read -- large-cap Vol makes anything beyond that implausible
	// over such a short span).
	book, ok := gen.BookOf(wantSymbol)
	if !ok || len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Fatalf("BookOf(%s) unavailable after fill: %+v ok=%v", wantSymbol, book, ok)
	}
	lo, hi := book.Bids[len(book.Bids)-1].Price*0.95, book.Asks[len(book.Asks)-1].Price*1.05
	if avgFillPx < lo || avgFillPx > hi {
		t.Fatalf("avgFillPrice = %v outside the current book's depth range [%v,%v] (bids/asks worst levels, 5%% slack)", avgFillPx, lo, hi)
	}

	// exec.account (AvailableCash decremented) and exec.positions (the
	// position opened at the filled size) must both show up -- waited for
	// TOGETHER in one read loop, not as two sequential waitFrame calls. Both
	// are hub.go "keep-latest, broadcast once per dirty tick" topics
	// (stageExec's classAccount/classPositions cases; hub.go:772-782), fed by
	// two INDEPENDENTLY ticked timers (acctTick/posTick, both configured to
	// the same 20ms interval below) with no ordering guarantee between them.
	// A first pass at this test used two sequential waitFrame calls here (one
	// per topic) and that was reliably flaky under `go test -race` (~1 in 8
	// runs): whichever delta's ticker happened to fire first sometimes went
	// out on the wire before the other, and a waitFrame scanning for the
	// OTHER topic would read-and-discard it while skipping non-matching
	// frames -- so the later, dedicated waitFrame for that topic then had
	// nothing left to ever see (each topic broadcasts its post-fill state
	// exactly once, since nothing in this test changes either again) and hit
	// waitFrame's fixed 4s deadline. Watching for both conditions in a single
	// pass, regardless of which frame arrives first, removes the race
	// entirely.
	acctPayload, posOK := waitAcctAndPosition(t, ctx, c, wantSymbol, wantQty)
	wantCash := 100_000.0 - wantQty*avgFillPx
	if cash := acctPayload["availableCash"].(float64); cash > wantCash+1e-6 || cash < wantCash-1e-6 {
		t.Fatalf("exec.account availableCash = %v, want %v (starting cash - qty*avgFillPrice)", cash, wantCash)
	}
	if !posOK {
		t.Fatalf("exec.positions never showed %s at qty=%v", wantSymbol, wantQty)
	}
}

// waitAcctAndPosition reads frames until it has seen BOTH an exec.account
// delta with availableCash < 100_000 AND an exec.positions delta whose rows
// contain symbol at qty (or the deadline passes), returning the account
// payload and whether the position was found. See the caller's doc comment
// for why this must watch for both conditions in one pass rather than two
// sequential waitFrame calls.
func waitAcctAndPosition(t *testing.T, ctx context.Context, c *websocket.Conn, symbol string, qty float64) (map[string]any, bool) {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	var acctPayload map[string]any
	posOK := false
	for acctPayload == nil || !posOK {
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read frame (acctSeen=%v posSeen=%v): %v", acctPayload != nil, posOK, err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil || m["kind"] != "delta" {
			continue
		}
		switch m["topic"] {
		case string(wsmsg.TopicExecAccount):
			p, _ := m["payload"].(map[string]any)
			if cash, ok := p["availableCash"].(float64); ok && cash < 100_000 {
				acctPayload = p
			}
		case string(wsmsg.TopicExecPositions):
			rows, _ := m["payload"].([]any)
			for _, r := range rows {
				row, _ := r.(map[string]any)
				if row != nil && row["symbol"] == symbol && row["qty"] == qty {
					posOK = true
				}
			}
		}
	}
	return acctPayload, posOK
}
