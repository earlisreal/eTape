package bingx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

type position struct {
	ID        json.RawMessage `json:"positionId"`
	Symbol    string          `json:"symbol"`
	Side      string          `json:"positionSide"`
	Isolated  bool            `json:"isolated"`
	Quantity  json.Number     `json:"positionAmt"`
	Available json.Number     `json:"availableAmt"`
	Average   json.Number     `json:"avgPrice"`
	Margin    json.Number     `json:"initialMargin"`
	Leverage  int             `json:"leverage"`
}

type order struct {
	ID           json.Number     `json:"orderId"`
	StringID     string          `json:"orderID"`
	PositionID   json.RawMessage `json:"positionID"`
	Client       string          `json:"clientOrderId"`
	Symbol       string          `json:"symbol"`
	Side         string          `json:"side"`
	PositionSide string          `json:"positionSide"`
	ReduceOnly   bool            `json:"reduceOnly"`
	Type         string          `json:"type"`
	Status       string          `json:"status"`
	Quantity     json.Number     `json:"origQty"`
	Filled       json.Number     `json:"executedQty"`
	Price        json.Number     `json:"price"`
	Time         int64           `json:"time"`
}

func (o order) id() string {
	if o.StringID != "" {
		return o.StringID
	}
	return o.ID.String()
}

func validID(id string) bool {
	return id != "" && id != "0" && !strings.ContainsFunc(id, func(r rune) bool { return r < '0' || r > '9' })
}

// Position IDs can be returned as quoted or numeric integers. Keep their
// exact digits and leave identity unknown when the exchange omits the ID.
func positionID(raw json.RawMessage) string {
	var id json.Number
	if json.Unmarshal(raw, &id) != nil || !validID(id.String()) {
		return ""
	}
	return id.String()
}

