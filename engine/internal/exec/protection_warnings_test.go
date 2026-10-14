package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

type protectionSnapshotAnswer struct {
	positions []Position
	err       error
}

type protectionTestBroker struct {
	*heldTestBroker
	requests chan context.Context
	answers  chan protectionSnapshotAnswer
}

func (b *protectionTestBroker) Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error) {
	select {
	case b.requests <- ctx:
	case <-ctx.Done():
		return AccountSnapshot{}, nil, nil, ctx.Err()
	}
	select {
	case answer := <-b.answers:
		return AccountSnapshot{Venue: "v", BuyingPower: 100000}, answer.positions, nil, answer.err
	case <-ctx.Done():
		return AccountSnapshot{}, nil, nil, ctx.Err()
	}
}

func newProtectionCore(t *testing.T) (*Core, *protectionTestBroker, context.Context) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC))
	b := &protectionTestBroker{heldTestBroker: newHeldTestBroker(), requests: make(chan context.Context, 2), answers: make(chan protectionSnapshotAnswer, 2)}
	c := NewCore(CoreConfig{Venues: []VenueID{"v"}, Store: &heldTestStore{}, Clock: clk, Brokers: map[VenueID]Broker{"v": b}})
	entry := Order{Venue: "v", ID: "entry", Symbol: "AAPL", Side: SideBuy, Status: StatusFilled, ExecutedQty: 3,
		CreatedMs: c.now(), UpdatedMs: c.now(), RiskEntry: &RiskEntry{StopID: "stop", Failure: "protection interrupted; verify stop"}}
	stop := Order{Venue: "v", ID: "stop", Symbol: "AAPL", Side: SideSell, Status: StatusCanceled, RiskEntryID: entry.ID}
	if err := c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: stop}, SrcLocal); err != nil {
		t.Fatal(err)
	}
	c.state.ReconcilePosition(Position{Venue: "v", Symbol: "AAPL", Qty: 3})
	c.state.SetPositionsReady("v", true)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return c, b, ctx
}

func expectProtectionRequest(t *testing.T, b *protectionTestBroker) {
	t.Helper()
	select {
	case ctx := <-b.requests:
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > defaultRecoverSnapshotTimeout {
			t.Fatal("broker verification has no bounded deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("automatic broker verification missing")
	}
}

func finishProtectionAnswer(t *testing.T, c *Core, b *protectionTestBroker, ctx context.Context, answer protectionSnapshotAnswer) {
	t.Helper()
	b.answers <- answer
	select {
	case result := <-c.bevents:
		c.handleBrokerEvent(ctx, result)
	case <-time.After(time.Second):
		t.Fatal("verification result missing")
	}
}

func TestProtectionWarningsVerifyActualQuantityWithoutOrderActions(t *testing.T) {
	c, b, ctx := newProtectionCore(t)
	// A snapshot through the ordinary risk synchronization path could trigger this stop.
	stop := c.order("stop")
	stop.Status, stop.StopPrice, stop.LimitPrice = StatusAccepted, 10, 10
	stop.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: c.now() + 60000}
	c.state.Apply(OrderSubmitted{Order: stop})
	c.printHealthy, c.heldDemand = true, &heldDemandStub{}
	c.lastEligible["AAPL"] = eligible(1, c.clk.Now(), 9)
	c.state.SetMasterArmed(true)
	c.refreshProtectionWarnings(ctx)
	expectProtectionRequest(t, b)
	for range 5 {
		c.refreshProtectionWarnings(ctx)
	}
	if len(b.requests) != 0 {
		t.Fatal("duplicate in-flight broker verification")
	}
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1}}})
	warnings := c.protectionChecks["v"].warnings
	if len(warnings) != 1 || warnings[0].Qty != 1 || warnings[0].State != "open" {
		t.Fatalf("warning must use the actual broker quantity, not historical three shares: %+v", warnings)
	}
	if c.order("stop").Held.Phase != HeldWaiting || b.cancelCount() != 0 || len(b.submits) != 0 {
		t.Fatal("observational verification changed broker orders or activated protection")
	}
}

