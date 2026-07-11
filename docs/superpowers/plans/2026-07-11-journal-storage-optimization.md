# Journal Storage Optimization — Seal-and-Compress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Seal each completed ET trading day's journal rows into zstd-compressed chunks (deleting the raw rows in the same transaction), merge chunks back on read so replay stays byte-identical, and add the missing post-prune `VACUUM` so the DB file actually shrinks — cutting 30-day storage from ~40–55 GB to ~3–5 GB.

**Architecture:** A new additive `journal_chunks` table holds zstd frames of JSONL-encoded rows, one set of chunks per day. `SealJournalDays()` streams each past day in 4,096-row chunks inside one transaction per day (crash-atomic). `ReadJournalDay` reads sealed chunks + any raw rows for the day inside a single read transaction (snapshot-consistent). Sealing triggers at boot (right after prune, before the feed producer starts) and via a 00:30-ET day-roll timer that routes the seal through the writer goroutine. Prune extends to chunks; a freelist-gated `VACUUM` runs once at boot.

**Tech Stack:** Go 1.26.4; `modernc.org/sqlite` (cgo-free, driver name `sqlite`); new dependency `github.com/klauspost/compress/zstd` (pure Go); stdlib `testing` (no testify).

## Global Constraints

- Module root is `engine/`; module path `github.com/earlisreal/eTape/engine`. **All Go commands below run from the `engine/` directory.**
- **Chunk size 4,096 rows; zstd default level** — verbatim spec constants (implemented as package vars `chunkSize`/an encoder default so tests can shrink `chunkSize`; production value unchanged).
- **VACUUM threshold: free pages exceeding ~64 MB** (`freelist_count × page_size > 64<<20`).
- **One transaction per day** when sealing: a crash leaves a day fully raw or fully sealed, never split.
- **The current ET day is never sealed** — it is still being written, and today's tick backfill (`ReadJournalTicks`) reads raw rows. "Current ET day" = `dayKey(s.clk.Now().UnixMilli())`.
- **No schema migration** — the new table is `CREATE TABLE IF NOT EXISTS`, appended to the existing `schemaSQL` string; existing raw days remain readable and seal on first boot.
- **Never block market data** — sealing failures degrade: log, leave the day raw, continue booting.
- Chunk JSONL row shape (decompressed body, one object per line, seq order): `{"seq":…,"ts_exch":…,"ts_recv":…,"symbol":…,"kind":…,"seed":…,"payload":…}` where `payload` is the original event JSON embedded as a JSON **string** value; `day` is omitted (it is in the table key).
- The writer goroutine is the single writer. `SealJournalDays()` and `VacuumIfNeeded()` touch `s.db` directly and are safe **only** when called before any `RecordEvent` producer exists (boot) — exactly like the existing `PruneJournal`. During live operation the day-roll path routes sealing through the writer goroutine via `RequestSeal()`.

---

## File Structure

- **Create** `engine/internal/store/seal.go` — chunk codec (`chunkRow`, `encodeChunkRows`, `decodeChunk`), the `rowQuerier` interface, sealed-row read helper (`readSealedRows`), seal write path (`readRawBatch`, `sealDay`, `daysToSeal`, `SealJournalDays`, `SealSummary`), and the `chunkSize`/`sealFaultHook` test seams.
- **Create** `engine/internal/store/seal_test.go` — codec round-trip, merged-read, seal golden, ratio floor, idempotency, crash safety, current-day-skip, prune-chunks, vacuum, boot-sequence, and `RequestSeal` tests.
- **Modify** `engine/internal/store/schema.go` — add the `journal_chunks` table to `schemaSQL`.
- **Modify** `engine/internal/store/journal.go` — refactor the raw read into `readRawDay`; rewrite `ReadJournalDay` to merge chunks+raw in a read tx; `JournalDays` union; document `ReadJournalTicks` today-only contract.
- **Modify** `engine/internal/store/retention.go` — `PruneJournal` also deletes chunks; add `VacuumIfNeeded`.
- **Modify** `engine/internal/store/store.go` — add `sealOp`, a writer-loop case, and `RequestSeal`.
- **Create** `engine/cmd/etape/scheduler.go` — `nextSealFire` + `runSealScheduler` (day-roll timer).
- **Create** `engine/cmd/etape/scheduler_test.go` — `nextSealFire` cases.
- **Modify** `engine/cmd/etape/main.go` — boot wiring (seal + Flush + vacuum after prune) and `go runSealScheduler(...)`.
- **Modify** `engine/internal/replay/determinism_test.go` — add the load-bearing sealed-day replay determinism test.
- **Modify** `engine/go.mod` / `engine/go.sum` — add `github.com/klauspost/compress`.

---

### Task 1: Add the `journal_chunks` schema

**Files:**
- Modify: `engine/internal/store/schema.go` (insert after the `journal` table block, before `bars_1m`)
- Test: `engine/internal/store/seal_test.go` (create)

**Interfaces:**
- Produces: a `journal_chunks` table present after `store.Open`, columns `(day TEXT, chunk_no INTEGER, first_seq INTEGER, last_seq INTEGER, n_rows INTEGER, body BLOB, PRIMARY KEY(day,chunk_no))`.

- [ ] **Step 1: Write the failing test**

Create `engine/internal/store/seal_test.go`:

```go
package store

import (
	"testing"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSchemaHasJournalChunks -v`
