// Package perps handles durable submission and polling for authenticated perpetual venues.
package perps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/atomicfile"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type API interface {
	Rules(symbol string) (instrument.ContractRules, bool)
	FairPrice(symbol string) (float64, bool)
	Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error)
	Prepare(context.Context, exec.OrderRequest) (func(context.Context) error, error)
	Observe(context.Context, exec.OrderRequest) (exec.PerpetualObservation, error)
	Cancel(context.Context, exec.OrderRequest) error
}

// PositionCloser uses a broker-generated order ID for an exact full close.
// The original request stays reserved even when the submission reply is lost.
type PositionCloser interface {
	PreparePositionClose(context.Context, exec.OrderRequest) (func(context.Context) (string, error), error)
	ObservePositionClose(context.Context, exec.OrderRequest, string) (exec.PerpetualObservation, error)
}

type pending struct {
	Request       exec.OrderRequest
	Rejection     string
	SubmittedMs   int64  `json:",omitempty"`
	BrokerOrderID string `json:",omitempty"`
}

func (p pending) isPositionClose() bool {
	return p.Request.Perpetual.WholePosition && (p.Request.Side == exec.SideSell || p.Request.Side == exec.SideCover)
}

type journal struct {
	Identity string
	Pending  map[string]pending
}

type Adapter struct {
	venue exec.VenueID
	clock clock.Clock
	path  string
	api   API
	event chan exec.BrokerEvent

	mu      sync.Mutex
	journal journal
}

func New(venue exec.VenueID, clk clock.Clock, path, identity string, api API) (*Adapter, error) {
	if venue == "" || clk == nil || path == "" || identity == "" || api == nil {
		return nil, errors.New("incomplete perpetual venue configuration")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	a := &Adapter{venue: venue, clock: clk, path: path, api: api, event: make(chan exec.BrokerEvent, 32),
		journal: journal{Identity: hash, Pending: make(map[string]pending)}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &a.journal); err != nil {
		return nil, fmt.Errorf("read perpetual journal: %w", err)
	}
	if a.journal.Pending == nil || a.journal.Identity != hash && len(a.journal.Pending) != 0 {
		return nil, errors.New("perpetual journal belongs to a different account; restore the previous credentials to reconcile")
	}
	a.journal.Identity = hash
	for id, p := range a.journal.Pending {
		if id == "" || id != p.Request.ClientOrderID || p.Request.Venue != venue || p.Request.Perpetual == nil {
			return nil, errors.New("invalid perpetual journal entry")
		}
	}
	return a, nil
}

func (a *Adapter) save() error {
	data, err := json.Marshal(a.journal)
	if err != nil {
		return err
	}
	return atomicfile.Write(a.path, data, 0o600)
}

func (a *Adapter) Capabilities() exec.Capabilities {
	return exec.Capabilities{ReconciledPerpetual: true, MarketOutsideRTH: true}
}
func (a *Adapter) ContractRules(symbol string) (instrument.ContractRules, bool) {
	return a.api.Rules(symbol)
}
func (a *Adapter) FairPrice(symbol string) (float64, bool) { return a.api.FairPrice(symbol) }
func (a *Adapter) Events() <-chan exec.BrokerEvent         { return a.event }

func (a *Adapter) PendingOrderIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.journal.Pending))
	for id := range a.journal.Pending {
		ids = append(ids, id)
	}
	return ids
}

// AccountIdentity binds protection to the same adapter account across recovery.
func (a *Adapter) AccountIdentity() string { return a.journal.Identity }

// RecordRejection restores a previously observed definitive submit rejection.
// The poller still waits for the persisted terminal engine event.
func (a *Adapter) RecordRejection(id, symbol, reason string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.journal.Pending[id]
	if !ok || p.Request.Symbol != symbol {
		return false, nil
	}
	if p.Rejection == "" {
		saved := p
		p.Rejection = reason
		a.journal.Pending[id] = p
		if err := a.save(); err != nil {
			a.journal.Pending[id] = saved
			return false, err
		}
	}
	return p.Rejection == reason, nil
}

