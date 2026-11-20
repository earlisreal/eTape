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
	"strconv"

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

func (a *apiClient) riskOpening(ctx context.Context, symbol string, item contract, state accountState) (exec.PerpetualOpeningTerms, error) {
	var terms exec.PerpetualOpeningTerms
	if slices.ContainsFunc(state.Orders, func(o order) bool { return o.Symbol == symbol }) {
		return terms, errors.New("resolve working BingX orders for this contract first")
	}
	var held instrument.RiskPosition
	for _, row := range state.Positions {
		if row.Symbol != symbol {
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
	if err := a.checkIsolated(ctx, symbol); err != nil {
		return terms, err
	}
	limits, err := a.openingLeverage(ctx, symbol)
	if err != nil {
		return terms, err
	}
	longQty, longOK := openingLots(limits.LongQty, *item.QtyDigits)
	shortQty, shortOK := openingLots(limits.ShortQty, *item.QtyDigits)
	if !longOK || !shortOK || limits.Long < 1 || limits.Short < 1 || limits.LongMax < 1 || limits.ShortMax < 1 {
		return terms, errors.New("BingX leverage or opening capacity is unavailable")
	}
	mark, err := a.markPrice(ctx, symbol)
	if err != nil {
		return terms, err
	}
	minimum, err := item.MinimumValue.Float64()
	if err != nil || !(minimum >= 0 && minimum < math.Inf(1)) {
		return terms, errors.New("BingX minimum order value is unavailable")
	}
	tiers, err := a.maintenanceTiers(ctx, symbol)
	if err != nil {
		return terms, err
	}
	terms.Long = exec.PerpetualOpeningLimit{Leverage: limits.Long, MaxContracts: longQty, MaxLeverage: limits.LongMax}
	terms.Short = exec.PerpetualOpeningLimit{Leverage: limits.Short, MaxContracts: shortQty, MaxLeverage: limits.ShortMax}
	terms.Notional = &instrument.NotionalRisk{Mark: mark, MinimumValue: minimum, Tiers: tiers, Position: held}
	terms.Collateral = state.Account.AvailableCash
	return terms, nil
}

func (a *Adapter) OpeningTerms(ctx context.Context, symbol string) (exec.PerpetualOpeningTerms, error) {
	return a.api.readRiskOpening(ctx, symbol)
}

func (a *apiClient) readRiskOpening(ctx context.Context, symbol string) (exec.PerpetualOpeningTerms, error) {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != "BINGX" {
		return exec.PerpetualOpeningTerms{}, errors.New("select a BingX contract")
	}
	if err := a.refreshCatalog(ctx); err != nil {
		return exec.PerpetualOpeningTerms{}, err
	}
	item, ok := a.contract(id.Native)
	if !ok || item.Close != "true" {
		return exec.PerpetualOpeningTerms{}, errors.New("BingX contract is unavailable for protected trading")
	}
	state, err := a.account(ctx)
	if err != nil {
		return exec.PerpetualOpeningTerms{}, err
	}
	return a.riskOpening(ctx, id.Native, item, state)
}

func (a *apiClient) checkRiskOpening(req exec.OrderRequest, rules instrument.ContractRules, terms exec.PerpetualOpeningTerms, dispatch bool) error {
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
	if dispatch && (long && terms.Notional.Mark <= p.ProtectionPrice || !long && terms.Notional.Mark >= p.ProtectionPrice) {
		return errors.New("BingX mark price has already reached the protective stop")
	}
	qty, _ := rules.SizeNotionalRisk(*terms.Notional, p.RiskBudget, terms.Collateral, req.LimitPrice, p.ProtectionPrice,
		p.Leverage, p.Leverage, side.Leverage, side.MaxContracts, int64(req.Qty), long)
	if float64(qty) != req.Qty {
		return errors.New("BingX capacity, collateral or liquidation clearance changed; review the entry")
	}
	return nil
}

func (a *Adapter) ArmPerpetualRisk(ctx context.Context, req exec.OrderRequest) (int64, error) {
	var capacity int64
	err := a.WithIdleContract(req.Symbol, func() error {
		p := req.Perpetual
		if p == nil || !p.WholePosition || !p.LocalProtection || !p.IsRiskEntry() || req.Side != exec.SideBuy && req.Side != exec.SideShort {
			return errors.New("invalid BingX protected entry")
		}
		terms, err := a.OpeningTerms(ctx, req.Symbol)
		if err != nil {
			return err
		}
		rules, ok := a.ContractRules(req.Symbol)
		if !ok || terms.PositionID != p.PositionID {
			return errors.New("BingX position or contract changed; review the entry")
		}
		side := terms.Long
		if req.Side == exec.SideShort {
			side = terms.Short
		}
		if side.Leverage != p.Leverage {
			if p.PositionID != "" || !p.AutoLeverage || p.Leverage < 1 || p.Leverage > side.MaxLeverage {
				return errors.New("use the verified BingX leverage for this entry")
			}
			qty, _ := rules.SizeNotionalRisk(*terms.Notional, p.RiskBudget, terms.Collateral, req.LimitPrice, p.ProtectionPrice,
				p.Leverage, p.Leverage, side.Leverage, side.MaxContracts, int64(req.Qty), req.Side == exec.SideBuy)
			if float64(qty) != req.Qty {
				return errors.New("BingX entry no longer fits the reviewed leverage")
			}
			id, _ := instrument.ParsePerpetual(req.Symbol)
			var response json.RawMessage
			writeErr := a.api.request(ctx, http.MethodPost, leveragePath, url.Values{"symbol": {id.Native}, "side": {"BOTH"}, "leverage": {strconv.Itoa(p.Leverage)}}, true, &response)
			terms, err = a.OpeningTerms(ctx, req.Symbol)
			if err != nil || terms.Long.Leverage != p.Leverage || terms.Short.Leverage != p.Leverage {
				return fmt.Errorf("BingX leverage change could not be verified: %w", errors.Join(writeErr, err, errors.New("check the contract's current leverage")))
			}
			side = terms.Long
			if req.Side == exec.SideShort {
				side = terms.Short
			}
		}
		if err := a.api.checkRiskOpening(req, rules, terms, false); err != nil {
			return err
		}
		capacity = side.MaxContracts
		return nil
	})
	return capacity, err
}
