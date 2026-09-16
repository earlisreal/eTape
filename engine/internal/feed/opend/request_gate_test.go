package opend

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestRequestGatePacesStartTimesAndDoesNotReserveCanceledWaiter(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	g := requestGate{clk: clk, spacing: 550 * time.Millisecond}
	if err := g.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- g.wait(canceled) }()
	select {
	case <-first:
		t.Fatal("waiter bypassed the gate")
	default:
	}
	cancel()
	if err := <-first; err == nil {
		t.Fatal("canceled waiter was admitted")
	}
	second := make(chan error, 1)
	go func() { second <- g.wait(context.Background()) }()
	clk.Advance(550 * time.Millisecond)
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("live waiter did not receive the next slot")
	}
}
