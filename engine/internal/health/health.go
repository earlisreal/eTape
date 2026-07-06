// Package health emits sys.health (link RTTs) and sys.events (connects/gaps/etc.).
package health

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type prober interface {
	ProbeRTT(ctx context.Context) (time.Duration, error)
}

type pingSource interface {
	LastPingRTT() (time.Duration, bool)
}

type Poller struct {
	cfg   config.Health
	pub   Publisher
	clk   clock.Clock
	probe prober
	pings pingSource
	hasTZ bool
	seq   int64
}

func New(cfg config.Health, pub Publisher, clk clock.Clock, probe prober, pings pingSource, hasTZ bool) *Poller {
	return &Poller{cfg: cfg, pub: pub, clk: clk, probe: probe, pings: pings, hasTZ: hasTZ}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.ProbeMs) * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			var mo *time.Duration
			if p.probe != nil {
				if d, err := p.probe.ProbeRTT(ctx); err == nil {
					mo = &d
				}
			}
			var ui *time.Duration
			if p.pings != nil {
				if d, ok := p.pings.LastPingRTT(); ok {
					ui = &d
				}
			}
			p.pub.Publish(wsmsg.TopicSysHealth, "", buildHealth(ui, mo, p.hasTZ))
		}
	}
}

// Event appends and publishes a sys.events item. main also persists it via store.
func (p *Poller) Event(kind, detail string) {
	p.seq++
	p.pub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
		Seq: p.seq, Ts: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Kind: kind, Detail: detail,
	})
}

func buildHealth(uiRTT, moomooRTT *time.Duration, hasTZ bool) wsmsg.HealthSnapshot {
	links := []wsmsg.HealthLink{
		linkFor("ui-engine", uiRTT),
		linkFor("engine-moomoo", moomooRTT),
	}
	if hasTZ {
		links = append(links, linkFor("engine-tz", nil)) // TZ RTT surfaced later from exec; down until wired
	}
	return wsmsg.HealthSnapshot{Links: links}
}

func linkFor(name string, rtt *time.Duration) wsmsg.HealthLink {
	if rtt == nil {
		return wsmsg.HealthLink{Link: wsmsg.LinkName(name), Status: wsmsg.LinkDown}
	}
	ms := float64(rtt.Microseconds()) / 1000.0
	status := wsmsg.LinkOK
	switch {
	case ms >= 2000:
		status = wsmsg.LinkDown
	case ms >= 500:
		status = wsmsg.LinkDegraded
	}
	return wsmsg.HealthLink{Link: wsmsg.LinkName(name), Ms: &ms, Status: status}
}
