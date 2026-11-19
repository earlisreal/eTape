package exec

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	accountPollInterval = time.Second
	accountStaleAfter   = 5
	accountBaselineKey  = "exec.account-baselines.v1"
)

// AccountReader lets an adapter provide an account-only request. Adapters that
// do not implement it fall back to their normal Snapshot and discard the
// positions/orders, which keeps this seam optional for small/test brokers.
type AccountReader interface {
	PollAccount(context.Context) (AccountSnapshot, bool, time.Duration, error)
}

type AccountBaselineStore interface {
	GetConfig(string) (string, bool, error)
	SetConfig(string, string)
}

type AccountBaseline struct {
	CycleStartMs int64   `json:"cycleStartMs"`
	Equity       float64 `json:"equity"`
	Provisional  bool    `json:"provisional"`
}

type AccountPollerConfig struct {
	Brokers      map[VenueID]Broker
	Demands      *AccountDemandRegistry
	RiskVenues   map[VenueID]bool
	HealthVenues map[VenueID]bool
	Store        AccountBaselineStore
	Clock        clock.Clock
	Interval     time.Duration
	Emit         func(BrokerEvent)
}

// AccountPoller drives one account request per distinct demanded/risk venue.
// Live risk venues are always included; display demand controls only whether a
// non-risk venue (for example a paper account) is polled.
type AccountPoller struct {
	clk          clock.Clock
	interval     time.Duration
	brokers      map[VenueID]Broker
	demands      *AccountDemandRegistry
	risk         map[VenueID]bool
	healthVenues map[VenueID]bool
	store        AccountBaselineStore
	emit         func(BrokerEvent)

	mu           sync.RWMutex
	healthOK     bool
	healthRTT    time.Duration
	healthActive bool
	changes      chan struct{}

	baselines map[VenueID]AccountBaseline
	last      map[VenueID]AccountSnapshot
	failures  map[VenueID]int
	fresh     map[VenueID]bool
	attempted map[VenueID]bool
}

func NewAccountPoller(cfg AccountPollerConfig) *AccountPoller {
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System{}
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = accountPollInterval
	}
	brokers := make(map[VenueID]Broker, len(cfg.Brokers))
	for v, b := range cfg.Brokers {
		brokers[v] = b
	}
	risk := make(map[VenueID]bool, len(cfg.RiskVenues))
	for v, on := range cfg.RiskVenues {
		if on {
			risk[v] = true
		}
	}
	healthVenues := make(map[VenueID]bool, len(cfg.HealthVenues))
	for v, on := range cfg.HealthVenues {
		if on {
			healthVenues[v] = true
		}
	}
	p := &AccountPoller{
		clk: clk, interval: interval, brokers: brokers, demands: cfg.Demands,
		risk: risk, healthVenues: healthVenues, store: cfg.Store, emit: cfg.Emit,
		changes: make(chan struct{}, 1), baselines: map[VenueID]AccountBaseline{},
		last:     map[VenueID]AccountSnapshot{},
		failures: map[VenueID]int{}, fresh: map[VenueID]bool{},
		attempted: map[VenueID]bool{},
	}
	p.loadBaselines()
	p.healthActive = len(healthVenues) > 0
	return p
}

func (p *AccountPoller) Latest() (rtt time.Duration, ok, active bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.healthRTT, p.healthOK, p.healthActive
}

func (p *AccountPoller) Changes() <-chan struct{} { return p.changes }

func (p *AccountPoller) notifyHealth() {
	select {
	case p.changes <- struct{}{}:
	default:
	}
}

func (p *AccountPoller) loadBaselines() {
	if p.store == nil {
		return
	}
	raw, ok, err := p.store.GetConfig(accountBaselineKey)
	if err != nil || !ok || raw == "" {
		return
	}
	var saved map[VenueID]AccountBaseline
	if json.Unmarshal([]byte(raw), &saved) == nil {
		for v, baseline := range saved {
			p.baselines[v] = baseline
		}
	}
}

func (p *AccountPoller) saveBaselines() {
	if p.store == nil {
		return
	}
	raw, err := json.Marshal(p.baselines)
	if err == nil {
		p.store.SetConfig(accountBaselineKey, string(raw))
	}
}

