package uihub

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fixedMarketClockSource struct {
	sample MarketClockSample
	ok     bool
}

func (s fixedMarketClockSource) LatestMarketClock(time.Time) (MarketClockSample, bool) {
	return s.sample, s.ok
}

func TestPongIncludesMarketClockSample(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(10_000))
	h := NewHub(clk, HubConfig{}, newMirror(nil, wsmsg.GlobalLimitsView{}, 1, 1, 1, 1, 1))
	sampledAt := time.UnixMilli(9_000)
	h.SetMarketClockSource(fixedMarketClockSource{
		sample: MarketClockSample{OffsetMs: 2_000, SampledAt: sampledAt, RTT: 120 * time.Millisecond},
		ok:     true,
	})

	msg := h.pong(123)
	if msg.EngineTimeMs == nil || *msg.EngineTimeMs != 10_000 {
		t.Fatalf("engineTimeMs=%v, want 10000", msg.EngineTimeMs)
	}
	if msg.MarketOffsetMs == nil || *msg.MarketOffsetMs != 2_000 {
		t.Fatalf("marketOffsetMs=%v, want 2000", msg.MarketOffsetMs)
	}
	if msg.MarketSampleAgeMs == nil || *msg.MarketSampleAgeMs != 1_000 {
		t.Fatalf("marketSampleAgeMs=%v, want 1000", msg.MarketSampleAgeMs)
	}
	if msg.MarketSampleRttMs == nil || *msg.MarketSampleRttMs != 120 {
		t.Fatalf("marketSampleRttMs=%v, want 120", msg.MarketSampleRttMs)
	}
}

func TestPongOmitsClockFieldsWithoutSource(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(10_000))
	h := NewHub(clk, HubConfig{}, newMirror(nil, wsmsg.GlobalLimitsView{}, 1, 1, 1, 1, 1))
	msg := h.pong(123)
	if msg.EngineTimeMs != nil || msg.MarketOffsetMs != nil || msg.MarketSampleAgeMs != nil || msg.MarketSampleRttMs != nil {
		t.Fatalf("clock fields must be omitted without a source: %+v", msg)
	}
}
