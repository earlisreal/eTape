// Package mexc owns the authenticated MEXC Futures API boundary.
package mexc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

type client struct {
	base  string
	http  *http.Client
	clock clock.Clock
	creds creds.Pair
}

type accountAsset struct {
	Currency  string      `json:"currency"`
	Available json.Number `json:"availableBalance"`
}

type AccountAccess struct {
	OneWay        bool
	OpenPositions int
	WorkingOrders int
	ActiveTPSL    int
}

// VerifyAccount reads Futures collateral, position mode and active account state.
// Successful reads establish account access, independently of order rights.
func VerifyAccount(ctx context.Context, cr creds.Pair, clk clock.Clock) (AccountAccess, error) {
	c := client{base: "https://api.mexc.com", clock: clk, creds: cr, http: &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	return c.verifyAccount(ctx)
}

func (c client) verifyAccount(ctx context.Context) (AccountAccess, error) {
	var assets []accountAsset
	if err := c.get(ctx, "/api/v1/private/account/assets", nil, &assets); err != nil {
		return AccountAccess{}, err
	}
	hasUSDT := false
	for _, a := range assets {
		if a.Currency == "USDT" {
			_, err := a.Available.Float64()
			hasUSDT = err == nil
			break
		}
	}
	if !hasUSDT {
		return AccountAccess{}, errors.New("MEXC Futures returned no USDT collateral account")
	}
	var mode int
	if err := c.get(ctx, "/api/v1/private/position/position_mode", nil, &mode); err != nil {
		return AccountAccess{}, err
	}
	if mode != 1 && mode != 2 {
		return AccountAccess{}, errors.New("MEXC Futures returned an unknown position mode")
	}
	var positions []struct {
		HoldVolume json.Number `json:"holdVol"`
	}
	if err := c.get(ctx, "/api/v1/private/position/open_positions", nil, &positions); err != nil {
		return AccountAccess{}, fmt.Errorf("read open positions: %w", err)
	}
	var stops []struct {
		State      int `json:"state"`
		IsFinished int `json:"isFinished"`
	}
	if err := c.get(ctx, "/api/v1/private/stoporder/open_orders", nil, &stops); err != nil {
		return AccountAccess{}, fmt.Errorf("read TP/SL orders: %w", err)
	}
	count, err := c.workingOrders(ctx)
	if err != nil {
		return AccountAccess{}, err
	}
	result := AccountAccess{OneWay: mode == 2, WorkingOrders: count}
	for _, p := range positions {
		qty, err := p.HoldVolume.Float64()
		if err != nil || qty < 0 {
			return AccountAccess{}, errors.New("MEXC Futures returned an invalid position quantity")
		}
		if qty > 0 {
			result.OpenPositions++
		}
	}
	for _, stop := range stops {
		if stop.State == 1 && stop.IsFinished == 0 {
			result.ActiveTPSL++
		}
	}
	return result, nil
}

func (c client) workingOrders(ctx context.Context) (int, error) {
	const pageSize = 100
	seen := make(map[json.Number]struct{})
	for page := 1; ; page++ {
		params := url.Values{"page_num": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
		var orders []struct {
			ID    json.Number `json:"orderId"`
			State int         `json:"state"`
		}
		if err := c.get(ctx, "/api/v1/private/order/list/open_orders", params, &orders); err != nil {
			return 0, fmt.Errorf("read current orders: %w", err)
		}
		for _, order := range orders {
			if order.State != 1 && order.State != 2 {
				continue
			}
			if order.ID == "" {
				return 0, errors.New("MEXC Futures returned a working order without an ID")
			}
			if _, dup := seen[order.ID]; dup {
				return 0, errors.New("MEXC Futures current orders changed during the check; retry")
			}
			seen[order.ID] = struct{}{}
		}
		if len(orders) < pageSize {
			return len(seen), nil
		}
	}
}

func (c client) get(ctx context.Context, path string, params url.Values, target any) error {
	if c.creds.KeyID == "" || c.creds.SecretKey == "" {
		return errors.New("MEXC Futures credentials are required")
	}
	query := params.Encode()
	url := c.base + path
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errors.New("could not build MEXC Futures request")
	}
	now := strconv.FormatInt(c.clock.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(c.creds.SecretKey))
	_, _ = mac.Write([]byte(c.creds.KeyID + now + query))
	req.Header.Set("ApiKey", c.creds.KeyID)
	req.Header.Set("Request-Time", now)
	req.Header.Set("Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("MEXC Futures connection failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("MEXC Futures returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return errors.New("invalid MEXC Futures response")
	}
	if !envelope.Success || envelope.Code != 0 {
		return fmt.Errorf("MEXC Futures rejected the account read (code %d)", envelope.Code)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return errors.New("invalid MEXC Futures account data")
	}
	return nil
}