Expected: FAIL — `no such table: journal_chunks`.

- [ ] **Step 3: Add the table to `schemaSQL`**

In `engine/internal/store/schema.go`, insert this block immediately after the `journal` `CREATE TABLE` block (after line 17, before `CREATE TABLE IF NOT EXISTS bars_1m`):

```sql
CREATE TABLE IF NOT EXISTS journal_chunks (
  day       TEXT    NOT NULL,   -- ET trading day, same domain as journal.day
  chunk_no  INTEGER NOT NULL,   -- 0-based within the day
  first_seq INTEGER NOT NULL,
  last_seq  INTEGER NOT NULL,
  n_rows    INTEGER NOT NULL,
  body      BLOB    NOT NULL,   -- zstd frame of JSONL-encoded rows
  PRIMARY KEY (day, chunk_no)
);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSchemaHasJournalChunks -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/store/schema.go engine/internal/store/seal_test.go
git commit -m "feat(store): add journal_chunks table for sealed days"
```

---

### Task 2: zstd dependency + chunk codec

**Files:**
- Modify: `engine/go.mod`, `engine/go.sum`
- Create: `engine/internal/store/seal.go`
- Test: `engine/internal/store/seal_test.go`

**Interfaces:**
- Produces:
  - `type chunkRow struct { Seq, TsExch, TsRecv int64; Symbol, Kind string; Seed int; Payload string }` with JSON tags `seq,ts_exch,ts_recv,symbol,kind,seed,payload`.
  - `func encodeChunkRows(enc *zstd.Encoder, crs []chunkRow) (body []byte, rawLen int, err error)` — JSONL then zstd; `rawLen` is the uncompressed JSONL byte count.
  - `func decodeChunk(dec *zstd.Decoder, body []byte) ([]chunkRow, error)` — zstd then JSONL parse.
  - `type rowQuerier interface { Query(query string, args ...any) (*sql.Rows, error) }` (satisfied by `*sql.DB` and `*sql.Tx`).

- [ ] **Step 1: Add the zstd dependency**

Run: `go get github.com/klauspost/compress@latest`
Expected: `go.mod` gains a `github.com/klauspost/compress vX.Y.Z` require line; `go.sum` updated. (Do NOT run `go mod tidy` yet — nothing imports it until Step 3, which is added in the same commit.)

- [ ] **Step 2: Write the failing test**

Add to `engine/internal/store/seal_test.go`:

```go
import (
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
)

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
```

- [ ] **Step 3: Create `seal.go` with the codec**

Create `engine/internal/store/seal.go`:

```go
package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"github.com/klauspost/compress/zstd"
)

// chunkSize is the number of journal rows per sealed chunk. A package var (not a
// const) purely so tests can shrink it; production keeps the spec value.
var chunkSize = 4096

// rowQuerier is the read surface shared by *sql.DB and *sql.Tx.
type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// chunkRow is one journal row as stored in a sealed chunk's JSONL body. Payload
// is the original event JSON embedded as a JSON string value; day is omitted
// (it lives in the journal_chunks key). Field order matches the spec's JSONL.
type chunkRow struct {
	Seq     int64  `json:"seq"`
	TsExch  int64  `json:"ts_exch"`
	TsRecv  int64  `json:"ts_recv"`
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"`
	Seed    int    `json:"seed"`
	Payload string `json:"payload"`
}

// encodeChunkRows renders crs as JSONL (one compact object per line) and returns
// the zstd frame plus the uncompressed byte count (for ratio accounting).
func encodeChunkRows(enc *zstd.Encoder, crs []chunkRow) (body []byte, rawLen int, err error) {
	var buf bytes.Buffer
	je := json.NewEncoder(&buf) // Encode writes compact JSON + '\n' per call
	for i := range crs {
		if err = je.Encode(&crs[i]); err != nil {
			return nil, 0, err
		}
	}
	return enc.EncodeAll(buf.Bytes(), nil), buf.Len(), nil
}

