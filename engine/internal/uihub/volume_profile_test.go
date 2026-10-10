package uihub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestVolumeProfileQueryIsAsyncAndUnavailableWithoutArchive(t *testing.T) {
	q := newQueries(&spyFills{}, clock.NewFake(time.Now()))
	replies := make(chan any, 1)
	if !q.handleAsync(context.Background(), "QueryVolumeProfile", json.RawMessage(`{"symbol":"US.TEST","timeframe":"1m","fromMs":1791540000000,"toMs":1791540060000,"rows":100,"valueArea":70}`), func(value any) { replies <- value }) {
		t.Fatal("profile query must be asynchronous")
	}
	select {
	case reply := <-replies:
		got := reply.(wsmsg.QueryVolumeProfileResult)
		if got.Status != "unavailable" || len(got.Rows) != 0 || got.POC != nil || got.VAH != nil || got.VAL != nil {
			t.Fatalf("unavailable: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("query did not settle")
	}
}

func TestVolumeProfileQueryReadsCommittedPrintsAndRejectsInvalidSelections(t *testing.T) {
	const ts int64 = 1791540000000
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	tick := feed.Tick{Symbol: "US.TEST", Seq: 1, TsMs: ts, Price: 1.2345, Volume: 17, Condition: feed.TradeConditionOddLot}
	if !s.Record(feed.Recording{Kind: "print", Symbol: tick.Symbol, TimeMs: ts, Data: struct{ Normalized feed.Tick }{tick}}) {
		t.Fatal("record rejected")
	}
	deadline := time.Now().Add(time.Second)
	for s.Stats().Committed == 0 {
		if time.Now().After(deadline) {
			t.Fatal("commit timeout")
		}
		time.Sleep(time.Millisecond)
	}
	q := newQueries(&spyFills{}, clock.NewFake(time.UnixMilli(ts+50)))
	q.profileReader = tickstore.NewProfileReader(dir, s)
	args := wsmsg.QueryVolumeProfileArgs{Symbol: "US.TEST", Timeframe: "1m", FromMs: ts, ToMs: ts + 100, Rows: 100, ValueArea: 70}
	raw, _ := json.Marshal(args)
	got := q.volumeProfile(context.Background(), raw)
	deadline = time.Now().Add(10 * time.Second)
	for (got.Status == "too_large" || got.Status == "busy") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		got = q.volumeProfile(context.Background(), raw)
	}
	if got.Status != "ready" || got.CapturedVolume != 17 || !got.Partial || got.POC == nil || *got.POC != 1.2345 || got.Selection != args {
		t.Fatalf("committed query: %+v", got)
	}
	args.Timeframe = "D"
	raw, _ = json.Marshal(args)
	if got := q.volumeProfile(context.Background(), raw); got.Status != "invalid" || len(got.Rows) != 0 {
		t.Fatalf("unsupported: %+v", got)
	}
}
