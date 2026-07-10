package store

import (
	"log/slog"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
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

// TestMaxSeqReturnsErrorNotZero guards against the latent poison this bug
// exposed: a maxSeq query failure must be reported as an error, never
// silently treated as "day is empty" (0). Returning 0 on error would seed
// the day's counter at 0 and re-mint seqs that collide with every row
// already journaled for that day.
func TestMaxSeqReturnsErrorNotZero(t *testing.T) {
	s := open(t)
	_ = s.db.Close() // force the next query to fail
	if _, err := s.maxSeq("2026-07-06"); err == nil {
		t.Fatal("maxSeq() error = nil after closing the DB, want non-nil")
	}
}

// TestAssignSeqRefusesToSeedOnQueryError: a transient maxSeq failure must not
// cache a (wrong) seq for the day — the day must re-seed cleanly once the
// transient clears, rather than being poisoned at 0 for the rest of the process.
func TestAssignSeqRefusesToSeedOnQueryError(t *testing.T) {
	s := open(t)
	_ = s.db.Close()
	const day = "2026-07-06"
	if _, err := s.assignSeq(day); err == nil {
		t.Fatal("assignSeq() error = nil on a broken DB, want non-nil")
	}
	if _, cached := s.daySeq[day]; cached {
		t.Fatal("assignSeq must not cache a seq for the day after a failed seed")
	}
}

// TestCommitReseedsOnPoisonedCounter is the core regression for the field
// incident: a per-day seq counter that thinks a day is empty (e.g. because
// maxSeq errored once and — pre-fix — seeded 0) must self-heal on its very
// next commit rather than flooding (day,seq) collisions. Simulates the
// poisoned state directly (s.daySeq[day]=0 despite existing rows) and drives
// commit() straight, bypassing the writer goroutine for determinism — safe
// here because FlushInterval is 1h (see open()), so the real writer goroutine
// never touches this Store's state concurrently with the test.
func TestCommitReseedsOnPoisonedCounter(t *testing.T) {
	s := open(t)
	const day = "2026-07-06"
	for i := int64(1); i <= 5; i++ {
		if _, err := s.db.Exec(journalInsertSQL, day, i, recvBase, recvBase, "", kindConnUp, 0, "{}"); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	s.daySeq[day] = 0 // poison: process believes the day is empty

	buf := []pendingWrite{{
		query:      journalInsertSQL,
		args:       []any{day, int64(0), recvBase, recvBase, "", kindConnUp, 0, "{}"},
		journalDay: day,
	}}
	s.commit(buf)

	if got := s.DroppedJournalRows(); got != 0 {
		t.Fatalf("DroppedJournalRows() = %d, want 0 (must self-heal, not drop)", got)
	}
	var maxSeq int64
	if err := s.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day=?", day).Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	if maxSeq != 6 {
		t.Fatalf("max seq after self-heal = %d, want 6 (5 pre-existing + 1 healed row)", maxSeq)
	}
	if s.daySeq[day] != 6 {
		t.Fatalf("daySeq[%q] after self-heal = %d, want 6", day, s.daySeq[day])
	}
}

// TestCommitLogsCollisionOnce asserts the collision diagnostic (Warn,
// "journal seq collision (self-healed)") fires exactly once per day even
// when multiple rows in the same batch collide — never once per colliding row.
func TestCommitLogsCollisionOnce(t *testing.T) {
	s := open(t)
	h := &countHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const day = "2026-07-06"
	if _, err := s.db.Exec(journalInsertSQL, day, int64(1), recvBase, recvBase, "", kindConnUp, 0, "{}"); err != nil {
		t.Fatal(err)
	}
	s.daySeq[day] = 0 // poison

	buf := []pendingWrite{
		{query: journalInsertSQL, args: []any{day, int64(0), recvBase, recvBase, "", kindConnUp, 0, "{}"}, journalDay: day},
		{query: journalInsertSQL, args: []any{day, int64(0), recvBase, recvBase, "", kindConnUp, 0, "{}"}, journalDay: day},
	}
	// Both rows start from the same poisoned cache: the first collides with the
	// pre-existing row and self-heals; the second reads the now-healthy cache
	// and never collides. Either way the Warn must appear at most once total.
	s.commit(buf)

	if got := h.n(); got != 1 {
		t.Fatalf("collision Warn logged %d times, want exactly 1", got)
	}
	if got := s.DroppedJournalRows(); got != 0 {
		t.Fatalf("DroppedJournalRows() = %d, want 0", got)
	}
}

// TestCommitSurvivesConcurrentWriters reproduces the field root cause
// directly: two independent Stores (i.e. two engine processes, each with its
// own in-memory daySeq cache) writing to the SAME SQLite file concurrently —
// exactly what happened when a pre-single-instance-guard Windows build let a
// second process open the already-running instance's DB. The self-heal
// machinery (assignSeq/reseedDay + bounded retry in commit()) must keep this
// safe: no panic, no hang, no event silently vanishing, and no duplicate
// (day,seq) row escapes into the DB (the PK would reject it, but a bug that
// let a bad row through undetected would still corrupt the day's read-back).
func TestCommitSurvivesConcurrentWriters(t *testing.T) {
	path := t.TempDir() + "/concurrent.db"
	const perWriter = 200
	const day = "2026-07-06"

	stores := make([]*Store, 2)
	for i := range stores {
		s, err := Open(Options{Path: path, Clock: clock.NewFake(time.UnixMilli(0)), FlushInterval: time.Millisecond})
		if err != nil {
			t.Fatalf("Open writer %d: %v", i, err)
		}
		stores[i] = s
		t.Cleanup(func() { _ = s.Close() })
	}

	var wg sync.WaitGroup
	for _, s := range stores {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				s.RecordEvent(feed.ConnUpEvent{}, recvBase+int64(i))
			}
			s.Flush()
		}(s)
	}
	wg.Wait()

	var committed int64
	if err := stores[0].db.QueryRow("SELECT COUNT(*) FROM journal WHERE day=?", day).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	dropped := int64(stores[0].DroppedJournalRows() + stores[1].DroppedJournalRows())
	const total = int64(2 * perWriter)
	if committed+dropped != total {
		t.Fatalf("committed(%d)+dropped(%d) = %d, want %d — an event vanished without being committed or counted as dropped",
			committed, dropped, committed+dropped, total)
	}
	if committed == 0 {
		t.Fatal("expected at least some rows to commit across two concurrent writers")
	}

	// Sanity: every committed row really has a unique (day,seq) — if a bug let
	// a would-be-duplicate through by not routing it through execJournalRow's
	// collision handling, this catches it even though the PK would also.
	var distinctSeqs int64
	if err := stores[0].db.QueryRow("SELECT COUNT(DISTINCT seq) FROM journal WHERE day=?", day).Scan(&distinctSeqs); err != nil {
		t.Fatal(err)
	}
	if distinctSeqs != committed {
		t.Fatalf("distinct seqs = %d, committed rows = %d — duplicate seq escaped", distinctSeqs, committed)
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
