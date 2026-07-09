package opend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

// fakeRPC records Qot_Sub calls and answers success by default. Tests that
// exercise the failure path (rule 4) set failNext/retType to make the next
// call(s) fail with a transport error or a non-zero RetType respectively.
// failSyms (quarantine tests) fails only calls whose SecurityList contains
// one of the named bare codes — modeling a real batch where only some
// symbols (e.g. OTC codes) are rejected.
type fakeRPC struct {
	mu       sync.Mutex
	calls    []*qotsub.C2S
	failNext int             // remaining calls to fail with a transport error
	retType  int32           // non-zero RetType to return instead of success
	failSyms map[string]bool // bare codes (no "US." prefix) that force retType!=0
}

func (f *fakeRPC) Request(_ context.Context, protoID uint32, req proto.Message) (Frame, error) {
	if protoID != ProtoQotSub {
		panic("subManager must only send Qot_Sub")
	}
	f.mu.Lock()
	c2s := proto.Clone(req.(*qotsub.Request)).(*qotsub.Request).GetC2S()
	f.calls = append(f.calls, c2s)
	failNow := f.failNext > 0
	if failNow {
		f.failNext--
	}
	retType := f.retType
	for _, sec := range c2s.GetSecurityList() {
		if f.failSyms[sec.GetCode()] && retType == 0 {
			retType = 1
		}
	}
	f.mu.Unlock()
	if failNow {
		return Frame{}, errors.New("fake rpc transport error")
	}
	body, _ := proto.Marshal(&qotsub.Response{RetType: proto.Int32(retType), S2C: &qotsub.S2C{}})
	return Frame{ProtoID: ProtoQotSub, Body: body}, nil
}

func (f *fakeRPC) snapshot() []*qotsub.C2S {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*qotsub.C2S(nil), f.calls...)
}

func (f *fakeRPC) setFailSyms(syms ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failSyms = make(map[string]bool, len(syms))
	for _, s := range syms {
		f.failSyms[strings.TrimPrefix(s, "US.")] = true
	}
}

// pump runs one synchronous worker pass (test seam — see step 3).
func newTestManager(t *testing.T, budget int) (*subManager, *fakeRPC, *clock.Fake) {
	t.Helper()
	rpc := &fakeRPC{}
	clk := clock.NewFake(time.Unix(1_782_000_000, 0))
	m := newSubManager(rpc, clk, subOptions{Budget: budget, MinHold: time.Minute, Hysteresis: 5 * time.Minute, ExtendedTime: true})
	return m, rpc, clk
}

func TestEnsureBatchesAndRefcounts(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w1", "US.AAPL"))
	m.Ensure(feed.WatchDemand("w2", "US.TSLA"))
	m.Ensure(feed.WatchDemand("w1b", "US.AAPL")) // second demand, same symbol
	m.pass(context.Background())

	calls := rpc.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 batched Qot_Sub for one subtype-group, got %d", len(calls))
	}
	c := calls[0]
	if !c.GetIsSubOrUnSub() || !c.GetIsRegOrUnRegPush() || !c.GetIsFirstPush() || !c.GetExtendedTime() {
		t.Fatalf("subscribe flags wrong: %+v", c)
	}
	if len(c.GetSecurityList()) != 2 || len(c.GetSubTypeList()) != 2 {
		t.Fatalf("want 2 symbols x [Ticker,KL1m], got %d symbols x %d subtypes",
			len(c.GetSecurityList()), len(c.GetSubTypeList()))
	}
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots = %d, want 4 (2 symbols x 2 subtypes)", got)
	}
	// Releasing one of AAPL's two demands must not unsubscribe.
	m.Release("w1")
	m.pass(context.Background())
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots after partial release = %d, want 4", got)
	}
}

