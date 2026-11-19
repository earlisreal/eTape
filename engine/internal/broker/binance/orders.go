package binance

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/linearperp"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type order struct {
	ID           int64       `json:"orderId"`
	ClientID     string      `json:"clientOrderId"`
	Symbol       string      `json:"symbol"`
	Direction    string      `json:"side"`
	PositionSide string      `json:"positionSide"`
	ReduceOnly   bool        `json:"reduceOnly"`
	Type         string      `json:"type"`
	State        string      `json:"status"`
	Quantity     json.Number `json:"origQty"`
	Executed     json.Number `json:"executedQty"`
	Price        json.Number `json:"price"`
	Average      json.Number `json:"avgPrice"`
	Time         int64       `json:"time"`
	Updated      int64       `json:"updateTime"`
}

func (o order) status() (exec.OrderStatus, bool) {
	switch o.State {
	case "NEW":
		return exec.StatusAccepted, true
	case "PARTIALLY_FILLED":
		return exec.StatusPartiallyFilled, true
	case "FILLED":
		return exec.StatusFilled, true
	case "CANCELED":
		return exec.StatusCanceled, true
	case "EXPIRED", "EXPIRED_IN_MATCH":
		return exec.StatusExpired, true
	case "REJECTED":
		return exec.StatusRejected, true
	default:
		return 0, false
	}
}

func (o order) side() (exec.Side, bool) {
	switch {
	case o.PositionSide != "BOTH":
		return 0, false
	case o.Direction == "BUY" && o.ReduceOnly:
		return exec.SideCover, true
	case o.Direction == "BUY":
		return exec.SideBuy, true
	case o.Direction == "SELL" && o.ReduceOnly:
		return exec.SideSell, true
	case o.Direction == "SELL":
		return exec.SideShort, true
	default:
		return 0, false
	}
}

func (a *demoAPI) Prepare(ctx context.Context, req exec.OrderRequest) (func(context.Context) error, error) {
	if err := a.catalog.Refresh(ctx); err != nil {
		return nil, err
	}
	id, parsed := instrument.ParsePerpetual(req.Symbol)
	terms, known := a.catalog.DemoRules(req.Symbol)
	if !parsed || id.Exchange != instrument.BinanceDemo || !known || req.Perpetual == nil ||
		terms.ContractSize() != req.Perpetual.Multiplier || req.ClientOrderID == "" || req.Session != exec.SessionAuto {
		return nil, errors.New("invalid Binance Futures Demo order terms")
	}
	reduce := req.Side == exec.SideSell || req.Side == exec.SideCover
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	if !buy && req.Side != exec.SideSell && req.Side != exec.SideShort {
		return nil, errors.New("invalid Binance Futures Demo order side")
	}
	rules := terms.ContractRules
	if req.Type == exec.TypeMarket {
		rules = terms.Market
	}
	if !rules.ValidQuantity(req.Qty) {
		return nil, errors.New("quantity is outside the Binance Futures Demo lot filter")
	}
	values := url.Values{
		"symbol": {id.Native}, "newClientOrderId": {req.ClientOrderID}, "newOrderRespType": {"ACK"},
		"positionSide": {"BOTH"}, "reduceOnly": {strconv.FormatBool(reduce)}, "type": {req.Type.String()},
		"quantity": {linearperp.DemoQuantity(int64(req.Qty), terms.Multiplier)}, "side": {"SELL"},
	}
	if buy {
		values.Set("side", "BUY")
	}
	switch req.Type {
	case exec.TypeMarket:
	case exec.TypeLimit:
		if !rules.ValidPrice(req.LimitPrice) || req.LimitPrice < terms.MinPrice || req.LimitPrice > terms.MaxPrice ||
			(req.TIF != exec.TIFGTC && req.TIF != exec.TIFIOC && req.TIF != exec.TIFFOK) {
			return nil, errors.New("invalid Binance Futures Demo limit price or time in force")
		}
		values.Set("price", strconv.FormatFloat(req.LimitPrice, 'f', -1, 64))
		values.Set("timeInForce", req.TIF.String())
	default:
		return nil, errors.New("Binance Futures Demo supports market and limit orders")
	}
	if err := a.checkPosition(ctx, req, terms, id.Native, buy, reduce); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		var reply order
		if err := a.request(ctx, http.MethodPost, "v1/order", values, &reply); err != nil {
			var code apiError
			if errors.As(err, &code) {
				switch code {
				case -1021, -1022, -1100, -1102, -1111, -1121, -2015, -2019, -2022, -2027,
					-4003, -4004, -4005, -4013, -4014, -4023, -4061, -4164, -4183, -4184:
					return exec.PerpetualSubmitDeclined{Cause: err}
				}
			}
			// A timeout, duplicate ID or unknown error is reconciled by ID.
			return err
		}
		if reply.ID <= 0 || reply.ClientID != req.ClientOrderID || reply.Symbol != id.Native {
			return errors.New("incomplete Binance Futures Demo order acknowledgement")
		}
		return nil
	}, nil
}

