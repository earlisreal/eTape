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

	mu     sync.Mutex
	latest wsmsg.QuotaInfo
	hasVal bool
	seq    int64
}

func New(cfg Config, r requester, pub Publisher, clk clock.Clock) *Poller {
	return &Poller{r: r, pub: pub, clk: clk, m: newMachine(cfg.SubWarnHeadroom, cfg.HistWarnRemain)}
}

// Latest returns the most recent snapshot; ok=false until the first successful
// poll (the health poller then omits Quota, hiding the UI section).
func (p *Poller) Latest() (wsmsg.QuotaInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest, p.hasVal
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
		}
	}
}

// poll performs one quota read + state-machine step. A failed read (OpenD
// down/timeout) skips the tick and holds the last state — no event spam; the
// engine-moomoo health link already reports the feed being down.
func (p *Poller) poll(ctx context.Context) {
	si, err := readSubInfo(ctx, p.r)
	if err != nil {
		slog.Debug("quota: sub-info read failed; holding state", "err", err)
		return
	}
	histUsed, histRemain, err := readHistoryQuota(ctx, p.r)
	if err != nil {
		slog.Debug("quota: history-quota read failed; holding state", "err", err)
		return
	}
	events := p.m.step(reading{subRemain: si.remain, foreign: si.foreign, histRemain: histRemain})

	p.mu.Lock()
	p.latest = wsmsg.QuotaInfo{
		SubUsed: si.totalUsed, SubRemain: si.remain, SubOwn: si.own, SubForeign: si.foreign,
		HistUsed: histUsed, HistRemain: histRemain,
		State: string(p.m.sub), HistState: string(p.m.hist),
	}
	p.hasVal = true
	p.mu.Unlock()

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
