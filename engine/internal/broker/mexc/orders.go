package mexc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type position struct {
	ID               json.Number `json:"positionId"`
	Symbol           string      `json:"symbol"`
	Side             int         `json:"positionType"`
	MarginMode       int         `json:"openType"`
	Volume           json.Number `json:"holdVol"`
	Frozen           json.Number `json:"frozenVol"`
	Average          json.Number `json:"holdAvgPriceFullyScale"`
	RoundedAverage   json.Number `json:"holdAvgPrice"`
	LiquidationPrice json.Number `json:"liquidatePrice"`
	Leverage         int         `json:"leverage"`
	InitialMargin    json.Number `json:"im"`
	OpenedMs         int64       `json:"createTime"`
}

type venueOrder struct {
	ID          json.Number `json:"orderId"`
	ClientID    string      `json:"externalOid"`
	PositionID  json.Number `json:"positionId"`
	Symbol      string      `json:"symbol"`
	Side        int         `json:"side"`
	State       int         `json:"state"`
	Type        int         `json:"orderType"`
	Volume      json.Number `json:"vol"`
	Price       json.Number `json:"price"`
	Filled      json.Number `json:"dealVol"`
	AverageFill json.Number `json:"dealAvgPrice"`
	UpdatedMs   int64       `json:"updateTime"`
}

type positionStop struct {
	ID         json.Number `json:"id"`
	OrderID    json.Number `json:"orderId"`
	PositionID json.Number `json:"positionId"`
	Symbol     string      `json:"symbol"`
	State      int         `json:"state"`
	IsFinished int         `json:"isFinished"`
	Side       int         `json:"positionType"`
	Trend      int         `json:"lossTrend"`
	LossType   *int        `json:"stopLossType"`
	Reverse    int         `json:"stopLossReverse"`
	VolumeType int         `json:"volType"`
	VolumeMode string      `json:"profitLossVolType"`
	Volume     json.Number `json:"vol"`
	LossVolume json.Number `json:"stopLossVol"`
	LossPrice  json.Number `json:"stopLossPrice"`
}

func (s positionStop) active() bool { return s.State == 1 && s.IsFinished == 0 }

type venueFill struct {
	ID          json.Number `json:"id"`
	OrderID     json.Number `json:"orderId"`
	Symbol      string      `json:"symbol"`
	Side        int         `json:"side"`
	Volume      json.Number `json:"vol"`
	Price       json.Number `json:"price"`
	Fee         json.Number `json:"fee"`
	FeeCurrency string      `json:"feeCurrency"`
	TsMs        int64       `json:"timestamp"`
}

type orderIdentity struct {
	Symbol   string `json:"symbol"`
	ClientID string `json:"externalOid"`
}

type orderSubmission struct {
	orderIdentity
	PositionID   json.Number `json:"positionId,omitempty"`
	Side         int         `json:"side"`
	Type         int         `json:"type"`
	PositionMode int         `json:"positionMode"`
	MarginMode   int         `json:"openType"`
	ReduceOnly   bool        `json:"reduceOnly"`
	Volume       json.Number `json:"vol"`
	Price        json.Number `json:"price"`
	Leverage     int         `json:"leverage,omitempty"`
	StopLoss     json.Number `json:"stopLossPrice,omitempty"`
	LossTrend    int         `json:"lossTrend,omitempty"`
}

type orderReceipt struct {
	ID   json.Number `json:"orderId"`
	TsMs int64       `json:"ts"`
}

const (
	isolated      = 1
	longPosition  = 1
	shortPosition = 2
	fairTrigger   = 2
)

// placeOrder assumes an admitted, durably recorded intent. A transport error
// leaves the outcome unknown; the caller can read it by the same external ID.
func (c client) placeOrder(ctx context.Context, req exec.OrderRequest, rules instrument.ContractRules, fairPrice float64) (orderReceipt, error) {
	input, err := c.prepareOrder(ctx, req, rules, fairPrice)
	if err != nil {
		return orderReceipt{}, err
	}
	return c.sendOrder(ctx, input)
}

