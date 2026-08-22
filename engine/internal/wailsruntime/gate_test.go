package wailsruntime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateStopsAdmissionAndWaitsForInFlightWork(t *testing.T) {
	gate := NewGate()
	release, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatalf("enter: %v", err)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- gate.Stop(context.Background()) }()

	select {
	case err := <-stopped:
		t.Fatalf("stop returned before admitted work released: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if _, err := gate.Enter(context.Background()); !errors.Is(err, ErrStopping) {
		t.Fatalf("enter after stop = %v, want %v", err, ErrStopping)
	}

	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not finish after admitted work released")
	}
}

func TestGateRejectsCanceledContextWithoutAdmission(t *testing.T) {
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := gate.Enter(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("enter with canceled context = %v, want context.Canceled", err)
	}
	if got := gate.InFlight(); got != 0 {
		t.Fatalf("in-flight after canceled enter = %d, want 0", got)
	}
}

func TestGateCancelsAdmittedContextWhenStopping(t *testing.T) {
	gate := NewGate()
	workCtx, release, err := gate.EnterContext(context.Background())
	if err != nil {
		t.Fatalf("enter context: %v", err)
	}
	defer release()

	gate.BeginStop()
	select {
	case <-workCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("admitted context was not canceled at stop")
	}
}
