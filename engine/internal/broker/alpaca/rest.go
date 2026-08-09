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
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/locates"
)

const (
	alpacaRESTRatePerSec = 200.0 / 60.0
	alpacaRESTBurst      = 5
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
		bucket: netx.NewTokenBucket(clk, alpacaRESTRatePerSec, alpacaRESTBurst),
	}
}

// do takes one token from the shared pooled bucket, then issues a normal
// request with Alpaca's key/secret headers.
func (rc *restClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	return rc.doWithHeaders(ctx, method, path, body, nil)
}

// doWithHeaders is the same pooled/authenticated request path as do, with a
// small escape hatch for endpoint-specific headers such as Idempotency-Key.
func (rc *restClient) doWithHeaders(ctx context.Context, method, path string, body io.Reader, extra http.Header) (*http.Response, error) {
	if err := rc.bucket.Take(ctx); err != nil {
		return nil, err
	}
	return rc.doHTTPWithHeaders(ctx, method, path, body, extra)
}

func (rc *restClient) doHTTP(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	return rc.doHTTPWithHeaders(ctx, method, path, body, nil)
}

func (rc *restClient) doHTTPWithHeaders(ctx context.Context, method, path string, body io.Reader, extra http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APCA-API-KEY-ID", rc.keyID)
	req.Header.Set("APCA-API-SECRET-KEY", rc.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, values := range extra {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return rc.hc.Do(req)
}

// alpacaError is Alpaca's structured error body on >=400 responses:
// {"code": 42210000, "message": "sub-penny increment"}.
type alpacaError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type assetResponse struct {
	Symbol       string  `json:"symbol"`
	BorrowStatus *string `json:"borrow_status"`
	Shortable    *bool   `json:"shortable"`
	Marginable   *bool   `json:"marginable"`
	Tradable     *bool   `json:"tradable"`
}

type locateQuoteResponse struct {
	Quotes []locateQuoteWire      `json:"quotes"`
	Errors []locateQuoteErrorWire `json:"errors"`
}

type locateQuoteWire struct {
	Symbol       string `json:"symbol"`
	AvailableQty int64  `json:"available_qty"`
	Price        string `json:"price"`
	QuotedAt     string `json:"quoted_at"`
}

type locateQuoteErrorWire struct {
	Symbol  string `json:"symbol"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type locateRecordWire struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Qty          int64  `json:"qty"`
	RequestedQty int64  `json:"requested_qty"`
	LimitPrice   string `json:"limit_price"`
	AllOrNone    bool   `json:"all_or_none"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	LocatedQty   int64  `json:"located_qty"`
	LocatedPrice string `json:"located_price"`
	TotalFee     string `json:"total_fee"`
	ExpiresAt    string `json:"expires_at"`
}

type locateListResponse struct {
	Locates       []locateRecordWire `json:"locates"`
	NextPageToken string             `json:"next_page_token"`
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

// activeAssets loads Alpaca's complete active asset directory. The response
// contract is an array; Stock Info treats this snapshot as session-static.
func (rc *restClient) activeAssets(ctx context.Context) ([]assetResponse, error) {
	resp, err := rc.do(ctx, http.MethodGet, "/v2/assets?status=active", nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca: active assets transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("alpaca: read active assets response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, apiError(resp.StatusCode, body)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("alpaca: active assets response is not an array")
	}
	var assets []assetResponse
	if err := json.Unmarshal(body, &assets); err != nil {
		return nil, fmt.Errorf("alpaca: decode active assets response: %w", err)
	}
	return assets, nil
}

func (rc *restClient) locateQuotes(ctx context.Context, symbols []string) (locates.QuoteResult, error) {
	normalized, err := normalizeLocateSymbols(symbols)
	if err != nil {
		return locates.QuoteResult{}, err
	}
	q := url.Values{}
	q.Set("symbols", strings.Join(normalized, ","))
	resp, err := rc.do(ctx, http.MethodGet, "/v1/locates/quotes?"+q.Encode(), nil)
	if err != nil {
		return locates.QuoteResult{}, fmt.Errorf("alpaca: locate quotes transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return locates.QuoteResult{}, fmt.Errorf("alpaca: read locate quotes response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return locates.QuoteResult{}, apiError(resp.StatusCode, body)
	}
	if err := requireLocateArrayField(body, "quotes", "quotes"); err != nil {
		return locates.QuoteResult{}, err
	}
	var wire locateQuoteResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return locates.QuoteResult{}, fmt.Errorf("alpaca: decode locate quotes response: %w", err)
	}
	result := locates.QuoteResult{
		Quotes: make([]locates.Quote, 0, len(wire.Quotes)),
		Errors: make([]locates.QuoteError, 0, len(wire.Errors)),
	}
	for _, quote := range wire.Quotes {
		if strings.TrimSpace(quote.Symbol) == "" || strings.TrimSpace(quote.Price) == "" {
			return locates.QuoteResult{}, fmt.Errorf("alpaca: locate quote response missing symbol or price")
		}
		quotedAt, err := parseLocateTime(quote.QuotedAt, "quoted_at")
		if err != nil {
			return locates.QuoteResult{}, err
		}
		result.Quotes = append(result.Quotes, locates.Quote{
			Symbol: locateDomainSymbol(quote.Symbol), AvailableQty: quote.AvailableQty,
			Price: quote.Price, QuotedAt: quotedAt,
		})
	}
	for _, item := range wire.Errors {
		result.Errors = append(result.Errors, locates.QuoteError{Symbol: locateDomainSymbol(item.Symbol), Code: item.Code, Message: item.Message})
	}
	return result, nil
}

func (rc *restClient) createLocate(ctx context.Context, request locates.Request) (locates.Record, error) {
	normalized, err := normalizeLocateSymbols([]string{request.Symbol})
	if err != nil {
		return locates.Record{}, err
	}
	request.Symbol = normalized[0]
	request.LimitPrice = strings.TrimSpace(request.LimitPrice)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if err := request.Validate(); err != nil {
		return locates.Record{}, err
	}
	body, err := json.Marshal(struct {
		Symbol     string `json:"symbol"`
		Qty        int64  `json:"qty"`
		LimitPrice string `json:"limit_price"`
		AllOrNone  bool   `json:"all_or_none"`
	}{
		Symbol: request.Symbol, Qty: request.Qty, LimitPrice: request.LimitPrice, AllOrNone: request.AllOrNone,
	})
	if err != nil {
		return locates.Record{}, fmt.Errorf("alpaca: marshal locate request: %w", err)
	}
	resp, err := rc.doWithHeaders(ctx, http.MethodPost, "/v1/locates", bytes.NewReader(body), http.Header{
		"Idempotency-Key": []string{request.IdempotencyKey},
	})
	if err != nil {
		return locates.Record{}, locates.MarkAmbiguous(fmt.Errorf("alpaca: create locate transport: %w", err))
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return locates.Record{}, locates.MarkAmbiguous(fmt.Errorf("alpaca: read create locate response: %w", err))
	}
	if resp.StatusCode >= 400 {
		apiErr := apiError(resp.StatusCode, responseBody)
		if resp.StatusCode >= 500 {
			return locates.Record{}, locates.MarkAmbiguous(apiErr)
		}
		return locates.Record{}, apiErr
	}
	var wire locateRecordWire
	if err := json.Unmarshal(responseBody, &wire); err != nil {
		return locates.Record{}, locates.MarkAmbiguous(fmt.Errorf("alpaca: decode create locate response: %w", err))
	}
	record, err := mapLocateRecord(wire)
	if err != nil {
		return locates.Record{}, locates.MarkAmbiguous(err)
	}
	return record, nil
}

func (rc *restClient) listLocates(ctx context.Context, filter locates.ListFilter) (locates.Page, error) {
	if err := filter.Validate(); err != nil {
		return locates.Page{}, err
	}
	q := url.Values{}
	if filter.PageToken != "" {
		q.Set("page_token", filter.PageToken)
	}
	if filter.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", filter.Limit))
	}
	if filter.Status != "" {
		q.Set("status", filter.Status)
	}
	if filter.Symbol != "" {
		normalized, err := normalizeLocateSymbols([]string{filter.Symbol})
		if err != nil {
			return locates.Page{}, err
		}
		q.Set("symbol", normalized[0])
	}
	if filter.Start != "" {
		q.Set("start", filter.Start)
	}
	if filter.End != "" {
		q.Set("end", filter.End)
	}
	path := "/v1/locates"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := rc.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return locates.Page{}, fmt.Errorf("alpaca: list locates transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return locates.Page{}, fmt.Errorf("alpaca: read locates response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return locates.Page{}, apiError(resp.StatusCode, body)
	}
	if err := requireLocateArrayField(body, "locates", "list"); err != nil {
		return locates.Page{}, err
	}
	var wire locateListResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return locates.Page{}, fmt.Errorf("alpaca: decode locates response: %w", err)
	}
	page := locates.Page{Locates: make([]locates.Record, 0, len(wire.Locates)), NextPageToken: wire.NextPageToken}
	for _, item := range wire.Locates {
		record, err := mapLocateRecord(item)
		if err != nil {
			return locates.Page{}, err
		}
		page.Locates = append(page.Locates, record)
	}
	return page, nil
}

func (rc *restClient) getLocate(ctx context.Context, id string) (locates.Record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return locates.Record{}, fmt.Errorf("alpaca: locate id is required")
	}
	resp, err := rc.do(ctx, http.MethodGet, "/v1/locates/"+url.PathEscape(id), nil)
	if err != nil {
		return locates.Record{}, fmt.Errorf("alpaca: get locate transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return locates.Record{}, fmt.Errorf("alpaca: read locate response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return locates.Record{}, apiError(resp.StatusCode, body)
	}
	var wire locateRecordWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return locates.Record{}, fmt.Errorf("alpaca: decode locate response: %w", err)
	}
	return mapLocateRecord(wire)
}

func parseLocateTime(raw, field string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("alpaca: invalid locate %s %q: %w", field, raw, err)
	}
	return t, nil
}