// Some BingX responses wrap the same order in a named field.
func (o *order) UnmarshalJSON(data []byte) error {
	type raw order
	var wire struct {
		raw
		Order *raw `json:"order"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*o = order(wire.raw)
	if wire.Order != nil {
		*o = order(*wire.Order)
	}
	return nil
}

type list[T any] struct {
	Items []T
	Key   string
}

func (l *list[T]) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("[")) {
		return json.Unmarshal(data, &l.Items)
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	if raw := wrapped[l.Key]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		return json.Unmarshal(raw, &l.Items)
	}
	return errors.New("invalid BingX list")
}

type accountState struct {
	Account   exec.AccountSnapshot
	Positions []position
	Orders    []order
}

// PositionMode accepts the documented boolean and the string returned by BingX Live.
type PositionMode struct {
	DualSidePosition json.RawMessage `json:"dualSidePosition"`
}

func (m PositionMode) OneWay() (bool, error) {
	switch string(m.DualSidePosition) {
	case `false`, `"false"`:
		return true, nil
	case `true`, `"true"`:
		return false, nil
	default:
		return false, errors.New("unknown position mode in the BingX Futures API response")
	}
}

func (a *apiClient) account(ctx context.Context) (accountState, error) {
	const read = http.MethodGet
	var state accountState
	var mode PositionMode
	if err := a.request(ctx, read, "/openApi/swap/v1/positionSide/dual", nil, true, &mode); err != nil {
		return state, err
	}
	oneWay, err := mode.OneWay()
	if err != nil {
		return state, err
	}
	if !oneWay {
		return state, fmt.Errorf("set %s to one-way position mode", a.env.label)
	}
	var balances []struct {
		Asset     string      `json:"asset"`
		Available json.Number `json:"availableMargin"`
		Equity    json.Number `json:"equity"`
		Used      json.Number `json:"usedMargin"`
	}
	if err := a.request(ctx, read, "/openApi/swap/v3/user/balance", nil, true, &balances); err != nil {
		return state, err
	}
	for _, b := range balances {
		if b.Asset != a.env.currency {
			continue
		}
		cash, cashErr := b.Available.Float64()
		equity, equityErr := b.Equity.Float64()
		used, usedErr := b.Used.Float64()
		if cashErr != nil || equityErr != nil || usedErr != nil || used < 0 {
			return state, fmt.Errorf("invalid %s collateral values", a.env.label)
		}
		state.Account = exec.AccountSnapshot{Currency: b.Asset, Equity: equity, BuyingPower: cash, AvailableCash: cash,
			MarginUsed: used, DayPnLProvisional: true}
		break
	}
	if state.Account.Currency == "" {
		return state, fmt.Errorf("%s returned no %s collateral account", a.env.label, a.env.currency)
	}
	if err := a.request(ctx, read, "/openApi/swap/v2/user/positions", nil, true, &state.Positions); err != nil {
		return state, err
	}
	orders := list[order]{Key: "orders"}
	if err := a.request(ctx, read, "/openApi/swap/v2/trade/openOrders", nil, true, &orders); err != nil {
		return state, err
	}
	state.Orders = orders.Items
	return state, nil
}

func positionLots(p position, digits int) (float64, bool) {
	qty, ok := lots(p.Quantity, digits)
	switch p.Side {
	case "SHORT":
		return -math.Abs(qty), ok
	case "LONG":
		return qty, ok && qty >= 0
	case "BOTH":
		return qty, ok
	default:
		return qty, ok && qty == 0
	}
}

func side(buy, reduce bool) exec.Side {
	switch {
	case buy && reduce:
		return exec.SideCover
	case buy:
		return exec.SideBuy
	case reduce:
		return exec.SideSell
	default:
		return exec.SideShort
	}
}

func (a *apiClient) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	if err := a.refreshCatalog(ctx); err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	state, err := a.account(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	seen := make(map[string]bool)
	var positions []exec.Position
	for _, p := range state.Positions {
		if !strings.HasSuffix(p.Symbol, "-USDT") {
			continue
		}
		terms, ok := a.contract(p.Symbol)
		if !ok {
			return state.Account, nil, nil, fmt.Errorf("missing %s position terms", a.env.label)
		}
		qty, ok := positionLots(p, *terms.QtyDigits)
		if !ok || qty != 0 && seen[p.Symbol] {
			return state.Account, nil, nil, fmt.Errorf("invalid or duplicate %s one-way position", a.env.label)
		}
		if qty == 0 {
			continue
		}
		avg, avgErr := p.Average.Float64()
		margin, marginErr := p.Margin.Float64()
		if avgErr != nil || marginErr != nil || avg <= 0 || margin < 0 {
			return state.Account, nil, nil, fmt.Errorf("invalid %s position values", a.env.label)
		}
		seen[p.Symbol] = true
		positions = append(positions, exec.Position{Symbol: "PERP.BINGX." + p.Symbol, Qty: qty, AvgPrice: avg,
			Multiplier: math.Pow10(-*terms.QtyDigits), Leverage: p.Leverage, MarginUsed: margin, BrokerPositionID: positionID(p.ID)})
	}
	var orders []exec.Order
	for _, o := range state.Orders {
		if !strings.HasSuffix(o.Symbol, "-USDT") {
			continue
		}
		terms, ok := a.contract(o.Symbol)
		if !ok {
			return state.Account, nil, nil, fmt.Errorf("missing %s working order terms", a.env.label)
		}
		qty, valid := lots(o.Quantity, *terms.QtyDigits)
		filled, validFill := lots(o.Filled, *terms.QtyDigits)
		if !validID(o.id()) || !valid || !validFill || filled < 0 || filled >= qty || o.Side != "BUY" && o.Side != "SELL" {
			return state.Account, nil, nil, fmt.Errorf("invalid %s working order", a.env.label)
		}
		id := "BINGX:" + o.id()
		if strings.HasPrefix(o.Client, "et") && len(o.Client) == 28 {
			id = strings.ToUpper(o.Client)
		}
		price, _ := o.Price.Float64()
		orders = append(orders, exec.Order{ID: id, Symbol: "PERP.BINGX." + o.Symbol,
			Side: side(o.Side == "BUY", o.ReduceOnly), Status: exec.StatusAccepted, Type: exec.TypeLimit,
			Qty: qty, ExecutedQty: filled, LeavesQty: qty - filled, LimitPrice: price, CreatedMs: o.Time})
	}
	return state.Account, positions, orders, nil
}
