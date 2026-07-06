package exectest

import (
	"context"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// waitForBrokerEvent drains ch until pred matches an event, or fails the test
// after a timeout. Named distinctly from lifecycle_test.go's waitUpdate
// (which drains exec.Core's Update channel, not an adapter's raw
// exec.BrokerEvent channel).
func waitForBrokerEvent(t *testing.T, ch <-chan exec.BrokerEvent, pred func(exec.BrokerEvent) bool) exec.BrokerEvent {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e := <-ch:
			if pred(e) {
				return e
			}
		case <-deadline:
			t.Fatal("waitForBrokerEvent: timed out waiting for a matching event")
			return nil
		}
	}
}

// TestAlpacaPaper_SubmitCancel_OptIn is an opt-in, real-network integration
// test against Alpaca's PAPER API (never live). It is skipped unless
// ETAPE_ALPACA_PAPER=1 is set, and it is never set or run by CI or by any
// automated agent — running it against the real Alpaca paper API is a
// decision Earl makes explicitly in his own session (per CLAUDE.md's
// multi-broker safety rules), never inferred from this test's mere
// existence.
//
// When it does run, it places a single $1 limit buy on AAPL — far enough
// below any realistic market price that it can never be marketable — asserts
// it reaches Accepted, then cancels it immediately. Paper only.
func TestAlpacaPaper_SubmitCancel_OptIn(t *testing.T) {
	if os.Getenv("ETAPE_ALPACA_PAPER") != "1" {
		t.Skip("set ETAPE_ALPACA_PAPER=1 to run the real Alpaca paper integration test")
	}
	f, err := creds.Load(creds.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := f.Get("alpaca")
	if err != nil {
		t.Fatal(err)
	}

	al, err := alpaca.New(alpaca.Config{Venue: "alpaca-paper", Env: "paper", Creds: pair, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go al.Run(ctx)

	oid := exec.NewOrderIDGen(clock.System{}, rand.New(rand.NewSource(time.Now().UnixNano()))).Next()
	if _, err := al.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "alpaca-paper", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 1, LimitPrice: 1.00, ClientOrderID: oid, // $1 limit: never marketable -> rests
	}); err != nil {
		t.Fatal(err)
	}
	waitForBrokerEvent(t, al.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })
	if err := al.CancelOrder(ctx, oid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForBrokerEvent(t, al.Events(), func(e exec.BrokerEvent) bool { oc, ok := e.(exec.OrderCanceled); return ok && oc.OID == oid })
}