func (a *Adapter) SubmitOrder(ctx context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	reject := func(err error) (exec.OrderAck, error) {
		return exec.OrderAck{}, exec.PerpetualSubmitDeclined{Cause: err}
	}
	if req.Venue != a.venue || req.ClientOrderID == "" || req.Perpetual == nil ||
		req.Perpetual.ProtectionPrice != 0 && (!req.Perpetual.LocalProtection || !req.Perpetual.IsRiskEntry()) {
		return reject(errors.New("unsupported perpetual order"))
	}
	for id, order := range a.journal.Pending {
		if id == req.ClientOrderID {
			return exec.OrderAck{}, errors.New("order already submitted; waiting for exchange reconciliation")
		}
		if order.Request.Symbol == req.Symbol {
			return reject(exec.ErrPerpetualContractBusy)
		}
	}
	p := pending{Request: req}
	send, err := a.prepare(ctx, p)
	if err != nil {
		return reject(err)
	}
	if err := ctx.Err(); err != nil {
		return reject(err)
	}
	p.SubmittedMs = a.clock.Now().UnixMilli()
	a.journal.Pending[req.ClientOrderID] = p
	if err := a.save(); err != nil {
		delete(a.journal.Pending, req.ClientOrderID)
		return reject(fmt.Errorf("persist order before submit: %w", err))
	}
	brokerID, err := send(ctx)
	if err != nil {
		var declined exec.PerpetualSubmitDeclined
		if errors.As(err, &declined) {
			p.Rejection = declined.Error()
			a.journal.Pending[req.ClientOrderID] = p
			if saveErr := a.save(); saveErr != nil {
				return exec.OrderAck{}, fmt.Errorf("persist perpetual submit rejection (%v): %w", err, saveErr)
			}
			// The poller keeps the rejection until the engine has persisted it.
			return exec.OrderAck{}, declined
		}
		return exec.OrderAck{}, err
	}
	if brokerID != "" {
		p.BrokerOrderID = brokerID
		a.journal.Pending[req.ClientOrderID] = p
		if err := a.save(); err != nil {
			return exec.OrderAck{}, fmt.Errorf("persist whole-position close acknowledgement: %w", err)
		}
	}
	// An acknowledgement can confirm receipt before the first order poll.
	return exec.OrderAck{Accepted: true}, nil
}

func (a *Adapter) prepare(ctx context.Context, p pending) (func(context.Context) (string, error), error) {
	if p.isPositionClose() {
		if api, ok := a.api.(PositionCloser); ok {
			return api.PreparePositionClose(ctx, p.Request)
		}
		return nil, errors.New("position-ID closes are unavailable")
	}
	send, err := a.api.Prepare(ctx, p.Request)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) { return "", send(ctx) }, nil
}

func (a *Adapter) observe(ctx context.Context, p pending) (exec.PerpetualObservation, error) {
	if p.isPositionClose() {
		if api, ok := a.api.(PositionCloser); ok {
			return api.ObservePositionClose(ctx, p.Request, p.BrokerOrderID)
		}
		return exec.PerpetualObservation{}, errors.New("position-ID closes are unavailable")
	}
	return a.api.Observe(ctx, p.Request)
}

func (a *Adapter) CancelOrder(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.journal.Pending[id]
	if !ok || p.Rejection != "" {
		return errors.New("no matching managed perpetual order")
	}
	if p.isPositionClose() {
		return errors.New("a submitted whole-position close must reconcile")
	}
	return a.api.Cancel(ctx, p.Request)
}

func (a *Adapter) CancelAll(ctx context.Context, symbol string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result error
	for _, p := range a.journal.Pending {
		if p.Rejection == "" && !p.isPositionClose() && (symbol == "" || p.Request.Symbol == symbol) {
			result = errors.Join(result, a.api.Cancel(ctx, p.Request))
		}
	}
	return result
}