func (c client) prepareOrder(ctx context.Context, req exec.OrderRequest, rules instrument.ContractRules, fairPrice float64) (orderSubmission, error) {
	return c.prepareOrderWithSnapshot(ctx, req, rules, fairPrice, nil, nil)
}

func (c client) prepareOrderWithSnapshot(ctx context.Context, req exec.OrderRequest, rules instrument.ContractRules, fairPrice float64, cached *riskAccountSnapshot, opening *exec.PerpetualOpeningTerms) (orderSubmission, error) {
	id, ok := instrument.ParsePerpetual(req.Symbol)
	if !ok || id.Exchange != "MEXC" || !validClientID(req.ClientOrderID) ||
		req.Perpetual == nil || !rules.Valid() || !rules.ValidQuantity(req.Qty) ||
		req.Perpetual.Multiplier != rules.ContractSize() {
		return orderSubmission{}, errors.New("invalid MEXC Futures order terms")
	}
	input := orderSubmission{orderIdentity: orderIdentity{Symbol: id.Native, ClientID: req.ClientOrderID},
		MarginMode: isolated, PositionMode: oneWay, Volume: decimal(req.Qty), Price: "0"}
	var direction int
	switch req.Side {
	case exec.SideBuy:
		input.Side, direction = 1, longPosition
	case exec.SideCover:
		input.Side, direction, input.ReduceOnly = 2, shortPosition, true
	case exec.SideShort:
		input.Side, direction = 3, shortPosition
	case exec.SideSell:
		input.Side, direction, input.ReduceOnly = 4, longPosition, true
	default:
		return orderSubmission{}, errors.New("invalid MEXC Futures direction")
	}
	switch {
	case req.Type == exec.TypeMarket:
		input.Type = 5
	case req.Type == exec.TypeLimit && rules.ValidPrice(req.LimitPrice):
		input.Price = decimal(req.LimitPrice)
		switch req.TIF {
		case exec.TIFGTC:
			input.Type = 1
		case exec.TIFIOC:
			input.Type = 3
		case exec.TIFFOK:
			input.Type = 4
		default:
			return orderSubmission{}, errors.New("unsupported MEXC Futures time in force")
		}
	default:
		return orderSubmission{}, errors.New("invalid MEXC Futures order price or type")
	}
	stop := req.Perpetual.ProtectionPrice
	if stop != 0 && !req.Perpetual.LocalProtection {
		if input.ReduceOnly || input.Type != 3 || !rules.ValidPrice(stop) ||
			direction == longPosition && (stop >= req.LimitPrice || stop >= fairPrice) ||
			direction == shortPosition && (stop <= req.LimitPrice || stop <= fairPrice) {
			return orderSubmission{}, errors.New("MEXC fair-price protection requires a price-capped IOC opening")
		}
		input.StopLoss, input.LossTrend = decimal(stop), fairTrigger
	}

	var state accountState
	if cached != nil {
		state = cached.state
	} else {
		var err error
		state, err = c.snapshot(ctx)
		if err != nil {
			return orderSubmission{}, fmt.Errorf("preflight MEXC Futures: %w", err)
		}
	}
	if state.Mode != oneWay {
		return orderSubmission{}, errors.New("MEXC Futures account must be in one-way mode")
	}
	var current *position
	for i, p := range state.Positions {
		if p.Symbol != id.Native {
			continue
		}
		qty, valid := quantity(p.Volume)
		if !valid {
			return orderSubmission{}, errors.New("invalid MEXC Futures position volume")
		}
		if qty != 0 {
			if current != nil || (p.Side != longPosition && p.Side != shortPosition) {
				return orderSubmission{}, errors.New("ambiguous MEXC Futures position")
			}
			current = &state.Positions[i]
		}
	}
	if input.ReduceOnly {
		if stop != 0 || current == nil || current.Side != direction || current.MarginMode != isolated || !validExchangeID(current.ID) {
			return orderSubmission{}, errors.New("MEXC close requires a matching isolated position")
		}
		if expected := req.Perpetual.PositionID; expected != "" && expected != current.ID.String() {
			return orderSubmission{}, errors.New("the protected MEXC position changed before close")
		}
		held, _ := quantity(current.Volume)
		frozen, valid := quantity(current.Frozen)
		if !valid || req.Qty > held-frozen {
			return orderSubmission{}, errors.New("MEXC close exceeds available contracts")
		}
		input.PositionID = current.ID
	} else {
		input.Leverage = req.Perpetual.Leverage
		if err := c.checkOpening(ctx, req, rules, fairPrice, input, direction, state, current, opening); err != nil {
			return orderSubmission{}, err
		}
	}
	return input, nil
}

