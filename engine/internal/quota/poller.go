package quota

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// pollInterval is the quota poll cadence — a code constant, well under
// moomoo's request rate limits; not configurable until a reason exists.
const pollInterval = 60 * time.Second

// Publisher emits sys.events (satisfied by *uihub.Hub), mirroring health.
type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

// Config holds the two warn thresholds (from the [feed] config block).
type Config struct {
	SubWarnHeadroom int
	HistWarnRemain  int
}

// Poller polls account quota every pollInterval, runs the state machine, emits
// a leveled sys.event per transition, and exposes the latest snapshot for the
// health poller to embed in sys.health.
type Poller struct {
	r   requester
	pub Publisher
	clk clock.Clock
	m   *machine

	mu                       sync.Mutex
	latest                   wsmsg.QuotaInfo
	hasVal                   bool
	seq                      int64
	observer                 func(wsmsg.QuotaInfo, time.Time)
	subscriptionObserver     func(int, time.Time)
	historyObserver          func(int, time.Time)
	beginSubscriptionRefresh func(context.Context) error
	endSubscriptionRefresh   func()
	beginHistoryRefresh      func(context.Context) error
	endHistoryRefresh        func()
	poke                     chan struct{}
}

func New(cfg Config, r requester, pub Publisher, clk clock.Clock) *Poller {
	return &Poller{r: r, pub: pub, clk: clk, m: newMachine(cfg.SubWarnHeadroom, cfg.HistWarnRemain), poke: make(chan struct{}, 1)}
}

// Latest returns the most recent snapshot; ok=false until the first successful
// poll (the health poller then omits Quota, hiding the UI section).
func (p *Poller) Latest() (wsmsg.QuotaInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest, p.hasVal
}

func (p *Poller) SetObserver(observer func(wsmsg.QuotaInfo, time.Time)) {
	p.mu.Lock()
	p.observer = observer
	p.mu.Unlock()
}

func (p *Poller) SetSubscriptionQuotaObserver(observer func(int, time.Time)) {
	p.mu.Lock()
	p.subscriptionObserver = observer
	p.mu.Unlock()
}

func (p *Poller) SetHistoryQuotaObserver(observer func(int, time.Time)) {
	p.mu.Lock()
	p.historyObserver = observer
	p.mu.Unlock()
}

func (p *Poller) SetSubscriptionQuotaRefresh(begin func(context.Context) error, end func()) {
	if begin == nil || end == nil {
		begin, end = nil, nil
	}
	p.mu.Lock()
	p.beginSubscriptionRefresh, p.endSubscriptionRefresh = begin, end
	p.mu.Unlock()
}

func (p *Poller) SetHistoryQuotaRefresh(begin func(context.Context) error, end func()) {
	if begin == nil || end == nil {
		begin, end = nil, nil
	}
	p.mu.Lock()
	p.beginHistoryRefresh, p.endHistoryRefresh = begin, end
	p.mu.Unlock()
}

// Poke requests an immediate quota refresh. Reconnect uses this to validate
// subscription capacity before restoring pushes instead of replaying stale
// subscriptions.
func (p *Poller) Poke() {
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

func (p *Poller) Run(ctx context.Context) error {
	p.poll(ctx) // immediate first poll so the panel populates without a 60s wait
	tick := p.clk.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.poll(ctx)
		case <-p.poke:
			p.poll(ctx)
		}
	}
}

// poll performs one quota read + state-machine step. A failed read (OpenD
// down/timeout) skips the tick and holds the last state — no event spam; the
// engine-moomoo health link already reports the feed being down.
func (p *Poller) poll(ctx context.Context) {
	p.mu.Lock()
	beginRefresh, endRefresh := p.beginSubscriptionRefresh, p.endSubscriptionRefresh
	beginHistoryRefresh, endHistoryRefresh := p.beginHistoryRefresh, p.endHistoryRefresh
	subscriptionObserver, historyObserver := p.subscriptionObserver, p.historyObserver
	p.mu.Unlock()
	refreshStarted := false
	if beginRefresh != nil {
		if err := beginRefresh(ctx); err != nil {
			slog.Debug("quota: subscription refresh barrier failed", "err", err)
			return
		}
		refreshStarted = true
	}
	if refreshStarted && endRefresh != nil {
		defer func() {
			if refreshStarted {
				endRefresh()
			}
		}()
	}
	si, err := readSubInfo(ctx, p.r)
	if err != nil {
		slog.Debug("quota: sub-info read failed; holding state", "err", err)
		return
	}
	if subscriptionObserver != nil {
		subscriptionObserver(si.remain, p.clk.Now())
	}
	if refreshStarted && endRefresh != nil {
		endRefresh()
		refreshStarted = false
	}
	historyRefreshStarted := false
	if beginHistoryRefresh != nil {
		if err := beginHistoryRefresh(ctx); err != nil {
			slog.Debug("quota: history refresh barrier failed", "err", err)
			return
		}
		historyRefreshStarted = true
	}
	if historyRefreshStarted && endHistoryRefresh != nil {
		defer func() {
			if historyRefreshStarted {
				endHistoryRefresh()
			}
		}()
	}
	histUsed, histRemain, err := readHistoryQuota(ctx, p.r)
	if err != nil {
		slog.Debug("quota: history-quota read failed; holding state", "err", err)
		return
	}
	if historyObserver != nil {
		historyObserver(histRemain, p.clk.Now())
	}
	if historyRefreshStarted && endHistoryRefresh != nil {
		endHistoryRefresh()
		historyRefreshStarted = false
	}
	events := p.m.step(reading{subRemain: si.remain, foreign: si.foreign, histRemain: histRemain})

	p.mu.Lock()
	p.latest = wsmsg.QuotaInfo{
		SubUsed: si.totalUsed, SubRemain: si.remain, SubOwn: si.own, SubForeign: si.foreign,
		HistUsed: histUsed, HistRemain: histRemain,
		State: string(p.m.sub), HistState: string(p.m.hist),
	}
	p.hasVal = true
	latest, observer := p.latest, p.observer
	p.mu.Unlock()
	if observer != nil {
		observer(latest, p.clk.Now())
	}

	for _, t := range events {
		p.emit(t)
	}
}

func (p *Poller) emit(t transition) {
	p.seq++
	p.pub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
		Seq:  p.seq,
		Ts:   p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Kind: eventKind, Detail: t.detail, Level: t.level,
	})
}