func (*Adapter) ReplaceOrder(context.Context, string, exec.ReplaceRequest) error {
	return errors.New("perpetual order replacement is unavailable")
}
func (*Adapter) Flatten(context.Context) error {
	return errors.New("use a contract-specific reduce-only close")
}
func (*Adapter) ResetBalance(context.Context, float64) error {
	return errors.New("balance reset is unavailable")
}

func (a *Adapter) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	account, positions, orders, err := a.api.Snapshot(ctx)
	if err != nil {
		return account, nil, nil, err
	}
	a.mu.Lock()
	account, positions, orders = a.filterSnapshot(account, positions, orders)
	a.mu.Unlock()
	return account, positions, orders, nil
}

func (a *Adapter) filterSnapshot(account exec.AccountSnapshot, positions []exec.Position, orders []exec.Order) (exec.AccountSnapshot, []exec.Position, []exec.Order) {
	account.Venue, account.TsMs = a.venue, a.clock.Now().UnixMilli()
	managedBrokerIDs := make(map[string]bool)
	for _, p := range a.journal.Pending {
		if p.BrokerOrderID != "" {
			managedBrokerIDs[p.BrokerOrderID] = true
		}
	}
	external := orders[:0]
	for _, order := range orders {
		if _, managed := a.journal.Pending[order.ID]; !managed && !managedBrokerIDs[order.BrokerOrderID] {
			// Never adopt another client's order merely because its client
			// ID uses the same prefix.
			order.ID = "external:" + order.ID
			external = append(external, order)
		}
	}
	for i := range positions {
		positions[i].Venue = a.venue
	}
	return account, positions, external
}

func (a *Adapter) emit(ctx context.Context, ev exec.BrokerEvent) bool {
	select {
	case a.event <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *Adapter) Run(ctx context.Context) {
	if stream, ok := a.api.(OrderStream); ok {
		a.runWithOrderStream(ctx, stream)
		return
	}
	tick := time.NewTicker(accountPollInterval)
	defer tick.Stop()
	connected := false
	for {
		connected = a.poll(ctx, connected)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (a *Adapter) poll(ctx context.Context, connected bool) bool {
	// Polls never hold the journal lock across network reads. A new order can
	// dispatch while a snapshot or a previous order's observation is in flight.
	a.mu.Lock()
	pendingOrders := maps.Clone(a.journal.Pending)
	a.mu.Unlock()
	reports := make([]exec.PerpetualObservation, 0, len(pendingOrders))
	for id, p := range pendingOrders {
		report := exec.PerpetualObservation{Venue: a.venue, OrderID: id, Rejection: p.Rejection}
		if p.Rejection == "" {
			var err error
			report, err = a.observe(ctx, p)
			if err != nil {
				continue
			}
			report.Venue, report.OrderID = a.venue, id
		}
		reports = append(reports, report)
	}
	account, positions, orders, err := a.Snapshot(ctx)
	for _, report := range reports {
		a.persistObservation(ctx, report)
		if ctx.Err() != nil {
			return false
		}
	}
	return a.publishSnapshot(ctx, connected, account, positions, orders, err)
}

func (a *Adapter) persistObservation(ctx context.Context, report exec.PerpetualObservation) bool {
	report.Persisted = make(chan bool, 1)
	if !a.emit(ctx, report) {
		return false
	}
	select {
	case confirmed := <-report.Persisted:
		if !confirmed || !report.Terminal() {
			return confirmed
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		p := a.journal.Pending[report.OrderID]
		delete(a.journal.Pending, report.OrderID)
		if a.save() != nil {
			a.journal.Pending[report.OrderID] = p
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *Adapter) publishSnapshot(ctx context.Context, connected bool, account exec.AccountSnapshot, positions []exec.Position, orders []exec.Order, err error) bool {
	if err != nil {
		a.emit(ctx, exec.BrokerConnDown{V: a.venue, Note: err.Error()})
		return false
	}
	if !connected {
		a.emit(ctx, exec.BrokerConnUp{V: a.venue})
	}
	a.emit(ctx, exec.BrokerSnapshot{V: a.venue, Account: account, Positions: positions, Orders: orders})
	return true
}