func (c client) sendOrder(ctx context.Context, input orderSubmission) (orderReceipt, error) {
	var result orderReceipt
	if err := c.post(ctx, "/api/v1/private/order/create", input, &result); err != nil {
		var apiErr apiError
		if errors.As(err, &apiErr) {
			// Duplicate IDs, busy responses and transport failures still
			// require lookup; these codes definitively reject this submit.
			switch apiErr.Code {
			case 6002:
				return orderReceipt{}, exec.PerpetualSubmitDeclined{Cause: errors.New(openingForbidden)}
			case 401, 402, 406, 511, 600, 601, 602, 702, 704, 1001, 1002, 1004,
				2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008, 2009, 2011, 2015,
				2021, 2027, 2029, 2030, 2038, 2043, 2051, 2057, 7005, 7008, 8920:
				return orderReceipt{}, exec.PerpetualSubmitDeclined{Cause: err}
			}
		}
		return orderReceipt{}, err
	}
	if !validExchangeID(result.ID) || result.TsMs <= 0 {
		return orderReceipt{}, errors.New("MEXC Futures returned an incomplete order acknowledgement")
	}
	return result, nil
}

type riskTier struct {
	Direction   int         `json:"positionType"`
	MarginMode  int         `json:"openType"`
	MaxVolume   json.Number `json:"maxVol"`
	MaxLeverage int         `json:"maxLeverage"`
	Leverage    int         `json:"leverage"`
}

func (c client) openingTerms(ctx context.Context, symbol string) (exec.PerpetualOpeningTerms, error) {
	var raw map[string][]riskTier
	var terms exec.PerpetualOpeningTerms
	if err := c.get(ctx, "/api/v1/private/account/risk_limit", url.Values{"symbol": {symbol}}, &raw); err != nil {
		return terms, fmt.Errorf("read MEXC risk tier: %w", err)
	}
	for _, tier := range raw[symbol] {
		if tier.MarginMode != isolated {
			continue
		}
		var side *exec.PerpetualOpeningLimit
		switch tier.Direction {
		case longPosition:
			side = &terms.Long
		case shortPosition:
			side = &terms.Short
		default:
			continue
		}
		maxQty, ok := quantity(tier.MaxVolume)
		if !ok || tier.Leverage <= 0 || tier.MaxLeverage < tier.Leverage || side.Leverage != 0 {
			return exec.PerpetualOpeningTerms{}, errors.New("MEXC returned invalid or ambiguous isolated risk terms")
		}
		*side = exec.PerpetualOpeningLimit{Leverage: tier.Leverage, MaxContracts: int64(maxQty)}
	}
	return terms, nil
}

