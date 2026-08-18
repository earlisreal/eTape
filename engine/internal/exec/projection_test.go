package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

type projectionBroker struct {
	caps Capabilities
	evs  chan BrokerEvent
}

func (b *projectionBroker) Capabilities() Capabilities { return b.caps }
func (*projectionBroker) SubmitOrder(context.Context, OrderRequest) (OrderAck, error) {
	return OrderAck{}, errors.New("unused")
}
func (*projectionBroker) ReplaceOrder(context.Context, string, ReplaceRequest) error {
	return errors.New("unused")
}
func (*projectionBroker) CancelOrder(context.Context, string) error   { return errors.New("unused") }
func (*projectionBroker) CancelAll(context.Context, string) error     { return errors.New("unused") }
func (*projectionBroker) Flatten(context.Context) error               { return errors.New("unused") }
func (*projectionBroker) ResetBalance(context.Context, float64) error { return errors.New("unused") }
func (*projectionBroker) Snapshot(context.Context) (AccountSnapshot, []Position, []Order, error) {
	return AccountSnapshot{}, nil, nil, errors.New("unused")
}
func (b *projectionBroker) Events() <-chan BrokerEvent { return b.evs }

func TestEmitProjectedAccount_AlpacaKeepsBrokerDayPnLAndLocalRealized(t *testing.T) {
	venue := VenueID("alpaca-paper")
	b := &projectionBroker{
		caps: Capabilities{AuthoritativeDayPnL: true},
		evs:  make(chan BrokerEvent),
	}
	c := NewCore(CoreConfig{
		Venues:  []VenueID{venue},
		Brokers: map[VenueID]Broker{venue: b},
		Clock:   clock.NewFake(time.UnixMilli(1_700_000_000_000)),
	})
	c.cycles.byVenue[venue] = &CycleCheckpoint{
		Venue: venue, StartMs: 1, Realized: 7,
		Positions: map[string]CyclePosition{"US.AAPL": {Realized: 3}},
	}
	c.state.ReconcileAccount(AccountSnapshot{Venue: venue, DayPnL: 42, Realized: 999})

	c.emitProjectedAccount(venue)
	u, ok := <-c.updates
	if !ok {
		t.Fatal("projected account update missing")
	}
	account := u.(AccountUpdate)
	if account.DisplayDayPnL != 42 {
		t.Fatalf("Alpaca DisplayDayPnL = %v, want broker 42", account.DisplayDayPnL)
	}
	if account.DisplayRealized != 3 {
		t.Fatalf("DisplayRealized = %v, want local 3", account.DisplayRealized)
	}
}

func TestHandleSetActiveVenue_ReevaluatesDayLoss(t *testing.T) {
	venue := VenueID("alpaca-paper")
	b := &projectionBroker{caps: Capabilities{DayLossActiveVenueOnly: true}, evs: make(chan BrokerEvent)}
	c := NewCore(CoreConfig{
		Venues:      []VenueID{"sim", venue},
		ActiveVenue: "sim",
		Gate: GateConfig{
			Global:          GlobalLimits{MaxDayLoss: 1000},
			DayLossPolicies: map[VenueID]DayLossPolicy{venue: {ActiveVenueOnly: true}},
		},
		Brokers: map[VenueID]Broker{venue: b},
		Clock:   clock.NewFake(time.UnixMilli(1_700_000_000_000)),
	})
	c.state.ReconcileAccount(AccountSnapshot{Venue: venue, DayPnL: -1200})
	c.state.SetMasterArmed(true)

	if ack := c.handleSetActiveVenue(SetActiveVenue{Venue: venue}); !ack.Accepted {
		t.Fatalf("set active venue: %+v", ack)
	}
	if c.state.MasterArmed {
		t.Fatal("switching onto a breached active-only Alpaca account must disarm")
	}
}