func TestUnsubscribeWaitsForMinHoldAndHysteresis(t *testing.T) {
	m, rpc, clk := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.pass(context.Background())
	m.Release("w")
	m.pass(context.Background()) // stamps droppedAt (worker's first observation)

	clk.Advance(2 * time.Minute) // past MinHold, inside Hysteresis
	m.pass(context.Background())
	if n := len(rpc.snapshot()); n != 1 {
		t.Fatalf("unsubscribed inside hysteresis window (calls=%d)", n)
	}
	clk.Advance(4 * time.Minute) // 6m since droppedAt: past Hysteresis
	m.pass(context.Background())
	calls := rpc.snapshot()
	if last := calls[len(calls)-1]; last.GetIsSubOrUnSub() {
		t.Fatal("expected an unsubscribe call")
	}
	if got := m.Slots(); got != 0 {
		t.Fatalf("Slots = %d, want 0", got)
	}

	// Re-Ensure inside the window cancels a pending unsubscribe.
	m.Ensure(feed.WatchDemand("w2", "US.MSFT"))
	m.pass(context.Background())
	m.Release("w2")
	m.pass(context.Background()) // droppedAt stamped
	clk.Advance(2 * time.Minute)
	m.Ensure(feed.WatchDemand("w3", "US.MSFT")) // re-desired: cancels the drop
	base := len(rpc.snapshot())
	clk.Advance(10 * time.Minute)
	m.pass(context.Background())
	for _, c := range rpc.snapshot()[base:] {
		if !c.GetIsSubOrUnSub() {
			t.Fatal("MSFT was unsubscribed despite re-Ensure")
		}
	}
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots = %d, want 2 (MSFT still live)", got)
	}
}

func TestPressureWaivesHysteresisButNeverMinHold(t *testing.T) {
	m, rpc, clk := newTestManager(t, 2) // room for exactly one watch symbol
	m.Ensure(feed.WatchDemand("a", "US.AAA"))
	m.pass(context.Background())
	m.Release("a")
	m.pass(context.Background()) // droppedAt stamped; slots still held

	// New demand needs the held slots. Inside MinHold nothing can move:
	// the add must wait (starved), the lingering subs must survive.
	m.Ensure(feed.WatchDemand("b", "US.BBB"))
	clk.Advance(30 * time.Second)
	m.pass(context.Background())
	if s := m.Starved(); len(s) != 1 || s[0] != "US.BBB" {
		t.Fatalf("Starved = %v, want [US.BBB] while MinHold pins the old slots", s)
	}
	// Past MinHold (but far inside the 5m hysteresis) pressure evicts AAA.
	clk.Advance(31 * time.Second)
	m.pass(context.Background())
	act := m.ActiveSymbols()
	if len(act["US.BBB"]) != 2 || len(act["US.AAA"]) != 0 {
		t.Fatalf("ActiveSymbols = %v, want BBB in, AAA pressure-evicted", act)
	}
	var unsubs int
	for _, c := range rpc.snapshot() {
		if !c.GetIsSubOrUnSub() {
			unsubs++
		}
	}
	if unsubs != 1 {
		t.Fatalf("unsubscribe calls = %d, want exactly 1 (pressure eviction)", unsubs)
	}
}

func TestBudgetStarvesLRUNonFocused(t *testing.T) {
	m, _, clk := newTestManager(t, 5)             // room for one watch(2) + one focused(4)? no: 5 slots
	m.Ensure(feed.WatchDemand("w-old", "US.OLD")) // 2 slots, oldest
	clk.Advance(time.Second)
	m.Ensure(feed.Demand{ID: "f", Symbol: "US.FOC", Focused: true,
		Subs: []feed.SubType{feed.SubQuote, feed.SubBook, feed.SubTicker, feed.SubKL1m}}) // 4 slots, focused
	m.pass(context.Background())
	// Focused first (4 slots), then LRU: OLD needs 2 > remaining 1 → starved.
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots = %d, want 4 (focused only)", got)
	}
	if s := m.Starved(); len(s) != 1 || s[0] != "US.OLD" {
		t.Fatalf("Starved = %v, want [US.OLD]", s)
	}
	// Freeing the focused demand lets the starved symbol subscribe next pass.
	m.Release("f")
	clk.Advance(10 * time.Minute)
	m.pass(context.Background())
	if s := m.Starved(); len(s) != 0 {
		t.Fatalf("Starved after release = %v, want none", s)
	}
}