func (c client) checkOpening(ctx context.Context, req exec.OrderRequest, rules instrument.ContractRules, fairPrice float64, input orderSubmission, direction int, state accountState, current *position, cached *exec.PerpetualOpeningTerms) error {
	if !rules.TradingEnabled || input.Leverage < rules.MinLeverage || input.Leverage > rules.MaxLeverage ||
		fairPrice <= 0 || math.IsInf(fairPrice, 0) || math.IsNaN(fairPrice) {
		return errors.New("MEXC Futures contract, leverage or fair price unavailable")
	}
	held := 0.0
	if current != nil {
		if current.Side != direction || current.MarginMode != isolated || current.Leverage != input.Leverage || input.StopLoss != "" || cached != nil {
			return errors.New("resolve the current MEXC Futures position before this opening")
		}
		held, _ = quantity(current.Volume)
	}
	for _, o := range state.Orders {
		if o.Symbol == input.Symbol {
			return errors.New("resolve working MEXC Futures orders for this contract first")
		}
	}
	for _, s := range state.Stops {
		if s.Symbol == input.Symbol && s.active() {
			return errors.New("resolve MEXC Futures TP/SL before adding")
		}
	}
	if cached == nil {
		terms, err := c.openingTerms(ctx, input.Symbol)
		if err != nil {
			return err
		}
		cached = &terms
	}
	limit := cached.Long
	if direction == shortPosition {
		limit = cached.Short
	}
	if limit.Leverage == 0 {
		return errors.New("MEXC isolated leverage is unavailable for this direction")
	}
	if limit.Leverage != input.Leverage {
		return fmt.Errorf("MEXC isolated leverage is %d× and this order uses %d×. Match the settings before sending", limit.Leverage, input.Leverage)
	}
	if held+req.Qty > float64(limit.MaxContracts) {
		return fmt.Errorf("MEXC isolated tier permits at most %d total contracts", limit.MaxContracts)
	}
	collateral, _ := state.Balance.Available.Float64()
	price := max(fairPrice, req.LimitPrice)
	margin := req.Qty * rules.ContractSize() * price * (1/float64(input.Leverage) + 2*rules.FeeRate())
	if margin > collateral {
		return errors.New("insufficient MEXC Futures USDT collateral")
	}
	return nil
}

// lookupOrder uses both the client ID and contract. A missing or mismatched
// response is not evidence that an ambiguous order did not reach the venue.
func (c client) lookupOrder(ctx context.Context, identity orderIdentity) (venueOrder, error) {
	if !validClientID(identity.Symbol) || !validClientID(identity.ClientID) {
		return venueOrder{}, errors.New("invalid MEXC Futures external order identity")
	}
	var order venueOrder
	path := "/api/v1/private/order/external/" + identity.Symbol + "/" + identity.ClientID
	if err := c.get(ctx, path, nil, &order); err != nil {
		return venueOrder{}, err
	}
	if !validExchangeID(order.ID) || order.Symbol != identity.Symbol || order.ClientID != identity.ClientID ||
		order.Side < 1 || order.Side > 4 || order.State < 1 || order.State > 5 {
		return venueOrder{}, errors.New("MEXC Futures lookup returned a mismatched order")
	}
	return order, nil
}

// cancelOrder uses venue evidence for the matching order identity. The cancel
// response acknowledges the request; status and fills still need reconciliation.
func (c client) cancelOrder(ctx context.Context, identity orderIdentity) error {
	order, err := c.lookupOrder(ctx, identity)
	if err != nil {
		return err
	}
	if order.State >= 3 {
		return nil
	}
	var result []struct {
		ID        json.Number `json:"orderId"`
		ErrorCode int         `json:"errorCode"`
	}
	if err := c.post(ctx, "/api/v1/private/order/cancel_with_external", []orderIdentity{identity}, &result); err != nil {
		return err
	}
	if len(result) != 1 || result[0].ID != order.ID {
		return errors.New("MEXC Futures cancel acknowledgement did not identify the order")
	}
	if result[0].ErrorCode != 0 {
		return apiError{Code: result[0].ErrorCode}
	}
	return nil
}

type orderState struct {
	Order              venueOrder
	Fills              []venueFill
	ProtectedContracts float64
}

