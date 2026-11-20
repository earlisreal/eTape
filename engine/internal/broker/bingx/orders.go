package bingx

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func (a *apiClient) Prepare(ctx context.Context, req exec.OrderRequest) (func(context.Context) error, error) {
	if req.Perpetual != nil && req.Perpetual.PositionID != "" && !req.Perpetual.WholePosition {
		return nil, errors.New("BingX position-bound quantity-limited closes are not verified")
	}
	protected := req.Perpetual != nil && req.Perpetual.WholePosition
	if !protected {
		if err := a.refreshCatalog(ctx); err != nil {
			return nil, err
		}
	}
	terms, ok := a.Rules(req.Symbol)
	if !ok || req.Perpetual == nil || !terms.ValidQuantity(req.Qty) || terms.ContractSize() != req.Perpetual.Multiplier {
		return nil, fmt.Errorf("invalid %s order terms", a.env.label)
	}
	id, _ := instrument.ParsePerpetual(req.Symbol)
	item, _ := a.contract(id.Native)
	params := orderIdentity(req)
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	reduce := req.Side == exec.SideCover || req.Side == exec.SideSell
	if !buy && req.Side != exec.SideSell && req.Side != exec.SideShort {
		return nil, fmt.Errorf("invalid %s direction", a.env.label)
	}
	unit, _ := new(big.Rat).SetString(terms.Multiplier)
	q := unit.Mul(unit, big.NewRat(int64(req.Qty), 1))
	params.Set("quantity", q.FloatString(*item.QtyDigits))
	params.Set("positionSide", "BOTH")
	params.Set("side", "SELL")
	if buy {
		params.Set("side", "BUY")
	}
	params.Set("reduceOnly", strconv.FormatBool(reduce))
	params.Set("type", req.Type.String())
	switch req.Type {
	case exec.TypeMarket:
	case exec.TypeLimit:
		if !terms.ValidPrice(req.LimitPrice) || req.TIF != exec.TIFGTC && req.TIF != exec.TIFIOC && req.TIF != exec.TIFFOK {
			return nil, fmt.Errorf("invalid %s limit price or time in force", a.env.label)
		}
		params.Set("price", strconv.FormatFloat(req.LimitPrice, 'f', -1, 64))
		params.Set("timeInForce", req.TIF.String())
	default:
		return nil, fmt.Errorf("%s supports market and limit orders", a.env.label)
	}
	if protected {
		if reduce || !req.Perpetual.LocalProtection || !req.Perpetual.IsRiskEntry() || req.Type != exec.TypeLimit ||
			(req.TIF != exec.TIFGTC && req.TIF != exec.TIFIOC) {
			return nil, errors.New("invalid BingX Chart Risk Entry child")
		}
		opening, err := a.cachedRiskOpening(req.Symbol)
		if err != nil {
			return nil, err
		}
		if err := a.checkRiskOpening(req, terms, opening); err != nil {
			return nil, err
		}
	} else if err := a.checkTicketOrder(ctx, req, terms, item, buy, reduce, params.Get("quantity")); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		var response order
		if err := a.request(ctx, http.MethodPost, "/openApi/swap/v2/trade/order", params, true, &response); err != nil {
			// Documented definitive permission and business rejections.
			var code apiError
			if errors.As(err, &code) {
				switch code.code {
				case 100001, 100004, 100412, 100413, 100419, 100421, 101204, 101206, 101211, 101415, 101419, 109415, 110206:
					return exec.PerpetualSubmitDeclined{Cause: err}
				}
			}
			return err
		}
		if !validID(response.id()) || !strings.EqualFold(response.Client, req.ClientOrderID) || response.Symbol != id.Native {
			return fmt.Errorf("incomplete %s order acknowledgement", a.env.label)
		}
		return nil
	}, nil
}

