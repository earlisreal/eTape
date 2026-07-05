package store

import (
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// every representative event, including the Seed flag and slice batches.
func sampleEvents() []feed.Event {
	return []feed.Event{
		feed.TicksEvent{Seed: false, Ticks: []feed.Tick{
			{Symbol: "US.AAPL", Seq: 1, TsMs: 1_700_000_000_000, Price: 309.12, Volume: 100, Turnover: 30912, Dir: feed.Buy, RecvTsMs: 1_700_000_000_050},
			{Symbol: "US.AAPL", Seq: 2, TsMs: 1_700_000_001_000, Price: 309.10, Volume: 50, Dir: feed.Sell},
		}},
		feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
			{Symbol: "US.MSFT", Seq: 10, TsMs: 1_700_000_002_000, Price: 400, Volume: 5, Dir: feed.Neutral},
		}},
		feed.QuoteEvent{Seed: false, Quote: feed.Quote{Symbol: "US.AAPL", TsMs: 1_700_000_003_000, Last: 309.2, Open: 300, High: 310, Low: 299, PrevClose: 301, Volume: 12345, Turnover: 3_800_000}},
		feed.QuoteEvent{Seed: true, Quote: feed.Quote{Symbol: "US.MSFT", TsMs: 1_700_000_004_000, Last: 401}},
		feed.BookEvent{Seed: false, Book: feed.Book{Symbol: "US.AAPL", TsMs: 1_700_000_005_000,
			Bids: []feed.BookLevel{{Price: 309.1, Volume: 300, Orders: 4}, {Price: 309.0, Volume: 500, Orders: 7}},
			Asks: []feed.BookLevel{{Price: 309.2, Volume: 200, Orders: 3}}}},
		feed.BookEvent{Seed: true, Book: feed.Book{Symbol: "US.MSFT", TsMs: 1_700_000_006_000,
			Bids: []feed.BookLevel{{Price: 400.1, Volume: 100, Orders: 2}},
			Asks: []feed.BookLevel{{Price: 400.2, Volume: 150, Orders: 3}}}},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: 1_700_000_040_000, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000, Turnover: 400000}}},
		feed.Bars1mEvent{Seed: false, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: 1_700_000_100_000, O: 100.4, H: 100.9, L: 100.1, C: 100.8, Volume: 900}}},
		feed.ConnUpEvent{},
		feed.ConnDownEvent{},
		feed.ResyncedEvent{},
	}
}

func TestCodecRoundTrip(t *testing.T) {
	for i, ev := range sampleEvents() {
		payload, err := encodePayload(ev)
		if err != nil {
			t.Fatalf("event %d encode: %v", i, err)
		}
		got, err := decodePayload(eventKind(ev), payload)
		if err != nil {
			t.Fatalf("event %d decode: %v", i, err)
		}
		if !reflect.DeepEqual(ev, got) {
			t.Fatalf("event %d round-trip mismatch:\n in: %#v\nout: %#v", i, ev, got)
		}
	}
}

func TestEventColumnHelpers(t *testing.T) {
	tks := feed.TicksEvent{Ticks: []feed.Tick{{Symbol: "US.AAPL", TsMs: 555}}}
	if eventKind(tks) != kindTicks || eventSymbol(tks) != "US.AAPL" || eventExchTs(tks, 9) != 555 {
		t.Fatalf("ticks helpers wrong: %s %s %d", eventKind(tks), eventSymbol(tks), eventExchTs(tks, 9))
	}
	if eventSeed(feed.QuoteEvent{Seed: true}) != true {
		t.Fatal("QuoteEvent seed not read")
	}
	cu := feed.ConnUpEvent{}
	if eventKind(cu) != kindConnUp || eventSymbol(cu) != "" || eventExchTs(cu, 42) != 42 {
		t.Fatalf("conn helpers wrong: %s %q %d", eventKind(cu), eventSymbol(cu), eventExchTs(cu, 42))
	}
}

func TestEventColumnHelpersEmptyBatch(t *testing.T) {
	// Test empty-batch fallback paths: eventSymbol returns "", eventExchTs returns fallback.
	emptyTicks := feed.TicksEvent{}
	if symbol := eventSymbol(emptyTicks); symbol != "" {
		t.Fatalf("empty TicksEvent eventSymbol = %q, want empty string", symbol)
	}
	const fallback int64 = 999
	if ts := eventExchTs(emptyTicks, fallback); ts != fallback {
		t.Fatalf("empty TicksEvent eventExchTs = %d, want %d", ts, fallback)
	}

	emptyBars := feed.Bars1mEvent{}
	if symbol := eventSymbol(emptyBars); symbol != "" {
		t.Fatalf("empty Bars1mEvent eventSymbol = %q, want empty string", symbol)
	}
	if ts := eventExchTs(emptyBars, fallback); ts != fallback {
		t.Fatalf("empty Bars1mEvent eventExchTs = %d, want %d", ts, fallback)
	}
}

func TestDecodeUnknownKind(t *testing.T) {
	if _, err := decodePayload("bogus", []byte("{}")); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDayKey(t *testing.T) {
	// 2026-07-06 13:30:00 UTC == 09:30 ET (EDT). Day key must be the ET date.
	const ms = int64(1783344600_000)
	if got := dayKey(ms); got != "2026-07-06" {
		t.Fatalf("dayKey = %q, want 2026-07-06", got)
	}
}
