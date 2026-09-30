package exec

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type heldTestStore struct {
	mu     sync.Mutex
	events []EventEnvelope
}

func (s *heldTestStore) AppendExecEvent(env EventEnvelope, _ *FillRow) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	env.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, env)
	return env.Seq, nil
}
func (s *heldTestStore) ReadExecEventsSince(fromMs int64) ([]EventEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []EventEnvelope
	for _, env := range s.events {
		if env.TsMs >= fromMs {
			out = append(out, env)
		}
	}
	return out, nil
}
func (s *heldTestStore) ReadExecOrderHistoriesFor(ids []string) ([]EventEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	var out []EventEnvelope
	for _, env := range s.events {
		if wanted[env.OrderID] {
			out = append(out, env)
		}
	}
	return out, nil
}
func (*heldTestStore) QueryFillsSince(context.Context, int64) ([]FillRow, error) { return nil, nil }

type heldTestBroker struct {
	ev            chan BrokerEvent
	submits       chan OrderRequest
	mu            sync.Mutex
	orders        map[string]Order
	submitErr     error
	submitBlockID string
	submitRelease <-chan struct{}
	replaceErr    error
	cancelErr     error
	cancelCalls   int
}

func newHeldTestBroker() *heldTestBroker {
	return &heldTestBroker{ev: make(chan BrokerEvent, 16), submits: make(chan OrderRequest, 8), orders: map[string]Order{}}
}
func (b *heldTestBroker) cancelCount() int         { b.mu.Lock(); defer b.mu.Unlock(); return b.cancelCalls }
func (*heldTestBroker) Capabilities() Capabilities { return Capabilities{} }
func (b *heldTestBroker) SubmitOrder(_ context.Context, req OrderRequest) (OrderAck, error) {
	b.mu.Lock()
	block := b.submitBlockID == req.ClientOrderID
	release := b.submitRelease
	b.mu.Unlock()
	if block {
		b.submits <- req
		<-release
	}
	b.mu.Lock()
	b.orders[req.ClientOrderID] = newOrderFromRequest(req, time.Now().UnixMilli())
	err := b.submitErr
	b.mu.Unlock()
	if !block {
		b.submits <- req
	}
	return OrderAck{OrderID: req.ClientOrderID, Accepted: err == nil}, err
}
func (b *heldTestBroker) ReplaceOrder(_ context.Context, id string, req ReplaceRequest) error {
	b.mu.Lock()
	err := b.replaceErr
	b.mu.Unlock()
	if err != nil {
		return err
	}
	b.ev <- OrderReplaced{V: "v", OID: id, NewQty: req.Qty, NewLimit: req.LimitPrice, NewStop: req.StopPrice, Ts: time.Now().UnixMilli()}
	return nil
}
func (b *heldTestBroker) CancelOrder(_ context.Context, id string) error {
	b.mu.Lock()
	b.cancelCalls++
	err := b.cancelErr
	if err != nil {
		b.mu.Unlock()
		return err
	}
	delete(b.orders, id)
	b.mu.Unlock()
	b.ev <- OrderCanceled{V: "v", OID: id, Ts: time.Now().UnixMilli()}
	return nil
}
func (*heldTestBroker) CancelAll(context.Context, string) error     { return nil }
func (*heldTestBroker) Flatten(context.Context) error               { return nil }
func (*heldTestBroker) ResetBalance(context.Context, float64) error { return nil }
func (b *heldTestBroker) Snapshot(context.Context) (AccountSnapshot, []Position, []Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	orders := make([]Order, 0, len(b.orders))
	for _, o := range b.orders {
		orders = append(orders, o)
	}
	return AccountSnapshot{Venue: "v", BuyingPower: 100_000, AvailableCash: 100_000, TsMs: time.Now().UnixMilli()}, nil, orders, nil
}
func (b *heldTestBroker) Events() <-chan BrokerEvent { return b.ev }

type heldDemandStub struct {
	mu   sync.Mutex
	held map[string]string
}

func (d *heldDemandStub) Acquire(_ context.Context, id, symbol string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.held == nil {
		d.held = map[string]string{}
	}
	d.held[id] = symbol
	return nil
}
func (d *heldDemandStub) Release(id string) { d.mu.Lock(); defer d.mu.Unlock(); delete(d.held, id) }

