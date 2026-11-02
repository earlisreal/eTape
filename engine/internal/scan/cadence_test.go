package scan

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type cadenceClock struct {
	*clock.Fake
	created chan time.Duration
}

func (c *cadenceClock) NewTicker(d time.Duration) clock.Ticker {
	ticker := c.Fake.NewTicker(d)
	c.created <- d
	return ticker
}

func TestRunUsesIndependentRegularSessionInterval(t *testing.T) {
	clk := &cadenceClock{Fake: clock.NewFake(et(2026, 7, 8, 10, 0)), created: make(chan time.Duration, 2)}
	pub := &capturePub{ranksCh: make(chan wsmsg.ScannerRankPayload, 4)}
	p := New(config.Scan{Enabled: true, PremarketMs: 2000, RTHMs: 3000},
		&fakeReq{rankResp: rankResp(), topMoversRsp: topResp()}, pub, clk, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = p.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for range 2 {
		select {
		case <-clk.created:
		case <-time.After(time.Second):
			t.Fatal("scanner tickers did not start")
		}
	}
	clk.Advance(3 * time.Second)
	waitRank(t, pub.ranksCh)
	clk.Advance(3 * time.Second)
	waitRank(t, pub.ranksCh)
}
