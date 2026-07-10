package store

import (
	"math"
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// recvBase: 2026-07-06 09:30 ET in ms (used so every event lands on one day).
const recvBase = int64(1783344600_000)

func TestRecordAssignsMonotonicSeqPerDay(t *testing.T) {
	s := open(t)
	evs := sampleEvents()
	for i, ev := range evs {
		s.RecordEvent(ev, recvBase+int64(i)) // monotonic recv, same ET day
	}
	s.Flush()

	rows, err := s.db.Query("SELECT seq, kind, seed, symbol FROM journal WHERE day='2026-07-06' ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	var prev int64
	for rows.Next() {
		var seq int64
		var kind, symbol string
		var seed int
		if err := rows.Scan(&seq, &kind, &seed, &symbol); err != nil {
			t.Fatal(err)
		}
		n++
		if seq != prev+1 {
			t.Fatalf("seq gap: got %d after %d", seq, prev)
		}
		prev = seq
	}
	if n != len(evs) {
		t.Fatalf("rows = %d, want %d", n, len(evs))
	}
}

func TestRecordContinuesSeqAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/j.db"
	s1, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	s1.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s1.RecordEvent(feed.ConnDownEvent{}, recvBase+1)
	s1.Flush()
	_ = s1.Close()

	s2, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.RecordEvent(feed.ResyncedEvent{}, recvBase+2)
	s2.Flush()

	var maxSeq int64
	if err := s2.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day='2026-07-06'").Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	if maxSeq != 3 {
		t.Fatalf("max seq after reopen = %d, want 3 (continues, no reset)", maxSeq)
	}
}

// TestRecordDropsRowOnEncodeFailure exercises the honesty-policy path: a
// payload that json.Marshal refuses (NaN is not valid JSON) must be counted
// and skipped rather than blocking the writer or crashing.
func TestRecordDropsRowOnEncodeFailure(t *testing.T) {
	s := open(t)
	bad := feed.TicksEvent{Ticks: []feed.Tick{{Symbol: "US.AAPL", TsMs: recvBase, Price: math.NaN()}}}
	good := feed.ConnUpEvent{}

	s.RecordEvent(bad, recvBase)
	s.RecordEvent(good, recvBase+1)
	s.Flush()

	if got := s.DroppedJournalRows(); got != 1 {
		t.Fatalf("DroppedJournalRows() = %d, want 1", got)
	}

	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM journal WHERE day='2026-07-06'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (only the good event journaled)", n)
	}

	// The surviving row's seq must not have a gap from the dropped one — the
	// dropped payload never consumed a seq value.
	var seq int64
	if err := s.db.QueryRow("SELECT seq FROM journal WHERE day='2026-07-06'").Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want 1 (dropped row must not burn a seq)", seq)
	}
}

func TestReadJournalDayRoundTrips(t *testing.T) {
	s := open(t)
	in := sampleEvents()
	for i, ev := range in {
		s.RecordEvent(ev, recvBase+int64(i))
	}
	s.Flush()

	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(in) {
		t.Fatalf("read %d rows, want %d", len(rows), len(in))
	}
	for i, r := range rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("row %d seq = %d, want %d", i, r.Seq, i+1)
		}
		if !reflect.DeepEqual(r.Event, in[i]) {
			t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, in[i], r.Event)
		}
	}
}

func TestReadJournalTicksFiltersBySymbolKindAndDay(t *testing.T) {
	s := open(t)
	aTicks := feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 1, TsMs: recvBase, Price: 100.0, Volume: 10, Dir: feed.Buy},
		{Symbol: "US.AAPL", Seq: 2, TsMs: recvBase + 1000, Price: 100.5, Volume: 20, Dir: feed.Sell},
	}}
	bTicks := feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.MSFT", Seq: 1, TsMs: recvBase, Price: 400.0, Volume: 5, Dir: feed.Buy},
	}}
	quote := feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", TsMs: recvBase, Last: 100.5}}

	s.RecordEvent(aTicks, recvBase)
	s.RecordEvent(bTicks, recvBase+1)
	s.RecordEvent(quote, recvBase+2)
	s.Flush()

	got, err := s.ReadJournalTicks("US.AAPL", recvBase)
	if err != nil {
		t.Fatal(err)
	}
	want := aTicks.Ticks
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadJournalTicks = %#v, want %#v", got, want)
	}
}

func TestReadJournalTicksEmptyWhenNoneRecorded(t *testing.T) {
	s := open(t)
	s.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s.Flush()

	got, err := s.ReadJournalTicks("US.AAPL", recvBase)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("ReadJournalTicks = %#v, want nil", got)
	}
}

// TestReadJournalTicksPreservesSeqOverlap documents that de-dup across
// journaled batches (e.g. a seed batch followed by a live push covering some
// of the same tick seqs) is NOT this method's job — both copies come back,
// in journaled order.
func TestReadJournalTicksPreservesSeqOverlap(t *testing.T) {
	s := open(t)
	seed := feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 1, TsMs: recvBase, Price: 100.0, Volume: 10, Dir: feed.Buy},
		{Symbol: "US.AAPL", Seq: 2, TsMs: recvBase + 1000, Price: 100.5, Volume: 20, Dir: feed.Sell},
	}}
	live := feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 2, TsMs: recvBase + 1000, Price: 100.5, Volume: 20, Dir: feed.Sell},
		{Symbol: "US.AAPL", Seq: 3, TsMs: recvBase + 2000, Price: 101.0, Volume: 15, Dir: feed.Buy},
	}}
	s.RecordEvent(seed, recvBase)
	s.RecordEvent(live, recvBase+1)
	s.Flush()

	got, err := s.ReadJournalTicks("US.AAPL", recvBase)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]feed.Tick{}, seed.Ticks...), live.Ticks...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadJournalTicks = %#v, want %#v", got, want)
	}
}

func TestJournalDaysDistinct(t *testing.T) {
	s := open(t)
	// One event on 2026-07-06, one a day later (recvBase + 24h).
	s.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s.RecordEvent(feed.ConnUpEvent{}, recvBase+24*3600*1000)
	s.Flush()
	days, err := s.JournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-06" || days[1] != "2026-07-07" {
		t.Fatalf("days = %v, want [2026-07-06 2026-07-07]", days)
	}
}