func (p *AccountPoller) targets() []VenueID {
	set := map[VenueID]bool{}
	for v := range p.risk {
		set[v] = true
	}
	if p.demands != nil {
		for _, v := range p.demands.Venues() {
			set[v] = true
		}
	}
	for v := range p.brokers {
		if p.healthVenues[v] && p.risk[v] {
			set[v] = true
		}
	}
	out := make([]VenueID, 0, len(set))
	for v := range set {
		if broker := p.brokers[v]; broker != nil && !broker.Capabilities().ReconciledPerpetual {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (p *AccountPoller) pollOne(ctx context.Context, venue VenueID, broker Broker) (AccountSnapshot, bool, time.Duration, error) {
	start := time.Now()
	if reader, ok := broker.(AccountReader); ok {
		acct, admitted, rtt, err := reader.PollAccount(ctx)
		return acct, admitted, rtt, err
	}
	acct, _, _, err := broker.Snapshot(ctx)
	return acct, true, time.Since(start), err
}

// pollOnce is kept synchronous so tests and the Run loop share the exact same
// target/dedup/baseline behavior.
func (p *AccountPoller) pollOnce(ctx context.Context) {
	for _, venue := range p.targets() {
		broker := p.brokers[venue]
		if _, ok := p.fresh[venue]; !ok {
			// Give a newly demanded venue the same five-interval grace period
			// as a venue known at boot; no account snapshot is fabricated.
			p.fresh[venue] = true
		}
		firstAttempt := !p.attempted[venue]
		acct, admitted, rtt, err := p.pollOne(ctx, venue, broker)
		p.attempted[venue] = true
		if !admitted || err != nil {
			p.failures[venue]++
			if p.healthVenues[venue] {
				p.mu.Lock()
				p.healthOK, p.healthRTT = false, 0
				p.mu.Unlock()
				p.notifyHealth()
			}
			if p.failures[venue] >= accountStaleAfter && p.fresh[venue] {
				p.fresh[venue] = false
				p.emitFresh(venue, false)
			}
			if err != nil {
				slog.Warn("account poll failed", "venue", venue, "err", err)
			}
			continue
		}
		p.failures[venue] = 0
		acct.Venue = venue
		if acct.TsMs == 0 {
			acct.TsMs = p.clk.Now().UnixMilli()
		}
		if broker.Capabilities().AuthoritativeDayPnL {
			if acct.DayPnLSource == "" {
				acct.DayPnLSource = "broker"
			}
		} else if broker.Capabilities().CalculatedDayPnL {
			p.applyCalculatedDayPnL(&acct)
		}
		p.last[venue] = acct
		if p.healthVenues[venue] {
			p.mu.Lock()
			p.healthOK, p.healthRTT = true, rtt
			p.mu.Unlock()
			p.notifyHealth()
		}
		if p.emit != nil {
			p.emit(BrokerAccount{Account: acct})
		}
		if !p.fresh[venue] || firstAttempt {
			p.fresh[venue] = true
			p.emitFresh(venue, true)
		}
	}
}

func (p *AccountPoller) emitFresh(venue VenueID, fresh bool) {
	if p.emit != nil {
		p.emit(BrokerAccountFresh{V: venue, Fresh: fresh})
	}
}

func (p *AccountPoller) applyCalculatedDayPnL(acct *AccountSnapshot) {
	cycle := session.TradingCycleStart(p.clk.Now()).UnixMilli()
	baseline, ok := p.baselines[acct.Venue]
	newBaseline := false
	if !ok || baseline.CycleStartMs != cycle {
		newBaseline = true
		// Prefer a snapshot taken immediately before the scheduled close. This
		// preserves the prior-close equity instead of treating the first
		// post-close poll as the baseline. A persisted non-provisional baseline
		// is the restart path when no in-memory pre-close snapshot exists.
		captured := false
		previousCycle := session.TradingCycleStart(time.UnixMilli(cycle).Add(-time.Millisecond)).UnixMilli()
		if last, exists := p.last[acct.Venue]; exists && last.TsMs < cycle &&
			cycle-last.TsMs <= int64((5*p.interval).Milliseconds()) &&
			session.TradingCycleStart(time.UnixMilli(last.TsMs)).UnixMilli() == previousCycle {
			baseline = AccountBaseline{CycleStartMs: cycle, Equity: last.Equity}
			captured = true
		} else if ok && baseline.CycleStartMs == previousCycle && !baseline.Provisional {
			baseline.CycleStartMs = cycle
			captured = true
		}
		if !captured {
			baseline = AccountBaseline{CycleStartMs: cycle, Equity: acct.Equity, Provisional: true}
		}
		p.baselines[acct.Venue] = baseline
		p.saveBaselines()
	}
	acct.SodEquity = baseline.Equity
	if newBaseline && baseline.Provisional {
		acct.DayPnL = 0
		acct.DayPnLSource = "calculated"
		acct.DayPnLProvisional = true
		return
	}
	acct.DayPnL = acct.Equity - baseline.Equity - acct.NetCashFlow
	acct.DayPnLSource = "calculated"
	acct.DayPnLProvisional = baseline.Provisional
}

func (p *AccountPoller) Run(ctx context.Context) error {
	tick := p.clk.NewTicker(p.interval)
	defer tick.Stop()
	p.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.pollOnce(ctx)
		}
	}
}

// AccountSourceName normalizes labels used by UI tooltips and old snapshots.
func AccountSourceName(source string) string {
	if strings.EqualFold(source, "broker") {
		return "Broker reported"
	}
	if strings.EqualFold(source, "calculated") {
		return "Calculated"
	}
	return ""
}