func (a *demoAPI) checkPosition(ctx context.Context, req exec.OrderRequest, terms linearperp.DemoRules, native string, buy, reduce bool) error {
	account, err := a.account(ctx)
	if err != nil {
		return err
	}
	symbol := func() url.Values { return url.Values{"symbol": {native}} }
	positions, err := a.positions(ctx, symbol())
	if err != nil {
		return err
	}
	var held float64
	for _, p := range positions {
		if p.Symbol != req.Symbol {
			return errors.New("unexpected position in Binance Futures Demo response")
		}
		held = p.Qty
	}
	if reduce {
		if held == 0 || buy != (held < 0) || req.Qty > math.Abs(held) {
			return errors.New("Binance Futures Demo close must reduce the confirmed position")
		}
		return nil
	}
	if !terms.TradingEnabled || (held != 0 && buy != (held > 0)) {
		return errors.New("resolve the Binance Futures Demo position before opening")
	}
	working, err := a.workingOrders(ctx, symbol())
	if err != nil {
		return err
	}
	if len(working) != 0 {
		return errors.New("resolve working Binance Futures Demo orders for this contract first")
	}
	var config []struct {
		Symbol     string `json:"symbol"`
		MarginType string `json:"marginType"`
		Leverage   int    `json:"leverage"`
	}
	if err := a.request(ctx, http.MethodGet, "v1/symbolConfig", symbol(), &config); err != nil {
		return err
	}
	if len(config) != 1 || config[0].Symbol != native || config[0].MarginType != "ISOLATED" {
		return errors.New("set this contract to isolated margin in Binance Futures Demo")
	}
	leverage := config[0].Leverage
	if leverage < 1 || leverage != req.Perpetual.Leverage {
		return fmt.Errorf("match Binance Futures Demo's configured leverage of %d×", leverage)
	}
	mark, fresh := a.FairPrice(req.Symbol)
	if !fresh {
		return errors.New("waiting for a fresh Binance Futures Demo mark price")
	}
	quantity := req.Qty * terms.ContractSize()
	price := mark
	if req.Type == exec.TypeLimit {
		price = req.LimitPrice
	}
	if quantity*price < terms.MinNotional {
		return fmt.Errorf("order is below the Binance Futures Demo minimum of %.8g USDT", terms.MinNotional)
	}
	if quantity*max(mark, price)/float64(leverage) > account.AvailableCash {
		return errors.New("insufficient Binance Futures Demo collateral")
	}
	return nil
}

func (a *demoAPI) lookup(ctx context.Context, req exec.OrderRequest) (order, exec.OrderStatus, error) {
	var row order
	id, valid := instrument.ParsePerpetual(req.Symbol)
	if !valid || id.Exchange != instrument.BinanceDemo {
		return row, 0, errors.New("invalid Binance Futures Demo symbol")
	}
	params := url.Values{"symbol": {id.Native}, "origClientOrderId": {req.ClientOrderID}}
	if err := a.request(ctx, http.MethodGet, "v1/order", params, &row); err != nil {
		return row, 0, err
	}
	status, statusOK := row.status()
	side, sideOK := row.side()
	if row.ID <= 0 || row.ClientID != req.ClientOrderID || row.Symbol != id.Native ||
		row.Type != req.Type.String() || !sideOK || side != req.Side || !statusOK {
		return row, 0, errors.New("mismatched Binance Futures Demo order")
	}
	return row, status, nil
}