func TestProtectionWarningsFlatBoundarySurvivesRestartAndAllowsLateFills(t *testing.T) {
	c, b, ctx := newProtectionCore(t)
	c.refreshProtectionWarnings(ctx)
	expectProtectionRequest(t, b)
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{})
	if c.order("entry").RiskEntry.FlatConfirmedQty != 3 || len(c.protectionChecks["v"].warnings) != 0 {
		t.Fatal("confirmed flat did not retire the historical warning")
	}
	broker := newHeldTestBroker()
	broker.setPositions([]Position{{Venue: "v", Symbol: "AAPL", Qty: 2}})
	restarted := NewCore(CoreConfig{Venues: []VenueID{"v"}, Store: c.store, Clock: c.clk, Brokers: map[VenueID]Broker{"v": broker}})
	if err := restarted.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if len(restarted.protectionChecks["v"].warnings) != 0 || restarted.order("entry").RiskEntry.FlatConfirmedQty != 3 {
		t.Fatal("a new same-symbol position revived the retired warning after restart")
	}
	restarted.handleBrokerEvent(ctx, OrderFilled{F: Fill{Venue: "v", OrderID: "entry", Symbol: "AAPL", Side: SideBuy, Qty: 1, Price: 10, TsMs: c.now()}, CumQty: 4, AvgPrice: 10})
	if len(restarted.protectionChecks["v"].warnings) != 1 {
		t.Fatal("a newly confirmed late entry fill was hidden by flat retirement")
	}
}

func TestProtectionWarningsFailedCheckStaysUnknownAndRetries(t *testing.T) {
	c, b, ctx := newProtectionCore(t)
	c.state.ReconcilePosition(Position{Venue: "v", Symbol: "AAPL"})
	c.refreshProtectionWarnings(ctx)
	expectProtectionRequest(t, b)
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{err: errors.New("snapshot unavailable")})
	warnings := c.protectionChecks["v"].warnings
	if len(warnings) != 1 || warnings[0].State != "unverified" || c.order("entry").RiskEntry.FlatConfirmedQty != 0 {
		t.Fatalf("failure must not confirm flatness: %+v", warnings)
	}
	c.clk.(*clock.Fake).Advance(protectionCheckInterval)
	c.refreshProtectionWarnings(ctx)
	expectProtectionRequest(t, b)
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{})
	if len(c.protectionChecks["v"].warnings) != 0 {
		t.Fatal("successful automatic retry did not clear the warning")
	}
}

func TestProtectionWarningsRejectSnapshotRacingBrokerPush(t *testing.T) {
	c, b, ctx := newProtectionCore(t)
	c.refreshProtectionWarnings(ctx)
	expectProtectionRequest(t, b)
	c.handleBrokerEvent(ctx, BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 2}})
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{})
	if c.state.Venue("v").Positions["AAPL"].Qty != 2 || c.order("entry").RiskEntry.FlatConfirmedQty != 0 {
		t.Fatal("a stale flat snapshot overwrote a newer broker position")
	}
	if warnings := c.protectionChecks["v"].warnings; len(warnings) != 1 || warnings[0].State == "open" {
		t.Fatalf("racing snapshot must remain unverified: %+v", warnings)
	}
}

func TestProtectionWarningsAbsoluteFlatPushRetiresOrdinaryExit(t *testing.T) {
	c, _, ctx := newProtectionCore(t)
	c.handleBrokerEvent(ctx, BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL"}})
	if c.order("entry").RiskEntry.FlatConfirmedQty != 3 || len(c.protectionChecks["v"].warnings) != 0 {
		t.Fatal("ordinary broker-confirmed flat exit did not retire the linked-entry warning")
	}
}

func TestProtectionWarningsObservationDoesNotRestoreStreamReadiness(t *testing.T) {
	c, b, ctx := newProtectionCore(t)
	c.handleBrokerEvent(ctx, BrokerConnDown{V: "v"})
	expectProtectionRequest(t, b)
	finishProtectionAnswer(t, c, b, ctx, protectionSnapshotAnswer{positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1}}})
	if c.state.PositionsReady("v") {
		t.Fatal("an observational check cleared the disconnected venue's readiness gate")
	}
	if warnings := c.protectionChecks["v"].warnings; len(warnings) != 1 || warnings[0].State != "unverified" {
		t.Fatalf("disconnected positions must remain visibly unverified: %+v", warnings)
	}
}
