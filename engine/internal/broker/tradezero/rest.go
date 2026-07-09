package tradezero

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// restClient is TradeZero's REST transport: order entry/cancel, account
// snapshot (equity/pnl/positions/orders), and routes. It never assumes the
// HTTP status code carries the semantic outcome of an order — TZ returns
// HTTP 200 even for a rejected order (see submitOrder) — and it treats a
// transport-level failure (no HTTP response at all) as retryable exactly
// once, using the R114 duplicate-order code on the retry as a signal that
// the original attempt actually landed.
type restClient struct {
	base      string
	accountID string
	keyID     string
	secret    string
	hc        *http.Client
	clk       clock.Clock

	// per-endpoint token buckets (TZ documented limits).
	bOrder  *netx.TokenBucket // POST /order: 10/s
	bCancel *netx.TokenBucket // DELETE /orders/{id}: 15/s
	bCanAll *netx.TokenBucket // DELETE /orders: 3/s
	bGet    *netx.TokenBucket // GET orders/order: 2/s
	bAcct   *netx.TokenBucket // GET positions/pnl/account: 3/s
	bRoutes *netx.TokenBucket // GET routes: 1/s

	mu     sync.Mutex
	routes []route // cached by fetchRoutes; read by pickRoute
}

func newRESTClient(base, accountID, keyID, secret string, clk clock.Clock) *restClient {
	return &restClient{
		base: base, accountID: accountID, keyID: keyID, secret: secret,
		hc: netx.NewHTTPClient(10 * time.Second), clk: clk,
		bOrder:  netx.NewTokenBucket(clk, 10, 10),
		bCancel: netx.NewTokenBucket(clk, 15, 15),
		bCanAll: netx.NewTokenBucket(clk, 3, 3),
		bGet:    netx.NewTokenBucket(clk, 2, 2),
		bAcct:   netx.NewTokenBucket(clk, 3, 3),
		bRoutes: netx.NewTokenBucket(clk, 1, 1),
	}
}

