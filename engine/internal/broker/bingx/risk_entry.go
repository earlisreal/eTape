package bingx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const leveragePath = "/openApi/swap/v2/trade/leverage"

type leverageSettings struct {
	Long     int         `json:"longLeverage"`
	Short    int         `json:"shortLeverage"`
	LongMax  int         `json:"maxLongLeverage"`
	ShortMax int         `json:"maxShortLeverage"`
	LongQty  json.Number `json:"availableLongVol"`
	ShortQty json.Number `json:"availableShortVol"`
}

func (a *apiClient) openingLeverage(ctx context.Context, symbol string) (leverageSettings, error) {
	var limits leverageSettings
	err := a.request(ctx, http.MethodGet, leveragePath, url.Values{"symbol": {symbol}}, true, &limits)
	return limits, err
}

func (a *apiClient) checkIsolated(ctx context.Context, symbol string) error {
	var margin struct {
		Type string `json:"marginType"`
	}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/marginType", url.Values{"symbol": {symbol}}, true, &margin); err != nil {
		return err
	}
	if margin.Type != "ISOLATED" {
		return fmt.Errorf("set this %s contract to isolated margin", a.env.label)
	}
	return nil
}

func (a *apiClient) markPrice(ctx context.Context, symbol string) (float64, error) {
	var mark struct {
		Symbol string      `json:"symbol"`
		Price  json.Number `json:"markPrice"`
	}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/quote/premiumIndex", url.Values{"symbol": {symbol}}, false, &mark); err != nil {
		return 0, err
	}
	price, err := mark.Price.Float64()
	if err != nil || mark.Symbol != symbol || !(price > 0 && price < math.Inf(1)) {
		return 0, fmt.Errorf("%s mark price is unavailable", a.env.label)
	}
	return price, nil
}

func openingLots(value json.Number, digits int) (int64, bool) {
	amount, ok := new(big.Rat).SetString(value.String())
	if !ok || amount.Sign() < 0 {
		return 0, false
	}
	unit, _ := new(big.Rat).SetString(tick(digits))
	amount.Quo(amount, unit)
	qty := new(big.Int).Quo(amount.Num(), amount.Denom())
	return qty.Int64(), qty.BitLen() <= 53
}

func (a *apiClient) maintenanceTiers(ctx context.Context, symbol string) ([]instrument.NotionalTier, error) {
	type maintenanceTier struct {
		Symbol    string      `json:"symbol"`
		Minimum   json.Number `json:"minPositionVal"`
		Maximum   json.Number `json:"maxPositionVal"`
		Rate      json.Number `json:"maintMarginRatio"`
		Deduction json.Number `json:"maintAmount"`
	}
	rows := list[maintenanceTier]{Key: "tiers"}
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v1/maintMarginRatio", url.Values{"symbol": {symbol}}, true, &rows); err != nil {
		return nil, err
	}
	var tiers []instrument.NotionalTier
	var previous, previousRate, previousDeduction float64
	for _, row := range rows.Items {
		lower, e1 := row.Minimum.Float64()
		upper, e2 := row.Maximum.Float64()
		rate, e3 := row.Rate.Float64()
		deduct, e4 := row.Deduction.Float64()
		if row.Symbol != symbol || errors.Join(e1, e2, e3, e4) != nil || !(lower == previous && upper > lower && upper < math.Inf(1) &&
			rate >= previousRate && rate < 1 && deduct >= 0 && deduct < math.Inf(1)) ||
			lower*rate-deduct+0.01 < lower*previousRate-previousDeduction {
			return nil, errors.New("BingX maintenance tier data is incomplete")
		}
		tiers = append(tiers, instrument.NotionalTier{Maximum: upper, Rate: rate, Deduction: deduct})
		previous, previousRate, previousDeduction = upper, rate, deduct
	}
	if len(tiers) == 0 {
		return nil, errors.New("BingX maintenance tiers are unavailable")
	}
	return tiers, nil
}

func (a *apiClient) cachedRiskOpening(symbol string) (exec.PerpetualOpeningTerms, error) {
	now := a.clock.Now()
	terms, err := a.opening.Current(symbol, now)
	if err != nil {
		return terms, err
	}
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != "BINGX" {
		return terms, errors.New("select a BingX contract")
	}
	item, ok := a.contract(id.Native)
	if !ok || item.Close != "true" {
		return terms, errors.New("BingX contract is unavailable for protected trading")
	}
	snapshot := a.riskAccount.Load()
	if snapshot == nil || now.Sub(snapshot.at) >= perps.OpeningSnapshotMaxAge {
		return terms, errors.New("fresh BingX account data required")
	}
	state := snapshot.state
	if slices.ContainsFunc(state.Orders, func(o order) bool { return o.Symbol == id.Native }) {
		return terms, errors.New("resolve working BingX orders for this contract first")
	}
	var held instrument.RiskPosition
	for _, row := range state.Positions {
		if row.Symbol != id.Native {
			continue
		}
		qty, ok := positionLots(row, *item.QtyDigits)
		if !ok {
			return terms, errors.New("invalid BingX position quantity")
		}
		if qty == 0 {
			continue
		}
		id := positionID(row.ID)
		average, e1 := row.Average.Float64()
		margin, e2 := row.IsolatedMargin.Float64()
		liq, e3 := row.Liquidation.Float64()
		if terms.PositionID != "" || id == "" || !row.Isolated || errors.Join(e1, e2, e3) != nil || row.Leverage < 1 ||
			!(average > 0 && average < math.Inf(1) && margin > 0 && margin < math.Inf(1) && liq >= 0 && liq < math.Inf(1)) {
			return terms, errors.New("BingX position margin or identity could not be verified")
		}
		terms.PositionID = id
		held = instrument.RiskPosition{Qty: qty, AvgPrice: average, Margin: margin, LiquidationPrice: liq, Leverage: row.Leverage}
	}
	mark, fresh := a.FairPrice(symbol)
	if !fresh {
		return terms, errors.New("fresh BingX mark price required")
	}
	notional := *terms.Notional
	notional.Mark, notional.Position = mark, held
	terms.Notional, terms.Collateral = &notional, state.Account.AvailableCash
	return terms, nil
}