func newHeldCore(t *testing.T, at time.Time) (*Core, *clock.Fake, *heldTestBroker, context.Context) {
	return newHeldCoreWithLiveAck(t, at, nil, nil)
}

func newHeldCoreWithLiveAck(t *testing.T, at time.Time, identity, acknowledged map[VenueID]string) (*Core, *clock.Fake, *heldTestBroker, context.Context) {
	t.Helper()
	clk := clock.NewFake(at)
	broker := newHeldTestBroker()
	c := NewCore(CoreConfig{
		Venues: []VenueID{"v"}, Gate: GateConfig{
			Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionValue: 1_000_000, MaxSymbolPositionShares: 1000},
			Venue:  map[VenueID]VenueLimits{"v": {MaxOrderValue: 100_000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
		},
		Store: &heldTestStore{}, Brokers: map[VenueID]Broker{"v": broker}, Clock: clk, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
		HeldStopLimitLiveIdentity: identity, HeldStopLimitAcknowledged: acknowledged,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	if ack := c.Do(ConfigureHeldDemand{Demand: &heldDemandStub{}}); !ack.Accepted {
		t.Fatalf("configure held demand: %+v", ack)
	}
	if ack := c.Do(Arm{}); !ack.Accepted {
		t.Fatalf("arm: %+v", ack)
	}
	return c, clk, broker, ctx
}

func waitHeldOrder(t *testing.T, c *Core, id string, phase HeldPhase) Order {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case update := <-c.Updates():
			if got, ok := update.(OrderUpdate); ok && got.Order.ID == id && got.Order.Held != nil && got.Order.Held.Phase == phase {
				return got.Order
			}
		case <-deadline:
			t.Fatalf("timed out waiting for held %s phase", phase)
			return Order{}
		}
	}
}

func waitOrder(t *testing.T, c *Core, id string, match func(Order) bool) Order {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case update := <-c.Updates():
			if got, ok := update.(OrderUpdate); ok && got.Order.ID == id && match(got.Order) {
				return got.Order
			}
		case <-deadline:
			t.Fatal("timed out waiting for order update")
			return Order{}
		}
	}
}

func eligible(seq int64, at time.Time, price float64) EligiblePrint {
	return EligiblePrint{Symbol: "AAPL", Price: price, TsMs: at.UnixMilli(), RecvTsMs: at.UnixMilli(), Seq: seq}
}

func TestEngineHeldStopLimitPostsOneLimitOnlyAfterEligibleTrigger(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, now)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionAuto, Qty: 10, StopPrice: 101, LimitPrice: 101.5, RouteExpected: RouteEngineHeld})
	if !ack.Accepted {
		t.Fatalf("held submit: %+v", ack)
	}
	parent := waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	if parent.Session != SessionExtended || parent.Held.DeadlineMs != session.Schedule(now).Open.UnixMilli() {
		t.Fatalf("bad held parent: %+v", parent)
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("posted before trigger: %+v", req)
	default:
	}

	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 100))
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	select {
	case req := <-broker.submits:
		t.Fatalf("posted before crossing: %+v", req)
	default:
	}

	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 101))
	activating := waitHeldOrder(t, c, ack.OrderID, HeldActivating)
	if activating.Held.ChildClientID != ack.OrderID {
		t.Fatalf("child identity not persisted on parent: %+v", activating.Held)
	}
	select {
	case req := <-broker.submits:
		if req.Type != TypeLimit || req.ClientOrderID != ack.OrderID || req.Qty != parent.Qty || req.LimitPrice != parent.LimitPrice || req.Session != SessionExtended {
			t.Fatalf("trigger child=%+v, parent=%+v", req, parent)
		}
	case <-time.After(time.Second):
		t.Fatal("trigger did not submit child")
	}
	broker.ev <- OrderAccepted{V: "v", OID: ack.OrderID, BrokerOrderID: "broker-child", Ts: clk.Now().UnixMilli()}
	working := waitHeldOrder(t, c, ack.OrderID, HeldWorking)
	if working.Type != TypeStopLimit || working.Held.ChildBrokerID != "broker-child" {
		t.Fatalf("parent identity/child link lost: %+v", working)
	}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: parent.Qty, Price: parent.LimitPrice, TsMs: clk.Now().UnixMilli()}, CumQty: parent.Qty, AvgPrice: parent.LimitPrice}
	terminal := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusFilled })
	if terminal.Held == nil || terminal.ExecutedQty != parent.Qty || terminal.LeavesQty != 0 {
		t.Fatalf("terminal child fill lost parent lifecycle: %+v", terminal)
	}
}

