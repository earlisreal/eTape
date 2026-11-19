package venueprobe

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

type perpetualProbe struct {
	base, keyHeader, balancePath, modePath string
	enveloped                              bool
}

var binanceProbe = perpetualProbe{base: "https://fapi.binance.com", keyHeader: "X-MBX-APIKEY",
	balancePath: "/fapi/v3/balance", modePath: "/fapi/v1/positionSide/dual"}
var bingXProbe = perpetualProbe{base: "https://open-api.bingx.com", keyHeader: "X-BX-APIKEY",
	balancePath: "/openApi/swap/v3/user/balance", modePath: "/openApi/swap/v1/positionSide/dual", enveloped: true}

func (p perpetualProbe) inspect(ctx context.Context, clk clock.Clock, cr creds.Pair) (bool, error) {
	h := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	var balances []struct {
		Asset     string      `json:"asset"`
		Available json.Number `json:"availableBalance"`
		Margin    json.Number `json:"availableMargin"`
	}
	if err := p.read(ctx, h, clk, cr, p.balancePath, &balances); err != nil {
		return false, fmt.Errorf("read Futures balance: %w", err)
	}
	found := false
	for _, a := range balances {
		if a.Asset != "USDT" {
			continue
		}
		n := a.Available
		if p.enveloped {
			n = a.Margin
		}
		if _, err := n.Float64(); err == nil {
			found = true
		}
	}
	if !found {
		return false, errors.New("no USDT collateral account in the Futures API response")
	}
	var mode struct {
		Hedge *bool `json:"dualSidePosition"`
	}
	if err := p.read(ctx, h, clk, cr, p.modePath, &mode); err != nil {
		return false, fmt.Errorf("read Futures position mode: %w", err)
	}
	if mode.Hedge == nil {
		return false, errors.New("unknown position mode in the Futures API response")
	}
	return !*mode.Hedge, nil
}

func (p perpetualProbe) read(ctx context.Context, h *http.Client, clk clock.Clock, cr creds.Pair, path string, target any) error {
	params := url.Values{"timestamp": {strconv.FormatInt(clk.Now().UnixMilli(), 10)}, "recvWindow": {"5000"}}
	query := params.Encode()
	mac := hmac.New(sha256.New, []byte(cr.SecretKey))
	_, _ = mac.Write([]byte(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path+"?"+query+"&signature="+hex.EncodeToString(mac.Sum(nil)), nil)
	if err != nil {
		return errors.New("could not build signed request")
	}
	req.Header.Set(p.keyHeader, cr.KeyID)
	response, err := h.Do(req)
	if err != nil {
		return errors.New("failed to reach the Futures API")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("received HTTP %d from the Futures API", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if !p.enveloped {
		return decoder.Decode(target)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return err
	}
	if envelope.Code == nil {
		return errors.New("missing Futures API status")
	}
	if *envelope.Code != 0 {
		return fmt.Errorf("read rejected by Futures API (code %d)", *envelope.Code)
	}
	return json.Unmarshal(envelope.Data, target)
}