func (c client) reconcileOrder(ctx context.Context, identity orderIdentity, reviewedStop float64) (orderState, error) {
	order, err := c.lookupOrder(ctx, identity)
	if err != nil {
		return orderState{}, err
	}
	state := orderState{Order: order}
	expected, ok := quantity(order.Filled)
	ordered, valid := quantity(order.Volume)
	if !ok || !valid || ordered <= 0 || expected > ordered || order.State == 3 && expected != ordered {
		return state, errors.New("invalid MEXC Futures filled quantity")
	}
	if expected == 0 {
		return state, nil
	}
	if err := c.get(ctx, "/api/v1/private/order/deal_details/"+order.ID.String(), nil, &state.Fills); err != nil {
		return state, err
	}
	var total float64
	seen := make(map[json.Number]struct{}, len(state.Fills))
	for _, fill := range state.Fills {
		qty, valid := quantity(fill.Volume)
		price, priceErr := fill.Price.Float64()
		if !validExchangeID(fill.ID) || fill.OrderID != order.ID || fill.Symbol != order.Symbol ||
			fill.Side != order.Side || !valid || qty <= 0 || priceErr != nil || price <= 0 || fill.TsMs <= 0 {
			return state, errors.New("invalid MEXC Futures fill identity or quantity")
		}
		if _, duplicate := seen[fill.ID]; duplicate {
			return state, errors.New("duplicate MEXC Futures fill")
		}
		seen[fill.ID] = struct{}{}
		total += qty
	}
	if total != expected {
		return state, errors.New("MEXC Futures fills do not yet reconcile with the order")
	}
	slices.SortStableFunc(state.Fills, func(a, b venueFill) int {
		if n := cmp.Compare(a.TsMs, b.TsMs); n != 0 {
			return n
		}
		return cmp.Compare(a.ID.String(), b.ID.String())
	})
	if reviewedStop <= 0 {
		return state, nil
	}
	var stops []positionStop
	if err := c.get(ctx, "/api/v1/private/stoporder/open_orders", url.Values{"symbol": {order.Symbol}}, &stops); err != nil {
		return state, err
	}
	direction := longPosition
	if order.Side == 3 {
		direction = shortPosition
	} else if order.Side != 1 {
		return state, errors.New("protective stop reconciliation requires an opening order")
	}
	for _, s := range stops {
		price, _ := s.LossPrice.Float64()
		if !s.active() || !validExchangeID(s.ID) || !validExchangeID(s.PositionID) || s.PositionID != order.PositionID ||
			s.OrderID != order.ID || s.Symbol != order.Symbol || s.Side != direction || s.Trend != fairTrigger ||
			s.LossType == nil || *s.LossType != 0 || s.Reverse != 2 || s.VolumeType != 1 || price != reviewedStop {
			continue
		}
		raw := s.Volume
		if s.VolumeMode == "SEPARATE" {
			raw = s.LossVolume
		} else if s.VolumeMode != "SAME" {
			continue
		}
		qty, ok := quantity(raw)
		if ok && qty > 0 && qty <= expected {
			if state.ProtectedContracts > 0 {
				state.ProtectedContracts = 0
				return state, errors.New("multiple MEXC Futures stops claim the same entry fill")
			}
			state.ProtectedContracts = qty
		}
	}
	return state, nil
}

func quantity(n json.Number) (float64, bool) {
	whole, fraction, _ := strings.Cut(n.String(), ".")
	if strings.TrimRight(fraction, "0") != "" {
		return 0, false
	}
	v, err := strconv.ParseUint(whole, 10, 53)
	return float64(v), err == nil
}

func decimal(v float64) json.Number { return json.Number(strconv.FormatFloat(v, 'f', -1, 64)) }

func validExchangeID(n json.Number) bool {
	return n != "" && n != "0" && !strings.ContainsFunc(n.String(), func(r rune) bool { return r < '0' || r > '9' })
}

func validClientID(s string) bool {
	return s != "" && len(s) <= 32 && !strings.ContainsFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != '_' && r != '-'
	})
}
