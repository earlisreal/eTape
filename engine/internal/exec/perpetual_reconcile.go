package exec

import (
	"errors"
	"fmt"
)

// PerpetualSubmitDeclined identifies a request that definitely did not create
// an order. All other transport errors leave the submit outcome unresolved.
type PerpetualSubmitDeclined struct{ Cause error }

func (e PerpetualSubmitDeclined) Error() string { return e.Cause.Error() }
func (e PerpetualSubmitDeclined) Unwrap() error { return e.Cause }

type PerpetualFill struct {
	ID    string
	Qty   float64
	Price float64
	TsMs  int64
}

// PerpetualObservation contains all fills accounted for by the exchange's
// order status. The adapter retains its journal entry until Persisted is true.
type PerpetualObservation struct {
	Venue         VenueID
	OrderID       string
	BrokerOrderID string
	Status        OrderStatus
	Fills         []PerpetualFill
	Rejection     string
	Persisted     chan bool
}

func (PerpetualObservation) isBrokerEvent() {}

func (o PerpetualObservation) Terminal() bool {
	return o.Rejection != "" || o.Status == StatusFilled || o.Status == StatusCanceled ||
		o.Status == StatusExpired || o.Status == StatusRejected
}

func (c *Core) recoverPerpetualOrders() error {
	for venue, broker := range c.brokers {
		pending, ok := broker.(interface{ PendingOrderIDs() []string })
		if !ok {
			continue
		}
		ids := pending.PendingOrderIDs()
		if len(ids) == 0 {
			continue
		}
		history, ok := c.store.(orderHistoryByIDStore)
		if !ok {
			return errors.New("perpetual order recovery requires order history")
		}
		envs, err := history.ReadExecOrderHistoriesFor(ids)
		if err != nil {
			return err
		}
		// Rebuild each order from its original submission even if today's
		// replay already included events for it.
		for _, env := range envs {
			event, err := DecodeEvent(env.Kind, env.Payload)
			if err != nil {
				return err
			}
			if event.Venue() != venue {
				return errors.New("perpetual order journal has a mismatched venue")
			}
			c.state.Apply(event)
			if f, ok := event.(OrderFilled); ok && f.PositionExecID != "" {
				c.positionExecIDs[positionExecKey(venue, f.PositionExecID)] = true
			}
		}
		for _, id := range ids {
			if c.order(id).ID == "" {
				return fmt.Errorf("perpetual journal lacks the engine order %s", id)
			}
		}
	}
	return nil
}

func (c *Core) foldPerpetualObservation(report PerpetualObservation) error {
	o := c.order(report.OrderID)
	if o.Venue != report.Venue || o.Perpetual == nil {
		return errors.New("perpetual observation has no matching local order")
	}
	if !o.Working() {
		if report.Terminal() {
			return nil
		}
		return errors.New("working exchange order conflicts with terminal engine order")
	}
	if report.Rejection != "" {
		return c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Ts: c.now(), Reason: report.Rejection}, SrcReconcile)
	}
	if report.BrokerOrderID == "" {
		return errors.New("perpetual observation is missing the exchange order ID")
	}
	if o.Status == StatusSubmitted {
		if err := c.appendAndFold(OrderAccepted{V: o.Venue, OID: o.ID, Ts: c.now(), BrokerOrderID: report.BrokerOrderID}, SrcReconcile); err != nil {
			return err
		}
	}
	c.confirmSellSubmit(o.Venue, o.ID)
	for _, fill := range report.Fills {
		id := report.BrokerOrderID + ":" + fill.ID
		key := positionExecKey(o.Venue, id)
		if c.positionExecIDs[key] {
			continue
		}
		cum := o.ExecutedQty + fill.Qty
		if fill.ID == "" || !finitePositive(fill.Qty) || !finitePositive(fill.Price) || fill.TsMs <= 0 || cum > o.Qty {
			return errors.New("invalid perpetual fill in order observation")
		}
		event := OrderFilled{
			F: Fill{Venue: o.Venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: fill.Qty,
				Price: fill.Price, TsMs: fill.TsMs, Multiplier: o.Perpetual.Multiplier},
			CumQty: cum, LeavesQty: o.Qty - cum, AvgPrice: (o.AvgFillPrice*o.ExecutedQty + fill.Qty*fill.Price) / cum,
			PositionExecID: id, PositionFromSnapshot: true,
		}
		if err := c.appendAndFold(event, SrcReconcile); err != nil {
			return err
		}
		c.positionExecIDs[key] = true
		o = c.order(o.ID)
	}
	now := c.now()
	switch report.Status {
	case StatusCanceled:
		return c.appendAndFold(OrderCanceled{V: o.Venue, OID: o.ID, Ts: now}, SrcReconcile)
	case StatusExpired:
		return c.appendAndFold(OrderExpired{V: o.Venue, OID: o.ID, Ts: now}, SrcReconcile)
	case StatusRejected:
		return c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Ts: now, Reason: "exchange marked order invalid"}, SrcReconcile)
	case StatusFilled:
		if o.Status != StatusFilled {
			return errors.New("perpetual order marked filled before all fills reconciled")
		}
	}
	return nil
}
