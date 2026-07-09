package alpaca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// restClient is Alpaca's REST transport: order entry/replace/cancel, kill
// switches (cancel-all, flatten), and account snapshot. Unlike TradeZero,
// Alpaca returns proper HTTP status codes with structured JSON errors
// (`{"code":...,"message":...}`) — there is no "HTTP 200 but rejected"
// trap to work around, so every method here treats any HTTP status >= 400
// as a hard error (parsing the structured body when present, but NEVER
// falling through to a default-success return when the body doesn't match
// that shape). Rate limiting is a single pooled 200/min bucket shared by
// every endpoint (Alpaca docs: "pooled across all endpoints"), unlike
// TradeZero's per-endpoint buckets.
type restClient struct {
	base   string
	keyID  string
	secret string
	hc     *http.Client
	clk    clock.Clock

	bucket *netx.TokenBucket // single pooled 200/min (~3.33/s) bucket, burst 5
}

func newRESTClient(base, keyID, secret string, clk clock.Clock) *restClient {
	return &restClient{
		base: base, keyID: keyID, secret: secret,
		hc:     netx.NewHTTPClient(10 * time.Second),
		clk:    clk,
		bucket: netx.NewTokenBucket(clk, 200.0/60.0, 5),
	}
}

// do takes one token from the shared pooled bucket, then issues the request
// with Alpaca's key/secret headers. Every restClient method funnels through
// here so no endpoint can bypass the pooled limit.
func (rc *restClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if err := rc.bucket.Take(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APCA-API-KEY-ID", rc.keyID)
	req.Header.Set("APCA-API-SECRET-KEY", rc.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return rc.hc.Do(req)
}

// alpacaError is Alpaca's structured error body on >=400 responses:
// {"code": 42210000, "message": "sub-penny increment"}.
type alpacaError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// apiError turns a >=400 HTTP response into a Go error. It tries to decode
// the structured {code,message} shape Alpaca documents, but an unparseable
// or differently-shaped body (an HTML error page from a proxy outage, an
// empty 503 body, an auth gateway's own JSON shape) still produces a real
// error carrying the raw body — never nil, and never a value that could be
// mistaken for success by a caller that forgets to check it.
func apiError(status int, body []byte) error {
	var ae alpacaError
	if err := json.Unmarshal(body, &ae); err == nil && ae.Message != "" {
		return fmt.Errorf("alpaca: status=%d code=%d message=%s", status, ae.Code, ae.Message)
	}
	return fmt.Errorf("alpaca: status=%d body=%s", status, body)
}

// submitOrder POSTs an order and returns Alpaca's broker-assigned order id.
// clientOrderID is the domain id echoed back on every later trade_updates
// event (Task 12's normalizeUpdate keys off it). limit_price/stop_price are
// only sent for the order types that need them, rounded via Task 11's
// roundPrice (Alpaca rejects sub-penny increments with a structured 422).
// extended_hours is set for day/gtc limit orders submitted while rc.clk reads
// pre-market, post-market, or overnight (isExtendedHours) — Alpaca requires
// the flag to work the order immediately in those sessions rather than
// queuing it for the next RTH open; it is omitted (defaulting to false) for
// every other order type/session combination since Alpaca rejects the flag
// on market/stop/stop-limit orders.
//
// A >=400 response is ALWAYS an error — parsed via apiError — and this
// never falls through to a default-accept on a response it can't parse: a
// 200 that doesn't even decode an order id is treated as an error too.
func (rc *restClient) submitOrder(ctx context.Context, req exec.OrderRequest, clientOrderID string) (string, error) {
	ot, err := orderTypeWire(req.Type)
	if err != nil {
		return "", err
	}
	tif, err := tifWire(req.TIF)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"symbol":          req.Symbol,
		"qty":             req.Qty,
		"side":            sideWire(req.Side),
		"type":            ot,
		"time_in_force":   tif,
		"client_order_id": clientOrderID,
	}
	if req.Type == exec.TypeLimit || req.Type == exec.TypeStopLimit {
		payload["limit_price"] = roundPrice(req.LimitPrice)
	}
	if req.Type == exec.TypeStop || req.Type == exec.TypeStopLimit {
		payload["stop_price"] = roundPrice(req.StopPrice)
	}
	// extended_hours is only valid for limit day/gtc orders (Alpaca rejects it
	// on market/stop/stop-limit orders); omit the key otherwise so it defaults
	// to Alpaca's false rather than risk a rejection.
	if req.Type == exec.TypeLimit && (req.TIF == exec.TIFDay || req.TIF == exec.TIFGTC) && isExtendedHours(rc.clk) {
		payload["extended_hours"] = true
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("alpaca: marshal order: %w", err)
	}

	resp, err := rc.do(ctx, http.MethodPost, "/v2/orders", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("alpaca: submit transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("alpaca: read submit response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", apiError(resp.StatusCode, body)
	}
	var ord auOrder
	if err := json.Unmarshal(body, &ord); err != nil {
		return "", fmt.Errorf("alpaca: decode submit response: %w", err)
	}
	if ord.ID == "" {
		return "", fmt.Errorf("alpaca: submit response missing order id: %s", body)
	}
	return ord.ID, nil
}

// replaceOrder PATCHes qty/limit/stop — Alpaca's native replace, unlike
// TradeZero's cancel-then-re-place emulation. Only non-zero fields of rr are
// sent so an unset field is left as-is on Alpaca's side rather than being
// coerced to zero.
//
// clientOrderID is the domain order's ORIGINAL client_order_id, explicitly
// re-sent in the PATCH body. Alpaca's documented replace behavior is to
// auto-generate a brand-new client_order_id for the replaced order when this
// field is left out of the request — which would silently break every piece
// of this adapter's bookkeeping (brokerIDByClientID, the WS "replaced" event
// correlation, reconcile) that assumes client_order_id never changes across
// a replace (see the package doc). Sending it back unchanged is what actually
// keeps that assumption true.
func (rc *restClient) replaceOrder(ctx context.Context, brokerID, clientOrderID string, rr exec.ReplaceRequest) error {
	payload := map[string]any{
		"client_order_id": clientOrderID,
	}
	if rr.Qty > 0 {
		payload["qty"] = rr.Qty
	}
	if rr.LimitPrice > 0 {
		payload["limit_price"] = roundPrice(rr.LimitPrice)
	}
	if rr.StopPrice > 0 {
		payload["stop_price"] = roundPrice(rr.StopPrice)
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("alpaca: marshal replace: %w", err)
	}
	resp, err := rc.do(ctx, http.MethodPatch, "/v2/orders/"+brokerID, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("alpaca: replace transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("alpaca: read replace response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiError(resp.StatusCode, body)
	}
	return nil
}

// cancelOrder DELETEs a single working order by Alpaca's broker order id.
func (rc *restClient) cancelOrder(ctx context.Context, brokerID string) error {
	resp, err := rc.do(ctx, http.MethodDelete, "/v2/orders/"+brokerID, nil)
	if err != nil {
		return fmt.Errorf("alpaca: cancel transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("alpaca: read cancel response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiError(resp.StatusCode, body)
	}
	return nil
}

// alpacaBatchItem is one entry in the per-item result array Alpaca returns
// from its batch DELETE endpoints (account-wide `DELETE /v2/orders` and
// `DELETE /v2/positions`). These endpoints answer HTTP 207 Multi-Status on a
// partial batch failure: the OUTER status stays below 400, but each item
// carries its own status (e.g. `{"id":"...","status":500,"body":{...}}` for
// orders, `{"symbol":"...","status":422,"body":{...}}` for positions), so a
// caller that only checks the outer status can silently miss a failed
// cancel/close.
//
// Status is a pointer rather than a plain int: a plain int's zero value
// (0) is indistinguishable from an absent or JSON-null status key, and 0 is
// NOT >= 400, so a per-item field silently missing from the response (a
// plausible API-shape change, stripping proxy, or partial-response bug on
// Alpaca's side that still produces syntactically valid JSON) would
// previously decode cleanly and be treated as a genuine success. A nil
// pointer means "presence unconfirmed" and checkBatchItems treats that as a
// hard failure rather than a pass-through.
type alpacaBatchItem struct {
	ID     string          `json:"id,omitempty"`
	Symbol string          `json:"symbol,omitempty"`
	Status *int            `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// checkBatchItems inspects a batch-DELETE response body (already confirmed
// to have an outer status < 400) for per-item failures. It decodes the body
// as a JSON array of alpacaBatchItem and joins an error for every item whose
// own status is >= 400, mirroring the errors.Join pattern the symbol-scoped
// cancelAll path already uses per-order.
//
// Only a genuinely empty body -- no bytes, whitespace only, the literal `[]`,
// or the literal `null` -- is treated as success without further inspection:
// that is Alpaca's documented "nothing to cancel/flatten" response for these
// two endpoints. Anything else that fails to decode as []alpacaBatchItem
// (a truncated array, a different envelope shape such as
// `{"orders":[...]}`, a per-item field type drift, etc.) is a REAL error and
// must fail closed rather than silently reporting success -- a body we
// cannot understand may well contain a failed cancel/close we then hide.
func checkBatchItems(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var items []alpacaBatchItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		const maxSnippet = 500
		snippet := trimmed
		suffix := ""
		if len(snippet) > maxSnippet {
			snippet = snippet[:maxSnippet]
			suffix = "...(truncated)"
		}
		return fmt.Errorf("alpaca: batch response body is neither empty nor a decodable item array: %w (body=%s%s)", err, snippet, suffix)
	}

	var errs []error
	for _, it := range items {
		label := it.ID
		if label == "" {
			label = it.Symbol
		}
		if it.Status == nil {
			errs = append(errs, fmt.Errorf("item %s: status field missing or null -- cannot confirm success, failing closed (body=%s)", label, it.Body))
			continue
		}
		if *it.Status >= 400 {
			errs = append(errs, fmt.Errorf("item %s: status=%d body=%s", label, *it.Status, it.Body))
		}
	}
	return errors.Join(errs...)
}

// cancelAll cancels every open order. With no symbol it is a single
// account-wide `DELETE /v2/orders` (Alpaca's native cancel-all has no
// symbol filter); Alpaca answers this with HTTP 207 on a partial failure, so
// the per-item array is inspected via checkBatchItems rather than trusting a
// <400 outer status alone. With a symbol it lists open orders scoped to that
// symbol (`GET /v2/orders?status=open&symbols=...`) and cancels each
// individually, joining any per-order failures rather than stopping at the
// first one.
func (rc *restClient) cancelAll(ctx context.Context, symbol string) error {
	if symbol == "" {
		resp, err := rc.do(ctx, http.MethodDelete, "/v2/orders", nil)
		if err != nil {
			return fmt.Errorf("alpaca: cancel-all transport: %w", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("alpaca: read cancel-all response: %w", err)
		}
		if resp.StatusCode >= 400 {
			return apiError(resp.StatusCode, body)
		}
		return checkBatchItems(body)
	}

	q := url.Values{"status": {"open"}, "symbols": {symbol}}
	resp, err := rc.do(ctx, http.MethodGet, "/v2/orders?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("alpaca: list open orders transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("alpaca: read open orders: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiError(resp.StatusCode, body)
	}
	var orders []auOrder
	if err := json.Unmarshal(body, &orders); err != nil {
		return fmt.Errorf("alpaca: decode open orders: %w", err)
	}
	var errs []error
	for _, o := range orders {
		if err := rc.cancelOrder(ctx, o.ID); err != nil {
			errs = append(errs, fmt.Errorf("cancel %s: %w", o.ID, err))
		}
	}
	return errors.Join(errs...)
}

// flatten DELETEs every position (`DELETE /v2/positions`) — Alpaca's native
// flatten-all, which TradeZero has no equivalent for at all. This is eTape's
// documented emergency kill-switch, so a partial-failure 207 (some positions
// closed, some not) must never be reported as a clean nil the way a plain
// outer-status check would: the per-item array is always inspected via
// checkBatchItems.
func (rc *restClient) flatten(ctx context.Context) error {
	resp, err := rc.do(ctx, http.MethodDelete, "/v2/positions", nil)
	if err != nil {
		return fmt.Errorf("alpaca: flatten transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("alpaca: read flatten response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiError(resp.StatusCode, body)
	}
	return checkBatchItems(body)
}

// alpacaAccount is GET /v2/account's response shape. Every numeric field
// arrives as a JSON string (Alpaca convention), hence numString. last_equity
// is Alpaca's documented Account field name for prior-close equity (per
// Alpaca's public Trading API reference — NOT independently re-verified
// against a live paper /v2/account response in this task; the sandbox
// denied reading ~/.eTape/credentials.json to do so, see task-13-report.md
// concerns). DayPnL = equity - last_equity.
type alpacaAccount struct {
	Equity      numString `json:"equity"`
	LastEquity  numString `json:"last_equity"`
	BuyingPower numString `json:"buying_power"`
	Cash        numString `json:"cash"`
	Multiplier  numString `json:"multiplier"`
}

// alpacaPosition is one GET /v2/positions entry. qty is signed for shorts on
// a real account, but this also tolerates the (undocumented / defensive)
// case of a positive qty paired with side:"short" by negating it — belt and
// suspenders against either wire convention.
type alpacaPosition struct {
	Symbol        string    `json:"symbol"`
	Qty           numString `json:"qty"`
	Side          string    `json:"side"`
	AvgEntryPrice numString `json:"avg_entry_price"`
}

func positionQtyDomain(p alpacaPosition) float64 {
	qty := float64(p.Qty)
	if p.Side == "short" && qty > 0 {
		return -qty
	}
	return qty
}

// orderTypeDomain reverses orderTypeWire for decoding REST order objects.
func orderTypeDomain(s string) exec.OrderType {
	switch s {
	case "limit":
		return exec.TypeLimit
	case "stop":
		return exec.TypeStop
	case "stop_limit":
		return exec.TypeStopLimit
	default:
		return exec.TypeMarket
	}
}

// restOrderStatusDomain maps a REST/trade_updates order status string to the
// domain OrderStatus, for the resting-order list snapshot returns (distinct
// from normalizeUpdate's event-based switch, which drives fill/cancel/etc.
// logic off the trade_updates event type rather than the order's own status
// field).
func restOrderStatusDomain(s string) exec.OrderStatus {
	switch s {
	case "new", "accepted", "pending_new", "accepted_for_bidding":
		return exec.StatusAccepted
	case "partially_filled":
		return exec.StatusPartiallyFilled
	case "filled":
		return exec.StatusFilled
	case "canceled", "pending_cancel":
		return exec.StatusCanceled
	case "rejected":
		return exec.StatusRejected
	case "expired", "done_for_day", "stopped", "suspended", "calculated":
		return exec.StatusExpired
	case "replaced", "pending_replace":
		return exec.StatusReplaced
	default:
		return exec.StatusSubmitted
	}
}

// restOrderSideDomain is a context-free side mapping for a resting order
// listed by snapshot: Alpaca's order object only carries "buy"/"sell", with
// no position-before context to distinguish Buy-from-flat vs Cover-from-short
// (unlike a fill event, which normalizeUpdate resolves via sideDomain in
// mapping.go). Good enough for the read-only order-list display; the
// Buy/Cover and Sell/Short distinction is only load-bearing on fills.
func restOrderSideDomain(wireSide string) exec.Side {
	if wireSide == "buy" {
		return exec.SideBuy
	}
	return exec.SideSell
}

// domain converts a REST-decoded auOrder into the broker-agnostic exec.Order
// shape used by snapshot. Venue is left zero-value here; the Task 15
// Adapter stamps it, mirroring tzRestOrder.domain() in the tradezero
// package.
func (o auOrder) domain() exec.Order {
	return exec.Order{
		ID:           o.ClientOrderID,
		Symbol:       o.Symbol,
		Side:         restOrderSideDomain(o.Side),
		Type:         orderTypeDomain(o.OrderType),
		Qty:          float64(o.Qty),
		LimitPrice:   float64(o.LimitPrice),
		StopPrice:    float64(o.StopPrice),
		Status:       restOrderStatusDomain(o.Status),
		ExecutedQty:  float64(o.FilledQty),
		LeavesQty:    float64(o.Qty) - float64(o.FilledQty),
		AvgFillPrice: float64(o.FilledAvgPrice),
	}
}

// snapshot fetches account equity/buying-power/day-P&L, open positions, and
// working orders. Unlike TradeZero's snapshot, a failure on ANY of the three
// calls fails the whole snapshot — Alpaca's /v2/account is not documented to
// have TZ's "platform asleep" degraded-empty-response behavior, so masking
// an account-fetch error here would silently hide a real auth/outage
// problem rather than surface it.
func (rc *restClient) snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	var acct exec.AccountSnapshot

	acctResp, err := rc.do(ctx, http.MethodGet, "/v2/account", nil)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: fetch account transport: %w", err)
	}
	acctBody, err := io.ReadAll(acctResp.Body)
	_ = acctResp.Body.Close()
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: read account: %w", err)
	}
	if acctResp.StatusCode >= 400 {
		return exec.AccountSnapshot{}, nil, nil, apiError(acctResp.StatusCode, acctBody)
	}
	var aa alpacaAccount
	if err := json.Unmarshal(acctBody, &aa); err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: decode account: %w", err)
	}
	acct.Equity = float64(aa.Equity)
	acct.BuyingPower = float64(aa.BuyingPower)
	acct.AvailableCash = float64(aa.Cash)
	acct.SodEquity = float64(aa.LastEquity)
	acct.Leverage = float64(aa.Multiplier)
	acct.DayPnL = float64(aa.Equity) - float64(aa.LastEquity)
	acct.TsMs = rc.clk.Now().UnixMilli()

	posResp, err := rc.do(ctx, http.MethodGet, "/v2/positions", nil)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: fetch positions transport: %w", err)
	}
	posBody, err := io.ReadAll(posResp.Body)
	_ = posResp.Body.Close()
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: read positions: %w", err)
	}
	if posResp.StatusCode >= 400 {
		return exec.AccountSnapshot{}, nil, nil, apiError(posResp.StatusCode, posBody)
	}
	var aps []alpacaPosition
	if err := json.Unmarshal(posBody, &aps); err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: decode positions: %w", err)
	}
	positions := make([]exec.Position, 0, len(aps))
	for _, p := range aps {
		positions = append(positions, exec.Position{Symbol: p.Symbol, Qty: positionQtyDomain(p), AvgPrice: float64(p.AvgEntryPrice)})
	}

	ordResp, err := rc.do(ctx, http.MethodGet, "/v2/orders?status=open", nil)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: fetch orders transport: %w", err)
	}
	ordBody, err := io.ReadAll(ordResp.Body)
	_ = ordResp.Body.Close()
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: read orders: %w", err)
	}
	if ordResp.StatusCode >= 400 {
		return exec.AccountSnapshot{}, nil, nil, apiError(ordResp.StatusCode, ordBody)
	}
	var aos []auOrder
	if err := json.Unmarshal(ordBody, &aos); err != nil {
		return exec.AccountSnapshot{}, nil, nil, fmt.Errorf("alpaca: decode orders: %w", err)
	}
	orders := make([]exec.Order, 0, len(aos))
	for _, o := range aos {
		orders = append(orders, o.domain())
	}

	return acct, positions, orders, nil
}

// orderByClientID resolves the ambiguity left by a transport failure on
// submitOrder (no HTTP response at all — did the order land or not?) by
// asking Alpaca directly whether an order with this client_order_id exists:
// Alpaca's answer to TradeZero's retry-once-R114 probe. A 404 is a normal,
// non-error "does not exist" result (found=false); any other >=400 is a
// real error.
func (rc *restClient) orderByClientID(ctx context.Context, clientOrderID string) (auOrder, bool, error) {
	q := url.Values{"client_order_id": {clientOrderID}}
	resp, err := rc.do(ctx, http.MethodGet, "/v2/orders:by_client_order_id?"+q.Encode(), nil)
	if err != nil {
		return auOrder{}, false, fmt.Errorf("alpaca: order-by-client-id transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return auOrder{}, false, fmt.Errorf("alpaca: read order-by-client-id: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return auOrder{}, false, nil
	}
	if resp.StatusCode >= 400 {
		return auOrder{}, false, apiError(resp.StatusCode, body)
	}
	var ord auOrder
	if err := json.Unmarshal(body, &ord); err != nil {
		return auOrder{}, false, fmt.Errorf("alpaca: decode order-by-client-id: %w", err)
	}
	return ord, true, nil
}
