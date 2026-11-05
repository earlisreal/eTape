package screener

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type Publisher interface {
	Publish(wsmsg.Topic, string, any)
}
type job struct {
	state        wsmsg.ScreenerState
	cancel       context.CancelFunc
	active       bool
	nextProgress time.Time
}
type cachedHistory struct {
	date    string
	listing time.Time
	at      time.Time
	bars    []feed.Bar
}

type Service struct {
	ctx       context.Context
	clk       clock.Clock
	source    Source
	pub       Publisher
	mu        sync.Mutex
	jobs      map[string]*job
	seq       uint64
	gate      chan struct{}
	wg        sync.WaitGroup
	cache     map[string]cachedHistory // accessed only while holding gate
	cacheBars int
	stopped   bool
}

func New(ctx context.Context, clk clock.Clock, source Source, pub Publisher) *Service {
	return &Service{ctx: ctx, clk: clk, source: source, pub: pub, jobs: map[string]*job{}, gate: make(chan struct{}, 1), cache: map[string]cachedHistory{}}
}

var panelIdentity = regexp.MustCompile(`^[a-zA-Z0-9/-]{1,140}$`)

func (s *Service) Start(args wsmsg.RunScreenerArgs) error {
	if s.source == nil {
		return errors.New("screener requires Yahoo daily history: enable Yahoo in Data settings and restart the engine")
	}
	if !panelIdentity.MatchString(args.PanelID) {
		return errors.New("invalid Screener panel identity")
	}
	if err := Validate(args.Filters); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.ctx.Err() != nil {
		return errors.New("screener is shutting down")
	}
	if old := s.jobs[args.PanelID]; old != nil {
		old.cancel()
	}
	if s.jobs[args.PanelID] == nil && len(s.jobs) >= 32 {
		var oldest *job
		for _, j := range s.jobs {
			if !j.active && (oldest == nil || j.state.RunID < oldest.state.RunID) {
				oldest = j
			}
		}
		if oldest == nil {
			return errors.New("too many queued screens; cancel an existing screen first")
		}
		removed := oldest.state
		removed.Status = "removed"
		removed.Rows = []wsmsg.ScreenerRow{}
		s.pub.Publish(wsmsg.TopicScreenerState, removed.PanelID, removed)
		delete(s.jobs, removed.PanelID)
	}
	s.seq++
	ctx, cancel := context.WithCancel(s.ctx)
	filters := args.Filters
	if filters.MinChangePct != nil {
		v := *filters.MinChangePct
		filters.MinChangePct = &v
	}
	if filters.MaxChangePct != nil {
		v := *filters.MaxChangePct
		filters.MaxChangePct = &v
	}
	j := &job{cancel: cancel, active: true, state: wsmsg.ScreenerState{PanelID: args.PanelID, RunID: s.seq, Status: "queued", AsOf: CompletedDate(s.clk.Now()).Format("2006-01-02"), StartedAt: s.clk.Now().UTC().Format(time.RFC3339), Filters: filters, Rows: []wsmsg.ScreenerRow{}}}
	s.jobs[args.PanelID] = j
	s.publish(j)
	s.wg.Add(1)
	go func() { defer s.wg.Done(); defer cancel(); s.run(ctx, j) }()
	return nil
}