func TestResubscribeAllReissuesActiveSet(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.Ensure(feed.Demand{ID: "f", Symbol: "US.TSLA", Focused: true,
		Subs: []feed.SubType{feed.SubQuote, feed.SubBook, feed.SubTicker, feed.SubKL1m}})
	m.pass(context.Background())
	before := len(rpc.snapshot())
	if err := m.ResubscribeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := rpc.snapshot()[before:]
	if len(after) != 2 { // two subtype-groups: [Ticker,KL1m] and [Quote,Book,Ticker,KL1m]
		t.Fatalf("ResubscribeAll issued %d calls, want 2 (one per subtype-group)", len(after))
	}
	act := m.ActiveSymbols()
	if len(act) != 2 || len(act["US.TSLA"]) != 4 {
		t.Fatalf("ActiveSymbols = %v", act)
	}
}

// TestQotSubTransportErrorLeavesStateUnchangedAndRetries covers rule 4: a
// transport-level error from rpc.Request must not mutate active state, must
// not panic, and must be retried on the next pass.
func TestQotSubTransportErrorLeavesStateUnchangedAndRetries(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	rpc.mu.Lock()
	rpc.failNext = 1
	rpc.mu.Unlock()

	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.pass(context.Background()) // subscribe attempt fails
	if got := m.Slots(); got != 0 {
		t.Fatalf("Slots after failed subscribe = %d, want 0 (state unchanged)", got)
	}
	if n := len(rpc.snapshot()); n != 1 {
		t.Fatalf("want 1 attempted (failed) call, got %d", n)
	}

	m.pass(context.Background()) // retry succeeds
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots after retry = %d, want 2", got)
	}
	if n := len(rpc.snapshot()); n != 2 {
		t.Fatalf("want 2 total calls (1 failed + 1 retry), got %d", n)
	}
}

// TestQotSubNonZeroRetTypeLeavesStateUnchangedAndRetries covers the other
// half of rule 4: a non-zero RetType in an otherwise successful response is
// also a failure that must not mutate state and must be retried.
func TestQotSubNonZeroRetTypeLeavesStateUnchangedAndRetries(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	rpc.mu.Lock()
	rpc.retType = 1
	rpc.mu.Unlock()

	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.pass(context.Background()) // subscribe attempt "succeeds" transportwise but RetType != 0
	if got := m.Slots(); got != 0 {
		t.Fatalf("Slots after RetType!=0 = %d, want 0 (state unchanged)", got)
	}

	rpc.mu.Lock()
	rpc.retType = 0
	rpc.mu.Unlock()
	m.pass(context.Background()) // retry succeeds
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots after retry = %d, want 2", got)
	}
}

func TestEnsureEmptySubsUsesNoQuota(t *testing.T) {
	m, _, _ := newTestManager(t, 100)
	m.Ensure(feed.Demand{ID: "dyn/1/interest", Symbol: "US.AAPL"}) // no Subs
	want, _ := m.desired(100)
	if len(want) != 0 {
		t.Fatalf("empty-subs demand consumed %d slot(s), want 0", len(want))
	}
}

