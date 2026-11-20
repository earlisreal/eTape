package perps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type apiFixture struct {
	send   func() error
	report exec.PerpetualObservation
}

func (*apiFixture) Rules(string) (instrument.ContractRules, bool) {
	return instrument.ContractRules{}, false
}
func (*apiFixture) FairPrice(string) (float64, bool) { return 0, false }
func (*apiFixture) Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	return exec.AccountSnapshot{Currency: "USDT"}, nil, nil, nil
}
func (f *apiFixture) Prepare(context.Context, exec.OrderRequest) (func(context.Context) error, error) {
	return func(context.Context) error { return f.send() }, nil
}
func (f *apiFixture) Observe(context.Context, exec.OrderRequest) (exec.PerpetualObservation, error) {
	return f.report, nil
}
func (*apiFixture) Cancel(context.Context, exec.OrderRequest) error { return nil }

func TestLostAcknowledgementRecoversWithoutResubmitting(t *testing.T) {
	const id = "ETTEST"
	const venue = "test-live"
	path := filepath.Join(t.TempDir(), "journal")
	clk := clock.NewFake(time.UnixMilli(10000))
	fixture := &apiFixture{}
	calls := 0
	fixture.send = func() error {
		calls++
		raw, err := os.ReadFile(path)
		var saved journal
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Pending[id].Request.ClientOrderID != id {
			t.Error("order was sent before writing its journal")
		}
		return context.DeadlineExceeded
	}
	adapter, err := New(venue, clk, path, "test-key", fixture)
	if err != nil {
		t.Fatal(err)
	}
	req := exec.OrderRequest{Venue: venue, ClientOrderID: id, Symbol: "PERP.MEXC.BTC_USDT",
		Qty: 2, Perpetual: &exec.PerpetualOrder{Leverage: 1}}
	if _, err := adapter.SubmitOrder(t.Context(), req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("submit error = %v", err)
	}
	if _, err := New(venue, clk, path, "new-key", fixture); err == nil {
		t.Fatal("a different credential loaded unresolved orders")
	}
	restarted, err := New(venue, clk, path, "test-key", fixture)
	if err != nil || !slices.Equal(restarted.PendingOrderIDs(), []string{id}) {
		t.Fatalf("restore error = %v", err)
	}
	req.ClientOrderID = "ETRETRY"
	var rejected exec.PerpetualSubmitDeclined
	if _, err := restarted.SubmitOrder(t.Context(), req); !errors.As(err, &rejected) || calls != 1 {
		t.Fatalf("contract reserved by uncertain order; submit calls = %d, err = %v", calls, err)
	}
	fixture.report = exec.PerpetualObservation{Status: exec.StatusFilled, BrokerOrderID: "123"}
	for _, persisted := range []bool{false, true} {
		done := make(chan struct{})
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		go func() {
			defer close(done)
			restarted.poll(ctx, true)
		}()
		select {
		case event := <-restarted.Events():
			report, ok := event.(exec.PerpetualObservation)
			if !ok || report.OrderID != id {
				cancel()
				t.Fatalf("unexpected report %+v", event)
			}
			report.Persisted <- persisted
		case <-ctx.Done():
			cancel()
			t.Fatal("missing reconciliation")
		}
		<-done
		cancel()
		if len(restarted.PendingOrderIDs()) == 0 != persisted {
			t.Fatalf("wrong journal state following persistence result %t", persisted)
		}
		<-restarted.Events() // snapshot follows the durable order observation
	}
	restarted, err = New(venue, clk, path, "test-key", fixture)
	if err != nil || len(restarted.PendingOrderIDs()) != 0 || calls != 1 {
		t.Fatalf("terminal order was not resolved durably: calls %d, err %v", calls, err)
	}
}

type streamFixture struct {
	apiFixture
	hintReads, recoveryReads int
}

func (*streamFixture) StreamOrders(ctx context.Context, _ func(bool), _ func(FillHint)) {
	<-ctx.Done()
}

