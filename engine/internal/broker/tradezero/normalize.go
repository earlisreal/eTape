package tradezero

import (
	"strings"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// tzOrder is a decode-tolerant view of a TZ order object. It covers both REST
// and Portfolio-WS spellings; unknown fields are ignored by encoding/json.
type tzOrder struct {
	Account       string  `json:"account"`
	AccountID     string  `json:"accountId"`
	UserOrderID   string  `json:"userOrderId"`
	Symbol        string  `json:"symbol"`
	OrderType     string  `json:"orderType"`
	Side          string  `json:"side"`
	OpenClose     string  `json:"openClose"`
	OrderQuantity float64 `json:"orderQuantity"`
	Executed      float64 `json:"executed"`
	LastQty       float64 `json:"lastQty"`
	LimitPrice    float64 `json:"limitPrice"`
	StopPrice     float64 `json:"stopPrice"`
	PriceAvg      float64 `json:"priceAvg"`
	Status        string  `json:"status"`
	OrderStatus   string  `json:"orderStatus"`
	Text          string  `json:"text"`
}

func (o tzOrder) status() string {
	if o.OrderStatus != "" {
		return o.OrderStatus
	}
	return o.Status
}

func splitUserOrderID(u string) (accountID, clientOrderID string) {
	if i := strings.IndexByte(u, ':'); i >= 0 {
		return u[:i], u[i+1:]
	}
	return "", u
}

func statusDomain(s string) exec.OrderStatus {
	switch s {
	case "PendingNew", "New":
		return exec.StatusAccepted
	case "PartiallyFilled":
		return exec.StatusPartiallyFilled
	case "Filled":
		return exec.StatusFilled
	case "Canceled":
		return exec.StatusCanceled
	case "PendingCancel":
		// Non-terminal (TZ: PendingCancel -> Canceled). Must NOT fire the
		// terminal-cancel path — the emulated replace (Task 10) awaits the real
		// Canceled, and a premature signal would resubmit the new leg while the
		// old leg is still resting. Falls through to no domain event.
		return exec.StatusSubmitted
	case "Rejected":
		return exec.StatusRejected
	case "Expired", "DoneForDay":
		return exec.StatusExpired
	default:
		return exec.StatusSubmitted
	}
}

// normalizeOrder turns one order object into the domain events it implies. The
// domain client-order-id is recovered by splitting userOrderId and stripping any
// "-rN" replace suffix (Task 10) so a replace-chain reports as one domain order.
func (a *Adapter) normalizeOrder(venue exec.VenueID, o tzOrder) []exec.BrokerEvent {
	_, tzCID := splitUserOrderID(o.UserOrderID)
	oid := a.domainID(tzCID) // strips "-rN"; identity if no suffix
	ts := a.now()
	var out []exec.BrokerEvent

	// Fill derivation: cumulative executed rose and this slice reported lastQty.
	a.mu.Lock()
	prev := a.seenExecuted[tzCID]
	newFill := o.LastQty > 0 && o.Executed > prev
	if newFill {
		a.seenExecuted[tzCID] = o.Executed
	}
	a.mu.Unlock()
	if newFill {
		out = append(out, exec.OrderFilled{
			F: exec.Fill{
				Venue: venue, OrderID: oid, Symbol: o.Symbol,
				Side: sideDomain(o.Side, o.OpenClose), Qty: o.LastQty, Price: o.PriceAvg, TsMs: ts,
			},
			CumQty: o.Executed, LeavesQty: o.OrderQuantity - o.Executed, AvgPrice: o.PriceAvg,
		})
	}

	switch statusDomain(o.status()) {
	case exec.StatusAccepted:
		out = append(out, exec.OrderAccepted{V: venue, OID: oid, BrokerOrderID: tzCID, Ts: ts})
	case exec.StatusCanceled:
		out = append(out, a.onCanceled(venue, oid, ts)...) // Task 10 hook: swallow-during-replace
	case exec.StatusRejected:
		out = append(out, exec.OrderRejected{V: venue, OID: oid, Reason: rejectText(o.Text), Ts: ts})
	case exec.StatusExpired:
		out = append(out, exec.OrderExpired{V: venue, OID: oid, Ts: ts})
	}
	return out
}

func rejectText(t string) string {
	if t == "" {
		return "rejected"
	}
	return t
}
