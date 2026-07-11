package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/klauspost/compress/zstd"
)

func TestSchemaHasJournalChunks(t *testing.T) {
	s := open(t) // helper in store_test.go: temp DB, fake clock, hourly flush
	var name string
	row := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='journal_chunks'")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("journal_chunks table missing: %v", err)
	}
	// Columns are queryable (empty result, no error).
	if _, err := s.db.Query("SELECT day, chunk_no, first_seq, last_seq, n_rows, body FROM journal_chunks"); err != nil {
		t.Fatalf("journal_chunks columns wrong: %v", err)
	}
}

func TestChunkCodecRoundTrip(t *testing.T) {
	crs := []chunkRow{
		{Seq: 1, TsExch: 1_700_000_000_000, TsRecv: 1_700_000_000_050, Symbol: "US.AAPL", Kind: "ticks", Seed: 0, Payload: `{"Ticks":[{"Symbol":"US.AAPL","Price":100.1}],"Seed":false}`},
		{Seq: 2, TsExch: 1_700_000_001_000, TsRecv: 1_700_000_001_010, Symbol: "US.AAPL", Kind: "book", Seed: 1, Payload: `{"Book":{"Symbol":"US.AAPL"},"Seed":true}`},
		{Seq: 3, TsExch: 1_700_000_002_000, TsRecv: 1_700_000_002_000, Symbol: "", Kind: "conn_up", Seed: 0, Payload: `{}`},
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer enc.Close()
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer dec.Close()

	body, rawLen, err := encodeChunkRows(enc, crs)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if rawLen <= 0 || len(body) == 0 {
		t.Fatalf("empty encode: rawLen=%d body=%d", rawLen, len(body))
	}
	got, err := decodeChunk(dec, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(crs, got) {
		t.Fatalf("round-trip mismatch:\n in: %#v\nout: %#v", crs, got)
	}
}

// openAtClock opens a temp store whose clock is `now`, so tests can put the
// recorded day strictly in the past (sealable). Mirrors open() in store_test.go.
func openAtClock(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := Open(Options{Path: t.TempDir() + "/seal.db", Clock: clock.NewFake(now), FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// insertChunk hand-writes one chunk so read-path tests don't depend on sealing.
func insertChunk(t *testing.T, s *Store, day string, chunkNo int, crs []chunkRow) {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	body, _, err := encodeChunkRows(enc, crs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(
		`INSERT INTO journal_chunks (day, chunk_no, first_seq, last_seq, n_rows, body) VALUES (?,?,?,?,?,?)`,
		day, chunkNo, crs[0].Seq, crs[len(crs)-1].Seq, int64(len(crs)), body)
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
}

func TestReadJournalDayNoChunksReturnsRaw(t *testing.T) {
	s := open(t)
	evs := sampleEvents() // codec_test.go
	for i, ev := range evs {
		s.RecordEvent(ev, recvBase+int64(i)) // recvBase = 2026-07-06 09:30 ET (journal_test.go)
	}
	s.Flush()
	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(evs) {
		t.Fatalf("rows = %d, want %d", len(rows), len(evs))
	}
	for i := range rows {
		if !reflect.DeepEqual(rows[i].Event, evs[i]) {
			t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, evs[i], rows[i].Event)
		}
		if rows[i].Day != "2026-07-06" {
			t.Fatalf("row %d day = %q", i, rows[i].Day)
		}
	}
}

func TestReadJournalDayMergesChunksThenRaw(t *testing.T) {
	s := open(t)
	// Two sealed rows (seq 1,2) + one raw row (seq 3) for the same day.
	insertChunk(t, s, "2026-07-06", 0, []chunkRow{
		{Seq: 1, TsExch: 10, TsRecv: 10, Symbol: "US.AAPL", Kind: "conn_up", Seed: 0, Payload: `{}`},
		{Seq: 2, TsExch: 20, TsRecv: 20, Symbol: "US.AAPL", Kind: "conn_down", Seed: 0, Payload: `{}`},
	})
	_, err := s.db.Exec(
		`INSERT INTO journal (day, seq, ts_exch, ts_recv, symbol, kind, seed, payload) VALUES (?,?,?,?,?,?,?,?)`,
		"2026-07-06", 3, 30, 30, "US.AAPL", "resynced", 0, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	wantSeq := []int64{1, 2, 3}
	for i := range rows {
		if rows[i].Seq != wantSeq[i] {
			t.Fatalf("row %d seq = %d, want %d", i, rows[i].Seq, wantSeq[i])
		}
	}
}

func TestJournalDaysUnionsChunksAndRaw(t *testing.T) {
	s := open(t)
	// 2026-07-05 exists only as a chunk; 2026-07-06 only as a raw row.
	insertChunk(t, s, "2026-07-05", 0, []chunkRow{
		{Seq: 1, TsExch: 1, TsRecv: 1, Symbol: "", Kind: "conn_up", Seed: 0, Payload: `{}`},
	})
	s.RecordEvent(feed.ConnUpEvent{}, recvBase) // 2026-07-06
	s.Flush()
	days, err := s.JournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-05" || days[1] != "2026-07-06" {
		t.Fatalf("days = %v, want [2026-07-05 2026-07-06]", days)
	}
}

// afterDay is a fake "now" two days after the recorded 2026-07-06 events, so
// that day is strictly in the past and therefore sealable.
func afterDay(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 7, 8, 12, 0, 0, 0, mustLoc(t)) // mustLoc: retention_test.go
}

func chunkCount(t *testing.T, s *Store, day string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM journal_chunks WHERE day=?", day).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func rawCount(t *testing.T, s *Store, day string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM journal WHERE day=?", day).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSealRoundTripGolden(t *testing.T) {
	s := openAtClock(t, afterDay(t))
	evs := sampleEvents()
	for i, ev := range evs {
		s.RecordEvent(ev, recvBase+int64(i))
	}
	s.Flush()

	before, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SealJournalDays(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if rawCount(t, s, "2026-07-06") != 0 {
		t.Fatal("raw rows survived sealing")
	}
	if chunkCount(t, s, "2026-07-06") == 0 {
		t.Fatal("no chunks written")
	}
	after, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("seal changed the day:\n before: %#v\n after:  %#v", before, after)
	}
}

func TestSealSkipsCurrentDay(t *testing.T) {
	// Clock = the same day as the events → nothing is older than today.
	s := openAtClock(t, time.Date(2026, 7, 6, 20, 0, 0, 0, mustLoc(t)))
	s.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s.Flush()
	sum, err := s.SealJournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Days != 0 || chunkCount(t, s, "2026-07-06") != 0 || rawCount(t, s, "2026-07-06") != 1 {
		t.Fatalf("current day was sealed: sum=%+v", sum)
	}
}

func TestSealRatioFloor(t *testing.T) {
	s := openAtClock(t, afterDay(t))
	// 500 near-identical book snapshots — the cross-row redundancy zstd targets.
	for i := 0; i < 500; i++ {
		px := 100.0 + float64(i%5)*0.01
		s.RecordEvent(feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", TsMs: recvBase + int64(i),
			Bids: []feed.BookLevel{{Price: px, Volume: 300, Orders: 4}, {Price: px - 0.01, Volume: 500, Orders: 7}},
			Asks: []feed.BookLevel{{Price: px + 0.01, Volume: 200, Orders: 3}}}}, recvBase+int64(i))
	}
	s.Flush()
	sum, err := s.SealJournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if sum.BytesBefore == 0 || sum.BytesAfter*4 >= sum.BytesBefore {
		t.Fatalf("ratio floor failed: before=%d after=%d (want after < 25%% of before)", sum.BytesBefore, sum.BytesAfter)
	}
}

func TestSealIsIdempotent(t *testing.T) {
	s := openAtClock(t, afterDay(t))
	for i, ev := range sampleEvents() {
		s.RecordEvent(ev, recvBase+int64(i))
	}
	s.Flush()
	if _, err := s.SealJournalDays(); err != nil {
		t.Fatal(err)
	}
	first := chunkCount(t, s, "2026-07-06")
	sum, err := s.SealJournalDays() // second pass: day has no raw rows → no-op
	if err != nil {
		t.Fatal(err)
	}
	if sum.Days != 0 || chunkCount(t, s, "2026-07-06") != first {
		t.Fatalf("second seal was not a no-op: sum=%+v chunks now %d (was %d)", sum, chunkCount(t, s, "2026-07-06"), first)
	}
}

func TestSealCrashLeavesDayRaw(t *testing.T) {
	s := openAtClock(t, afterDay(t))
	// Small chunks + a fault on chunk index 1 so the day needs >1 chunk and fails mid-seal.
	prevSize := chunkSize
	chunkSize = 2
	defer func() { chunkSize = prevSize }()
	sealFaultHook = func(chunkNo int) error {
		if chunkNo == 1 {
			return errors.New("injected mid-seal fault")
		}
		return nil
	}
	defer func() { sealFaultHook = nil }()

	for i := 0; i < 5; i++ {
		s.RecordEvent(feed.ConnUpEvent{}, recvBase+int64(i))
	}
	s.Flush()

	sum, err := s.SealJournalDays()
	if err != nil {
		t.Fatalf("SealJournalDays must not return an error on a per-day fault: %v", err)
	}
	if sum.Failed != 1 || sum.Days != 0 {
		t.Fatalf("expected Failed=1 Days=0, got %+v", sum)
	}
	if rawCount(t, s, "2026-07-06") != 5 {
		t.Fatalf("raw rows lost after crash: %d, want 5", rawCount(t, s, "2026-07-06"))
	}
	if chunkCount(t, s, "2026-07-06") != 0 {
		t.Fatalf("partial chunks persisted: %d, want 0", chunkCount(t, s, "2026-07-06"))
	}
}

func TestRequestSealSealsThroughWriter(t *testing.T) {
	s := openAtClock(t, afterDay(t)) // today = 2026-07-08
	for i := 0; i < 3; i++ {
		s.RecordEvent(feed.ConnUpEvent{}, recvBase+int64(i)) // 2026-07-06
	}
	s.Flush()

	s.RequestSeal()
	s.Flush() // FIFO barrier: sealOp is processed before this flushReq completes

	if rawCount(t, s, "2026-07-06") != 0 {
		t.Fatal("raw rows survived RequestSeal")
	}
	if chunkCount(t, s, "2026-07-06") == 0 {
		t.Fatal("RequestSeal wrote no chunks")
	}
}

func TestPruneDeletesChunksAndRaw(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, mustLoc(t)) // retention 2d keeps 07-05,07-06
	s := openAtClock(t, now)
	// Old day 2026-07-01 exists ONLY as a chunk (already sealed); must be pruned.
	insertChunk(t, s, "2026-07-01", 0, []chunkRow{
		{Seq: 1, TsExch: 1, TsRecv: 1, Symbol: "", Kind: "conn_up", Seed: 0, Payload: `{}`},
	})
	dayMs := func(y, m, d int) int64 {
		return time.Date(y, time.Month(m), d, 10, 0, 0, 0, mustLoc(t)).UnixMilli()
	}
	s.RecordEvent(feed.ConnUpEvent{}, dayMs(2026, 7, 5)) // kept
	s.RecordEvent(feed.ConnUpEvent{}, dayMs(2026, 7, 6)) // kept
	s.Flush()

	if _, err := s.PruneJournal(2); err != nil {
		t.Fatal(err)
	}
	if chunkCount(t, s, "2026-07-01") != 0 {
		t.Fatal("old sealed chunk survived prune")
	}
	days, _ := s.JournalDays()
	if len(days) != 2 || days[0] != "2026-07-05" || days[1] != "2026-07-06" {
		t.Fatalf("remaining days = %v, want [2026-07-05 2026-07-06]", days)
	}
}

func TestVacuumIfNeeded(t *testing.T) {
	s := open(t)
	// Small DB → freelist below threshold → no vacuum, no error.
	ran, err := s.VacuumIfNeeded()
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("vacuum ran on a tiny DB; freelist should be under threshold")
	}
}