func (a *apiClient) checkTicketOrder(ctx context.Context, req exec.OrderRequest, terms instrument.ContractRules, item contract, buy, reduce bool, quantity string) error {
	state, err := a.account(ctx)
	if err != nil {
		return err
	}
	var held, available float64
	var leverage int
	for _, p := range state.Positions {
		if p.Symbol != item.Symbol {
			continue
		}
		qty, valid := positionLots(p, *item.QtyDigits)
		avail, validAvailable := lots(p.Available, *item.QtyDigits)
		if !valid || qty != 0 && (held != 0 || !validAvailable || !p.Isolated) {
			return fmt.Errorf("%s requires one isolated position per contract", a.env.label)
		}
		if qty != 0 {
			held, available, leverage = qty, math.Abs(avail), p.Leverage
		}
	}
	if reduce {
		if item.Close != "true" || held == 0 || buy != (held < 0) || req.Qty > min(math.Abs(held), available) {
			return fmt.Errorf("%s close exceeds the confirmed isolated position available to reduce", a.env.label)
		}
		return nil
	}
	if !terms.TradingEnabled || held != 0 && (buy != (held > 0) || leverage != req.Perpetual.Leverage) {
		return fmt.Errorf("resolve the current isolated %s position before opening", a.env.label)
	}
	for _, order := range state.Orders {
		if order.Symbol == item.Symbol {
			return fmt.Errorf("resolve working %s orders for this contract first", a.env.label)
		}
	}
	qty, _ := strconv.ParseFloat(quantity, 64)
	return a.checkOpening(ctx, item.Symbol, buy, qty, req, state.Account.AvailableCash, item)
}

func (a *apiClient) checkOpening(ctx context.Context, symbol string, buy bool, qty float64, req exec.OrderRequest, available float64, item contract) error {
	if err := a.checkIsolated(ctx, symbol); err != nil {
		return err
	}
	limit, err := a.openingLeverage(ctx, symbol)
	if err != nil {
		return err
	}
	current, maximum, maxQty := limit.Short, limit.ShortMax, limit.ShortQty
	if buy {
		current, maximum, maxQty = limit.Long, limit.LongMax, limit.LongQty
	}
	maximumQty, parseErr := maxQty.Float64()
	leverage := req.Perpetual.Leverage
	if parseErr != nil || leverage < 1 || current != leverage || maximum < leverage || qty > maximumQty {
		return fmt.Errorf("match the %s contract's configured leverage and available size", a.env.label)
	}
	price, err := a.markPrice(ctx, symbol)
	if err != nil {
		return err
	}
	minimum, minErr := item.MinimumValue.Float64()
	fee, feeErr := item.Fee.Float64()
	if minErr != nil || feeErr != nil || minimum < 0 || fee < 0 {
		return fmt.Errorf("%s mark or minimum trade value unavailable", a.env.label)
	}
	if qty*max(price, req.LimitPrice)*(1/float64(leverage)+2*fee) > available {
		return fmt.Errorf("insufficient %s collateral", a.env.label)
	}
	if req.Type == exec.TypeLimit {
		price = req.LimitPrice
	}
	if qty*price < minimum {
		return fmt.Errorf("order is below the %s minimum trade value", a.env.label)
	}
	return nil
}

func orderIdentity(req exec.OrderRequest) url.Values {
	id, _ := instrument.ParsePerpetual(req.Symbol)
	return url.Values{"symbol": {id.Native}, "clientOrderId": {strings.ToLower(req.ClientOrderID)}}
}

func (a *apiClient) lookup(ctx context.Context, req exec.OrderRequest) (order, exec.OrderStatus, error) {
	var o order
	params := orderIdentity(req)
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/order", params, true, &o); err != nil {
		return o, 0, err
	}
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	reduce := req.Side == exec.SideSell || req.Side == exec.SideCover
	if !validID(o.id()) || !strings.EqualFold(o.Client, req.ClientOrderID) || o.Symbol != params.Get("symbol") ||
		o.Type != req.Type.String() || o.Side != "BUY" && o.Side != "SELL" || buy != (o.Side == "BUY") || o.ReduceOnly != reduce {
		return o, 0, fmt.Errorf("mismatched %s order identity", a.env.label)
	}
	status, err := a.orderStatus(o.Status)
	return o, status, err
}

func (a *apiClient) orderStatus(status string) (exec.OrderStatus, error) {
	switch status {
	case "NEW", "PENDING", "PARTIALLYFILLED":
		return exec.StatusAccepted, nil
	case "FILLED":
		return exec.StatusFilled, nil
	case "CANCELLED":
		return exec.StatusCanceled, nil
	case "FAILED":
		return exec.StatusRejected, nil
	case "EXPIRED":
		return exec.StatusExpired, nil
	default:
		return 0, fmt.Errorf("unknown %s order state", a.env.label)
	}
}

