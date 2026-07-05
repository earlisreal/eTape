package replay

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestFeedEmitsInSeqOrderAndAdvancesClock(t *testing.T) {
	rows := []store.JournalRow{
		{Seq: 1, TsExch: 1000, Kind: "conn_up", Event: feed.ConnUpEvent{}},
		{Seq: 2, TsExch: 2000, Kind: "ticks", Event: feed.TicksEvent{Ticks: []feed.Tick{{Symbol: "US.AAPL", TsMs: 2000, Price: 10}}}},
		{Seq: 3, TsExch: 3000, Kind: "resynced", Event: feed.ResyncedEvent{}},
	}
	sim := NewClock(time.UnixMilli(1000))
	f := NewFeed(FeedOptions{Rows: rows, Sim: sim, Speed: 0}) // no throttle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	var got []feed.Event
	for ev := range f.Events() { // closes at end-of-journal
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("emitted %d events, want 3", len(got))
	}
	if _, ok := got[0].(feed.ConnUpEvent); !ok {
		t.Fatalf("first event = %T, want ConnUpEvent", got[0])
	}
	if _, ok := got[1].(feed.TicksEvent); !ok {
		t.Fatalf("second event = %T, want TicksEvent", got[1])
	}
	if sim.Now().UnixMilli() != 3000 {
		t.Fatalf("sim clock = %d, want 3000 (advanced to last event)", sim.Now().UnixMilli())
	}
}

func TestFeedQueriesUnsupported(t *testing.T) {
	f := NewFeed(FeedOptions{Sim: NewClock(time.UnixMilli(0))})
	if _, err := f.RecentTicks(context.Background(), "US.AAPL", 10); err != ErrUnsupported {
		t.Fatalf("RecentTicks err = %v, want ErrUnsupported", err)
	}
	if _, err := f.BookSnapshot(context.Background(), "US.AAPL"); err != ErrUnsupported {
		t.Fatalf("BookSnapshot err = %v, want ErrUnsupported", err)
	}
}
