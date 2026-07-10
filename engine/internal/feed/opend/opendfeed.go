package opend

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"golang.org/x/sync/singleflight"
)

// FeedOptions configures the OpenD feed adapter. Zero values get defaults —
// note DisableExtendedTime is inverted so the zero value means extended
// hours ON (eTape is a pre-market-first product).
type FeedOptions struct {
	Budget              int
	Hysteresis          time.Duration
	DisableExtendedTime bool
	EventBuf            int
	Clock               clock.Clock
}

// OpenDFeed implements feed.Feed over the low-level Client: pushes are decoded
// into events, Ensure auto-seeds from OpenD's quota-free caches, and
// reconnects re-subscribe, re-seed, and emit Resynced.
type OpenDFeed struct {
	cli *Client
	sub *subManager
	bf  *backfill
	clk clock.Clock

	events chan feed.Event
	seedq  chan seedJob

	mu          sync.Mutex
	fetched     map[string]time.Time // history-quota dedup window (30 days)
	validated   map[string]struct{}  // process-lifetime positive existence cache
	decodeFails uint64

	// hbGroup coalesces concurrent HistoryBars calls for the same
	// symbol+resolution into a single fetch. Deep-backfill can now be
	// triggered from two independent producers (scanner-pool admission and
	// UI chart-open demand, see uihub.Hub.handleEnsureDemand) that share no
	// synchronization with each other; without this, both could race past
	// the fetched-map check below before either updates it, each spending a
	// real history-quota slot for what should be one fetch.
	hbGroup singleflight.Group
}

type seedJob struct {
	symbol string
	subs   []feed.SubType
}

// fetchDedupWindow mirrors moomoo's 30-day rule: re-requesting a symbol's
// history within 30 days consumes no quota, so only new symbols are guarded.
const fetchDedupWindow = 30 * 24 * time.Hour

// NewOpenDFeed wires the adapter. Call Run to start it.
func NewOpenDFeed(cli *Client, opt FeedOptions) *OpenDFeed {
	if opt.EventBuf == 0 {
		opt.EventBuf = 4096
	}
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	return &OpenDFeed{
		cli: cli,
		sub: newSubManager(cli, opt.Clock, subOptions{
			Budget:       opt.Budget,
			Hysteresis:   opt.Hysteresis,
			ExtendedTime: !opt.DisableExtendedTime,
		}),
		bf:        newBackfill(cli),
		clk:       opt.Clock,
		events:    make(chan feed.Event, opt.EventBuf),
		seedq:     make(chan seedJob, 64),
		fetched:   make(map[string]time.Time),
		validated: make(map[string]struct{}),
	}
}

func (f *OpenDFeed) Events() <-chan feed.Event { return f.events }

func (f *OpenDFeed) Ensure(d feed.Demand) {
	f.sub.Ensure(d)
	select {
	case f.seedq <- seedJob{symbol: d.Symbol, subs: d.Subs}:
	default:
		slog.Warn("seed queue full; symbol will seed on next resync", "symbol", d.Symbol)
	}
}

func (f *OpenDFeed) Release(id string) { f.sub.Release(id) }

// Run blocks until ctx is done, supervising the pump, state, seed, and
// subscription-manager goroutines. The caller runs Client.Run separately.
func (f *OpenDFeed) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); f.sub.Run(ctx) }()
	go func() { defer wg.Done(); f.pump(ctx) }()
	go func() { defer wg.Done(); f.seedWorker(ctx) }()
	f.stateLoop(ctx)
	wg.Wait()
	return ctx.Err()
}

func (f *OpenDFeed) emit(ctx context.Context, ev feed.Event) {
	select {
	case f.events <- ev:
	case <-ctx.Done():
	}
}

func (f *OpenDFeed) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-f.cli.Pushes():
			evs, err := DecodePush(frame)
			if err != nil {
				f.mu.Lock()
				f.decodeFails++
				n := f.decodeFails
				f.mu.Unlock()
				if n%100 == 1 { // log the 1st, 101st, ... — visible, never spammy
					slog.Warn("push decode failure", "protoID", frame.ProtoID, "total", n, "err", err)
				}
				continue
			}
			for _, ev := range evs {
				f.emit(ctx, ev)
			}
		}
	}
}

func (f *OpenDFeed) stateLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-f.cli.State():
			switch st {
			case ConnDown:
				f.emit(ctx, feed.ConnDownEvent{})
			case ConnUp:
				f.emit(ctx, feed.ConnUpEvent{})
				if err := f.sub.ResubscribeAll(ctx); err != nil {
					slog.Error("resubscribe after reconnect failed", "err", err)
					continue // client will cycle the connection; next ConnUp retries
				}
				for symbol, subs := range f.sub.ActiveSymbols() {
					f.seed(ctx, symbol, subs)
				}
				f.emit(ctx, feed.ResyncedEvent{})
			}
		}
	}
}

func (f *OpenDFeed) seedWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-f.seedq:
			f.seed(ctx, job.symbol, job.subs)
		}
	}
}

// seedRetryAttempts and seedRetryDelay bound seed's retry-on-error window.
// Ensure fires the KL_1Min subscribe and enqueues the seed job with no
// ordering between them, so a seed's Qot_GetKL can reach OpenD before the
// subscribe acks — OpenD rejects that with "please subscribe to KL_1Min data
// first." Even after the ack, the real-time cache can briefly lack data (a
// second, narrower race window). A few short retries ride out both without
// requiring a subManager API change to gate the seed on the ack.
const (
	seedRetryAttempts = 5
	seedRetryDelay    = 300 * time.Millisecond
)

