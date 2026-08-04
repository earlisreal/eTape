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

	events          chan feed.Event
	foregroundSeedq chan seedJob
	backgroundSeedq chan seedJob

	mu          sync.Mutex
	fetched     map[string]time.Time // history-quota dedup window (30 days)
	validated   map[string]struct{}  // process-lifetime positive existence cache
	seedStates  map[seedKey]seedState
	tickerGated map[string]bool
	tickerLive  map[string][]feed.TicksEvent
	decodeFails uint64

	// hbGroup coalesces concurrent HistoryBars calls for the same
	// symbol+resolution into a single fetch. Deep-backfill can now be
	// triggered from two independent producers (scanner-pool admission and
	// UI chart-open demand, see uihub.Hub.handleEnsureDemand) that share no
	// synchronization with each other; without this, both could race past
	// the fetched-map check below before either updates it, each spending a
	// real history-quota slot for what should be one fetch.
	hbGroup         singleflight.Group
	validateGroup   singleflight.Group
	dailyCacheGroup singleflight.Group
}

type seedJob struct {
	symbol     string
	subs       []feed.SubType
	enqueuedAt time.Time
	background bool
	force      bool
}

type seedKey struct {
	symbol string
	sub    feed.SubType
}

type seedState struct {
	inFlight    bool
	completedAt time.Time
}

// fetchDedupWindow mirrors moomoo's 30-day rule: re-requesting a symbol's
// history within 30 days consumes no quota, so only new symbols are guarded.
const fetchDedupWindow = 30 * 24 * time.Hour

// seedDedupWindow collapses the burst of overlapping chart/ladder/tape
// demands emitted when a linked symbol changes. Live pushes keep the stores
// current after the first cache replay, so another replay seconds later only
// adds OpenD latency and occupies an interactive worker.
const seedDedupWindow = 2 * time.Second

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
		bf:              newBackfill(cli),
		clk:             opt.Clock,
		events:          make(chan feed.Event, opt.EventBuf),
		foregroundSeedq: make(chan seedJob, 64),
		backgroundSeedq: make(chan seedJob, 64),
		fetched:         make(map[string]time.Time),
		validated:       make(map[string]struct{}),
		seedStates:      make(map[seedKey]seedState),
		tickerGated:     make(map[string]bool),
		tickerLive:      make(map[string][]feed.TicksEvent),
	}
}

func (f *OpenDFeed) Events() <-chan feed.Event { return f.events }

func (f *OpenDFeed) Ensure(d feed.Demand) {
	if len(d.Subs) == 0 {
		f.sub.Ensure(d)
		return
	}
	lane := "foreground"
	queue := f.foregroundSeedq
	if d.BackgroundSeed {
		lane = "background"
		queue = f.backgroundSeedq
	}
	job := seedJob{symbol: d.Symbol, subs: d.Subs, enqueuedAt: f.clk.Now(), background: d.BackgroundSeed}
	ticker := false
	for _, sub := range d.Subs {
		ticker = ticker || sub == feed.SubTicker
	}
	f.mu.Lock()
	select {
	case queue <- job:
		if ticker {
			f.tickerGated[d.Symbol] = true
		}
	default:
		slog.Warn("seed queue full; symbol will seed on next resync", "symbol", d.Symbol, "lane", lane)
	}
	f.mu.Unlock()
	f.sub.Ensure(d)
}

func (f *OpenDFeed) claimSeed(symbol string, sub feed.SubType, force bool) bool {
	key := seedKey{symbol: symbol, sub: sub}
	now := f.clk.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.seedStates[key]
	if state.inFlight || (!force && !state.completedAt.IsZero() && now.Sub(state.completedAt) < seedDedupWindow) {
		return false
	}
	state.inFlight = true
	f.seedStates[key] = state
	return true
}

func (f *OpenDFeed) finishSeed(symbol string, sub feed.SubType, success bool) {
	key := seedKey{symbol: symbol, sub: sub}
	f.mu.Lock()
	state := f.seedStates[key]
	state.inFlight = false
	if success {
		state.completedAt = f.clk.Now()
	} else {
		state.completedAt = time.Time{}
	}
	f.seedStates[key] = state
	f.mu.Unlock()
}

