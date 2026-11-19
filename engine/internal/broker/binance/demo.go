// Package binance connects to Binance USDⓈ-M Futures Demo.
package binance

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
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/linearperp"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type Demo struct {
	*perps.Adapter
	api *demoAPI
}

type demoCatalog interface {
	Refresh(context.Context) error
	DemoRules(string) (linearperp.DemoRules, bool)
}

type demoAPI struct {
	clock   clock.Clock
	creds   creds.Pair
	http    *http.Client
	catalog demoCatalog
	quotes  sync.Map
}

func NewDemo(venue exec.VenueID, pair creds.Pair, clk clock.Clock, path string, catalog *linearperp.Client) (*Demo, error) {
	if pair.KeyID == "" || pair.SecretKey == "" || catalog == nil {
		return nil, errors.New("missing API credentials or market feed for Binance Futures Demo")
	}
	api := newDemoAPI(pair, clk)
	api.catalog = catalog
	adapter, err := perps.New(venue, clk, path, "binance-demo:"+pair.KeyID, api)
	if err != nil {
		return nil, err
	}
	return &Demo{Adapter: adapter, api: api}, nil
}

func newDemoAPI(pair creds.Pair, clk clock.Clock) *demoAPI {
	return &demoAPI{clock: clk, creds: pair, http: &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func VerifyDemo(ctx context.Context, pair creds.Pair, clk clock.Clock) error {
	api := newDemoAPI(pair, clk)
	_, err := api.account(ctx)
	return err
}

func (d *Demo) SetFairQuote(q feed.Quote) {
	if id, ok := instrument.ParsePerpetual(q.Symbol); ok && id.Exchange == instrument.BinanceDemo {
		d.api.quotes.Store(q.Symbol, q)
	}
}

func (a *demoAPI) Rules(symbol string) (instrument.ContractRules, bool) {
	terms, ok := a.catalog.DemoRules(symbol)
	return terms.ContractRules, ok
}

func (a *demoAPI) FairPrice(symbol string) (float64, bool) {
	value, ok := a.quotes.Load(symbol)
	if !ok {
		return 0, false
	}
	q := value.(feed.Quote)
	age := a.clock.Now().Sub(time.UnixMilli(q.TsMs))
	return q.Mark, q.Mark > 0 && age >= -2*time.Second && age < 5*time.Second
}

type apiError int

func (e apiError) Error() string { return fmt.Sprintf("Binance Futures Demo API code %d", e) }

func (a *demoAPI) request(ctx context.Context, method, path string, params url.Values, out any) error {
	if params == nil {
		params = make(url.Values)
	}
	params.Set("timestamp", strconv.FormatInt(a.clock.Now().UnixMilli(), 10))
	params.Set("recvWindow", "5000")
	query := params.Encode()
	mac := hmac.New(sha256.New, []byte(a.creds.SecretKey))
	_, _ = mac.Write([]byte(query))
	target := linearperp.BinanceDemoREST + "/fapi/" + path + "?" + query + "&signature=" + hex.EncodeToString(mac.Sum(nil))
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return errors.New("could not build Binance Futures Demo request")
	}
	req.Header.Set("X-MBX-APIKEY", a.creds.KeyID)
	res, err := a.http.Do(req)
	if err != nil {
		return errors.New("could not connect to Binance Futures Demo")
	}
	defer res.Body.Close()
	const maxResponse = 4 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return errors.New("could not read Binance Futures Demo response")
	}
	if res.StatusCode < 500 {
		var failure struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Code < 0 {
			return apiError(failure.Code)
		}
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Binance Futures Demo HTTP %d", res.StatusCode)
	}
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) || json.Unmarshal(body, out) != nil {
		return errors.New("invalid Binance Futures Demo response")
	}
	return nil
}

func decimal(value json.Number) (float64, bool) {
	n, err := value.Float64()
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func demoSymbol(native string) string {
	return instrument.Perpetual{Exchange: instrument.BinanceDemo, Native: native}.String()
}