func TestResumeWaitsForANewEligiblePrint(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, now)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	if !ack.Accepted {
		t.Fatalf("held submit: %+v", ack)
	}
	orderID := ack.OrderID
	waitHeldOrder(t, c, orderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 100))
	waitHeldOrder(t, c, orderID, HeldArmed)
	c.Do(Disarm{})
	waitHeldOrder(t, c, orderID, HeldPaused)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 101)) // crossed while paused
	clk.Advance(time.Millisecond)
	c.Do(Arm{})
	if resumeAck := c.Do(ResumeHeldOrder{Venue: "v", OrderID: orderID}); !resumeAck.Accepted {
		t.Fatalf("resume: %+v", resumeAck)
	}
	resumed := waitHeldOrder(t, c, orderID, HeldWaiting)
	if resumed.Held.ResumeAfterMs != clk.Now().UnixMilli() {
		t.Fatalf("resume timestamp=%d, want %d", resumed.Held.ResumeAfterMs, clk.Now().UnixMilli())
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("resume reused a pre-resume print: %+v", req)
	default:
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 101))
	select {
	case req := <-broker.submits:
		if req.Type != TypeLimit || req.ClientOrderID != orderID {
			t.Fatalf("trigger order=%+v, want one linked LIMIT", req)
		}
	case <-time.After(time.Second):
		t.Fatal("new post-resume print did not trigger")
	}
}

func TestEligiblePrintGapPausesHeldOrderUntilManualResume(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, now)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay,
		Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	if !ack.Accepted {
		t.Fatalf("held submit: %+v", ack)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 100))
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	c.FeedEligiblePrint(ctx, EligiblePrint{Gap: true})
	paused := waitHeldOrder(t, c, ack.OrderID, HeldPaused)
	if paused.Held.PausedReason != "eligible-print gap; resume manually" {
		t.Fatalf("gap pause reason = %q", paused.Held.PausedReason)
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("gap-crossed order was submitted: %+v", req)
	default:
	}

	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 101)) // recover feed, but paused order stays paused
	if resume := c.Do(ResumeHeldOrder{Venue: "v", OrderID: ack.OrderID}); !resume.Accepted {
		t.Fatalf("resume after feed recovery: %+v", resume)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	select {
	case req := <-broker.submits:
		t.Fatalf("resume reused a pre-resume crossing: %+v", req)
	default:
	}

	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 101))
	if got := waitHeldOrder(t, c, ack.OrderID, HeldActivating); got.Held.ChildClientID != ack.OrderID {
		t.Fatalf("fresh post-resume crossing did not activate same parent: %+v", got.Held)
	}
}

func TestBufferedPreGapPrintCannotRestoreTriggerHealth(t *testing.T) {
	at := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	clk := clock.NewFake(at)
	c := NewCore(CoreConfig{Venues: []VenueID{"v"}, Clock: clk})
	gapAt := at.Add(time.Millisecond)
	c.handleEligiblePrint(context.Background(), EligiblePrint{Gap: true, RecvTsMs: gapAt.UnixMilli()})
	c.handleEligiblePrint(context.Background(), eligible(1, at, 101))
	if c.printHealthy || len(c.lastEligible) != 0 {
		t.Fatalf("pre-gap print restored trigger health: healthy=%v last=%+v", c.printHealthy, c.lastEligible)
	}
}