// TestSubscribeBinarySplitIsolatesBadSymbolAndQuarantinesAfterThreshold
// covers the two real defects this fixes: batch poisoning (one hard-failing
// symbol in a Qot_Sub batch must not block its batch-mates from
// subscribing) and no-give-up (a symbol that keeps hard-failing must stop
// being retried every pass once it crosses subQuarantineThreshold).
func TestSubscribeBinarySplitIsolatesBadSymbolAndQuarantinesAfterThreshold(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	rpc.setFailSyms("BAD") // e.g. an OTC code with no quote entitlement

	for _, sym := range []string{"US.AAA", "US.BBB", "US.BAD", "US.CCC"} {
		m.Ensure(feed.WatchDemand(sym, sym))
	}
	m.pass(context.Background()) // pass 1: all four batched together (same subtype set)

	act := m.ActiveSymbols()
	for _, sym := range []string{"US.AAA", "US.BBB", "US.CCC"} {
		if len(act[sym]) != 2 {
			t.Fatalf("ActiveSymbols[%s] = %v, want 2 subs active despite BAD sharing the batch", sym, act[sym])
		}
	}
	if len(act["US.BAD"]) != 0 {
		t.Fatalf("ActiveSymbols[US.BAD] = %v, want none (still failing)", act["US.BAD"])
	}
	if q := m.Quarantined(); len(q) != 0 {
		t.Fatalf("Quarantined = %v after 1 failure, want none (below threshold)", q)
	}

	// subQuarantineThreshold-1 more isolated failures cross the threshold.
	for i := 1; i < subQuarantineThreshold; i++ {
		m.pass(context.Background())
	}
	if q := m.Quarantined(); len(q) != 1 || q[0] != "US.BAD" {
		t.Fatalf("Quarantined = %v, want [US.BAD]", q)
	}

	// Once quarantined, BAD must stop generating Qot_Sub calls entirely —
	// this is the fix for the once-per-second WARN spam.
	callsAtQuarantine := len(rpc.snapshot())
	m.pass(context.Background())
	m.pass(context.Background())
	if n := len(rpc.snapshot()); n != callsAtQuarantine {
		t.Fatalf("Qot_Sub calls after quarantine = %d, want %d (BAD no longer retried)", n, callsAtQuarantine)
	}
}

// TestSubscribeTransientBizFailureRecoversBeforeQuarantine guards the
// threshold: a business rejection that clears before reaching
// subQuarantineThreshold consecutive failures must retry-and-recover, not
// quarantine — a one-off rejection (rate limit, momentary entitlement
// hiccup) must not be treated as permanent.
func TestSubscribeTransientBizFailureRecoversBeforeQuarantine(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	rpc.setFailSyms("FLAKY")

	m.Ensure(feed.WatchDemand("w", "US.FLAKY"))
	for i := 0; i < subQuarantineThreshold-1; i++ {
		m.pass(context.Background())
	}
	if got := m.Slots(); got != 0 {
		t.Fatalf("Slots = %d, want 0 (still failing)", got)
	}
	if q := m.Quarantined(); len(q) != 0 {
		t.Fatalf("Quarantined = %v, want none (below threshold)", q)
	}

	rpc.setFailSyms() // recovers before hitting the threshold
	m.pass(context.Background())
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots after recovery = %d, want 2", got)
	}
	if q := m.Quarantined(); len(q) != 0 {
		t.Fatalf("Quarantined after recovery = %v, want none", q)
	}
}

// TestResubscribeAllClearsQuarantine covers the reconnect boundary: a
// quarantined symbol gets exactly one fresh retry after ResubscribeAll,
// since a reconnect is a clean session boundary (entitlements may have
// changed, or the failure may have been server-side).
func TestResubscribeAllClearsQuarantine(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	rpc.setFailSyms("BAD")
	m.Ensure(feed.WatchDemand("w", "US.BAD"))
	for i := 0; i < subQuarantineThreshold; i++ {
		m.pass(context.Background())
	}
	if q := m.Quarantined(); len(q) != 1 || q[0] != "US.BAD" {
		t.Fatalf("Quarantined = %v, want [US.BAD]", q)
	}

	rpc.setFailSyms() // e.g. entitlement recovered server-side
	if err := m.ResubscribeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q := m.Quarantined(); len(q) != 0 {
		t.Fatalf("Quarantined after ResubscribeAll = %v, want none", q)
	}

	// BAD was never active, so ResubscribeAll (which only reissues
	// m.active) doesn't resubscribe it directly — but clearing quarantine
	// means the next pass retries it.
	m.pass(context.Background())
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots after retry post-reconnect = %d, want 2", got)
	}
}