func (a *demoAPI) Observe(ctx context.Context, req exec.OrderRequest) (exec.PerpetualObservation, error) {
	o, status, err := a.lookup(ctx, req)
	if err != nil {
		return exec.PerpetualObservation{}, err
	}
	// Continue using the admitted precision even if the catalog changed.
	quantum := strconv.FormatFloat(req.Perpetual.Multiplier, 'f', -1, 64)
	qty, valid := linearperp.DemoLots(o.Quantity, quantum)
	filled, validFill := linearperp.DemoLots(o.Executed, quantum)
	if !valid || !validFill || float64(qty) != req.Qty || filled < 0 || filled > qty ||
		(status == exec.StatusFilled && filled != qty) {
		return exec.PerpetualObservation{}, errors.New("invalid Binance Futures Demo order quantity")
	}
	report := exec.PerpetualObservation{BrokerOrderID: strconv.FormatInt(o.ID, 10), Status: status}
	if filled == 0 {
		return report, nil
	}
	var trades []struct {
		ID           int64       `json:"id"`
		OrderID      int64       `json:"orderId"`
		Symbol       string      `json:"symbol"`
		PositionSide string      `json:"positionSide"`
		Side         string      `json:"side"`
		Qty          json.Number `json:"qty"`
		Price        json.Number `json:"price"`
		Time         int64       `json:"time"`
	}
	params := url.Values{"symbol": {o.Symbol}, "orderId": {report.BrokerOrderID}, "limit": {"1000"}}
	if err := a.request(ctx, http.MethodGet, "v1/userTrades", params, &trades); err != nil {
		return report, err
	}
	var sum int64
	seen := make(map[int64]bool)
	for _, trade := range trades {
		qty, ok := linearperp.DemoLots(trade.Qty, quantum)
		price, priceOK := decimal(trade.Price)
		if trade.ID < 0 || seen[trade.ID] || trade.OrderID != o.ID || trade.Symbol != o.Symbol ||
			trade.Side != o.Direction || trade.PositionSide != "BOTH" || !ok || qty <= 0 ||
			!priceOK || price <= 0 || trade.Time <= 0 || qty > filled-sum {
			return report, errors.New("invalid Binance Futures Demo fills")
		}
		seen[trade.ID] = true
		sum += qty
		// Binance order and trade IDs are scoped to the symbol.
		fillID := o.Symbol + ":" + strconv.FormatInt(trade.ID, 10)
		report.Fills = append(report.Fills, exec.PerpetualFill{ID: fillID, Qty: float64(qty), Price: price, TsMs: trade.Time})
	}
	if sum != filled {
		return report, errors.New("Binance Futures Demo fills do not yet reconcile with the order")
	}
	slices.SortFunc(report.Fills, func(a, b exec.PerpetualFill) int { return cmp.Compare(a.TsMs, b.TsMs) })
	return report, nil
}

func (a *demoAPI) Cancel(ctx context.Context, req exec.OrderRequest) error {
	o, status, err := a.lookup(ctx, req)
	if err != nil {
		return err
	}
	if status != exec.StatusAccepted && status != exec.StatusPartiallyFilled {
		return nil
	}
	var reply order
	params := url.Values{"symbol": {o.Symbol}, "orderId": {strconv.FormatInt(o.ID, 10)}}
	if err := a.request(ctx, http.MethodDelete, "v1/order", params, &reply); err != nil {
		return err
	}
	if reply.ID != o.ID || reply.ClientID != o.ClientID || reply.Symbol != o.Symbol {
		return errors.New("mismatched Binance Futures Demo cancellation acknowledgement")
	}
	return nil
}
