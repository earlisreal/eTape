package bingx

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func (a *virtualAPI) Prepare(ctx context.Context, req exec.OrderRequest) (func(context.Context) error, error) {
	if err := a.refreshCatalog(ctx); err != nil {
		return nil, err
	}
	terms, ok := a.Rules(req.Symbol)
	if !ok || req.Perpetual == nil || !terms.ValidQuantity(req.Qty) || terms.ContractSize() != req.Perpetual.Multiplier {
		return nil, errors.New("invalid BingX Virtual order terms")
	}
	id, _ := instrument.ParsePerpetual(req.Symbol)
	item, _ := a.contract(id.Native)
	params := orderIdentity(req)
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	reduce := req.Side == exec.SideCover || req.Side == exec.SideSell
	if !buy && req.Side != exec.SideSell && req.Side != exec.SideShort {
		return nil, errors.New("invalid BingX Virtual direction")
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
			return nil, errors.New("invalid BingX Virtual limit price or time in force")
		}
		params.Set("price", strconv.FormatFloat(req.LimitPrice, 'f', -1, 64))
		params.Set("timeInForce", req.TIF.String())
	default:
		return nil, errors.New("BingX Virtual supports market and limit orders")
	}
	state, err := a.account(ctx)
	if err != nil {
		return nil, err
	}
	var held, available float64
	var leverage int
	for _, p := range state.Positions {
		if p.Symbol != id.Native {
			continue
		}
		qty, valid := positionLots(p, *item.QtyDigits)
		avail, validAvailable := lots(p.Available, *item.QtyDigits)
		if !valid || qty != 0 && (held != 0 || !validAvailable || !p.Isolated) {
			return nil, errors.New("BingX Virtual requires one isolated position per contract")
		}
		if qty != 0 {
			held, available, leverage = qty, math.Abs(avail), p.Leverage
		}
	}
	if reduce {
		if item.Close != "true" || held == 0 || buy != (held < 0) || req.Qty > min(math.Abs(held), available) {
			return nil, errors.New("BingX Virtual close exceeds the confirmed isolated position available to reduce")
		}
	} else {
		if !terms.TradingEnabled || held != 0 && (buy != (held > 0) || leverage != req.Perpetual.Leverage) {
			return nil, errors.New("resolve the current isolated BingX Virtual position before opening")
		}
		for _, order := range state.Orders {
			if order.Symbol == id.Native {
				return nil, errors.New("resolve working BingX Virtual orders for this contract first")
			}
		}
		qty, _ := q.Float64()
		if err := a.checkOpening(ctx, id.Native, buy, qty, req, state.Account.AvailableCash, item); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) error {
		var response order
		if err := a.request(ctx, http.MethodPost, "/openApi/swap/v2/trade/order", params, true, &response); err != nil {
			// Documented definitive permission and business rejections.
			var code apiError
			if errors.As(err, &code) {
				switch code {
				case 100001, 100004, 100412, 100413, 100419, 100421, 101204, 101206, 101211, 101415, 101419, 109415, 110206:
					return exec.PerpetualSubmitDeclined{Cause: err}
				}
			}
			return err
		}
		if !validID(response.id()) || !strings.EqualFold(response.Client, req.ClientOrderID) || response.Symbol != id.Native {
			return errors.New("incomplete BingX Virtual order acknowledgement")
		}
		return nil
	}, nil
}

func (a *virtualAPI) checkOpening(ctx context.Context, symbol string, buy bool, qty float64, req exec.OrderRequest, available float64, item contract) error {
	params := func() url.Values { return url.Values{"symbol": {symbol}} }
	var margin struct {
		Type string `json:"marginType"`
	}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/marginType", params(), true, &margin); err != nil {
		return err
	}
	if margin.Type != "ISOLATED" {
		return errors.New("set this BingX Virtual contract to isolated margin")
	}
	var limit struct {
		Long     int         `json:"longLeverage"`
		Short    int         `json:"shortLeverage"`
		LongMax  int         `json:"maxLongLeverage"`
		ShortMax int         `json:"maxShortLeverage"`
		LongQty  json.Number `json:"availableLongVol"`
		ShortQty json.Number `json:"availableShortVol"`
	}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/leverage", params(), true, &limit); err != nil {
		return err
	}
	current, maximum, maxQty := limit.Short, limit.ShortMax, limit.ShortQty
	if buy {
		current, maximum, maxQty = limit.Long, limit.LongMax, limit.LongQty
	}
	maximumQty, parseErr := maxQty.Float64()
	leverage := req.Perpetual.Leverage
	if parseErr != nil || leverage < 1 || current != leverage || maximum < leverage || qty > maximumQty {
		return errors.New("match the BingX Virtual contract's configured leverage and available size")
	}
	var mark struct {
		Symbol string      `json:"symbol"`
		Price  json.Number `json:"markPrice"`
	}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/quote/premiumIndex", params(), false, &mark); err != nil {
		return err
	}
	price, priceErr := mark.Price.Float64()
	minimum, minErr := item.MinimumValue.Float64()
	fee, feeErr := item.Fee.Float64()
	if mark.Symbol != symbol || priceErr != nil || price <= 0 || minErr != nil || feeErr != nil || minimum < 0 || fee < 0 {
		return errors.New("BingX Virtual mark or minimum trade value unavailable")
	}
	if qty*max(price, req.LimitPrice)*(1/float64(leverage)+2*fee) > available {
		return errors.New("insufficient BingX Virtual collateral")
	}
	if req.Type == exec.TypeLimit {
		price = req.LimitPrice
	}
	if qty*price < minimum {
		return errors.New("order is below the BingX Virtual minimum trade value")
	}
	return nil
}

