package stub_test

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/broker/stub"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestStubRejectsSubmit(t *testing.T) {
	b := stub.New()
	if _, err := b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "moomoo"}); err == nil {
		t.Fatal("stub venue must reject SubmitOrder")
	}
	if err := b.ReplaceOrder(context.Background(), "id", exec.ReplaceRequest{}); err == nil {
		t.Fatal("stub venue must reject ReplaceOrder")
	}
}

func TestStubCancelFlattenAreNoops(t *testing.T) {
	b := stub.New()
	if err := b.CancelAll(context.Background(), ""); err != nil {
		t.Fatalf("CancelAll should be a no-op, got %v", err)
	}
	if err := b.CancelOrder(context.Background(), "id"); err != nil {
		t.Fatalf("CancelOrder should be a no-op, got %v", err)
	}
	if err := b.Flatten(context.Background()); err != nil {
		t.Fatalf("Flatten should be a no-op, got %v", err)
	}
}

func TestStubRejectsResetBalance(t *testing.T) {
	b := stub.New()
	if err := b.ResetBalance(context.Background(), 100_000); err == nil {
		t.Fatal("stub venue must reject ResetBalance (not an exposure-reducing no-op)")
	}
	if b.Capabilities().ResetBalance {
		t.Fatal("stub venue must not advertise ResetBalance capability")
	}
}

func TestStubEventsChannelIsClosed(t *testing.T) {
	b := stub.New()
	if _, ok := <-b.Events(); ok {
		t.Fatal("stub venue never connects — Events() must be a closed channel")
	}
}