func (f *streamFixture) ObserveFill(context.Context, exec.OrderRequest, int64, FillHint) (exec.PerpetualObservation, error) {
	f.hintReads++
	if f.hintReads == 1 {
		return exec.PerpetualObservation{}, errors.New("fill history is behind the stream")
	}
	return f.report, nil
}

func (f *streamFixture) Observe(context.Context, exec.OrderRequest) (exec.PerpetualObservation, error) {
	f.recoveryReads++
	return f.report, nil
}

func TestStreamCoalescesDuplicateAndRecoversSnapshotGap(t *testing.T) {
	const id, venue = "ETTEST", "test-live"
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	fixture := &streamFixture{apiFixture: apiFixture{send: func() error { return nil }}}
	a, err := New(venue, clock.System{}, filepath.Join(t.TempDir(), "journal"), "test-key", fixture)
	if err != nil {
		t.Fatal(err)
	}
	req := exec.OrderRequest{Venue: venue, ClientOrderID: id, Symbol: "PERP.BINGX.DOGE-USDT",
		Qty: 2, Perpetual: &exec.PerpetualOrder{Multiplier: 1}}
	if _, err := a.SubmitOrder(ctx, req); err != nil {
		t.Fatal(err)
	}
	r := streamReconciler{adapter: a, stream: fixture, progress: make(map[string]fillProgress)}
	hint := FillHint{ClientOrderID: id, BrokerOrderID: "123", Filled: "1", Status: exec.StatusAccepted}
	r.fill(ctx, hint)
	r.fill(ctx, hint)
	if fixture.hintReads != 1 || r.progress[id].retryAt.IsZero() {
		t.Fatal("duplicate fill triggered another request or late history lost its retry")
	}
	if len(a.event) != 0 {
		t.Fatal("incomplete fill history published an observation")
	}
	ack := func(action func(), wantStatus exec.OrderStatus, wantFills int) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			action()
		}()
		select {
		case message := <-a.Events():
			report := message.(exec.PerpetualObservation)
			report.Persisted <- true
			if report.OrderID != id || report.Status != wantStatus || len(report.Fills) != wantFills {
				t.Errorf("unexpected observation %+v", report)
			}
		case <-ctx.Done():
			t.Error("expected a fill observation")
		}
		<-done
	}
	first := exec.PerpetualFill{ID: "first", Qty: 1, Price: 1, TsMs: 1000}
	fixture.report = exec.PerpetualObservation{BrokerOrderID: "123", Status: exec.StatusAccepted, Fills: []exec.PerpetualFill{first}}
	ack(func() { r.observe(ctx, id, true) }, exec.StatusAccepted, 1)
	r.fill(ctx, hint)
	started := time.Now().Add(time.Second).UnixMilli()
	r.recover(ctx, accountOrders{orders: map[string]float64{id: 1}, startedMs: started}, true)
	if fixture.hintReads != 2 || fixture.recoveryReads != 0 {
		t.Fatal("confirmed partial fill caused redundant lookups")
	}
	fixture.report.Status = exec.StatusFilled
	fixture.report.Fills = append(fixture.report.Fills, exec.PerpetualFill{ID: "second", Qty: 1, Price: 2, TsMs: 1001})
	ack(func() { r.recover(ctx, accountOrders{orders: map[string]float64{}, startedMs: started}, true) }, exec.StatusFilled, 2)
	if fixture.recoveryReads != 1 || len(a.PendingOrderIDs()) != 0 {
		t.Fatal("missing working order was not recovered and retired")
	}
}

type blockedSnapshotFixture struct {
	apiFixture
	snapshotStarted chan struct{}
	fill            chan FillHint
}

type positionCloseFixture struct {
	blockedSnapshotFixture
	reads int
}

func (*positionCloseFixture) StreamOrders(ctx context.Context, _ func(bool), _ func(FillHint)) {
	<-ctx.Done()
}

func (*positionCloseFixture) PreparePositionClose(context.Context, exec.OrderRequest) (func(context.Context) (string, error), error) {
	return func(context.Context) (string, error) { return "123", nil }, nil
}

