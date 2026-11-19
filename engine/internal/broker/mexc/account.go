// Package mexc owns the authenticated MEXC Futures API boundary.
package mexc

import (
	"bytes"
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
	Equity    json.Number `json:"equity"`
	Margin    json.Number `json:"positionMargin"`
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
	return newClient(cr, clk).verifyAccount(ctx)
}

func newClient(cr creds.Pair, clk clock.Clock) client {
	return client{base: "https://api.mexc.com", clock: clk, creds: cr, http: &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c client) verifyAccount(ctx context.Context) (AccountAccess, error) {
	s, err := c.snapshot(ctx)
	if err != nil {
		return AccountAccess{}, err
	}
	result := AccountAccess{OneWay: s.Mode == oneWay, WorkingOrders: len(s.Orders)}
	for _, p := range s.Positions {
		qty, err := p.Volume.Float64()
		if err != nil || qty < 0 {
			return AccountAccess{}, errors.New("MEXC Futures returned an invalid position quantity")
		}
		if qty > 0 {
			result.OpenPositions++
		}
	}
	for _, stop := range s.Stops {
		if stop.active() {
			result.ActiveTPSL++
		}
	}
	return result, nil
}

const oneWay = 2

type accountState struct {
	Balance   accountAsset
	Mode      int
	Positions []position
	Orders    []venueOrder
	Stops     []positionStop
}

func (c client) snapshot(ctx context.Context) (accountState, error) {
	var assets []accountAsset
	if err := c.get(ctx, "/api/v1/private/account/assets", nil, &assets); err != nil {
		return accountState{}, err
	}
	var s accountState
	for _, a := range assets {
		if a.Currency == "USDT" {
			s.Balance = a
			break
		}
	}
	if _, err := s.Balance.Available.Float64(); err != nil {
		return accountState{}, errors.New("MEXC Futures returned no USDT collateral account")
	}
	if err := c.get(ctx, "/api/v1/private/position/position_mode", nil, &s.Mode); err != nil {
		return accountState{}, err
	}
	if s.Mode != 1 && s.Mode != oneWay {
		return accountState{}, errors.New("MEXC Futures returned an unknown position mode")
	}
	if err := c.get(ctx, "/api/v1/private/position/open_positions", nil, &s.Positions); err != nil {
		return accountState{}, fmt.Errorf("read open positions: %w", err)
	}
	if err := c.get(ctx, "/api/v1/private/stoporder/open_orders", nil, &s.Stops); err != nil {
		return accountState{}, fmt.Errorf("read TP/SL orders: %w", err)
	}
	orders, err := c.workingOrders(ctx)
	if err != nil {
		return accountState{}, err
	}
	s.Orders = orders
	return s, nil
}

func (c client) workingOrders(ctx context.Context) ([]venueOrder, error) {
	const pageSize = 100
	seen := make(map[json.Number]struct{})
	var working []venueOrder
	for page := 1; page <= 100; page++ {
		params := url.Values{"page_num": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
		var orders []venueOrder
		if err := c.get(ctx, "/api/v1/private/order/list/open_orders", params, &orders); err != nil {
			return nil, fmt.Errorf("read current orders: %w", err)
		}
		for _, order := range orders {
			if order.State != 1 && order.State != 2 {
				continue
			}
			if order.ID == "" {
				return nil, errors.New("MEXC Futures returned a working order without an ID")
			}
			if _, dup := seen[order.ID]; dup {
				return nil, errors.New("MEXC Futures current orders changed during the check; retry")
			}
			seen[order.ID] = struct{}{}
			working = append(working, order)
		}
		if len(orders) < pageSize {
			return working, nil
		}
	}
	return nil, errors.New("MEXC Futures returned too many working order pages")
}

func (c client) get(ctx context.Context, path string, params url.Values, target any) error {
	return c.request(ctx, http.MethodGet, path, params, nil, target)
}

func (c client) post(ctx context.Context, path string, body, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode MEXC Futures request: %w", err)
	}
	return c.request(ctx, http.MethodPost, path, nil, payload, target)
}

type apiError struct{ Code int }

func (e apiError) Error() string {
	return fmt.Sprintf("MEXC Futures rejected the request (code %d)", e.Code)
}

func (c client) request(ctx context.Context, method, path string, params url.Values, payload []byte, target any) error {
	if c.creds.KeyID == "" || c.creds.SecretKey == "" {
		return errors.New("MEXC Futures credentials are required")
	}
	query := params.Encode()
	signed := query
	url := c.base + path
	if query != "" {
		url += "?" + query
	}
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader(payload)
		signed = string(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return errors.New("could not build MEXC Futures request")
	}
	now := strconv.FormatInt(c.clock.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(c.creds.SecretKey))
	_, _ = mac.Write([]byte(c.creds.KeyID + now + signed))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
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
		return apiError{Code: envelope.Code}
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return errors.New("invalid MEXC Futures data")
	}
	return nil
}