func (a *apiClient) Observe(ctx context.Context, req exec.OrderRequest) (exec.PerpetualObservation, error) {
	o, status, err := a.lookup(ctx, req)
	if err != nil {
		return exec.PerpetualObservation{}, err
	}
	digits := lotDigits(req)
	filled, valid := filledLots(req, o, status, digits)
	if !valid {
		return exec.PerpetualObservation{}, fmt.Errorf("invalid %s order quantity", a.env.label)
	}
	if req.Perpetual.WholePosition && req.Perpetual.IsRiskEntry() && filled > 0 && positionID(o.PositionID) == "" {
		o.PositionID, err = a.filledPositionID(ctx, o)
		if err != nil {
			return exec.PerpetualObservation{}, err
		}
	}
	return a.readFills(ctx, o, status, digits, filled, o.Time-1000, a.clock.Now().UnixMilli())
}

func (a *apiClient) filledPositionID(ctx context.Context, observed order) (json.RawMessage, error) {
	if observed.Time <= 1000 {
		return nil, errors.New("BingX entry order time is unavailable")
	}
	// An exact order-history match can supply the position ID omitted by
	// order detail. The current position alone cannot identify an IOC fill.
	orders := list[order]{Key: "orders"}
	params := url.Values{"symbol": {observed.Symbol}, "limit": {"1000"},
		"startTime": {strconv.FormatInt(observed.Time-1000, 10)},
		"endTime":   {strconv.FormatInt(max(observed.Time, min(observed.Time+60_000, a.clock.Now().UnixMilli())), 10)}}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/allOrders", params, true, &orders); err != nil {
		return nil, err
	}
	var position json.RawMessage
	for _, row := range orders.Items {
		if row.id() != observed.id() || row.Symbol != observed.Symbol || row.Side != observed.Side || row.Type != observed.Type ||
			row.ReduceOnly || !strings.EqualFold(row.Client, observed.Client) || positionID(row.PositionID) == "" {
			continue
		}
		if position != nil && positionID(position) != positionID(row.PositionID) {
			return nil, errors.New("BingX entry order history has conflicting position IDs")
		}
		position = row.PositionID
	}
	if position == nil {
		return nil, errors.New("BingX fills are awaiting a verified position ID")
	}
	return position, nil
}

func lotDigits(req exec.OrderRequest) int {
	// Use the admitted lot quantum even if exchange precision later changes.
	quantum := strconv.FormatFloat(req.Perpetual.Multiplier, 'f', -1, 64)
	_, fraction, _ := strings.Cut(quantum, ".")
	return len(fraction)
}

func filledLots(req exec.OrderRequest, o order, status exec.OrderStatus, digits int) (float64, bool) {
	qty, validQty := lots(o.Quantity, digits)
	filled, validFill := lots(o.Filled, digits)
	return filled, validQty && validFill && qty == req.Qty && filled >= 0 && filled <= qty &&
		(status != exec.StatusFilled || filled == qty)
}

// ObserveFill uses the stream's order identity and cumulative quantity to
// fetch the exact executions in one read. The journal and REST recovery remain
// the sources of order ownership and missing execution history.
func (a *apiClient) ObserveFill(ctx context.Context, req exec.OrderRequest, submittedMs int64, hint perps.FillHint) (exec.PerpetualObservation, error) {
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	if submittedMs <= 0 || req.Perpetual.LocalProtection || !validID(hint.BrokerOrderID) ||
		hint.ClientOrderID != req.ClientOrderID || hint.Symbol != orderIdentity(req).Get("symbol") ||
		hint.Type != req.Type.String() || hint.Side != "BUY" && hint.Side != "SELL" || buy != (hint.Side == "BUY") ||
		hint.TimeMs < submittedMs-5000 {
		return a.Observe(ctx, req)
	}
	o := order{ID: json.Number(hint.BrokerOrderID), Symbol: hint.Symbol, Side: hint.Side,
		Quantity: hint.Quantity, Filled: hint.Filled}
	digits := lotDigits(req)
	filled, valid := filledLots(req, o, hint.Status, digits)
	if !valid || filled == 0 {
		return a.Observe(ctx, req)
	}
	endMs := max(hint.TimeMs, a.clock.Now().UnixMilli())
	report, err := a.readFills(ctx, o, hint.Status, digits, filled, submittedMs-5000, endMs)
	var mismatch fillQuantityMismatch
	if errors.As(err, &mismatch) && mismatch.actual > filled {
		// History has already overtaken this event. A signed order read can
		// confirm the newer cumulative quantity and terminal status.
		return a.Observe(ctx, req)
	}
	return report, err
}

type fillQuantityMismatch struct {
	label  string
	actual float64
}

func (e fillQuantityMismatch) Error() string {
	return fmt.Sprintf("%s fills do not yet reconcile with the order", e.label)
}

