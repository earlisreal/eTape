package alpaca

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// numString decodes one of Alpaca's numeric trade_updates fields, which are
// sent as JSON strings (e.g. "qty": "40", "price": "190.48"), into a float64.
// It also tolerates a bare JSON number, in case a future field or a real
// capture is unquoted.
type numString float64

func (n *numString) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("alpaca: numString: %w", err)
	}
	*n = numString(f)
	return nil
}

// auOrder is the "order" object embedded in every trade_updates event. It
// doubles (Task 13) as the decode target for the REST order shapes returned
// by POST/GET /v2/orders and GET /v2/orders:by_client_order_id — Alpaca uses
// the same field spellings in both the WS embed and the REST order object,
// unlike TradeZero's REST-vs-Portfolio-WS spelling drift (tzOrder vs
// tzRestOrder in the tradezero package).
type auOrder struct {
	ID             string    `json:"id"`
	ClientOrderID  string    `json:"client_order_id"`
	Symbol         string    `json:"symbol"`
	Side           string    `json:"side"`
	OrderType      string    `json:"order_type"`
	Qty            numString `json:"qty"`
	FilledQty      numString `json:"filled_qty"`
	FilledAvgPrice numString `json:"filled_avg_price"`
	LimitPrice     numString `json:"limit_price"`
	StopPrice      numString `json:"stop_price"`
	Status         string    `json:"status"`
}

// tradeUpdate is one Alpaca `trade_updates` WebSocket event.
type tradeUpdate struct {
	Event       string    `json:"event"`
	ExecutionID string    `json:"execution_id"`
	Price       numString `json:"price"`
	Qty         numString `json:"qty"`
	PositionQty numString `json:"position_qty"`
	Timestamp   string    `json:"timestamp"`
	Reason      string    `json:"reason"` // not always present; rejected events rarely carry one
	Order       auOrder   `json:"order"`
}

// parseTs converts trade_updates' RFC3339 timestamp string to epoch
// milliseconds, defaulting to 0 if the field is empty or malformed rather
// than failing the whole event (a bad/missing timestamp must not drop an
// otherwise-valid order-lifecycle event).
func parseTs(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// rejectReason returns the human-readable rejection text for a `rejected`
// event, falling back to the order status when Alpaca doesn't populate the
// (rare) top-level "reason" field.
func rejectReason(tu tradeUpdate) string {
	if tu.Reason != "" {
		return tu.Reason
	}
	if tu.Order.Status != "" {
		return tu.Order.Status
	}
	return "rejected"
}

// normalizeUpdate turns one trade_updates event into the domain events it
// implies. Fill/partial_fill events dedup on execution_id (not on
// (orderID, qty) like TradeZero's cumulative-executed tracking — Alpaca
// hands out a stable per-execution id directly) and are paired with a
// BrokerPositions carrying the post-fill position_qty for free
// reconciliation. Rare event types are logged and ignored, never treated as
// errors.
func (a *Adapter) normalizeUpdate(venue exec.VenueID, tu tradeUpdate) []exec.BrokerEvent {
	oid := tu.Order.ClientOrderID
	ts := parseTs(tu.Timestamp)

	switch tu.Event {
	case "new":
		return []exec.BrokerEvent{exec.OrderAccepted{V: venue, OID: oid, BrokerOrderID: tu.Order.ID, Ts: ts}}

	case "fill", "partial_fill":
		a.mu.Lock()
		if a.seenExecIDs == nil {
			a.seenExecIDs = map[string]bool{}
		}
		if tu.ExecutionID != "" && a.seenExecIDs[tu.ExecutionID] {
			a.mu.Unlock()
			return nil
		}
		if tu.ExecutionID != "" {
			a.seenExecIDs[tu.ExecutionID] = true
		}
		side, tracked := a.sideByID[oid]
		a.mu.Unlock()
		if !tracked {
			// positionQtyBefore = position_qty (after this execution) undone
			// by this execution's signed delta: a buy added +qty, a sell
			// removed qty.
			delta := float64(tu.Qty)
			if tu.Order.Side == "sell" {
				delta = -delta
			}
			side = sideDomain(tu.Order.Side, float64(tu.PositionQty)-delta)
		}

		return []exec.BrokerEvent{
			exec.OrderFilled{
				F: exec.Fill{
					Venue: venue, OrderID: oid, Symbol: domainSymbol(tu.Order.Symbol),
					Side: side, Qty: float64(tu.Qty), Price: float64(tu.Price), TsMs: ts,
				},
				CumQty:    float64(tu.Order.FilledQty),
				LeavesQty: float64(tu.Order.Qty) - float64(tu.Order.FilledQty),
				AvgPrice:  float64(tu.Order.FilledAvgPrice),
			},
			exec.BrokerPositions{
				V: venue,
				Positions: []exec.Position{
					{Venue: venue, Symbol: domainSymbol(tu.Order.Symbol), Qty: float64(tu.PositionQty)},
				},
			},
		}

	case "canceled":
		return []exec.BrokerEvent{exec.OrderCanceled{V: venue, OID: oid, Ts: ts}}

	case "expired", "done_for_day":
		return []exec.BrokerEvent{exec.OrderExpired{V: venue, OID: oid, Ts: ts}}

	case "replaced":
		return []exec.BrokerEvent{exec.OrderReplaced{
			V: venue, OID: oid,
			NewQty: float64(tu.Order.Qty), NewLimit: float64(tu.Order.LimitPrice), Ts: ts,
		}}

	case "rejected":
		return []exec.BrokerEvent{exec.OrderRejected{V: venue, OID: oid, Reason: rejectReason(tu), Ts: ts}}

	case "pending_new", "pending_cancel", "pending_replace",
		"stopped", "suspended", "calculated",
		"order_replace_rejected", "order_cancel_rejected":
		slog.Debug("alpaca: ignoring rare trade_updates event", "event", tu.Event, "order_id", oid)
		return nil

	default:
		slog.Debug("alpaca: unknown trade_updates event", "event", tu.Event, "order_id", oid)
		return nil
	}
}