func orderIdentity(req exec.OrderRequest) url.Values {
	id, _ := instrument.ParsePerpetual(req.Symbol)
	return url.Values{"symbol": {id.Native}, "clientOrderId": {strings.ToLower(req.ClientOrderID)}}
}

func (a *virtualAPI) lookup(ctx context.Context, req exec.OrderRequest) (order, exec.OrderStatus, error) {
	var o order
	params := orderIdentity(req)
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/order", params, true, &o); err != nil {
		return o, 0, err
	}
	buy := req.Side == exec.SideBuy || req.Side == exec.SideCover
	reduce := req.Side == exec.SideSell || req.Side == exec.SideCover
	if !validID(o.id()) || !strings.EqualFold(o.Client, req.ClientOrderID) || o.Symbol != params.Get("symbol") ||
		o.Type != req.Type.String() || o.Side != "BUY" && o.Side != "SELL" || buy != (o.Side == "BUY") || o.ReduceOnly != reduce {
		return o, 0, errors.New("mismatched BingX Virtual order identity")
	}
	switch o.Status {
	case "NEW", "PENDING", "PARTIALLYFILLED":
		return o, exec.StatusAccepted, nil
	case "FILLED":
		return o, exec.StatusFilled, nil
	case "CANCELLED":
		return o, exec.StatusCanceled, nil
	case "FAILED":
		return o, exec.StatusRejected, nil
	case "EXPIRED":
		return o, exec.StatusExpired, nil
	default:
		return o, 0, errors.New("unknown BingX Virtual order state")
	}
}

func (a *virtualAPI) Observe(ctx context.Context, req exec.OrderRequest) (exec.PerpetualObservation, error) {
	o, status, err := a.lookup(ctx, req)
	if err != nil {
		return exec.PerpetualObservation{}, err
	}
	// Use the admitted lot quantum even if exchange precision later changes.
	quantum := strconv.FormatFloat(req.Perpetual.Multiplier, 'f', -1, 64)
	_, fraction, _ := strings.Cut(quantum, ".")
	digits := len(fraction)
	qty, valid := lots(o.Quantity, digits)
	filled, validFill := lots(o.Filled, digits)
	if !valid || !validFill || qty != req.Qty || filled < 0 || filled > qty || status == exec.StatusFilled && filled != qty {
		return exec.PerpetualObservation{}, errors.New("invalid BingX Virtual order quantity")
	}
	report := exec.PerpetualObservation{BrokerOrderID: o.id(), Status: status}
	if filled == 0 {
		return report, nil
	}
	var reply list[struct {
		OrderID  json.Number `json:"orderId"`
		Symbol   string      `json:"symbol"`
		Side     string      `json:"side"`
		Quantity json.Number `json:"volume"`
		Price    json.Number `json:"price"`
		Time     string      `json:"filledTime"`
	}]
	reply.Key = "fill_orders"
	if o.Time <= 0 {
		return report, errors.New("missing BingX Virtual order time")
	}
	params := url.Values{"tradingUnit": {"COIN"}, "currency": {"USDT"}, "orderId": {o.id()},
		"startTs": {strconv.FormatInt(o.Time-1000, 10)}, "endTs": {strconv.FormatInt(a.clock.Now().UnixMilli(), 10)}}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/allFillOrders", params, true, &reply); err != nil {
		return report, err
	}
	var sum float64
	occurrences := make(map[string]int)
	for _, f := range reply.Items {
		q, ok := lots(f.Quantity, digits)
		price, err := f.Price.Float64()
		when, timeErr := time.Parse(time.RFC3339Nano, f.Time)
		if f.OrderID.String() != o.id() || f.Symbol != o.Symbol || f.Side != o.Side ||
			!ok || q <= 0 || err != nil || price <= 0 || timeErr != nil || when.UnixMilli() <= 0 {
			return report, errors.New("invalid BingX Virtual fills")
		}
		// VST returns no execution ID. Build stable identities from the
		// normalized execution and preserve its multiplicity within this order.
		key := strconv.FormatInt(when.UnixMilli(), 10) + ":" + strconv.FormatInt(int64(q), 10) + ":" +
			strconv.FormatFloat(price, 'f', -1, 64)
		occurrences[key]++
		id := key + ":" + strconv.Itoa(occurrences[key])
		sum += q
		report.Fills = append(report.Fills, exec.PerpetualFill{ID: id, Qty: q, Price: price, TsMs: when.UnixMilli()})
	}
	if sum != filled {
		return report, errors.New("BingX Virtual fills do not yet reconcile with the order")
	}
	slices.SortFunc(report.Fills, func(a, b exec.PerpetualFill) int {
		if n := cmp.Compare(a.TsMs, b.TsMs); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return report, nil
}

func (a *virtualAPI) Cancel(ctx context.Context, req exec.OrderRequest) error {
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
		return errors.New("BingX Virtual cancellation acknowledgement did not identify the order")
	}
	return nil
}