func TestPreviewUsesOnlyFreshEligiblePrintsAndClearsOnGap(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, _, ctx := newHeldCore(t, now)
	if got := c.PreviewEligiblePrint(ctx, "AAPL"); got.Trusted {
		t.Fatalf("preview trusted a print before any live tick: %+v", got)
	}
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 101))
	deadline := time.After(time.Second)
	for {
		got := c.PreviewEligiblePrint(ctx, "AAPL")
		if got.Trusted {
			if got.Price != 101 || got.TsMs != clk.Now().UnixMilli() {
				t.Fatalf("trusted preview = %+v", got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("fresh eligible print was not exposed to preview")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	c.FeedEligiblePrint(ctx, EligiblePrint{Gap: true})
	deadline = time.After(time.Second)
	for {
		got := c.PreviewEligiblePrint(ctx, "AAPL")
		if !got.Trusted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("preview retained trust after a stream gap")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestHeldOrderMetadataReplays(t *testing.T) {
	h := HeldOrder{Phase: HeldPaused, DeadlineMs: 7, ResumeAfterMs: 6, ChildClientID: "child", PausedReason: "feed gap"}
	ev := HeldOrderChanged{V: "v", OID: "o", Held: h, Ts: 8}
	kind, payload, err := EncodeEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvent(kind, payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire HeldOrderChanged
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if got := decoded.(HeldOrderChanged).Held; got != h {
		t.Fatalf("decoded=%+v want %+v", got, h)
	}
	if wire.Held.ResumeAfterMs != 6 {
		t.Fatalf("resume timestamp lost in event JSON: %+v", wire.Held)
	}
}

func TestVenueReplacePersistsIntentAndPreservesCurrentTotalWhenQtyOmitted(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, _ := newHeldCore(t, now)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 99})
	if !ack.Accepted {
		t.Fatalf("submit: %+v", ack)
	}
	broker.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusAccepted })
	if got := c.Do(ReplaceOrder{Venue: "v", OrderID: ack.OrderID, LimitPrice: 101}); !got.Accepted {
		t.Fatalf("price-only replace: %+v", got)
	}
	order := waitOrder(t, c, ack.OrderID, func(o Order) bool {
		return o.LimitPrice == 101 && o.Action != nil && o.Action.Phase == ActionConfirmed
	})
	if order.Qty != 10 || order.LeavesQty != 10 {
		t.Fatalf("price-only replace changed quantity: %+v", order)
	}
}

func TestVenueReplaceTransportErrorRetainsOldAndRequestedPricesAsUnknown(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, _ := newHeldCore(t, now)
	broker.replaceErr = errors.New("response timed out")
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 99})
	if !ack.Accepted {
		t.Fatalf("submit: %+v", ack)
	}
	broker.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusAccepted })
	if got := c.Do(ReplaceOrder{Venue: "v", OrderID: ack.OrderID, LimitPrice: 101}); !got.Accepted {
		t.Fatalf("replace: %+v", got)
	}
	order := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Action != nil && o.Action.Phase == ActionUnknown })
	if order.LimitPrice != 99 || order.Action.RequestedLimitPrice != 101 || order.Action.PreviousLimitPrice != 99 {
		t.Fatalf("unknown replace lost price state: %+v", order)
	}
	if got := c.Do(ReplaceOrder{Venue: "v", OrderID: ack.OrderID, LimitPrice: 102}); got.Accepted {
		t.Fatal("second replace accepted before the unknown outcome was reconciled")
	}
}

func TestCancelDuringHeldActivationWaitsForChildAcceptance(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, now)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay,
		Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	if !ack.Accepted {
		t.Fatalf("held submit: %+v", ack)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 101))
	waitHeldOrder(t, c, ack.OrderID, HeldActivating)
	if got := c.Do(CancelOrder{Venue: "v", OrderID: ack.OrderID}); !got.Accepted {
		t.Fatalf("cancel while activating: %+v", got)
	}
	canceling := waitHeldOrder(t, c, ack.OrderID, HeldCancelRequested)
	if canceling.Held.CancelSent || broker.cancelCount() != 0 {
		t.Fatalf("cancel was sent before child acknowledgement: held=%+v calls=%d", canceling.Held, broker.cancelCount())
	}
	broker.mu.Lock()
	broker.cancelErr = errors.New("child not visible yet")
	broker.mu.Unlock()
	broker.ev <- HeldActivationOutcome{V: "v", OID: ack.OrderID, Reason: "submit response timed out"}
	unknown := waitHeldOrder(t, c, ack.OrderID, HeldUnknown)
	if !unknown.Held.CancelSent {
		t.Fatalf("ambiguous activation cancel was not recorded: held=%+v", unknown.Held)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Action != nil && o.Action.Phase == ActionUnknown })
	if broker.cancelCount() != 1 {
		t.Fatalf("ambiguous activation did not attempt cancellation once: calls=%d", broker.cancelCount())
	}
	broker.mu.Lock()
	broker.cancelErr = nil
	broker.mu.Unlock()
	broker.ev <- OrderAccepted{V: "v", OID: ack.OrderID, BrokerOrderID: "child", Ts: clk.Now().UnixMilli()}
	waitOrder(t, c, ack.OrderID, func(o Order) bool {
		return o.Status == StatusCanceled && o.Action != nil && o.Action.Phase == ActionConfirmed
	})
	if broker.cancelCount() != 2 {
		t.Fatalf("late child acceptance did not retry the deferred cancel: calls=%d", broker.cancelCount())
	}
}

