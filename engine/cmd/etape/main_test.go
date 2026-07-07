package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
)

type recordingSink struct {
	mu    sync.Mutex
	marks map[string]float64
}

func (r *recordingSink) SetMark(sym string, px float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.marks == nil {
		r.marks = map[string]float64{}
	}
	r.marks[sym] = px
}

func (r *recordingSink) get(sym string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.marks[sym]
	return v, ok
}

func TestMarkBridgeForwardsToSinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	core := md.New(md.Config{TapeRing: 1024, AnchorSecs: 9*3600 + 30*60})
	go func() { _ = core.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim-paper"}, Clock: clock.System{},
		Brokers: map[exec.VenueID]exec.Broker{}, IDGen: exec.NewOrderIDGen(clock.System{}, nil),
	})
	go func() { _ = execCore.Run(ctx) }()

	sink := &recordingSink{}
	go markBridge(ctx, core, execCore, []markSink{sink})

	core.Feed(feed.TicksEvent{Ticks: []feed.Tick{{
		Symbol: "US.AAPL", TsMs: time.Now().UnixMilli(), Price: 191.23, Volume: 100,
	}}})

	deadline := time.After(2 * time.Second)
	for {
		if v, ok := sink.get("US.AAPL"); ok {
			if v != 191.23 {
				t.Fatalf("mark = %v, want 191.23", v)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("sink never received a mark")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