// seedRetry calls fn until it succeeds or seedRetryAttempts is exhausted,
// waiting seedRetryDelay (via clk, so tests drive it with a fake clock)
// between tries. It aborts immediately on ctx.Done() rather than waiting out
// the remaining attempts.
func seedRetry[T any](ctx context.Context, clk clock.Clock, fn func() (T, error)) (T, error) {
	var (
		v   T
		err error
	)
	for attempt := 0; attempt < seedRetryAttempts; attempt++ {
		v, err = fn()
		if err == nil {
			return v, nil
		}
		if attempt == seedRetryAttempts-1 {
			return v, err
		}
		select {
		case <-clk.After(seedRetryDelay):
		case <-ctx.Done():
			return v, ctx.Err()
		}
	}
	return v, err
}

// seed replays OpenD's local caches as Seed events, per subtype, in a fixed
// order (bars, ticks, book, quote). Each read goes through seedRetry: it's a
// quota-free real-time-cache lookup that can lose the subscribe-ack race (see
// seedRetryAttempts above). Failures that survive every retry log and
// continue — a partial seed beats none, and the md core's dedup makes
// overlap harmless.
func (f *OpenDFeed) seed(ctx context.Context, symbol string, subs []feed.SubType) {
	has := func(want feed.SubType) bool {
		for _, s := range subs {
			if s == want {
				return true
			}
		}
		return false
	}
	if has(feed.SubKL1m) {
		bars, err := seedRetry(ctx, f.clk, func() ([]feed.Bar, error) {
			return f.bf.cachedBars1m(ctx, symbol, maxAPIRows)
		})
		if err != nil {
			slog.Warn("seed bars1m failed", "symbol", symbol, "err", err)
		} else if len(bars) > 0 {
			f.emit(ctx, feed.Bars1mEvent{Bars: bars, Seed: true})
		}
	}
	if has(feed.SubTicker) {
		ticks, err := seedRetry(ctx, f.clk, func() ([]feed.Tick, error) {
			return f.bf.recentTicks(ctx, symbol, maxAPIRows)
		})
		if err != nil {
			slog.Warn("seed ticks failed", "symbol", symbol, "err", err)
		} else if len(ticks) > 0 {
			f.emit(ctx, feed.TicksEvent{Ticks: ticks, Seed: true})
		}
	}
	if has(feed.SubBook) {
		book, err := seedRetry(ctx, f.clk, func() (feed.Book, error) {
			return f.bf.bookSnapshot(ctx, symbol)
		})
		if err != nil {
			slog.Warn("seed book failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.BookEvent{Book: book, Seed: true})
		}
	}
	if has(feed.SubQuote) {
		q, err := seedRetry(ctx, f.clk, func() (feed.Quote, error) {
			return f.bf.quoteSnapshot(ctx, symbol)
		})
		if err != nil {
			slog.Warn("seed quote failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.QuoteEvent{Quote: q, Seed: true})
		}
	}
}

// HistoryBars spends history quota; guard new symbols against exhaustion.
// Symbols fetched within the 30-day dedup window are free re-requests.
// Concurrent calls for the same symbol+resolution (e.g. scanner-pool
// admission and a UI chart-open demand racing on the same symbol) coalesce
// into a single fetch via hbGroup, so quota is spent at most once per
// distinct request rather than once per caller.
func (f *OpenDFeed) HistoryBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) {
	key := fmt.Sprintf("%s|%d", symbol, res)
	v, err, _ := f.hbGroup.Do(key, func() (any, error) {
		f.mu.Lock()
		last, ok := f.fetched[symbol]
		f.mu.Unlock()
		if !ok || f.clk.Now().Sub(last) > fetchDedupWindow {
			_, remain, err := f.bf.historyQuota(ctx)
			if err != nil {
				return nil, err
			}
			if remain == 0 {
				slog.Warn("history quota exhausted; deep backfill degraded to cache depth", "symbol", symbol)
				return nil, ErrHistoryQuotaExhausted
			}
		}
		bars, err := f.bf.historyBars(ctx, symbol, res, from, to)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.fetched[symbol] = f.clk.Now()
		f.mu.Unlock()
		return bars, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]feed.Bar), nil
}

func (f *OpenDFeed) RecentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error) {
	return f.bf.recentTicks(ctx, symbol, n)
}

func (f *OpenDFeed) CachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) {
	return f.bf.cachedBars1m(ctx, symbol, n)
}

func (f *OpenDFeed) BookSnapshot(ctx context.Context, symbol string) (feed.Book, error) {
	return f.bf.bookSnapshot(ctx, symbol)
}

func (f *OpenDFeed) QuoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error) {
	return f.bf.quoteSnapshot(ctx, symbol)
}

// Validate confirms a symbol exists before the UI commits a panel load. It is
// subscription-free and quota-free (Qot_GetSecuritySnapshot). Positive results
// are cached for the process lifetime; negatives are not (an intraday listing
// must not be locked out). Returns feed.ErrUnknownSymbol or
// feed.ErrFeedUnavailable on failure.
func (f *OpenDFeed) Validate(ctx context.Context, symbol string) error {
	f.mu.Lock()
	_, ok := f.validated[symbol]
	f.mu.Unlock()
	if ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := f.bf.securityExists(ctx, symbol); err != nil {
		return err
	}
	f.mu.Lock()
	f.validated[symbol] = struct{}{}
	f.mu.Unlock()
	return nil
}

var _ feed.Feed = (*OpenDFeed)(nil)