func (a *apiClient) readFills(ctx context.Context, o order, status exec.OrderStatus, digits int, filled float64, startMs, endMs int64) (exec.PerpetualObservation, error) {
	report := exec.PerpetualObservation{BrokerOrderID: o.id(), Status: status}
	if filled == 0 {
		return report, nil
	}
	var reply list[tradeFill]
	reply.Key = "fill_orders"
	if startMs <= 0 || endMs < startMs {
		return report, fmt.Errorf("missing %s order time", a.env.label)
	}
	params := url.Values{"tradingUnit": {"COIN"}, "currency": {"USDT"}, "orderId": {o.id()},
		"startTs": {strconv.FormatInt(startMs, 10)}, "endTs": {strconv.FormatInt(endMs, 10)}}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/allFillOrders", params, true, &reply); err != nil {
		return report, err
	}
	var sum float64
	occurrences := make(map[string]int)
	seen := make(map[string]bool)
	for _, f := range reply.Items {
		execution, ok := a.execution(f, o, digits, occurrences)
		if !ok || seen[execution.ID] {
			return report, fmt.Errorf("invalid %s fills", a.env.label)
		}
		seen[execution.ID] = true
		sum += execution.Qty
		report.Fills = append(report.Fills, execution)
	}
	if sum != filled {
		return report, fillQuantityMismatch{label: a.env.label, actual: sum}
	}
	report.PositionID = positionID(o.PositionID)
	slices.SortFunc(report.Fills, func(a, b exec.PerpetualFill) int {
		if n := cmp.Compare(a.TsMs, b.TsMs); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return report, nil
}

type tradeFill struct {
	OrderID    json.Number `json:"orderId"`
	TradeID    json.Number `json:"tradeId"`
	Symbol     string      `json:"symbol"`
	Side       string      `json:"side"`
	Price      json.Number `json:"price"`
	Quantity   json.Number `json:"qty"`
	Time       json.Number `json:"time"`
	Volume     json.Number `json:"volume"`
	FilledTime string      `json:"filledTime"`
}

func (a *apiClient) execution(f tradeFill, o order, digits int, occurrences map[string]int) (exec.PerpetualFill, bool) {
	price, priceErr := f.Price.Float64()
	if f.OrderID.String() != o.id() || priceErr != nil || price <= 0 {
		return exec.PerpetualFill{}, false
	}
	if a.env.broker == live.broker && f.TradeID != "" {
		qty, ok := lots(f.Quantity, digits)
		when, err := f.Time.Int64()
		id := f.TradeID.String()
		return exec.PerpetualFill{ID: id, Qty: qty, Price: price, TsMs: when},
			ok && qty > 0 && err == nil && when > 0 && validID(id) && f.Symbol == o.Symbol && f.Side == o.Side
	}

	// Legacy order-filtered records may omit the symbol and side in production.
	// Preserve Virtual's existing fill identities for orders recovered on upgrade.
	symbol, side := f.Symbol, f.Side
	if a.env.broker == live.broker {
		if symbol == "" {
			symbol = o.Symbol
		}
		if side == "" {
			side = o.Side
		}
	}
	qty, ok := lots(f.Volume, digits)
	when, err := time.Parse(time.RFC3339Nano, f.FilledTime)
	if err != nil && a.env.broker == live.broker {
		when, err = time.Parse("2006-01-02T15:04:05Z0700", f.FilledTime)
	}
	ts := when.UnixMilli()
	if !ok || qty <= 0 || err != nil || ts <= 0 || symbol != o.Symbol || side != o.Side {
		return exec.PerpetualFill{}, false
	}
	key := strconv.FormatInt(ts, 10) + ":" + strconv.FormatInt(int64(qty), 10) + ":" +
		strconv.FormatFloat(price, 'f', -1, 64)
	occurrences[key]++
	id := key + ":" + strconv.Itoa(occurrences[key])
	return exec.PerpetualFill{ID: id, Qty: qty, Price: price, TsMs: ts}, true
}

func (a *apiClient) Cancel(ctx context.Context, req exec.OrderRequest) error {
	o, status, err := a.lookup(ctx, req)
	if err != nil {
		return err
	}
	if status != exec.StatusAccepted {
		return nil
	}
	var response order
	params := url.Values{"symbol": {o.Symbol}, "orderId": {o.id()}}
	if err := a.request(ctx, http.MethodDelete, "/openApi/swap/v2/trade/order", params, true, &response); err != nil {
		return err
	}
	if response.id() != o.id() {
		return fmt.Errorf("%s cancellation acknowledgement did not identify the order", a.env.label)
	}
	return nil
}
