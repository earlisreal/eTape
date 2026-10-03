package opend

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

const openDStartupQuiet = 31 * time.Second

type requestPacer struct {
	clk     clock.Clock
	readyAt time.Time
	mu      sync.Mutex
	next    map[string]time.Time
	queues  map[string][]pacerWaiter
	changed map[string]chan struct{}
	seq     uint64
}

type pacerWaiter struct {
	seq        uint64
	background bool
}

type backgroundRequestKey struct{}

// WithBackgroundRequest marks provider work that should yield to foreground
// data calls when both are waiting for the same rate-limit family.
func WithBackgroundRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundRequestKey{}, true)
}

func isBackgroundRequest(ctx context.Context) bool {
	background, _ := ctx.Value(backgroundRequestKey{}).(bool)
	return background
}

func newRequestPacer(clk clock.Clock) *requestPacer {
	return &requestPacer{
		clk: clk, readyAt: clk.Now().Add(openDStartupQuiet),
		next: make(map[string]time.Time), queues: make(map[string][]pacerWaiter), changed: make(map[string]chan struct{}),
	}
}

func marketDataRequestPacing(protoID uint32) (string, time.Duration) {
	switch protoID {
	case ProtoQotGetSecuritySnapshot, ProtoQotRequestHistoryKL:
		return fmt.Sprintf("opend-%d", protoID), 750 * time.Millisecond
	case ProtoQotGetUSPreMarketRank, ProtoQotGetUSAfterHoursRank,
		ProtoQotGetUSOvernightRank, ProtoQotGetTopMoversRank:
		return fmt.Sprintf("opend-%d", protoID), time.Second
	case ProtoQotSub:
		// OpenD documents subscription capacity but not a request-frequency
		// limit. Keep reconnect and batch-split bursts conservatively paced.
		return "opend-subscription", time.Second
	case ProtoQotStockFilter, ProtoQotGetStockScreen:
		return "opend-stock-screen", 5 * time.Second
	case ProtoQotGetOwnerPlate:
		return "opend-owner-plate", 5 * time.Second
	case ProtoQotGetShortInterest:
		return "opend-short-interest", 1500 * time.Millisecond
	case ProtoQotGetSearchNews:
		return "opend-search-news", 5 * time.Second
	case ProtoQotGetStaticInfo, ProtoQotGetSubInfo, ProtoQotRequestHistoryKLQuota:
		return "opend-unpublished", 5 * time.Second
	default:
		return "", 0
	}
}

func (p *requestPacer) reserve(bucket string, spacing time.Duration) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clk.Now()
	slot := now
	if bucket != "opend-unpublished" && p.readyAt.After(slot) {
		slot = p.readyAt
	}
	if next := p.next[bucket]; next.After(slot) {
		slot = next
	}
	p.next[bucket] = slot.Add(spacing)
	return slot
}

func (p *requestPacer) wait(ctx context.Context, bucket string, spacing time.Duration) error {
	return p.waitPriority(ctx, bucket, spacing, false)
}

func (p *requestPacer) waitPriority(ctx context.Context, bucket string, spacing time.Duration, background bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.seq++
	waiter := pacerWaiter{seq: p.seq, background: background}
	p.queues[bucket] = append(p.queues[bucket], waiter)
	p.signalBucketLocked(bucket)
	p.mu.Unlock()
	granted := false
	defer func() {
		if !granted {
			p.mu.Lock()
			p.removeWaiterLocked(bucket, waiter.seq)
			p.signalBucketLocked(bucket)
			p.mu.Unlock()
		}
	}()

	for {
		p.mu.Lock()
		queue := p.queues[bucket]
		sort.SliceStable(queue, func(i, j int) bool {
			if queue[i].background != queue[j].background {
				return !queue[i].background
			}
			return queue[i].seq < queue[j].seq
		})
		p.queues[bucket] = queue
		now := p.clk.Now()
		slot := now
		if bucket != "opend-unpublished" && p.readyAt.After(slot) {
			slot = p.readyAt
		}
		if next := p.next[bucket]; next.After(slot) {
			slot = next
		}
		if len(queue) > 0 && queue[0].seq == waiter.seq && !slot.After(now) {
			p.next[bucket] = now.Add(spacing)
			p.removeWaiterLocked(bucket, waiter.seq)
			p.signalBucketLocked(bucket)
			p.mu.Unlock()
			granted = true
			return nil
		}
		changed := p.changed[bucket]
		if changed == nil {
			changed = make(chan struct{})
			p.changed[bucket] = changed
		}
		var timer <-chan time.Time
		if len(queue) > 0 && queue[0].seq == waiter.seq && slot.After(now) {
			timer = p.clk.After(slot.Sub(now))
		}
		p.mu.Unlock()
		if timer == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			case <-timer:
			}
		}
	}
}

func (p *requestPacer) signalBucketLocked(bucket string) {
	if changed := p.changed[bucket]; changed != nil {
		close(changed)
	}
	p.changed[bucket] = make(chan struct{})
}

func (p *requestPacer) removeWaiterLocked(bucket string, seq uint64) {
	queue := p.queues[bucket]
	for i, waiter := range queue {
		if waiter.seq == seq {
			queue = append(queue[:i], queue[i+1:]...)
			break
		}
	}
	if len(queue) == 0 {
		delete(p.queues, bucket)
	} else {
		p.queues[bucket] = queue
	}
}
