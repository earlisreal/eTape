package main

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

type protectionDemandSpy struct {
	ensured  []feed.Demand
	released []string
}

func (s *protectionDemandSpy) Ensure(d feed.Demand) { s.ensured = append(s.ensured, d) }
func (s *protectionDemandSpy) Release(id string)    { s.released = append(s.released, id) }

func TestProtectionBookDemandIsOptionalAndReleased(t *testing.T) {
	s := &protectionDemandSpy{}
	d := engineHeldDemand{feed: s, wait: func(context.Context, string) error { t.Fatal("optional book waited for coverage"); return nil }, waitExecution: func(context.Context, string, []feed.SubType) error {
		t.Fatal("optional book waited for execution")
		return nil
	}}
	d.EnsureProtectionBook("o", "US.AAPL")
	if len(s.ensured) != 1 {
		t.Fatal(s.ensured)
	}
	got := s.ensured[0]
	if got.ID != "held-price/o" || got.Symbol != "US.AAPL" || !got.Background || got.Focused || len(got.Subs) != 1 || got.Subs[0] != feed.SubBook {
		t.Fatal(got)
	}
	d.Release("o")
	if len(s.released) != 2 || s.released[0] != "held-stop/o" || s.released[1] != "held-price/o" {
		t.Fatal(s.released)
	}
}