func (f *positionCloseFixture) ObservePositionClose(_ context.Context, _ exec.OrderRequest, brokerID string) (exec.PerpetualObservation, error) {
	f.reads++
	if f.reads == 1 {
		return exec.PerpetualObservation{}, errors.New("close order is not yet visible")
	}
	return exec.PerpetualObservation{Status: exec.StatusFilled, BrokerOrderID: brokerID, PositionID: "456", OrderQty: 1,
		Fills: []exec.PerpetualFill{{ID: "fill", Qty: 1, Price: 99, TsMs: 1000}}}, nil
}

func TestPositionCloseReconcilesBeforeAccountPoll(t *testing.T) {
	const id, venue = "ETTEST", "test-live"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	fixture := &positionCloseFixture{blockedSnapshotFixture: blockedSnapshotFixture{snapshotStarted: make(chan struct{})}}
	a, err := New(venue, clock.System{}, filepath.Join(t.TempDir(), "journal"), "test-key", fixture)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-fixture.snapshotStarted:
	case <-ctx.Done():
		t.Fatal("account snapshot did not start")
	}
	req := exec.OrderRequest{Venue: venue, ClientOrderID: id, Symbol: "PERP.BINGX.BTC-USDT", Side: exec.SideSell,
		Type: exec.TypeMarket, Qty: 1, Perpetual: &exec.PerpetualOrder{Multiplier: 0.0001, WholePosition: true, PositionID: "456"}}
	if _, err := a.SubmitOrder(ctx, req); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-a.Events():
		report, ok := event.(exec.PerpetualObservation)
		if !ok || report.OrderID != id || report.BrokerOrderID != "123" || report.Status != exec.StatusFilled || len(report.Fills) != 1 {
			t.Fatalf("unexpected close observation: %+v", event)
		}
		report.Persisted <- true
	case <-ctx.Done():
		t.Fatal("close stayed unreconciled while the account poll was in flight")
	}
}

func (f *blockedSnapshotFixture) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	close(f.snapshotStarted)
	<-ctx.Done()
	return exec.AccountSnapshot{}, nil, nil, ctx.Err()
}

func (f *blockedSnapshotFixture) StreamOrders(ctx context.Context, ready func(bool), fill func(FillHint)) {
	ready(true)
	select {
	case hint := <-f.fill:
		fill(hint)
	case <-ctx.Done():
	}
	<-ctx.Done()
}

func (*blockedSnapshotFixture) ObserveFill(context.Context, exec.OrderRequest, int64, FillHint) (exec.PerpetualObservation, error) {
	return exec.PerpetualObservation{Status: exec.StatusFilled, BrokerOrderID: "123",
		Fills: []exec.PerpetualFill{{ID: "fill", Qty: 1, Price: 1, TsMs: 1000}}}, nil
}

func TestFillStreamRunsWhileAccountSnapshotIsInFlight(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	fixture := &blockedSnapshotFixture{snapshotStarted: make(chan struct{}), fill: make(chan FillHint, 1),
		apiFixture: apiFixture{send: func() error { return nil },
			report: exec.PerpetualObservation{Status: exec.StatusAccepted, BrokerOrderID: "123"}}}
	const id, venue = "ETTEST", "test-live"
	a, err := New(venue, clock.System{}, filepath.Join(t.TempDir(), "journal"), "test-key", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{Venue: venue, ClientOrderID: id, Symbol: "PERP.BINGX.DOGE-USDT",
		Qty: 1, Perpetual: &exec.PerpetualOrder{Multiplier: 1}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-fixture.snapshotStarted:
	case <-ctx.Done():
		t.Fatal("account snapshot did not start")
	}
	fixture.fill <- FillHint{ClientOrderID: id, BrokerOrderID: "123", Filled: "1", Status: exec.StatusFilled}
	for {
		select {
		case message := <-a.Events():
			if report, ok := message.(exec.PerpetualObservation); ok {
				report.Persisted <- true
				if report.Status == exec.StatusFilled {
					return
				}
			}
		case <-ctx.Done():
			t.Fatal("in-flight account snapshot blocked fill reconciliation")
		}
	}
}
