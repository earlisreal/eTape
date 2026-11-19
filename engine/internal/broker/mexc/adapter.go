package mexc

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	market "github.com/earlisreal/eTape/engine/internal/feed/mexc"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type Live struct {
	*perps.Adapter
	api *liveAPI
}

type liveAPI struct {
	client
	market *market.Client
	quotes sync.Map
}

func NewLive(venue exec.VenueID, cr creds.Pair, clk clock.Clock, path string, data *market.Client) (*Live, error) {
	if cr.KeyID == "" || cr.SecretKey == "" || data == nil {
		return nil, errors.New("MEXC Live requires Futures credentials and market data")
	}
	api := &liveAPI{client: newClient(cr, clk), market: data}
	a, err := perps.New(venue, clk, path, "mexc-live:"+cr.KeyID, api)
	if err != nil {
		return nil, err
	}
	return &Live{Adapter: a, api: api}, nil
}

func (a *Live) SetFairQuote(q feed.Quote) {
	if id, ok := instrument.ParsePerpetual(q.Symbol); ok && id.Exchange == market.Exchange {
		a.api.quotes.Store(q.Symbol, q)
	}
}

func (a *liveAPI) Rules(symbol string) (instrument.ContractRules, bool) {
	return a.market.Rules(symbol)
}

func (a *liveAPI) FairPrice(symbol string) (float64, bool) {
	raw, exists := a.quotes.Load(symbol)
	if !exists {
		return 0, false
	}
	q := raw.(feed.Quote)
	age := a.clock.Now().Sub(time.UnixMilli(q.TsMs))
	return q.Mark, q.Mark > 0 && age >= -2*time.Second && age < 5*time.Second
}

func (a *liveAPI) Prepare(ctx context.Context, req exec.OrderRequest) (func(context.Context) error, error) {
	rules, ok := a.Rules(req.Symbol)
	if !ok {
		return nil, errors.New("MEXC contract terms expired; review the order")
	}
	fair, current := a.FairPrice(req.Symbol)
	if !current {
		fair = 0
	}
	input, err := a.prepareOrder(ctx, req, rules, fair)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		// Preflight uses the larger of fair and limit price for collateral.
		if current, fresh := a.FairPrice(req.Symbol); !input.ReduceOnly && (!fresh || current > max(fair, req.LimitPrice)) {
			return exec.PerpetualSubmitDeclined{Cause: errors.New("MEXC fair price changed; review the order")}
		}
		_, err := a.sendOrder(ctx, input)
		var apiErr apiError
		if errors.As(err, &apiErr) {
			// Duplicate IDs, busy responses and transport failures still
			// require lookup; these codes definitively reject this submit.
			switch apiErr.Code {
			case 401, 402, 406, 511, 600, 601, 602, 702, 704, 1001, 1002, 1004,
				2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008, 2009, 2011, 2015,
				2021, 2027, 2029, 2030, 2038, 2043, 2051, 2057, 7005, 7008, 8920:
				return exec.PerpetualSubmitDeclined{Cause: err}
			}
		}
		return err
	}, nil
}

var sides = [...]exec.Side{1: exec.SideBuy, 2: exec.SideCover, 3: exec.SideShort, 4: exec.SideSell}

func (a *liveAPI) Observe(ctx context.Context, req exec.OrderRequest) (exec.PerpetualObservation, error) {
	id, _ := instrument.ParsePerpetual(req.Symbol)
	state, err := a.reconcileOrder(ctx, orderIdentity{Symbol: id.Native, ClientID: req.ClientOrderID}, 0)
	if err != nil {
		return exec.PerpetualObservation{}, err
	}
	order := state.Order
	qty, ok := quantity(order.Volume)
	if !ok || qty != req.Qty || sides[order.Side] != req.Side {
		return exec.PerpetualObservation{}, errors.New("MEXC order terms differ from the admitted intent")
	}
	status := [...]exec.OrderStatus{1: exec.StatusAccepted, 2: exec.StatusAccepted, 3: exec.StatusFilled, 4: exec.StatusCanceled, 5: exec.StatusRejected}
	report := exec.PerpetualObservation{BrokerOrderID: order.ID.String(), Status: status[order.State]}
	for _, raw := range state.Fills {
		qty, _ := quantity(raw.Volume)
		price, _ := raw.Price.Float64()
		report.Fills = append(report.Fills, exec.PerpetualFill{ID: raw.ID.String(), Qty: qty, Price: price, TsMs: raw.TsMs})
	}
	return report, nil
}