func requireLocateArrayField(body []byte, field, kind string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return fmt.Errorf("alpaca: decode locate %s response: %w", kind, err)
	}
	raw, ok := object[field]
	trimmed := bytes.TrimSpace(raw)
	if !ok || len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return fmt.Errorf("alpaca: locate %s response missing array %q", kind, field)
	}
	return nil
}

func mapLocateRecord(wire locateRecordWire) (locates.Record, error) {
	if wire.ID == "" {
		return locates.Record{}, fmt.Errorf("alpaca: locate response missing id")
	}
	createdAt, err := parseLocateTime(wire.CreatedAt, "created_at")
	if err != nil {
		return locates.Record{}, err
	}
	expiresAt, err := parseLocateTime(wire.ExpiresAt, "expires_at")
	if err != nil {
		return locates.Record{}, err
	}
	requestedQty := wire.RequestedQty
	if requestedQty == 0 {
		requestedQty = wire.Qty
	}
	return locates.Record{
		ID: wire.ID, Symbol: locateDomainSymbol(wire.Symbol), RequestedQty: requestedQty,
		LimitPrice: wire.LimitPrice, AllOrNone: wire.AllOrNone, Status: wire.Status,
		CreatedAt: createdAt, LocatedQty: wire.LocatedQty, LocatedPrice: wire.LocatedPrice,
		TotalFee: wire.TotalFee, ExpiresAt: expiresAt,
	}, nil
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
		"symbol":          wireSymbol(req.Symbol),
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
	if req.Type == exec.TypeLimit && (req.TIF == exec.TIFDay || req.TIF == exec.TIFGTC) && extendedHoursFor(req.Session, rc.clk) {
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

// ping issues a lightweight read-only GET /v2/clock, used only to measure
// Alpaca REST reachability/RTT (Adapter.ProbeRTT below mirrors moomooProbe's
// Qot_GetGlobalState round trip). The response body is never decoded — only
// the round trip and status matter here — but a >=400 status still surfaces
// as a real error via apiError, never a false "reachable" nil.
func (rc *restClient) ping(ctx context.Context) error {
	resp, err := rc.do(ctx, http.MethodGet, "/v2/clock", nil)
	if err != nil {
		return fmt.Errorf("alpaca: ping transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("alpaca: read ping response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiError(resp.StatusCode, body)
	}
	return nil
}

// alpacaAccountID is a minimal GET /v2/account decode scoped to
// verifyAccount only — deliberately separate from alpacaAccount (which has
// no id field at all: snapshot's equity/buying-power/day-P&L fields have
// never needed one). account_number is Alpaca's documented Account field for
// a human-readable account identifier (consistent with alpacaAccount's own
// doc comment, which already notes its fields are per Alpaca's public
// Trading API reference); status is decoded for parity with the documented
// Account object but is currently unused by verifyAccount.
type alpacaAccountID struct {
	AccountNumber string `json:"account_number"`
	Status        string `json:"status"`
}

// verifyAccount issues a read-only GET /v2/account and returns the account
// number. Its only purpose is verifying that a credential authenticates and
// surfacing a human-readable account id for display — it must never be used
// to drive order logic (see alpacaAccount/snapshot for the account shape
// order logic actually depends on).
//
// A >=400 response is a real error via apiError, deliberately: this is
// exactly how VerifyCredentials (alpaca.go) discriminates a paper key from a
// live key, since a key only authenticates against its own environment's
// base URL (the wrong env's key gets a 401/403 here; the right one gets
// 200). A 200 whose body doesn't decode, or decodes but carries no
// account_number, is also an error rather than a default-accept — mirroring
// submitOrder's "200 but no order id" check — so this can never silently
// report success with an empty account number.
func (rc *restClient) verifyAccount(ctx context.Context) (string, error) {
	resp, err := rc.do(ctx, http.MethodGet, "/v2/account", nil)
	if err != nil {
		return "", fmt.Errorf("alpaca: verify account transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("alpaca: read verify account response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", apiError(resp.StatusCode, body)
	}
	var aid alpacaAccountID
	if err := json.Unmarshal(body, &aid); err != nil {
		return "", fmt.Errorf("alpaca: decode verify account response: %w", err)
	}
	if aid.AccountNumber == "" {
		return "", fmt.Errorf("alpaca: verify account response missing account_number: %s", body)
	}
	return aid.AccountNumber, nil
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
	symbol = wireSymbol(symbol)
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
		Symbol:       domainSymbol(o.Symbol),
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
		positions = append(positions, exec.Position{Symbol: domainSymbol(p.Symbol), Qty: positionQtyDomain(p), AvgPrice: float64(p.AvgEntryPrice)})
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