func (a *apiClient) readRiskSettings(ctx context.Context, symbol string) (exec.PerpetualOpeningTerms, error) {
	var terms exec.PerpetualOpeningTerms
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != "BINGX" {
		return terms, errors.New("select a BingX contract")
	}
	item, ok := a.contract(id.Native)
	if !ok || item.Close != "true" {
		return terms, errors.New("BingX contract is unavailable for protected trading")
	}
	symbol = id.Native
	minimum, err := item.MinimumValue.Float64()
	if err != nil || !(minimum >= 0 && minimum < math.Inf(1)) {
		return terms, errors.New("BingX minimum order value is unavailable")
	}
	var limits leverageSettings
	var tiers []instrument.NotionalTier
	checks, checkCtx := errgroup.WithContext(ctx)
	checks.Go(func() error { return a.checkIsolated(checkCtx, symbol) })
	checks.Go(func() error {
		var err error
		limits, err = a.openingLeverage(checkCtx, symbol)
		return err
	})
	checks.Go(func() error {
		var err error
		tiers, err = a.maintenanceTiers(checkCtx, symbol)
		return err
	})
	if err := checks.Wait(); err != nil {
		return terms, err
	}
	longQty, longOK := openingLots(limits.LongQty, *item.QtyDigits)
	shortQty, shortOK := openingLots(limits.ShortQty, *item.QtyDigits)
	if !longOK || !shortOK || limits.Long < 1 || limits.Short < 1 || limits.LongMax < 1 || limits.ShortMax < 1 {
		return terms, errors.New("BingX leverage or opening capacity is unavailable")
	}
	terms.Long = exec.PerpetualOpeningLimit{Leverage: limits.Long, MaxContracts: longQty, MaxLeverage: limits.LongMax}
	terms.Short = exec.PerpetualOpeningLimit{Leverage: limits.Short, MaxContracts: shortQty, MaxLeverage: limits.ShortMax}
	terms.Notional = &instrument.NotionalRisk{MinimumValue: minimum, Tiers: tiers}
	return terms, nil
}

func (a *Adapter) OpeningTerms(ctx context.Context, symbol string) (exec.PerpetualOpeningTerms, error) {
	if _, ok := a.ContractRules(symbol); !ok {
		return exec.PerpetualOpeningTerms{}, errors.New("BingX contract terms are unavailable")
	}
	a.api.opening.Refresh(ctx, symbol, a.api.clock.Now(), a.api.readRiskSettings)
	return a.CachedOpeningTerms(symbol)
}

func (a *Adapter) CachedOpeningTerms(symbol string) (exec.PerpetualOpeningTerms, error) {
	return a.api.cachedRiskOpening(symbol)
}

func (a *apiClient) checkRiskOpening(req exec.OrderRequest, rules instrument.ContractRules, terms exec.PerpetualOpeningTerms) error {
	p := req.Perpetual
	long := req.Side == exec.SideBuy
	side := terms.Long
	if !long {
		side = terms.Short
	}
	if !rules.TradingEnabled || rules.ContractSize() != p.Multiplier || !rules.ValidQuantity(req.Qty) || terms.Notional == nil ||
		terms.PositionID != p.PositionID || terms.Notional.Position.Qty != 0 && terms.Notional.Position.Leverage != p.Leverage {
		return errors.New("BingX position or contract changed; review the entry")
	}
	if p.Leverage != side.Leverage || p.Leverage > side.MaxLeverage {
		return errors.New("BingX isolated leverage differs from the reviewed setting")
	}
	if long && terms.Notional.Mark <= p.ProtectionPrice || !long && terms.Notional.Mark >= p.ProtectionPrice {
		return errors.New("BingX mark price has already reached the protective stop")
	}
	qty, _ := rules.SizeNotionalRisk(*terms.Notional, p.RiskBudget, terms.Collateral, req.LimitPrice, p.ProtectionPrice,
		p.Leverage, p.Leverage, side.Leverage, side.MaxContracts, int64(req.Qty), long)
	if float64(qty) != req.Qty {
		return errors.New("BingX capacity, collateral or liquidation clearance changed; review the entry")
	}
	return nil
}