func (a *liveAPI) Cancel(ctx context.Context, req exec.OrderRequest) error {
	id, _ := instrument.ParsePerpetual(req.Symbol)
	return a.cancelOrder(ctx, orderIdentity{Symbol: id.Native, ClientID: req.ClientOrderID})
}

func (a *liveAPI) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	state, err := a.snapshot(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	if state.Mode != oneWay {
		return exec.AccountSnapshot{}, nil, nil, errors.New("set MEXC Futures to one-way position mode")
	}
	cash, _ := state.Balance.Available.Float64()
	equity, eqErr := state.Balance.Equity.Float64()
	margin, marginErr := state.Balance.Margin.Float64()
	if eqErr != nil || marginErr != nil || margin < 0 || math.IsNaN(cash) || math.IsNaN(equity) {
		return exec.AccountSnapshot{}, nil, nil, errors.New("invalid MEXC Futures account valuation")
	}
	account := exec.AccountSnapshot{Currency: "USDT", Equity: equity, AvailableCash: cash, BuyingPower: cash,
		MarginUsed: margin, DayPnLProvisional: true}
	var positions []exec.Position
	seen := make(map[string]bool)
	for _, p := range state.Positions {
		if !validClientID(p.Symbol) {
			return account, nil, nil, errors.New("invalid MEXC position symbol")
		}
		if !strings.HasSuffix(p.Symbol, "_USDT") {
			continue
		}
		qty, valid := quantity(p.Volume)
		if !valid || qty != 0 && (p.Side != longPosition && p.Side != shortPosition || seen[p.Symbol]) {
			return account, nil, nil, errors.New("invalid or duplicate MEXC one-way position")
		}
		if qty == 0 {
			continue
		}
		symbol := instrument.Perpetual{Exchange: market.Exchange, Native: p.Symbol}.String()
		rules, ok := a.Rules(symbol)
		if !ok {
			if _, err := a.market.Contract(ctx, symbol); err != nil {
				return account, nil, nil, err
			}
			rules, ok = a.Rules(symbol)
		}
		avg, _ := p.Average.Float64()
		if avg <= 0 {
			avg, _ = p.RoundedAverage.Float64()
		}
		if !ok || avg <= 0 {
			return account, nil, nil, errors.New("missing MEXC position price or contract multiplier")
		}
		if p.Side == shortPosition {
			qty = -qty
		}
		used, _ := p.InitialMargin.Float64()
		seen[p.Symbol] = true
		positions = append(positions, exec.Position{Symbol: symbol, Qty: qty, AvgPrice: avg, OpenedMs: p.OpenedMs,
			Multiplier: rules.ContractSize(), Leverage: p.Leverage, MarginUsed: used})
	}
	var orders []exec.Order
	for _, raw := range state.Orders {
		if !strings.HasSuffix(raw.Symbol, "_USDT") {
			continue
		}
		qty, valid := quantity(raw.Volume)
		filled, validFill := quantity(raw.Filled)
		if !validExchangeID(raw.ID) || !valid || !validFill || filled >= qty || raw.Side < 1 || raw.Side > 4 {
			return account, nil, nil, errors.New("invalid working MEXC Futures order")
		}
		id := raw.ClientID
		if id == "" {
			id = "MEXC:" + raw.ID.String()
		}
		price, _ := raw.Price.Float64()
		orders = append(orders, exec.Order{ID: id, Symbol: instrument.Perpetual{Exchange: market.Exchange, Native: raw.Symbol}.String(),
			Side: sides[raw.Side], Status: exec.StatusAccepted, Type: exec.TypeLimit, Qty: qty, ExecutedQty: filled,
			LeavesQty: qty - filled, LimitPrice: price, UpdatedMs: raw.UpdatedMs})
	}
	return account, positions, orders, nil
}