// finishTickerSeed emits the cache tail before any live pushes buffered while
// Qot_GetTicker was in flight, then opens the symbol's live gate.
func (f *OpenDFeed) finishTickerSeed(ctx context.Context, symbol string, ticks []feed.Tick, success bool) {
	f.mu.Lock()
	if success && len(ticks) > 0 {
		f.emit(ctx, feed.TicksEvent{Ticks: ticks, Seed: true})
	}
	for _, ev := range f.tickerLive[symbol] {
		f.emit(ctx, ev)
	}
	delete(f.tickerLive, symbol)
	delete(f.tickerGated, symbol)
	state := f.seedStates[seedKey{symbol: symbol, sub: feed.SubTicker}]
	state.inFlight = false
	if success {
		state.completedAt = f.clk.Now()
	} else {
		state.completedAt = time.Time{}
	}
	f.seedStates[seedKey{symbol: symbol, sub: feed.SubTicker}] = state
	f.mu.Unlock()
}

func (f *OpenDFeed) emitPush(ctx context.Context, ev feed.Event) {
	ticks, ok := ev.(feed.TicksEvent)
	if !ok || ticks.Seed || len(ticks.Ticks) == 0 {
		f.emit(ctx, ev)
		return
	}
	symbol := ticks.Ticks[0].Symbol
	f.mu.Lock()
	if f.tickerGated[symbol] {
		f.tickerLive[symbol] = append(f.tickerLive[symbol], ticks)
		f.mu.Unlock()
		return
	}
	f.emit(ctx, ev)
	f.mu.Unlock()
}

func (f *OpenDFeed) Release(id string) { f.sub.Release(id) }

// Run blocks until ctx is done, supervising the pump, state, seed, and
// subscription-manager goroutines. The caller runs Client.Run separately.
func (f *OpenDFeed) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); f.sub.Run(ctx) }()
	go func() { defer wg.Done(); f.pump(ctx) }()
	for range 2 {
		go func() { defer wg.Done(); f.seedWorker(ctx, f.foregroundSeedq) }()
	}
	go func() { defer wg.Done(); f.seedWorker(ctx, f.backgroundSeedq) }()
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
				f.emitPush(ctx, ev)
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
				active := f.sub.ActiveSymbols()
				f.mu.Lock()
				for symbol, subs := range active {
					for _, sub := range subs {
						if sub == feed.SubTicker {
							f.tickerGated[symbol] = true
						}
					}
				}
				f.mu.Unlock()
				f.emit(ctx, feed.ConnUpEvent{})
				if err := f.sub.ResubscribeAll(ctx); err != nil {
					slog.Error("resubscribe after reconnect failed", "err", err)
					continue // client will cycle the connection; next ConnUp retries
				}
				for symbol, subs := range active {
					f.seed(ctx, seedJob{symbol: symbol, subs: subs, enqueuedAt: f.clk.Now(), force: true})
				}
				f.emit(ctx, feed.ResyncedEvent{})
			}
		}
	}
}

