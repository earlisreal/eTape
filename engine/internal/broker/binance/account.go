package binance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/linearperp"
)

type position struct {
	Symbol   string      `json:"symbol"`
	Side     string      `json:"positionSide"`
	Quantity json.Number `json:"positionAmt"`
	Entry    json.Number `json:"entryPrice"`
	Margin   json.Number `json:"positionInitialMargin"`
}

func (a *demoAPI) checkAccountMode(ctx context.Context) error {
	var mode struct {
		Hedge      *bool `json:"dualSidePosition"`
		MultiAsset *bool `json:"multiAssetsMargin"`
		CanTrade   *bool `json:"canTrade"`
	}
	if err := a.request(ctx, http.MethodGet, "v1/accountConfig", nil, &mode); err != nil {
		return err
	}
	if mode.Hedge == nil || *mode.Hedge {
		return errors.New("set Binance Futures Demo to one-way position mode")
	}
	if mode.MultiAsset == nil || *mode.MultiAsset {
		return errors.New("set Binance Futures Demo to single-asset mode")
	}
	if mode.CanTrade == nil || !*mode.CanTrade {
		return errors.New("trading is unavailable on this Binance Futures Demo account")
	}
	return nil
}

func (a *demoAPI) account(ctx context.Context) (exec.AccountSnapshot, error) {
	if err := a.checkAccountMode(ctx); err != nil {
		return exec.AccountSnapshot{}, err
	}
	var data struct {
		Equity json.Number `json:"totalMarginBalance"`
		Cash   json.Number `json:"availableBalance"`
		Used   json.Number `json:"totalInitialMargin"`
	}
	if err := a.request(ctx, http.MethodGet, "v3/account", nil, &data); err != nil {
		return exec.AccountSnapshot{}, err
	}
	equity, equityOK := decimal(data.Equity)
	cash, cashOK := decimal(data.Cash)
	used, usedOK := decimal(data.Used)
	if !equityOK || !cashOK || !usedOK || used < 0 {
		return exec.AccountSnapshot{}, errors.New("invalid Binance Futures Demo collateral values")
	}
	return exec.AccountSnapshot{Currency: "USDT", Equity: equity, AvailableCash: cash, BuyingPower: cash,
		MarginUsed: used, DayPnLProvisional: true}, nil
}

func (a *demoAPI) positions(ctx context.Context, params url.Values) ([]exec.Position, error) {
	var rows []position
	if err := a.request(ctx, http.MethodGet, "v3/positionRisk", params, &rows); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var positions []exec.Position
	for _, row := range rows {
		symbol := demoSymbol(row.Symbol)
		terms, ok := a.catalog.DemoRules(symbol)
		if !ok {
			continue
		}
		qty, valid := linearperp.DemoLots(row.Quantity, terms.Multiplier)
		if !valid || (qty != 0 && (row.Side != "BOTH" || seen[symbol])) {
			return nil, errors.New("invalid Binance Futures Demo one-way position")
		}
		if qty == 0 {
			continue
		}
		avg, avgOK := decimal(row.Entry)
		margin, marginOK := decimal(row.Margin)
		if !avgOK || !marginOK || avg <= 0 || margin < 0 {
			return nil, errors.New("invalid Binance Futures Demo position values")
		}
		seen[symbol] = true
		positions = append(positions, exec.Position{Symbol: symbol, Qty: float64(qty), AvgPrice: avg,
			Multiplier: terms.ContractSize(), MarginUsed: margin})
	}
	return positions, nil
}

func (a *demoAPI) workingOrders(ctx context.Context, params url.Values) ([]order, error) {
	var rows []order
	err := a.request(ctx, http.MethodGet, "v1/openOrders", params, &rows)
	return rows, err
}

func (a *demoAPI) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	if err := a.catalog.Refresh(ctx); err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	account, err := a.account(ctx)
	if err != nil {
		return account, nil, nil, err
	}
	positions, err := a.positions(ctx, nil)
	if err != nil {
		return account, nil, nil, err
	}
	working, err := a.workingOrders(ctx, nil)
	if err != nil {
		return account, nil, nil, err
	}
	var orders []exec.Order
	for _, row := range working {
		symbol := demoSymbol(row.Symbol)
		terms, ok := a.catalog.DemoRules(symbol)
		if !ok {
			continue
		}
		qty, valid := linearperp.DemoLots(row.Quantity, terms.Multiplier)
		filled, validFill := linearperp.DemoLots(row.Executed, terms.Multiplier)
		status, statusOK := row.status()
		side, sideOK := row.side()
		if row.ID <= 0 || row.ClientID == "" || !valid || !validFill || filled < 0 || filled >= qty ||
			!sideOK || !statusOK || (status != exec.StatusAccepted && status != exec.StatusPartiallyFilled) {
			return account, nil, nil, errors.New("invalid Binance Futures Demo working order")
		}
		price, _ := decimal(row.Price)
		avg, _ := decimal(row.Average)
		typ := exec.TypeLimit
		if row.Type == "MARKET" {
			typ = exec.TypeMarket
		}
		orders = append(orders, exec.Order{ID: row.ClientID, Symbol: symbol, Side: side, Status: status, Type: typ,
			Qty: float64(qty), ExecutedQty: float64(filled), LeavesQty: float64(qty - filled),
			LimitPrice: price, AvgFillPrice: avg, CreatedMs: row.Time, UpdatedMs: row.Updated})
	}
	return account, positions, orders, nil
}
