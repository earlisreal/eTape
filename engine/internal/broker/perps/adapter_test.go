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