func TestLiveHeldStopLimitRequiresIdentityScopedAcknowledgement(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	c, _, _, _ := newHeldCoreWithLiveAck(t, now, map[VenueID]string{"v": "account-a"}, nil)
	request := SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay,
		Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld}
	if ack := c.Do(request); ack.Accepted || ack.Reason != "live engine-held stop-limit requires account acknowledgement" {
		t.Fatalf("unacknowledged live held order was not blocked: %+v", ack)
	}
	if ack := c.Do(AcknowledgeHeldStopLimit{Venue: "v", Identity: "different-account"}); ack.Accepted {
		t.Fatalf("wrong account identity acknowledged: %+v", ack)
	}
	if ack := c.Do(AcknowledgeHeldStopLimit{Venue: "v", Identity: "account-a"}); !ack.Accepted {
		t.Fatalf("current account acknowledgement blocked: %+v", ack)
	}
	if ack := c.Do(request); !ack.Accepted {
		t.Fatalf("acknowledged account could not submit held order: %+v", ack)
	}
}

func TestRecoverRestoresUnknownActionForLongLivedWorkingOrder(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	old := now.Add(-24 * time.Hour).UnixMilli()
	order := Order{Venue: "v", ID: "ET-old", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFGTC,
		Qty: 10, LimitPrice: 100, Status: StatusAccepted, LeavesQty: 10, CreatedMs: old, UpdatedMs: old}
	action := OrderAction{Kind: ActionReplace, Phase: ActionRequested, PreviousLimitPrice: 100, RequestedLimitPrice: 101, RequestedQty: 10}
	store := &heldTestStore{events: []EventEnvelope{
		EnvelopeOf(OrderSubmitted{Order: order}, SrcLocal, 1),
		EnvelopeOf(OrderActionChanged{V: "v", OID: order.ID, Action: action, Ts: old + 1}, SrcLocal, 2),
	}}
	broker := newHeldTestBroker()
	broker.orders[order.ID] = order
	clk := clock.NewFake(now)
	c := NewCore(CoreConfig{Venues: []VenueID{"v"}, Store: store, Brokers: map[VenueID]Broker{"v": broker}, Clock: clk,
		IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(2)))})
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.state.Venue("v").Orders[order.ID]
	if got.Action == nil || got.Action.Phase != ActionUnknown || got.Action.PreviousLimitPrice != 100 || got.Action.RequestedLimitPrice != 101 {
		t.Fatalf("long-lived action was not restored as unknown: %+v", got)
	}
}

