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

type AccountHealthSource interface {
	Latest() (rtt time.Duration, ok, active bool)
	Changes() <-chan struct{}
}

type pingSource interface {
	LastPingRTT() (time.Duration, bool)
}

// QuotaSource supplies the latest account-quota snapshot for embedding in
// sys.health. Satisfied by *quota.Poller; nil when the quota poller is absent.
type QuotaSource interface {
	Latest() (wsmsg.QuotaInfo, bool)
}

type Poller struct {
	cfg     config.Health
	pub     Publisher
	clk     clock.Clock
	probe   prober
	account AccountHealthSource
	pings   pingSource
	hasTZ   bool
	quota   QuotaSource
	seq     int64
}

func New(cfg config.Health, pub Publisher, clk clock.Clock, probe prober, pings pingSource, hasTZ bool, account AccountHealthSource, quota QuotaSource) *Poller {
	return &Poller{cfg: cfg, pub: pub, clk: clk, probe: probe, pings: pings, hasTZ: hasTZ, account: account, quota: quota}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.ProbeMs) * time.Millisecond)
	defer tick.Stop()
	var uiRTT, moomooRTT *time.Duration
	var quotaInfo *wsmsg.QuotaInfo
	publish := func() {
		var alpacaRTT *time.Duration
		hasAlpaca := false
		if p.account != nil {
			rtt, ok, active := p.account.Latest()
			hasAlpaca = active
			if active && ok {
				alpacaRTT = &rtt
			}
		}
		p.pub.Publish(wsmsg.TopicSysHealth, "", buildHealth(uiRTT, moomooRTT, alpacaRTT, p.hasTZ, hasAlpaca, quotaInfo))
	}
	probeAndPublish := func() {
		uiRTT = nil
		if p.pings != nil {
			if d, ok := p.pings.LastPingRTT(); ok {
				uiRTT = &d
			}
		}
		moomooRTT = nil
		if p.probe != nil {
			if d, err := p.probe.ProbeRTT(ctx); err == nil {
				moomooRTT = &d
			}
		}
		quotaInfo = nil
		if p.quota != nil {
			if qi, ok := p.quota.Latest(); ok {
				quotaInfo = &qi
			}
		}
		publish()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			probeAndPublish()
		case <-p.accountChanges():
			publish()
		}
	}
}

func (p *Poller) accountChanges() <-chan struct{} {
	if p.account == nil {
		return nil
	}
	return p.account.Changes()
}

// Event appends and publishes a sys.events item. main also persists it via store.
func (p *Poller) Event(kind, detail string) {
	p.seq++
	p.pub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
		Seq: p.seq, Ts: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Kind: kind, Detail: detail,
	})
}

func buildHealth(uiRTT, moomooRTT, alpacaRTT *time.Duration, hasTZ, hasAlpaca bool, quota *wsmsg.QuotaInfo) wsmsg.HealthSnapshot {
	links := []wsmsg.HealthLink{
		linkFor("ui-engine", uiRTT),
		linkFor("engine-moomoo", moomooRTT),
	}
	if hasTZ {
		links = append(links, linkFor("engine-tz", nil)) // TZ RTT surfaced later from exec; down until wired
	}
	if hasAlpaca {
		links = append(links, linkFor("engine-alpaca", alpacaRTT))
	}
	return wsmsg.HealthSnapshot{Links: links, Quota: quota}
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