// decodeChunk decompresses a chunk body and parses its JSONL rows in order.
func decodeChunk(dec *zstd.Decoder, body []byte) ([]chunkRow, error) {
	raw, err := dec.DecodeAll(body, nil)
	if err != nil {
		return nil, err
	}
	var out []chunkRow
	jd := json.NewDecoder(bytes.NewReader(raw))
	for {
		var cr chunkRow
		if err := jd.Decode(&cr); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out = append(out, cr)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestChunkCodecRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/go.mod engine/go.sum engine/internal/store/seal.go engine/internal/store/seal_test.go
git commit -m "feat(store): add zstd JSONL chunk codec"
```

---

### Task 3: Merged read path (chunks + raw)

**Files:**
- Modify: `engine/internal/store/journal.go` (refactor `ReadJournalDay`, `JournalDays`; doc `ReadJournalTicks`)
- Modify: `engine/internal/store/seal.go` (add `readSealedRows`)
- Test: `engine/internal/store/seal_test.go`

**Interfaces:**
- Consumes: `decodeChunk`, `chunkRow`, `rowQuerier` (Task 2); `decodePayload` (codec.go).
- Produces:
  - `func readSealedRows(q rowQuerier, day string) ([]JournalRow, error)` — sealed rows for `day`, chunk_no then seq order, payloads decoded to `feed.Event`.
  - `func readRawDay(q rowQuerier, day string) ([]JournalRow, error)` — the existing raw read, extracted.
  - `ReadJournalDay(day string) ([]JournalRow, error)` — now `readSealedRows` + `readRawDay` inside one read transaction; identical `[]JournalRow` contract as before.
  - `JournalDays() ([]string, error)` — union of `journal` and `journal_chunks` distinct days, ascending.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/store/seal_test.go`:

```go
import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestReadJournalDay|TestJournalDaysUnions' -v`
Expected: FAIL — `TestReadJournalDayMergesChunksThenRaw` returns only the raw row (chunks ignored); `TestJournalDaysUnions` is missing the chunk-only day. (`TestReadJournalDayNoChunksReturnsRaw` may already pass.)

- [ ] **Step 3: Add `readSealedRows` to `seal.go`**

Append to `engine/internal/store/seal.go`:

```go
// readSealedRows returns a day's sealed rows in chunk_no (hence seq) order,
// decoding each row's payload to a feed.Event.
func readSealedRows(q rowQuerier, day string) ([]JournalRow, error) {
	rows, err := q.Query(`SELECT body FROM journal_chunks WHERE day=? ORDER BY chunk_no`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	var out []JournalRow
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		crs, err := decodeChunk(dec, body)
		if err != nil {
			return nil, err
		}
		for _, cr := range crs {
			ev, err := decodePayload(cr.Kind, []byte(cr.Payload))
			if err != nil {
				return nil, err
			}
			out = append(out, JournalRow{
				Seq: cr.Seq, TsExch: cr.TsExch, TsRecv: cr.TsRecv, Day: day,
				Symbol: cr.Symbol, Kind: cr.Kind, Seed: cr.Seed != 0, Event: ev,
			})
		}
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Refactor the raw read + rewrite `ReadJournalDay`/`JournalDays` in `journal.go`**

Replace the existing `ReadJournalDay` (journal.go:123-150) with a merged, transaction-wrapped version plus an extracted `readRawDay`:

```go
// ReadJournalDay returns a day's events in seq order, decoded to feed.Events.
// It merges sealed chunks (older, compressed) with any raw rows for the day
// (normally only today). Both reads run in one transaction so a concurrent
// per-day seal (which inserts chunks and deletes raw rows atomically) can never
// produce a torn read that drops or duplicates the day.
func (s *Store) ReadJournalDay(day string) ([]JournalRow, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() // read-only tx: rollback just releases it
	sealed, err := readSealedRows(tx, day)
	if err != nil {
		return nil, err
	}
	raw, err := readRawDay(tx, day)
	if err != nil {
		return nil, err
	}
	return append(sealed, raw...), nil
}

// readRawDay returns the raw (unsealed) journal rows for a day, seq-ordered.
func readRawDay(q rowQuerier, day string) ([]JournalRow, error) {
	rows, err := q.Query(
		`SELECT seq, ts_exch, ts_recv, symbol, kind, seed, payload
		 FROM journal WHERE day=? ORDER BY seq`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalRow
	for rows.Next() {
		var r JournalRow
		var seed int
		var payload string
		if err := rows.Scan(&r.Seq, &r.TsExch, &r.TsRecv, &r.Symbol, &r.Kind, &seed, &payload); err != nil {
			return nil, err
		}
		r.Day = day
		r.Seed = seed != 0
		ev, err := decodePayload(r.Kind, []byte(payload))
		if err != nil {
			return nil, err
		}
		r.Event = ev
		out = append(out, r)
	}
	return out, rows.Err()
}
```

Replace the `JournalDays` body (journal.go:182-197) query line with the union:

```go
	rows, err := s.db.Query(
		"SELECT day FROM journal UNION SELECT day FROM journal_chunks ORDER BY day")
```

(Everything else in `JournalDays` is unchanged.)

- [ ] **Step 5: Document `ReadJournalTicks` today-only contract**

In `engine/internal/store/journal.go`, extend the `ReadJournalTicks` doc comment (above line 157) with a sentence noting it reads raw rows only:

```go
// ... existing comment ...
//
// This reads the raw `journal` table only (never `journal_chunks`): its sole
// caller is today's tick backfill, and the current ET day is never sealed, so
// today's ticks are always raw. Do not call it for a past day.
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestReadJournalDay|TestJournalDaysUnions' -v`
Expected: PASS. Then run the whole package to confirm no regression: `go test ./internal/store/`
Expected: ok.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/store/journal.go engine/internal/store/seal.go engine/internal/store/seal_test.go
git commit -m "feat(store): merge sealed chunks and raw rows on journal read"
```

---

### Task 4: Seal write path

**Files:**
- Modify: `engine/internal/store/seal.go` (add `SealSummary`, `sealFaultHook`, `daysToSeal`, `readRawBatch`, `sealDay`, `SealJournalDays`)
- Test: `engine/internal/store/seal_test.go`

**Interfaces:**
- Consumes: `chunkRow`, `encodeChunkRows`, `chunkSize` (Task 2); `dayKey` (codec.go).
- Produces:
  - `type SealSummary struct { Days, Chunks, Failed int; Rows, BytesBefore, BytesAfter int64 }`.
  - `func (s *Store) SealJournalDays() (SealSummary, error)` — seals every distinct `journal.day` strictly older than today; per-day failures are logged and left raw (counted in `Failed`), never fatal.
  - `var sealFaultHook func(chunkNo int) error` — test-only fault injection (nil in production).

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/store/seal_test.go`:

```go
import (
	"errors"
	"fmt"
)

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestSeal -v`
Expected: FAIL — `SealJournalDays`, `SealSummary`, `sealFaultHook` undefined (compile error).

- [ ] **Step 3: Implement the seal write path in `seal.go`**

Append to `engine/internal/store/seal.go` (add `"log/slog"` to the import block):

```go
// sealFaultHook, if non-nil, is called before each chunk insert with the 0-based
// chunk index; a non-nil return aborts that day's seal (the per-day transaction
// rolls back, leaving the day raw). Test-only fault injection; nil in production.
var sealFaultHook func(chunkNo int) error

// SealSummary reports what a SealJournalDays pass did.
type SealSummary struct {
	Days        int   // days fully sealed
	Chunks      int   // chunks written
	Failed      int   // days that errored and were left raw
	Rows        int64 // journal rows sealed
	BytesBefore int64 // uncompressed JSONL bytes
	BytesAfter  int64 // compressed chunk bytes
}

// SealJournalDays seals every distinct journal.day strictly older than the
// current ET day into zstd chunks, deleting each day's raw rows in the same
// transaction. Per-day failures are logged and the day is left raw (counted in
// Failed); the pass never aborts market data. Safe to call directly only before
// any RecordEvent producer starts (boot) — during live operation route it
// through RequestSeal (writer goroutine). See PruneJournal for the same rule.
func (s *Store) SealJournalDays() (SealSummary, error) {
	today := dayKey(s.clk.Now().UnixMilli())
	days, err := s.daysToSeal(today)
	if err != nil {
		return SealSummary{}, err
	}
	if len(days) == 0 {
		return SealSummary{}, nil
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return SealSummary{}, err
	}
	defer enc.Close()

	var sum SealSummary
	for _, day := range days {
		chunks, rowCount, rawBytes, compBytes, err := s.sealDay(enc, day)
		if err != nil {
			slog.Error("store: seal day failed (left raw)", "day", day, "err", err)
			sum.Failed++
			continue
		}
		sum.Days++
		sum.Chunks += chunks
		sum.Rows += rowCount
		sum.BytesBefore += rawBytes
		sum.BytesAfter += compBytes
		slog.Info("store: sealed day", "day", day, "chunks", chunks, "rows", rowCount)
	}
	return sum, nil
}

// daysToSeal returns distinct journal days strictly before `before`, ascending.
func (s *Store) daysToSeal(before string) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT day FROM journal WHERE day<? ORDER BY day", before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// sealDay seals one day inside a single transaction: clear any stale chunks (so
// a re-seal after a crash is clean), stream the raw rows in seq order in
// chunkSize batches (never the whole day in memory), insert each as a chunk,
// then delete the day's raw rows. Commit is the only durable step, so a crash
// before it leaves the day fully raw.
func (s *Store) sealDay(enc *zstd.Encoder, day string) (chunks int, rowCount, rawBytes, compBytes int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec("DELETE FROM journal_chunks WHERE day=?", day); err != nil {
		return
	}

	var afterSeq int64 // seq is per-day and starts at 1, so 0 includes all
	for {
		var crs []chunkRow
		crs, err = readRawBatch(tx, day, afterSeq, chunkSize)
		if err != nil {
			return
		}
		if len(crs) == 0 {
			break
		}
		if sealFaultHook != nil {
			if err = sealFaultHook(chunks); err != nil {
				return
			}
		}
		var body []byte
		var rawLen int
		body, rawLen, err = encodeChunkRows(enc, crs)
		if err != nil {
			return
		}
		if _, err = tx.Exec(
			`INSERT INTO journal_chunks (day, chunk_no, first_seq, last_seq, n_rows, body)
			 VALUES (?,?,?,?,?,?)`,
			day, chunks, crs[0].Seq, crs[len(crs)-1].Seq, int64(len(crs)), body); err != nil {
			return
		}
		rawBytes += int64(rawLen)
		compBytes += int64(len(body))
		rowCount += int64(len(crs))
		afterSeq = crs[len(crs)-1].Seq
		chunks++
	}

	if _, err = tx.Exec("DELETE FROM journal WHERE day=?", day); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	committed = true
	return
}

// readRawBatch reads up to `limit` raw rows for `day` with seq > afterSeq, in
// seq order, as chunkRows. Bounds memory to one chunk: the cursor is fully
// drained and closed before the caller issues its INSERT on the same tx.
func readRawBatch(tx *sql.Tx, day string, afterSeq int64, limit int) ([]chunkRow, error) {
	rows, err := tx.Query(
		`SELECT seq, ts_exch, ts_recv, symbol, kind, seed, payload
		 FROM journal WHERE day=? AND seq>? ORDER BY seq LIMIT ?`, day, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chunkRow
	for rows.Next() {
		var cr chunkRow
		if err := rows.Scan(&cr.Seq, &cr.TsExch, &cr.TsRecv, &cr.Symbol, &cr.Kind, &cr.Seed, &cr.Payload); err != nil {
			return nil, err
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestSeal -v`
Expected: PASS (all five: golden, skips-current-day, ratio floor, idempotent, crash-leaves-raw).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/store/seal.go engine/internal/store/seal_test.go
git commit -m "feat(store): seal completed journal days into zstd chunks"
```

---

### Task 5: Prune extension + freelist-gated VACUUM

**Files:**
- Modify: `engine/internal/store/retention.go`
- Test: `engine/internal/store/seal_test.go`

**Interfaces:**
- Consumes: `dayKey`/`session` (existing in retention.go).
- Produces:
  - `PruneJournal(retentionDays int) (int64, error)` — now also `DELETE FROM journal_chunks WHERE day < cutoff`; return value stays the raw-rows count (unchanged contract); sys_event detail reports both counts.
  - `func (s *Store) VacuumIfNeeded() (bool, error)` — runs `VACUUM` when `freelist_count × page_size > 64<<20`; returns whether it ran. Boot-only-safe (touches `s.db` directly).

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/store/seal_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestPruneDeletesChunks|TestVacuumIfNeeded' -v`
Expected: FAIL — `TestPruneDeletesChunks` leaves the old chunk (prune ignores `journal_chunks`); `VacuumIfNeeded` undefined.

- [ ] **Step 3: Extend `PruneJournal` and add `VacuumIfNeeded`**

In `engine/internal/store/retention.go`, replace the raw-delete + sys-event section of `PruneJournal` (lines 25-34) with:

```go
	res, err := s.db.Exec("DELETE FROM journal WHERE day < ?", cutoffDay)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	cres, err := s.db.Exec("DELETE FROM journal_chunks WHERE day < ?", cutoffDay)
	if err != nil {
		return 0, err
	}
	nc, _ := cres.RowsAffected()
	s.AppendSysEvent("retention", fmt.Sprintf(
		"pruned %d journal rows + %d sealed chunks before %s (retention %dd)", n, nc, cutoffDay, retentionDays))
	return n, nil
```

Then append `VacuumIfNeeded` to `retention.go`:

```go
// vacuumFreelistThreshold: reclaim disk when free pages exceed ~64 MB. Prune
// and seal delete large row spans but SQLite keeps the freed pages in the file;
// VACUUM is the only thing that returns them to the OS.
const vacuumFreelistThreshold = 64 << 20

// VacuumIfNeeded runs VACUUM when the freelist exceeds vacuumFreelistThreshold,
// reporting whether it ran. Like PruneJournal, it touches s.db directly and is
// a boot-time-only maintenance op: call it before the feed producer starts and
// after Flush() has drained queued writes, so no writer transaction races the
// VACUUM (which needs exclusive access).
func (s *Store) VacuumIfNeeded() (bool, error) {
	var freeCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&freeCount); err != nil {
		return false, err
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return false, err
	}
	if freeCount*pageSize < vacuumFreelistThreshold {
		return false, nil
	}
	if _, err := s.db.Exec("VACUUM"); err != nil {
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestPruneDeletesChunks|TestVacuumIfNeeded|TestPruneJournalByDay' -v`
Expected: PASS (including the existing `TestPruneJournalByDay`, whose `deleted == 1` still holds — the pruned day had one raw row and no chunks).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/store/retention.go engine/internal/store/seal_test.go
git commit -m "feat(store): prune sealed chunks and add freelist-gated VACUUM"
```

---

### Task 6: Boot wiring (prune → seal → flush → vacuum)

**Files:**
- Modify: `engine/cmd/etape/main.go` (the boot maintenance block, currently main.go:469-472)
- Test: `engine/internal/store/seal_test.go` (a store-level sequence test; main.go glue is build- and smoke-verified)

**Interfaces:**
- Consumes: `st.PruneJournal`, `st.SealJournalDays`, `st.Flush`, `st.VacuumIfNeeded`, `st.AppendSysEvent` (Tasks 4-5); `fmt`, `log` (slog logger already in scope in main.go).

- [ ] **Step 1: Write the failing sequence test**

Add to `engine/internal/store/seal_test.go` — this mirrors main.go's ordering and asserts the composed end state:

```go
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
```

- [ ] **Step 2: Run test to verify it fails, then passes**

Run: `go test ./internal/store/ -run TestBootMaintenanceSequence -v`
Expected: PASS immediately (Tasks 4-5 already provide all methods). If it fails, fix the store code — this test guards the composition (prune before seal, vacuum after flush). Treat a pass here as the store-side confirmation of the boot sequence.

- [ ] **Step 3: Wire the sequence into `main.go`**

In `engine/cmd/etape/main.go`, replace the prune block (currently lines 469-472) inside `if live {`:

```go
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err == nil && n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		if sum, err := st.SealJournalDays(); err != nil {
			log.Error("seal journal", "err", err)
			st.AppendSysEvent("retention", fmt.Sprintf("journal seal error: %v", err))
		} else if sum.Days > 0 || sum.Failed > 0 {
			log.Info("sealed journal", "days", sum.Days, "chunks", sum.Chunks, "rows", sum.Rows,
				"failed", sum.Failed, "mbBefore", sum.BytesBefore>>20, "mbAfter", sum.BytesAfter>>20)
			st.AppendSysEvent("retention", fmt.Sprintf(
				"sealed %d day(s): %d rows → %d chunks (%d MB → %d MB); %d day(s) left raw",
				sum.Days, sum.Rows, sum.Chunks, sum.BytesBefore>>20, sum.BytesAfter>>20, sum.Failed))
		}
		st.Flush() // drain queued sys_events so no writer tx races the VACUUM
		if vac, err := st.VacuumIfNeeded(); err != nil {
			log.Error("vacuum journal db", "err", err)
		} else if vac {
			log.Info("vacuumed journal db")
			st.AppendSysEvent("retention", "vacuumed journal db (reclaimed free pages)")
		}
		st.AppendSysEvent("boot", "engine up")
```

Confirm `fmt` is already imported in main.go (it is used elsewhere in the file); if not, add it.

- [ ] **Step 4: Build the engine**

Run: `go build ./...`
Expected: builds clean (from `engine/`).

- [ ] **Step 5: Smoke-verify against a generated day**

The boot seal path only runs in live mode, which needs OpenD. Verify the mechanics without a broker by generating a demo journal, then reading it back through the merged path after a manual seal is exercised by the tests above. As the runtime smoke, generate a DB and confirm the binary starts and the schema/tables are intact:

Run:
```bash
go run ./cmd/genjournal -db /tmp/etape-seal-smoke.db -day 2026-07-06
sqlite3 /tmp/etape-seal-smoke.db "SELECT name FROM sqlite_master WHERE type='table' AND name='journal_chunks';"
```
Expected: `genjournal` writes the day without error; the query prints `journal_chunks` (the additive schema is applied on open). Full live-boot seal (with real market data) is verified by Earl on the next live session — note that first live boot with existing raw days will log `sealed journal ...` and may take 1–3 minutes before the feed starts.

- [ ] **Step 6: Commit**

```bash
git add engine/cmd/etape/main.go engine/internal/store/seal_test.go
git commit -m "feat(engine): seal + vacuum journal at boot after prune"
```

---

### Task 7: Day-roll seal (00:30 ET timer through the writer goroutine)

**Files:**
- Modify: `engine/internal/store/store.go` (add `sealOp`, writer-loop case, `RequestSeal`)
- Create: `engine/cmd/etape/scheduler.go`
- Create: `engine/cmd/etape/scheduler_test.go`
- Modify: `engine/cmd/etape/main.go` (start the scheduler goroutine)
- Test: `engine/internal/store/seal_test.go` (RequestSeal via writer)

**Interfaces:**
- Consumes: `SealJournalDays` (Task 4); `s.writes`/`commit()`/`writer()` (store.go); `clock.Clock`, `session.Loc()`.
- Produces:
  - `func (s *Store) RequestSeal()` — enqueues a seal onto the writer goroutine (safe during live operation).
  - `func nextSealFire(now time.Time) time.Time` (cmd/etape) — next 00:30 ET strictly after `now`.
  - `func runSealScheduler(ctx context.Context, st *store.Store, clk clock.Clock, log *slog.Logger)` (cmd/etape).

- [ ] **Step 1: Write the failing store test**

Add to `engine/internal/store/seal_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRequestSealSealsThroughWriter -v`
Expected: FAIL — `RequestSeal` undefined.

- [ ] **Step 3: Add `sealOp` + `RequestSeal` and the writer-loop case in `store.go`**

Add near the `flushReq` type (after store.go:68):

```go
// sealOp asks the writer goroutine to seal completed days between batches,
// serializing the seal with normal writes. Enqueued by RequestSeal.
type sealOp struct{ s *Store }

func (sealOp) render() []pendingWrite { return nil }
```

Add the method (anywhere in store.go, e.g. after `Flush`):

```go
// RequestSeal enqueues a seal of completed days onto the writer goroutine, where
// it runs serialized with normal writes. Unlike SealJournalDays (boot-only-safe,
// called directly), this is safe during live operation — the writer runs the
// seal for you. Fires from the 00:30-ET day-roll scheduler, when US markets are
// closed and the write queue is effectively idle. Blocks only if the queue is
// full.
func (s *Store) RequestSeal() { s.writes <- sealOp{s: s} }
```

In the `writer` loop's `switch v := op.(type)` (store.go:155-164), add a case after the `execAppendOp` case:

```go
			case sealOp:
				commit() // flush any pending batch first
				if sum, err := v.s.SealJournalDays(); err != nil {
					slog.Error("store: day-roll seal", "err", err)
				} else if sum.Days > 0 || sum.Failed > 0 {
					slog.Info("store: day-roll sealed", "days", sum.Days, "chunks", sum.Chunks,
						"rows", sum.Rows, "failed", sum.Failed)
				}
				continue
```

(The day-roll summary logs to slog only — calling `AppendSysEvent` from inside the writer goroutine would enqueue onto its own channel; the boot path is where sys_events are emitted.)

- [ ] **Step 4: Run the store test to verify it passes**

Run: `go test ./internal/store/ -run TestRequestSealSealsThroughWriter -v`
Expected: PASS.

- [ ] **Step 5: Write the failing scheduler test**

Create `engine/cmd/etape/scheduler_test.go`:

```go
package main

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestNextSealFire(t *testing.T) {
	loc := session.Loc()
	cases := []struct {
		name              string
		now               time.Time
		wantY, wantD      int
		wantM             time.Month
	}{
		{"late evening rolls to next 00:30", time.Date(2026, 7, 6, 23, 0, 0, 0, loc), 2026, 7, time.July},
		{"just before fires same day", time.Date(2026, 7, 6, 0, 15, 0, 0, loc), 2026, 6, time.July},
		{"exactly at fire rolls forward", time.Date(2026, 7, 6, 0, 30, 0, 0, loc), 2026, 7, time.July},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextSealFire(c.now)
			if got.Year() != c.wantY || got.Month() != c.wantM || got.Day() != c.wantD ||
				got.Hour() != sealHourET || got.Minute() != sealMinET {
				t.Fatalf("nextSealFire(%v) = %v, want %04d-%02d-%02d %02d:%02d ET",
					c.now, got, c.wantY, c.wantM, c.wantD, sealHourET, sealMinET)
			}
			if !got.After(c.now) {
				t.Fatalf("nextSealFire(%v) = %v is not strictly after now", c.now, got)
			}
		})
	}
}
```

Note the "just before" case expects day 6 (fires later the same day at 00:30) and the "exactly at" case expects day 7 (strictly after).

- [ ] **Step 6: Run scheduler test to verify it fails**

Run: `go test ./cmd/etape/ -run TestNextSealFire -v`
Expected: FAIL — `nextSealFire`, `sealHourET`, `sealMinET` undefined.

- [ ] **Step 7: Create `scheduler.go`**

Create `engine/cmd/etape/scheduler.go`:

```go
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// The day-roll seal fires at 00:30 ET — inside the 20:00–04:00 ET US-market-
// closed window, so sealing the just-completed day serializes with a near-idle
// write queue. The day partition is by receive time, so no new rows for the
// prior day can arrive after midnight; the seal is safe.
const (
	sealHourET = 0
	sealMinET  = 30
)

// nextSealFire returns the next 00:30 ET instant strictly after now.
func nextSealFire(now time.Time) time.Time {
	et := now.In(session.Loc())
	fire := time.Date(et.Year(), et.Month(), et.Day(), sealHourET, sealMinET, 0, 0, session.Loc())
	if !fire.After(et) {
		fire = fire.AddDate(0, 0, 1)
	}
	return fire
}

// runSealScheduler enqueues a journal seal onto the store's writer goroutine at
// each 00:30 ET boundary, so an engine left running past midnight compresses the
// prior day without a restart. Returns when ctx is cancelled.
func runSealScheduler(ctx context.Context, st *store.Store, clk clock.Clock, log *slog.Logger) {
	for {
		wait := nextSealFire(clk.Now()).Sub(clk.Now())
		select {
		case <-ctx.Done():
			return
		case <-clk.After(wait):
			st.RequestSeal()
			log.Info("day-roll seal requested")
		}
	}
}
```

Before implementing, confirm `clock.Clock` exposes `After(d time.Duration) <-chan time.Time` (recon: `internal/clock/clock.go`). If the method name differs (e.g. `NewTimer`), adapt this call and the signature accordingly.

- [ ] **Step 8: Run scheduler test to verify it passes**

Run: `go test ./cmd/etape/ -run TestNextSealFire -v`
Expected: PASS.

- [ ] **Step 9: Start the scheduler in `main.go`**

In `engine/cmd/etape/main.go`, inside `if live {`, after the `go pipe(ctx, &pipeWG, fd.Events(), core, st)` line (main.go:484), add:

```go
		go runSealScheduler(ctx, st, clock.System{}, log)
```

(`clock` and `log` are already in scope in main.go.)

- [ ] **Step 10: Build and run the full store + cmd tests**

Run: `go build ./... && go test ./internal/store/ ./cmd/etape/`
Expected: builds clean; both packages `ok`.

- [ ] **Step 11: Commit**

```bash
git add engine/internal/store/store.go engine/internal/store/seal_test.go engine/cmd/etape/scheduler.go engine/cmd/etape/scheduler_test.go engine/cmd/etape/main.go
git commit -m "feat(engine): seal completed days at the 00:30 ET day roll"
```

---

### Task 8: Load-bearing sealed-day replay determinism

**Files:**
- Modify: `engine/internal/replay/determinism_test.go` (add one test)

**Interfaces:**
- Consumes: `scriptEvents`, `collect` (existing in determinism_test.go); `store.Open`, `store.SealJournalDays`, `store.ReadJournalDay`; `replay.NewClock`/`NewFeed`; `md.New`; `clock.NewFake`.

This is the invariant the whole design rests on: a sealed day must replay bit-for-bit identically to the raw day. It clones `TestReplayJournalMatchesLive` but seals the day between record and read.

- [ ] **Step 1: Write the test**

Add to `engine/internal/replay/determinism_test.go` (add `"github.com/earlisreal/eTape/engine/internal/clock"` to the imports):

```go
func TestReplaySealedJournalMatchesLive(t *testing.T) {
	evs := scriptEvents()

	// Live baseline: feed the scripted events straight into a fresh core.
	live := collect(t, func(feedOne func(feed.Event)) {
		for _, ev := range evs {
			feedOne(ev)
		}
	})

	// Sealed round-trip: record → SEAL → read (from chunks) → replay → fresh core.
	replayed := collect(t, func(feedOne func(feed.Event)) {
		// Clock two days after capBase so 2026-07-06 is strictly in the past (sealable).
		s, err := store.Open(store.Options{
			Path:  t.TempDir() + "/cap.db",
			Clock: clock.NewFake(time.UnixMilli(capBase + 2*86_400_000)),
		})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()
		for i, ev := range evs {
			s.RecordEvent(ev, capBase+int64(i))
		}
		s.Flush()

		sum, err := s.SealJournalDays()
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if sum.Days != 1 || sum.Rows != int64(len(evs)) {
			t.Fatalf("seal summary = %+v, want Days=1 Rows=%d", sum, len(evs))
		}

		rows, err := s.ReadJournalDay("2026-07-06") // served from chunks now
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		if len(rows) != len(evs) {
			t.Fatalf("journal rows = %d, want %d", len(rows), len(evs))
		}
		for i := range rows {
			if !reflect.DeepEqual(rows[i].Event, evs[i]) {
				t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, evs[i], rows[i].Event)
			}
		}

		sim := replay.NewClock(time.UnixMilli(rows[0].TsExch))
		rf := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: sim, Speed: 0})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = rf.Run(ctx) }()
		for ev := range rf.Events() {
			feedOne(ev)
		}
	})

	if len(live) != len(replayed) {
		t.Fatalf("update counts differ: live %d vs sealed-replay %d", len(live), len(replayed))
	}
	for i := range live {
		if !reflect.DeepEqual(live[i], replayed[i]) {
			t.Fatalf("update %d differs after sealing:\n live: %#v\nrepl: %#v", i, live[i], replayed[i])
		}
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/replay/ -run TestReplaySealedJournalMatchesLive -v`
Expected: PASS — bars and indicators from the sealed day match the live run exactly. (If it fails, the codec or merge path is lossy; debug there, do not weaken the assertion.)

- [ ] **Step 3: Run the full affected test suite**

Run: `go test ./internal/store/ ./internal/replay/ ./cmd/etape/`
Expected: all `ok`.

- [ ] **Step 4: Commit**

```bash
git add engine/internal/replay/determinism_test.go
git commit -m "test(replay): sealed day replays byte-identical to live"
```

---

## Final verification

- [ ] Run the whole engine test suite from `engine/`: `go test ./...` — expect all `ok`.
- [ ] Build all binaries: `go build ./...` — expect clean.
- [ ] `go vet ./internal/store/ ./cmd/etape/ ./internal/replay/` — expect no findings.
- [ ] Confirm `go.mod` has exactly one new require (`github.com/klauspost/compress`) and `go mod tidy` produces no diff.

---

## Notes for the implementer

- **`seal_test.go` imports accumulate into one block.** Tasks 1–7 each show the imports that task's snippet needs, but `seal_test.go` has a single `import (...)` block — merge each task's imports into it (Go rejects a package imported twice). By the end the block is roughly: `errors`, `reflect`, `testing`, `time`, `github.com/klauspost/compress/zstd`, `github.com/earlisreal/eTape/engine/internal/clock`, `github.com/earlisreal/eTape/engine/internal/feed`.
- **Test seams `chunkSize` and `sealFaultHook`** are package vars, not consts, on purpose: `chunkSize` lets the crash-safety test force multiple chunks cheaply; `sealFaultHook` injects a mid-seal failure to prove the per-day transaction is atomic. Both keep production behavior identical (default 4096; nil hook). This is the standard Go test-seam pattern (`var timeNow = time.Now`).
- **Read-transaction in `ReadJournalDay`** is load-bearing for correctness, not a style choice: without it, a concurrent per-day seal committing between the chunks query and the raw query would drop the day (chunks empty pre-commit, raw empty post-commit). One tx gives both reads the same WAL snapshot.
- **VACUUM ordering**: `st.Flush()` must precede `VacuumIfNeeded()` at boot — it drains the queued prune/seal sys_events so no writer transaction races the VACUUM's exclusive lock. After the flush, with no producer yet started, the writer's flush-ticker only ever commits an empty buffer (a no-op), so VACUUM stays uncontended.
- **Why sealing streams in seq-range pages** rather than holding one cursor open across inserts: `database/sql` on a single connection (a tx) does not allow a new `Exec` while a `Rows` iterator from the same tx is open. Paging with `WHERE seq > ? LIMIT ?` closes each cursor before its INSERT, bounding memory to one chunk (~tens of MB for book-heavy days) while keeping the whole day in one transaction.
- **First live boot** with existing raw days (~5 GB today) seals all of them in one pass before the feed starts — roughly 1–3 minutes, logged as `sealed journal ...`. This is a one-time cost; steady state seals one prior day per boot.

## Self-review (spec coverage)

- §1 Storage format → Task 1 (table) + Task 2 (JSONL/zstd codec, payload as JSON string value, day omitted).
- §2 Sealing pass (stream, 4096-row chunks, one tx/day, delete raw) → Task 4; boot trigger → Task 6; day-roll trigger → Task 7; current-day-never-sealed → Task 4 (`TestSealSkipsCurrentDay`).
- §3 Read paths (`ReadJournalDay` merge, `JournalDays` union, `ReadJournalTicks` doc, demo/e2e unmodified) → Task 3; sealed replay byte-identical → Task 8.
- §4 Prune extension + freelist-gated VACUUM → Task 5; wired at boot → Task 6.
- §5 Migration/first boot (no schema migration, seal existing days, summary to sys_events, degrade-on-failure) → Task 1 (additive schema), Task 4 (`Failed` accounting), Task 6 (boot log + sys_event + first-boot note).
- §6 Testing: round-trip golden → Task 4; replay determinism → Task 8; crash safety → Task 4; ratio floor → Task 4.