func TestCleanShutdownPersistsCancelIntentThroughActivationRestart(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, session.Loc())
	clk := clock.NewFake(now)
	store := &heldTestStore{}
	broker := newHeldTestBroker()
	c := NewCore(CoreConfig{
		Venues: []VenueID{"v"}, Gate: GateConfig{
			Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionValue: 1_000_000, MaxSymbolPositionShares: 1000},
			Venue:  map[VenueID]VenueLimits{"v": {MaxOrderValue: 100_000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
		},
		Store: store, Brokers: map[VenueID]Broker{"v": broker}, Clock: clk, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(4))),
	})
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	if ack := c.Do(ConfigureHeldDemand{Demand: &heldDemandStub{}}); !ack.Accepted {
		t.Fatal(ack)
	}
	if ack := c.Do(Arm{}); !ack.Accepted {
		t.Fatal(ack)
	}
	waiting := c.Do(SubmitOrder{Venue: "v", Symbol: "MSFT", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	working := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	activating := c.Do(SubmitOrder{Venue: "v", Symbol: "TSLA", Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 101, LimitPrice: 101, RouteExpected: RouteEngineHeld})
	if !waiting.Accepted || !working.Accepted || !activating.Accepted {
		t.Fatalf("held admission: waiting=%+v working=%+v activating=%+v", waiting, working, activating)
	}
	releaseSubmit := make(chan struct{})
	broker.mu.Lock()
	broker.submitBlockID, broker.submitRelease = activating.OrderID, releaseSubmit
	broker.mu.Unlock()
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 102))
	select {
	case <-broker.submits:
	case <-time.After(time.Second):
		t.Fatal("trigger did not submit child")
	}
	broker.ev <- OrderAccepted{V: "v", OID: working.OrderID, Ts: clk.Now().UnixMilli()}
	waitOrder(t, c, working.OrderID, func(o Order) bool { return o.Held != nil && o.Held.Phase == HeldWorking })
	p := eligible(1, clk.Now(), 102)
	p.Symbol = "TSLA"
	c.FeedEligiblePrint(ctx, p)
	waitHeldOrder(t, c, activating.OrderID, HeldActivating)
	select {
	case req := <-broker.submits:
		if req.ClientOrderID != activating.OrderID {
			t.Fatalf("blocked child submit = %+v, want %s", req, activating.OrderID)
		}
	case <-time.After(time.Second):
		t.Fatal("child submit did not enter the broker")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Core did not finish shutdown")
	}
	pre := c.order(waiting.OrderID)
	child := c.order(working.OrderID)
	if pre.Held == nil || pre.Held.Phase != HeldPaused {
		t.Fatalf("pretrigger order after shutdown = %+v", pre.Held)
	}
	if child.Held == nil || child.Held.Phase != HeldCancelRequested || child.Action == nil || child.Action.Phase != ActionRequested || broker.cancelCount() != 1 {
		t.Fatalf("child shutdown cancel not durably requested: order=%+v cancelCalls=%d", child, broker.cancelCount())
	}
	inFlight := c.order(activating.OrderID)
	if inFlight.Held == nil || inFlight.Held.Phase != HeldCancelRequested || inFlight.Held.CancelSent || inFlight.Action == nil || inFlight.Action.Phase != ActionRequested || broker.cancelCount() != 1 {
		t.Fatalf("shutdown raced the in-flight child POST: order=%+v cancelCalls=%d", inFlight, broker.cancelCount())
	}
	close(releaseSubmit)
	deadline := time.After(time.Second)
	for {
		broker.mu.Lock()
		_, submitted := broker.orders[activating.OrderID]
		broker.mu.Unlock()
		if submitted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("in-flight broker submit did not settle after release")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if summary := c.ShutdownHeldSummary(); summary != (HeldShutdownSummary{Paused: 1, CancelRequested: 2, Unconfirmed: 2}) {
		t.Fatalf("shutdown summary = %+v", summary)
	}

	recoveredBroker := newHeldTestBroker() // Snapshot initially reports the child absent.
	recovered := NewCore(CoreConfig{Venues: []VenueID{"v"}, Store: store,
		Brokers: map[VenueID]Broker{"v": recoveredBroker}, Clock: clk,
		IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(5)))})
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := recovered.order(activating.OrderID)
	if restored.Held == nil || restored.Held.Phase != HeldUnknown || !restored.Held.CancelRequested || restored.Held.CancelSent ||
		restored.Action == nil || restored.Action.Phase != ActionUnknown || recoveredBroker.cancelCount() != 0 {
		t.Fatalf("restart did not preserve unresolved activation without resubmit: order=%+v cancelCalls=%d", restored, recoveredBroker.cancelCount())
	}
	select {
	case req := <-recoveredBroker.submits:
		t.Fatalf("restart reposted child before reconciliation: %+v", req)
	default:
	}

	recoveryCtx, stopRecovery := context.WithCancel(context.Background())
	recoveryDone := make(chan struct{})
	go func() { defer close(recoveryDone); _ = recovered.Run(recoveryCtx) }()
	recoveredBroker.ev <- OrderAccepted{V: "v", OID: activating.OrderID, BrokerOrderID: "late-child", Ts: clk.Now().UnixMilli()}
	final := waitOrder(t, recovered, activating.OrderID, func(o Order) bool {
		return o.Status == StatusCanceled && o.Action != nil && o.Action.Phase == ActionConfirmed
	})
	if final.Held == nil || final.Held.ChildBrokerID != "late-child" || recoveredBroker.cancelCount() != 1 {
		t.Fatalf("late acceptance after restart was not canceled once: order=%+v cancelCalls=%d", final, recoveredBroker.cancelCount())
	}
	stopRecovery()
	select {
	case <-recoveryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("recovered Core did not stop")
	}
}