func TestBootMaintenanceSequence(t *testing.T) {
	s := openAtClock(t, afterDay(t)) // today = 2026-07-08
	// A past day (07-06) and "today" (07-08) both have raw rows.
	for i, ev := range sampleEvents() {
		s.RecordEvent(ev, recvBase+int64(i)) // 2026-07-06
	}
	todayMs := time.Date(2026, 7, 8, 10, 0, 0, 0, mustLoc(t)).UnixMilli()
	s.RecordEvent(feed.ConnUpEvent{}, todayMs)
	s.Flush()

	// main.go order: prune (retention huge → no-op) → seal → flush → vacuum.
	if _, err := s.PruneJournal(3650); err != nil {
		t.Fatal(err)
	}
	sum, err := s.SealJournalDays()
	if err != nil {
		t.Fatal(err)
	}
	s.Flush()
	if _, err := s.VacuumIfNeeded(); err != nil {
		t.Fatal(err)
	}

	if sum.Days != 1 {
		t.Fatalf("sealed %d days, want 1 (07-06 only)", sum.Days)
	}
	if rawCount(t, s, "2026-07-06") != 0 || chunkCount(t, s, "2026-07-06") == 0 {
		t.Fatal("past day 2026-07-06 not sealed")
	}
	if chunkCount(t, s, "2026-07-08") != 0 || rawCount(t, s, "2026-07-08") != 1 {
		t.Fatal("today 2026-07-08 must stay raw")
	}
	// Both days still fully readable.
	if rows, _ := s.ReadJournalDay("2026-07-06"); len(rows) != len(sampleEvents()) {
		t.Fatalf("07-06 read back %d rows, want %d", len(rows), len(sampleEvents()))
	}
	if rows, _ := s.ReadJournalDay("2026-07-08"); len(rows) != 1 {
		t.Fatalf("07-08 read back %d rows, want 1", len(rows))
	}
}