func (f *OpenDFeed) seedWorker(ctx context.Context, queue <-chan seedJob) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-queue:
			f.seed(ctx, job)
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
// order (bars, book, ticks, quote). Each read goes through seedRetry: it's a
// quota-free real-time-cache lookup that can lose the subscribe-ack race (see
// seedRetryAttempts above). Failures that survive every retry log and
// continue — a partial seed beats none, and the md core's dedup makes
// overlap harmless.
func (f *OpenDFeed) seed(ctx context.Context, job seedJob) {
	symbol, subs := job.symbol, job.subs
	startedAt := f.clk.Now()
	queueWait := startedAt.Sub(job.enqueuedAt)
	var barsDuration, bookDuration, ticksDuration, quoteDuration time.Duration
	has := func(want feed.SubType) bool {
		for _, s := range subs {
			if s == want {
				return true
			}
		}
		return false
	}
	// Claim every requested subtype before starting I/O. Otherwise a duplicate
	// worker can skip bars, claim ticks, and occupy the second foreground worker
	// while the focused job carrying book waits behind it.
	claimed := make(map[feed.SubType]bool, len(subs))
	for _, sub := range []feed.SubType{feed.SubKL1m, feed.SubBook, feed.SubTicker, feed.SubQuote} {
		if has(sub) {
			claimed[sub] = f.claimSeed(symbol, sub, job.force)
		}
	}
	if has(feed.SubTicker) && !claimed[feed.SubTicker] {
		f.mu.Lock()
		inFlight := f.seedStates[seedKey{symbol: symbol, sub: feed.SubTicker}].inFlight
		f.mu.Unlock()
		if !inFlight {
			f.finishTickerSeed(ctx, symbol, nil, true)
		}
	}
	if claimed[feed.SubKL1m] {
		start := f.clk.Now()
		bars, err := seedRetry(ctx, f.clk, func() ([]feed.Bar, error) {
			return f.bf.cachedBars1m(ctx, symbol, maxAPIRows)
		})
		barsDuration = f.clk.Now().Sub(start)
		f.finishSeed(symbol, feed.SubKL1m, err == nil)
		if err != nil {
			slog.Warn("seed bars1m failed", "symbol", symbol, "err", err)
		} else if len(bars) > 0 {
			f.emit(ctx, feed.Bars1mEvent{Bars: bars, Seed: true})
		}
	}
	if claimed[feed.SubBook] {
		start := f.clk.Now()
		book, err := seedRetry(ctx, f.clk, func() (feed.Book, error) {
			return f.bf.bookSnapshot(ctx, symbol)
		})
		bookDuration = f.clk.Now().Sub(start)
		f.finishSeed(symbol, feed.SubBook, err == nil)
		if err != nil {
			slog.Warn("seed book failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.BookEvent{Book: book, Seed: true})
		}
	}
	if claimed[feed.SubTicker] {
		start := f.clk.Now()
		ticks, err := seedRetry(ctx, f.clk, func() ([]feed.Tick, error) {
			return f.bf.recentTicks(ctx, symbol, maxAPIRows)
		})
		ticksDuration = f.clk.Now().Sub(start)
		if err != nil {
			slog.Warn("seed ticks failed", "symbol", symbol, "err", err)
		}
		f.finishTickerSeed(ctx, symbol, ticks, err == nil)
	}
	if claimed[feed.SubQuote] {
		start := f.clk.Now()
		q, err := seedRetry(ctx, f.clk, func() (feed.Quote, error) {
			return f.bf.quoteSnapshot(ctx, symbol)
		})
		quoteDuration = f.clk.Now().Sub(start)
		f.finishSeed(symbol, feed.SubQuote, err == nil)
		if err != nil {
			slog.Warn("seed quote failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.QuoteEvent{Quote: q, Seed: true})
		}
	}
	if barsDuration == 0 && bookDuration == 0 && ticksDuration == 0 && quoteDuration == 0 {
		return
	}
	log := slog.Debug
	lane := "foreground"
	if job.background {
		lane = "background"
	}
	log("seed timing", "symbol", symbol, "lane", lane,
		"queueWait", queueWait, "bars", barsDuration, "book", bookDuration, "ticks", ticksDuration,
		"quote", quoteDuration, "total", f.clk.Now().Sub(job.enqueuedAt))
}

// CachedDaily waits for K_DAY subscription success, then issues one cache read.
// Concurrent chart loads for one symbol share that request.
func (f *OpenDFeed) CachedDaily(ctx context.Context, symbol string) ([]feed.Bar, error) {
	v, err, _ := f.dailyCacheGroup.Do(symbol, func() (any, error) {
		if err := f.sub.WaitActive(ctx, subKey{Symbol: symbol, Sub: feed.SubKLDay}); err != nil {
			return nil, err
		}
		return seedRetry(ctx, f.clk, func() ([]feed.Bar, error) {
			return f.bf.cachedDaily(ctx, symbol)
		})
	})
	if err != nil {
		return nil, err
	}
	return v.([]feed.Bar), nil
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

// Tail1m returns the quota-free recent 1m window (≤1,000 bars) from moomoo's
// Qot_GetKL cache. It requires an active K_1M subscription; OpenD rejects the
// read otherwise, surfacing as an error the backfill orchestrator treats as
// "skip the tail step". Implements backfill.TailFetcher.
func (f *OpenDFeed) Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error) {
	if err := f.sub.WaitActive(ctx, subKey{Symbol: symbol, Sub: feed.SubKL1m}); err != nil {
		return nil, err
	}
	return seedRetry(ctx, f.clk, func() ([]feed.Bar, error) {
		return f.bf.cachedBars1m(ctx, symbol, maxAPIRows)
	})
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
	start := time.Now()
	_, err, shared := f.validateGroup.Do(symbol, func() (any, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := f.bf.securityExists(probeCtx, symbol); err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.validated[symbol] = struct{}{}
		f.mu.Unlock()
		return struct{}{}, nil
	})
	slog.Info("symbol validation timing", "symbol", symbol, "elapsed", time.Since(start).Round(time.Millisecond), "shared", shared, "err", err)
	return err
}

var _ feed.Feed = (*OpenDFeed)(nil)