func (rc *restClient) do(ctx context.Context, method, path string, body io.Reader, bucket *netx.TokenBucket) (*http.Response, error) {
	if err := bucket.Take(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("TZ-API-KEY-ID", rc.keyID)
	req.Header.Set("TZ-API-SECRET-KEY", rc.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return rc.hc.Do(req)
}

type orderResp struct {
	OrderStatus string `json:"orderStatus"`
	UserOrderID string `json:"userOrderId"`
	Text        string `json:"text"`
}

// submitOrder POSTs an order. TZ returns HTTP 200 even for a semantic
// rejection, so we read orderStatus, not the HTTP code. On a transport
// failure (no HTTP response) it retries ONCE with the same client-order-id:
// an R114 (duplicate) on the retry means the original landed; a clean accept
// means it did not.
func (rc *restClient) submitOrder(ctx context.Context, req exec.OrderRequest, tzClientOrderID, route string) (bool, string, error) {
	ot, err := orderTypeWire(req.Type)
	if err != nil {
		return false, "", err
	}
	side, openClose := sideWire(req.Side)
	payload := map[string]any{
		"symbol":        wireSymbol(req.Symbol),
		"orderQuantity": int(req.Qty),
		"orderType":     ot,
		"timeInForce":   tifWire(req.TIF, extendedHoursFor(req.Session, rc.clk), req.Type),
		"securityType":  "Stock",
		"side":          side,
		"openClose":     openClose,
		"clientOrderId": tzClientOrderID,
		"route":         route,
	}
	if req.Type == exec.TypeLimit || req.Type == exec.TypeStopLimit {
		payload["limitPrice"] = req.LimitPrice
	}
	if req.Type == exec.TypeStop || req.Type == exec.TypeStopLimit {
		payload["stopPrice"] = req.StopPrice
	}
	buf, _ := json.Marshal(payload)

	attempt := func() (orderResp, bool, error) { // (parsed, transportOK, err)
		resp, err := rc.do(ctx, http.MethodPost, "/v1/api/accounts/"+rc.accountID+"/order", strings.NewReader(string(buf)), rc.bOrder)
		if err != nil {
			return orderResp{}, false, err // transport failure — no HTTP response
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusBadRequest {
			return orderResp{OrderStatus: "Rejected", Text: "HTTP 400 schema: " + string(b)}, true, nil
		}
		if resp.StatusCode >= 400 {
			// Any other >=400 (401/403/5xx, e.g. an expired key or a TZ
			// outage) is NOT a semantic order-status response — the body is
			// not trustworthy as JSON matching orderResp, and unlike the 400
			// case above, TZ does not document this as "rejection reported
			// via non-200". Treat it as a hard transport-level failure so it
			// feeds the same retry-once logic as a dropped connection,
			// instead of silently falling through to a default-accept.
			return orderResp{}, false, fmt.Errorf("tradezero: submit status=%d body=%s", resp.StatusCode, b)
		}
		var or orderResp
		_ = json.Unmarshal(b, &or)
		return or, true, nil
	}

	or, ok, _ := attempt()
	if !ok { // transport/hard-status failure -> retry once with the SAME id (R114 probe)
		var retryErr error
		or, ok, retryErr = attempt()
		if !ok {
			return false, "", fmt.Errorf("tradezero: submit transport failed twice: %w", retryErr)
		}
		if strings.Contains(or.Text, "R114") {
			// duplicate -> the ORIGINAL landed; treat as accepted. Contains
			// (not HasPrefix) so this also matches when R114 arrives via the
			// 400-schema-violation path above, where Text is rewritten to
			// "HTTP 400 schema: " + body rather than the raw R-code prefix.
			return true, "", nil
		}
	}
	if or.OrderStatus == "Rejected" {
		return false, or.Text, nil
	}
	return true, "", nil
}

// tzRestOrder decodes an order object as returned by TZ's REST endpoints
// (GET .../orders, DELETE .../orders/{id} response body, the cancel-all
// truth-poll). Field spelling differs from the Portfolio-WS shape decoded by
// tzOrder in normalize.go (clientOrderId, not userOrderId; priceStop, not
// stopPrice; and per the reconstructed OpenAPI spec, "status" or
// "orderStatus" depending on payload) hence a separate decode-tolerant type
// scoped to this file rather than overloading tzOrder's meaning.
type tzRestOrder struct {
	ClientOrderID  string  `json:"clientOrderId"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"`
	OpenClose      string  `json:"openClose"`
	OrderType      string  `json:"orderType"`
	TimeInForce    string  `json:"timeInForce"`
	OrderQuantity  float64 `json:"orderQuantity"`
	Executed       float64 `json:"executed"`
	LeavesQuantity float64 `json:"leavesQuantity"`
	LimitPrice     float64 `json:"limitPrice"`
	PriceStop      float64 `json:"priceStop"`
	PriceAvg       float64 `json:"priceAvg"`
	OrderStatus    string  `json:"orderStatus"`
	Status         string  `json:"status"`
	Text           string  `json:"text"`
	Route          string  `json:"route"`
}

func (o tzRestOrder) status() string {
	if o.OrderStatus != "" {
		return o.OrderStatus
	}
	return o.Status
}

// domain converts a REST order object to the broker-agnostic exec.Order
// shape used by Snapshot. Venue is left zero-value here; the Adapter (Task
// 10) stamps it when merging per-venue snapshots.
func (o tzRestOrder) domain() exec.Order {
	return exec.Order{
		ID:           o.ClientOrderID,
		Symbol:       domainSymbol(o.Symbol),
		Side:         sideDomain(o.Side, o.OpenClose),
		Qty:          o.OrderQuantity,
		LimitPrice:   o.LimitPrice,
		StopPrice:    o.PriceStop,
		Status:       statusDomain(o.status()),
		ExecutedQty:  o.Executed,
		LeavesQty:    o.LeavesQuantity,
		AvgFillPrice: o.PriceAvg,
		RejectReason: rejectText(o.Text),
	}
}

// isTerminal reports whether a wire order status is a terminal state (no
// longer working). It reuses statusDomain/Order.Working so PendingCancel
// (mapped to StatusSubmitted, i.e. still working — see normalize.go) is
// correctly treated as non-terminal here too.
func orderStatusIsTerminal(wireStatus string) bool {
	return !(exec.Order{Status: statusDomain(wireStatus)}).Working()
}

func (rc *restClient) listOrders(ctx context.Context) ([]tzRestOrder, error) {
	resp, err := rc.do(ctx, http.MethodGet, "/v1/api/accounts/"+rc.accountID+"/orders", nil, rc.bGet)
	if err != nil {
		return nil, fmt.Errorf("tradezero: list orders transport: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tradezero: read orders body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradezero: list orders status=%d body=%s", resp.StatusCode, b)
	}
	var orders []tzRestOrder
	if err := json.Unmarshal(b, &orders); err != nil {
		return nil, fmt.Errorf("tradezero: decode orders: %w", err)
	}
	return orders, nil
}

// cancelOrder DELETEs a working order. TZ docs: canceling immediately after
// placing can 404 because the order isn't registered yet — a 404 is
// therefore never assumed to mean "already terminal"; it is resolved by
// polling GET .../orders for the truth and, if the order is found and still
// working, retrying the cancel once now that it is registered.
func (rc *restClient) cancelOrder(ctx context.Context, tzClientOrderID string) error {
	resp, err := rc.do(ctx, http.MethodDelete, "/v1/api/accounts/"+rc.accountID+"/orders/"+tzClientOrderID, nil, rc.bCancel)
	if err != nil {
		return fmt.Errorf("tradezero: cancel transport: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return rc.resolveCancel404(ctx, tzClientOrderID)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("tradezero: cancel status=%d", resp.StatusCode)
	}
	return nil
}

// resolveCancel404 is the truth poll for a 404 on cancel. It never assumes
// the order is terminal just because the cancel 404'd.
func (rc *restClient) resolveCancel404(ctx context.Context, tzClientOrderID string) error {
	orders, err := rc.listOrders(ctx)
	if err != nil {
		return fmt.Errorf("tradezero: cancel 404 truth-poll: %w", err)
	}
	for _, o := range orders {
		if o.ClientOrderID != tzClientOrderID {
			continue
		}
		if orderStatusIsTerminal(o.status()) {
			// Already resolved terminally (filled/canceled/rejected/expired)
			// by the time we polled — nothing left to cancel.
			return nil
		}
		// Found and still working: the order only just registered (the
		// documented race). Retry the cancel now that it exists.
		resp, err := rc.do(ctx, http.MethodDelete, "/v1/api/accounts/"+rc.accountID+"/orders/"+tzClientOrderID, nil, rc.bCancel)
		if err != nil {
			return fmt.Errorf("tradezero: cancel retry transport: %w", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("tradezero: cancel 404 twice for %s", tzClientOrderID)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("tradezero: cancel retry status=%d", resp.StatusCode)
		}
		return nil
	}
	return fmt.Errorf("tradezero: cancel 404 and %s not found in orders list", tzClientOrderID)
}

// cancelAll DELETEs every open order, optionally scoped to one symbol. Rate
// limited to 3/s with no burst (bCanAll's own bucket) since it is
// account-wide and far more destructive than a single cancel.
func (rc *restClient) cancelAll(ctx context.Context, symbol string) error {
	if err := rc.bCanAll.Take(ctx); err != nil {
		return err
	}
	symbol = wireSymbol(symbol)
	path := "/v1/api/accounts/orders"
	if symbol != "" {
		path += "?" + url.Values{"symbol": {symbol}}.Encode()
	}
	form := url.Values{"account": {rc.accountID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rc.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("TZ-API-KEY-ID", rc.keyID)
	req.Header.Set("TZ-API-SECRET-KEY", rc.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := rc.hc.Do(req)
	if err != nil {
		return fmt.Errorf("tradezero: cancel-all transport: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("tradezero: cancel-all status=%d body=%s", resp.StatusCode, b)
	}
	return nil
}

type tzAccount struct {
	AccountID     string  `json:"accountId"`
	Equity        float64 `json:"equity"`
	BuyingPower   float64 `json:"buyingPower"`
	AvailableCash float64 `json:"availableCash"`
	SodEquity     float64 `json:"sodEquity"`
	Leverage      float64 `json:"leverage"`
}

// decodeAccountDetails is decode-tolerant of both a bare array (the list-all
// shape, and the shape used by the brief's testdata/accounts.json fixture)
// and a single object (the real per-account endpoint's documented shape).
func decodeAccountDetails(b []byte, accountID string) (tzAccount, bool) {
	var arr []tzAccount
	if err := json.Unmarshal(b, &arr); err == nil && len(arr) > 0 {
		for _, a := range arr {
			if a.AccountID == accountID {
				return a, true
			}
		}
		return arr[0], true
	}
	var single tzAccount
	if err := json.Unmarshal(b, &single); err == nil {
		return single, true
	}
	return tzAccount{}, false
}

type tzPnl struct {
	DayPnl      float64 `json:"dayPnl"`
	DayRealized float64 `json:"dayRealized"`
	Realized    float64 `json:"realized"`
}

func (p tzPnl) realized() float64 {
	if p.Realized != 0 {
		return p.Realized
	}
	return p.DayRealized
}

type tzPosition struct {
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Shares   float64 `json:"shares"`
	PriceAvg float64 `json:"priceAvg"`
}

// snapshot fetches account equity/buying-power (best-effort — TZ's account
// endpoint is documented to sometimes 404 or go empty during a "platform
// asleep" window, which must never be a fatal error, only a degraded
// snapshot), pnl, positions, and today's orders. Venue is left zero-value on
// every returned struct; the Adapter (Task 10) stamps it.
func (rc *restClient) snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	var acct exec.AccountSnapshot

	if resp, err := rc.do(ctx, http.MethodGet, "/v1/api/account/"+rc.accountID, nil, rc.bAcct); err == nil {
		b, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr == nil && resp.StatusCode == http.StatusOK {
			if ad, ok := decodeAccountDetails(b, rc.accountID); ok {
				acct.Equity = ad.Equity
				acct.BuyingPower = ad.BuyingPower
				acct.AvailableCash = ad.AvailableCash
				acct.SodEquity = ad.SodEquity
				acct.Leverage = ad.Leverage
			}
		}
	}

	pnlResp, err := rc.do(ctx, http.MethodGet, "/v1/api/accounts/"+rc.accountID+"/pnl", nil, rc.bAcct)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: fetch pnl: %w", err)
	}
	pnlBody, err := io.ReadAll(pnlResp.Body)
	_ = pnlResp.Body.Close()
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: read pnl: %w", err)
	}
	if pnlResp.StatusCode >= 400 {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: pnl status=%d body=%s", pnlResp.StatusCode, pnlBody)
	}
	var pnl tzPnl
	if err := json.Unmarshal(pnlBody, &pnl); err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: decode pnl: %w", err)
	}
	acct.DayPnL = pnl.DayPnl
	acct.Realized = pnl.realized()
	acct.TsMs = rc.clk.Now().UnixMilli()

	posResp, err := rc.do(ctx, http.MethodGet, "/v1/api/accounts/"+rc.accountID+"/positions", nil, rc.bAcct)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: fetch positions: %w", err)
	}
	posBody, err := io.ReadAll(posResp.Body)
	_ = posResp.Body.Close()
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: read positions: %w", err)
	}
	if posResp.StatusCode >= 400 {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: positions status=%d body=%s", posResp.StatusCode, posBody)
	}
	var tzPositions []tzPosition
	if err := json.Unmarshal(posBody, &tzPositions); err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: decode positions: %w", err)
	}
	positions := make([]exec.Position, 0, len(tzPositions))
	for _, p := range tzPositions {
		qty := p.Shares
		if p.Side == "Short" {
			qty = -qty
		}
		positions = append(positions, exec.Position{Symbol: domainSymbol(p.Symbol), Qty: qty, AvgPrice: p.PriceAvg})
	}

	tzOrders, err := rc.listOrders(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("tradezero: fetch orders: %w", err)
	}
	orders := make([]exec.Order, 0, len(tzOrders))
	for _, o := range tzOrders {
		orders = append(orders, o.domain())
	}

	return acct, positions, orders, nil
}

// route is one entry of TZ's routes list. Field spelling is decode-tolerant
// of both "route" (the brief's testdata/routes.json fixture) and
// "routeName" (the reconstructed OpenAPI's documented field).
type route struct {
	Route         string   `json:"route"`
	RouteName     string   `json:"routeName"`
	SecurityTypes []string `json:"securityTypes"`
	OrderTypes    []string `json:"orderTypes"`
	TimesInForce  []string `json:"timesInForce"`
}

func (r route) name() string {
	if r.Route != "" {
		return r.Route
	}
	return r.RouteName
}

// defaultRoute is eTape's configured default route preference; pickRoute
// validates it actually exists for the requested security type before
// using it (paper accounts auto-assign a route regardless).
const defaultRoute = "SMART"

func (rc *restClient) fetchRoutes(ctx context.Context) ([]route, error) {
	resp, err := rc.do(ctx, http.MethodGet, "/v1/api/accounts/"+rc.accountID+"/routes", nil, rc.bRoutes)
	if err != nil {
		return nil, fmt.Errorf("tradezero: fetch routes transport: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tradezero: read routes body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradezero: routes status=%d body=%s", resp.StatusCode, b)
	}
	var routes []route
	if err := json.Unmarshal(b, &routes); err != nil {
		var wrapped struct {
			Routes []route `json:"routes"`
		}
		if err2 := json.Unmarshal(b, &wrapped); err2 != nil {
			return nil, fmt.Errorf("tradezero: decode routes: %w", err)
		}
		routes = wrapped.Routes
	}
	rc.mu.Lock()
	rc.routes = routes
	rc.mu.Unlock()
	return routes, nil
}

// pickRoute returns defaultRoute if it is valid for secType among the
// routes last fetched by fetchRoutes, else the first route that is valid for
// secType, else "" if fetchRoutes has not been called or none match.
func (rc *restClient) pickRoute(secType string) string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	fallback := ""
	for _, r := range rc.routes {
		if !routeSupports(r, secType) {
			continue
		}
		if fallback == "" {
			fallback = r.name()
		}
		if r.name() == defaultRoute {
			return defaultRoute
		}
	}
	return fallback
}

func routeSupports(r route, secType string) bool {
	for _, st := range r.SecurityTypes {
		if strings.EqualFold(st, secType) {
			return true
		}
	}
	return false
}