func (s *Service) Cancel(panelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[panelID]; j != nil {
		j.cancel()
	}
}
func (s *Service) Stop() {
	s.mu.Lock()
	s.stopped = true
	for _, j := range s.jobs {
		j.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *Service) publish(j *job) {
	state := j.state
	state.Rows = slices.Clone(j.state.Rows)
	if state.Rows == nil {
		state.Rows = []wsmsg.ScreenerRow{}
	}
	s.pub.Publish(wsmsg.TopicScreenerState, state.PanelID, state)
}

func (s *Service) update(j *job, fn func(*wsmsg.ScreenerState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[j.state.PanelID] != j {
		return
	}
	fn(&j.state)
	if j.state.Status == "running" && j.state.Checked > 0 && s.clk.Now().Before(j.nextProgress) {
		return
	}
	j.nextProgress = s.clk.Now().Add(250 * time.Millisecond)
	s.publish(j)
}

func (s *Service) finish(ctx context.Context, j *job, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[j.state.PanelID] != j {
		return
	}
	j.active = false
	j.state.Status = "complete"
	if err != nil {
		j.state.Status = "error"
		j.state.Error = err.Error()
	}
	if ctx.Err() != nil {
		j.state.Status = "canceled"
		j.state.Error = ""
	}
	j.state.FinishedAt = s.clk.Now().UTC().Format(time.RFC3339)
	s.publish(j)
}

func (s *Service) run(ctx context.Context, j *job) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		s.finish(ctx, j, ctx.Err())
		return
	}
	if ctx.Err() != nil {
		s.finish(ctx, j, ctx.Err())
		return
	}
	s.update(j, func(state *wsmsg.ScreenerState) { state.Status = "discovering" })
	date, err := time.ParseInLocation("2006-01-02", j.state.AsOf, session.Loc())
	if err != nil {
		s.finish(ctx, j, err)
		return
	}
	discovery, err := s.source.Discover(ctx, j.state.Filters, date, s.clk.Now())
	if err != nil {
		s.finish(ctx, j, err)
		return
	}
	s.update(j, func(state *wsmsg.ScreenerState) {
		state.Status = "running"
		state.Universe = discovery.Universe
		state.Candidates = len(discovery.Stocks)
		state.Warning = discovery.Warning
	})
	for batch := range slices.Chunk(discovery.Stocks, 64) {
		if ctx.Err() != nil {
			s.finish(ctx, j, ctx.Err())
			return
		}
		var batchBars map[string][]feed.Bar
		if discovery.BatchDaily != nil {
			batchBars, err = discovery.BatchDaily(ctx, batch)
			if err != nil && ctx.Err() == nil {
				slog.Warn("screener: Alpaca SIP history unavailable", "err", err)
				discovery.BatchDaily = nil
				s.update(j, func(state *wsmsg.ScreenerState) {
					state.Warning = "OpenD unavailable. Using Yahoo Finance for stock discovery. Delayed Alpaca SIP is unavailable for the remaining stocks, so the screen will use Yahoo daily history."
				})
			}
		}
		for _, stock := range batch {
			if ctx.Err() != nil {
				s.finish(ctx, j, ctx.Err())
				return
			}
			row, match, evalErr := s.evaluateStock(ctx, j, stock, date, batchBars[stock.Symbol])
			s.update(j, func(state *wsmsg.ScreenerState) {
				state.Checked++
				if evalErr != nil {
					state.Unavailable++
				} else if match {
					state.Rows = append(state.Rows, row)
				}
			})
		}
	}
	s.finish(ctx, j, nil)
}

func (s *Service) evaluateStock(ctx context.Context, j *job, stock Instrument, date time.Time, batchBars []feed.Bar) (wsmsg.ScreenerRow, bool, error) {
	if len(batchBars) > 0 {
		if row, match, err := Evaluate(j.state.Filters, stock, batchBars, date); err == nil {
			return row, match, nil
		}
	}
	cached, ok := s.cache[stock.Symbol]
	if ok && cached.date == j.state.AsOf && cached.listing.Equal(stock.ListingDate) && s.clk.Now().Sub(cached.at) < 15*time.Minute {
		if row, match, err := Evaluate(j.state.Filters, stock, cached.bars, date); err == nil {
			return row, match, nil
		}
	}
	if ok {
		s.cacheBars -= len(cached.bars)
		delete(s.cache, stock.Symbol)
	}
	bars, err := s.source.Daily(ctx, stock, date)
	if err != nil {
		return wsmsg.ScreenerRow{}, false, err
	}
	row, match, err := Evaluate(j.state.Filters, stock, bars, date)
	if err == nil && len(bars) <= 500_000 {
		for len(s.cache) > 0 && (len(s.cache) >= 512 || s.cacheBars+len(bars) > 500_000) {
			for key, old := range s.cache {
				s.cacheBars -= len(old.bars)
				delete(s.cache, key)
				break
			}
		}
		s.cache[stock.Symbol] = cachedHistory{date: j.state.AsOf, listing: stock.ListingDate, at: s.clk.Now(), bars: slices.Clone(bars)}
		s.cacheBars += len(bars)
	}
	return row, match, err
}
