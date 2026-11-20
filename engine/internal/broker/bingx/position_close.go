package bingx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const (
	closePositionPath = "/openApi/swap/v1/trade/closePosition"
	// Allow two normal account polling intervals before requiring a fresh read.
	positionCloseSnapshotMaxAge = 6 * time.Second
)

func (a *apiClient) PreparePositionClose(ctx context.Context, req exec.OrderRequest) (func(context.Context) (string, error), error) {
	id, ok := instrument.ParsePerpetual(req.Symbol)
	p := req.Perpetual
	if !ok || id.Exchange != "BINGX" || p == nil || !p.WholePosition || !p.LocalProtection ||
		!validID(p.PositionID) || req.Type != exec.TypeMarket || req.Side != exec.SideSell && req.Side != exec.SideCover {
		return nil, errors.New("invalid BingX full-position close")
	}
	if err := a.refreshCatalog(ctx); err != nil {
		return nil, err
	}
	item, ok := a.contract(id.Native)
	rules, validRules := a.Rules(req.Symbol)
	if !ok || !validRules || item.Close != "true" || p.Multiplier != rules.ContractSize() {
		return nil, errors.New("BingX full-position close terms are unavailable")
	}
	if err := a.verifyPositionClose(ctx, id.Native, p.PositionID, *item.QtyDigits, req.Side == exec.SideSell); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		var response struct {
			OrderID    json.Number `json:"orderId"`
			PositionID json.Number `json:"positionId"`
		}
		if err := a.request(ctx, http.MethodPost, closePositionPath, url.Values{"positionId": {p.PositionID}}, true, &response); err != nil {
			return "", err
		}
		if !validID(response.OrderID.String()) || response.PositionID.String() != p.PositionID {
			return "", errors.New("BingX full-position close acknowledgement is incomplete")
		}
		return response.OrderID.String(), nil
	}, nil
}

func (a *apiClient) verifyPositionClose(ctx context.Context, native, originalID string, digits int, long bool) error {
	if snapshot := a.riskAccount.Load(); snapshot != nil {
		if age := a.clock.Now().Sub(snapshot.at); age >= 0 && age < positionCloseSnapshotMaxAge &&
			matchPositionClose(snapshot.state.Positions, native, originalID, digits, long) == nil {
			return nil
		}
	}
	if err := a.checkOneWay(ctx); err != nil {
		return err
	}
	var positions []position
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/user/positions", url.Values{"symbol": {native}}, true, &positions); err != nil {
		return err
	}
	return matchPositionClose(positions, native, originalID, digits, long)
}

func matchPositionClose(positions []position, native, originalID string, digits int, long bool) error {
	matched := false
	for _, row := range positions {
		if row.Symbol != native {
			continue
		}
		qty, valid := positionLots(row, digits)
		if !valid || qty != 0 && (matched || !row.Isolated || positionID(row.ID) != originalID || (qty > 0) != long) {
			return errors.New("BingX original isolated position no longer matches its stop")
		}
		matched = matched || qty != 0
	}
	if !matched {
		return errors.New("BingX original position is no longer open")
	}
	return nil
}

func (a *apiClient) ObservePositionClose(ctx context.Context, req exec.OrderRequest, brokerID string) (exec.PerpetualObservation, error) {
	var result exec.PerpetualObservation
	id, ok := instrument.ParsePerpetual(req.Symbol)
	if !ok || id.Exchange != "BINGX" || req.Perpetual == nil || !validID(req.Perpetual.PositionID) {
		return result, errors.New("invalid BingX full-position close identity")
	}
	position := req.Perpetual.PositionID
	if brokerID == "" {
		// A missing acknowledgement cannot be matched to another trader's exit.
		// Retire a closed target using signed position evidence without claiming a fill.
		var positions []struct {
			Symbol string          `json:"symbol"`
			ID     json.RawMessage `json:"positionId"`
			Qty    json.Number     `json:"positionAmt"`
		}
		if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/user/positions", url.Values{"symbol": {id.Native}}, true, &positions); err != nil {
			return result, err
		}
		if positions == nil {
			return result, errors.New("BingX returned no authoritative position list")
		}
		for _, row := range positions {
			if row.Symbol != id.Native {
				continue
			}
			qty, err := row.Qty.Float64()
			observed := positionID(row.ID)
			if err != nil || math.IsNaN(qty) || math.IsInf(qty, 0) || qty != 0 && (observed == "" || observed == position) {
				return result, errors.New("BingX full-position close outcome is unresolved")
			}
		}
		return exec.PerpetualObservation{PositionID: position, PositionGone: true}, nil
	}
	var o order
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/trade/order", url.Values{"symbol": {id.Native}, "orderId": {brokerID}}, true, &o); err != nil {
		return result, err
	}
	expectedSide := "SELL"
	if req.Side == exec.SideCover {
		expectedSide = "BUY"
	}
	if observed := positionID(o.PositionID); !validID(brokerID) || o.id() != brokerID || o.Symbol != id.Native ||
		o.Side != expectedSide || o.Type != "MARKET" || observed != "" && observed != position {
		return result, errors.New("BingX full-position close order identity differs")
	}
	status, err := a.orderStatus(o.Status)
	if err != nil {
		return result, err
	}
	digits := lotDigits(req)
	qty, valid := lots(o.Quantity, digits)
	filled, validFill := lots(o.Filled, digits)
	if !valid || !validFill || qty <= 0 || filled < 0 || filled > qty || status == exec.StatusFilled && qty != filled {
		return result, fmt.Errorf("invalid %s full-position close quantity", a.env.label)
	}
	result, err = a.readFills(ctx, o, status, digits, filled, o.Time-1000, a.clock.Now().UnixMilli())
	result.PositionID, result.OrderQty = position, qty
	return result, err
}
