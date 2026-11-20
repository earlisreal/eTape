package perps

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// FillHint is order-level evidence. Fill records still come from REST.
type FillHint struct {
	ClientOrderID, BrokerOrderID, Symbol, Side, Type string
	Quantity, Filled                                 json.Number
	Status                                           exec.OrderStatus
	TimeMs                                           int64
}

// OrderStream opts a venue into event-driven fill lookups.
type OrderStream interface {
	StreamOrders(context.Context, func(bool), func(FillHint))
	ObserveFill(context.Context, exec.OrderRequest, int64, FillHint) (exec.PerpetualObservation, error)
}

type streamEvent struct {
	hint  *FillHint
	ready bool
}

type accountOrders struct {
	orders    map[string]float64
	startedMs int64
}

type fillProgress struct {
	hint          FillHint
	filled        float64
	retryAt       time.Time
	retryDelay    time.Duration
	retryHint     bool
	positionClose bool
}

const (
	accountPollInterval = 3 * time.Second
	firstFillRetry      = 250 * time.Millisecond
)

func (a *Adapter) runWithOrderStream(ctx context.Context, stream OrderStream) {
	ctx, cancel := context.WithCancel(ctx)
	events := make(chan streamEvent, 64)
	snapshots := make(chan accountOrders, 1)
	streamDone, accountsDone := make(chan struct{}), make(chan struct{})
	defer func() {
		cancel()
		<-streamDone
		<-accountsDone
	}()
	send := func(event streamEvent) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(streamDone)
		stream.StreamOrders(ctx, func(ready bool) { send(streamEvent{ready: ready}) },
			func(hint FillHint) { send(streamEvent{hint: &hint}) })
	}()
	go func() {
		defer close(accountsDone)
		a.runAccountSnapshots(ctx, snapshots)
	}()

	r := streamReconciler{adapter: a, stream: stream, progress: make(map[string]fillProgress)}
	r.recover(ctx, accountOrders{}, false)
	ready := false
	retry := time.NewTicker(firstFillRetry)
	poll := time.NewTicker(accountPollInterval)
	defer retry.Stop()
	defer poll.Stop()
	for ctx.Err() == nil {
		select {
		case event := <-events:
			if event.hint != nil {
				r.fill(ctx, *event.hint)
			} else if ready != event.ready {
				ready = event.ready
				r.recover(ctx, accountOrders{}, false)
			}
		case snapshot := <-snapshots:
			if ready {
				r.recover(ctx, snapshot, true)
			}
		case id := <-a.closeSubmitted:
			r.observe(ctx, id, false)
		case <-poll.C:
			if !ready {
				r.recover(ctx, accountOrders{}, false)
			}
		case now := <-retry.C:
			for id, p := range r.progress {
				if (ready || p.positionClose) && !p.retryAt.IsZero() && !now.Before(p.retryAt) {
					r.observe(ctx, id, p.retryHint)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *Adapter) runAccountSnapshots(ctx context.Context, out chan<- accountOrders) {
	tick := time.NewTicker(accountPollInterval)
	defer tick.Stop()
	connected := false
	for ctx.Err() == nil {
		started := a.clock.Now().UnixMilli()
		account, positions, orders, err := a.api.Snapshot(ctx)
		snapshot := accountOrders{startedMs: started}
		if err == nil {
			snapshot.orders = make(map[string]float64, len(orders))
			for _, order := range orders {
				snapshot.orders[order.ID] = order.ExecutedQty
			}
			a.mu.Lock()
			account, positions, orders = a.filterSnapshot(account, positions, orders)
			a.mu.Unlock()
		}
		connected = a.publishSnapshot(ctx, connected, account, positions, orders, err)
		select {
		case out <- snapshot:
		case <-ctx.Done():
			return
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

type streamReconciler struct {
	adapter  *Adapter
	stream   OrderStream
	progress map[string]fillProgress
}

func (r *streamReconciler) fill(ctx context.Context, hint FillHint) {
	id := hint.ClientOrderID
	p := r.progress[id]
	// BingX emits two event names for the same cumulative transition, with
	// different trade IDs. Compare order evidence before touching REST.
	if p.hint.BrokerOrderID == hint.BrokerOrderID && p.hint.Filled == hint.Filled && p.hint.Status == hint.Status {
		return
	}
	p.hint = hint
	p.retryDelay = 0
	r.progress[id] = p
	r.observe(ctx, id, true)
}

func (r *streamReconciler) recover(ctx context.Context, snapshot accountOrders, ready bool) {
	for _, id := range r.adapter.PendingOrderIDs() {
		if ctx.Err() != nil {
			return
		}
		if ready {
			if snapshot.orders == nil {
				continue
			}
			pending, ok := r.pending(id)
			if !ok || pending.SubmittedMs >= snapshot.startedMs {
				continue
			}
			if qty, working := snapshot.orders[id]; working && qty <= r.progress[id].filled && pending.Rejection == "" {
				continue
			}
		}
		r.observe(ctx, id, false)
	}
}

func (r *streamReconciler) pending(id string) (pending, bool) {
	r.adapter.mu.Lock()
	defer r.adapter.mu.Unlock()
	p, ok := r.adapter.journal.Pending[id]
	return p, ok
}

func (r *streamReconciler) observe(ctx context.Context, id string, useHint bool) {
	pending, ok := r.pending(id)
	if !ok {
		delete(r.progress, id)
		return
	}
	p := r.progress[id]
	p.positionClose = pending.isPositionClose()
	report := exec.PerpetualObservation{Rejection: pending.Rejection}
	var err error
	if pending.Rejection == "" {
		if useHint && !p.positionClose {
			report, err = r.stream.ObserveFill(ctx, pending.Request, pending.SubmittedMs, p.hint)
		} else {
			report, err = r.adapter.observe(ctx, pending)
		}
	}
	report.Venue, report.OrderID = r.adapter.venue, id
	if err != nil || !r.adapter.persistObservation(ctx, report) {
		r.retryLater(id, p, useHint)
		return
	}
	if report.Terminal() {
		delete(r.progress, id)
		return
	}
	if p.positionClose {
		r.retryLater(id, p, false)
		return
	}
	var filled float64
	for _, fill := range report.Fills {
		filled += fill.Qty
	}
	p.filled = max(p.filled, filled)
	if useHint || p.retryHint {
		native, _ := p.hint.Filled.Float64()
		expected := math.Round(native / pending.Request.Perpetual.Multiplier)
		if expected <= pending.Request.Qty && expected > p.filled {
			// An intervening order-detail lookup can still lag the stream.
			r.retryLater(id, p, true)
			return
		}
	}
	p.retryAt, p.retryDelay = time.Time{}, 0
	p.retryHint = false
	r.progress[id] = p
}

func (r *streamReconciler) retryLater(id string, p fillProgress, useHint bool) {
	p.retryDelay = min(max(2*p.retryDelay, firstFillRetry), accountPollInterval)
	p.retryAt = time.Now().Add(p.retryDelay)
	p.retryHint = useHint
	r.progress[id] = p
}
