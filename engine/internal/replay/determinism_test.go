package replay_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// capBase: 2026-07-06 09:30 ET in ms (all scripted events land on one day).
const capBase = int64(1783344600_000)

// scriptEvents is a deterministic mixed-event day: seed 1m bars, batched ticks,
// a quote, a book, a live 1m bar, a conn/resync cycle, and a re-seed.
func scriptEvents() []feed.Event {
	tk := func(seq, offMs int64, px float64, v int64, d feed.Direction) feed.Tick {
		return feed.Tick{Symbol: "US.AAPL", Seq: seq, TsMs: capBase + offMs, Price: px, Volume: v, Dir: d}
	}
	return []feed.Event{
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: capBase - 120_000, O: 99, H: 99.5, L: 98.9, C: 99.2, Volume: 800},
			{Symbol: "US.AAPL", BucketMs: capBase - 60_000, O: 99.2, H: 100, L: 99.1, C: 99.9, Volume: 900},
		}},
		feed.TicksEvent{Ticks: []feed.Tick{tk(1, 0, 100.0, 120, feed.Buy), tk(2, 1500, 100.2, 80, feed.Sell)}},
		feed.TicksEvent{Ticks: []feed.Tick{tk(3, 12_000, 100.1, 60, feed.Neutral), tk(4, 25_000, 100.4, 200, feed.Buy)}},
		feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", TsMs: capBase + 30_000, Last: 100.4}},
		feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", TsMs: capBase + 30_000,
			Bids: []feed.BookLevel{{Price: 100.39, Volume: 300}},
			Asks: []feed.BookLevel{{Price: 100.41, Volume: 200}}}},
		feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: capBase, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
		feed.ConnDownEvent{}, feed.ConnUpEvent{}, feed.ResyncedEvent{},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: capBase, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
	}
}

// collect runs a fresh core, registers a fixed indicator pair, feeds events via
// feedInto, and returns every update in order. Mirrors md's determinism harness.
func collect(t *testing.T, feedInto func(feedOne func(feed.Event))) []md.Update {
	t.Helper()
	c := md.New(md.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()

	var got []md.Update
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-done:
				for {
					select {
					case u := <-c.Updates():
						got = append(got, u)
					default:
						return
					}
				}
			}
		}
	}()

	c.EnsureIndicator("vwap-1", md.IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: md.IndVWAP})
	c.EnsureIndicator("ema-1", md.IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: md.IndEMA,
		Params: map[string]float64{"period": 2}})
	feedInto(c.Feed)
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	<-collected
	return got
}

func TestReplayJournalMatchesLive(t *testing.T) {
	evs := scriptEvents()

	// Live: feed the scripted events straight into a fresh core.
	live := collect(t, func(feedOne func(feed.Event)) {
		for _, ev := range evs {
			feedOne(ev)
		}
	})

	// Journal round-trip: record → read → replay.Feed → fresh core.
	replayed := collect(t, func(feedOne func(feed.Event)) {
		s, err := store.Open(store.Options{Path: t.TempDir() + "/cap.db"})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()
		for i, ev := range evs {
			s.RecordEvent(ev, capBase+int64(i))
		}
		s.Flush()
		rows, err := s.ReadJournalDay("2026-07-06")
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		// Codec + ordering proof: read-back events equal the recorded ones.
		if len(rows) != len(evs) {
			t.Fatalf("journal rows = %d, want %d", len(rows), len(evs))
		}
		for i := range rows {
			if !reflect.DeepEqual(rows[i].Event, evs[i]) {
				t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, evs[i], rows[i].Event)
			}
		}
		// Drive the replay feed into the core.
		sim := replay.NewClock(time.UnixMilli(rows[0].TsExch))
		rf := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: sim, Speed: 0})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = rf.Run(ctx) }()
		for ev := range rf.Events() { // closes at end-of-journal
			feedOne(ev)
		}
	})

	if len(live) != len(replayed) {
		t.Fatalf("update counts differ: live %d vs replay %d", len(live), len(replayed))
	}
	for i := range live {
		if !reflect.DeepEqual(live[i], replayed[i]) {
			t.Fatalf("update %d differs:\n live: %#v\nrepl: %#v", i, live[i], replayed[i])
		}
	}
}
