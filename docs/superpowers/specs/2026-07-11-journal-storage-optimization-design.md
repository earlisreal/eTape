# Journal storage optimization — seal-and-compress completed days

**Date:** 2026-07-11
**Status:** Approved
**Problem:** 30 days of full-fidelity journal data at current rates is ~40–55 GB.

## Context and measurements

The SQLite feed journal (`~/.eTape/etape.db`, table `journal`) records every
`feed.Event` as one row with the whole struct as a JSON `payload`. Measured on the
live DB (2026-07-11, 5 recorded days, 5.0 GB file):

- `journal` is 4.79 GB — 96% of the file. Bar archives (`bars_1m` + `bars_daily`)
  total ~105 MB and are kept forever by design.
- By kind: `book` 2.6 GB (455k full 10-level snapshots, ~6 KB each — 54% of payload
  bytes), `ticks` 1.06 GB (2.5M rows, ~438 B each), `bars1m` 468 MB (1.6M
  forming-bar pushes), `quote` 70 MB.
- Hot days (53–57 symbols, 2026-07-08/09) ran ~1.8 GB/day → 30 trading days at the
  default `retention_days = 30` is ~40–55 GB. This matches the worst case in
  `docs/2026-07-06-feed-measurements.md`, which carried "book payload needs
  compress/delta/truncate" as a backlog item.
- Compression measured on real payloads (seq-ordered samples from 2026-07-09):
  mixed stream **20.1×** with gzip -6; book-only **76×**. The redundancy is
  *cross-row* (repeated JSON keys, near-identical consecutive book ladders); zstd
  meets or beats gzip on both ratio and speed.
- `PruneJournal` deletes rows but nothing ever runs `VACUUM` and `auto_vacuum` is
  off, so the file never shrinks — retention currently caps row count, not bytes.

Requirement (decided this session): **every retained day stays byte-identical
replayable** — full DOM, T&S, bars — for the whole 30-day window. No tiering, no
lossy truncation.

## Decision

Seal each completed ET trading day into zstd-compressed chunks of its journal rows,
stored in a new `journal_chunks` table; delete the raw rows in the same
transaction. The hot write path and the live day are untouched. Add the missing
file-shrink step (post-prune/seal `VACUUM`). Expected steady state: hot day
≈ 90–150 MB sealed; 30 days ≈ **3–5 GB**.

## Design

### 1. Storage format

New table in `engine/internal/store/schema.go` (additive, `CREATE TABLE IF NOT
EXISTS` — no migration):

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

`body` decompresses to JSON Lines, one object per journal row in `seq` order:
`{"seq":…,"ts_exch":…,"ts_recv":…,"symbol":…,"kind":…,"seed":…,"payload":…}` —
`payload` is the original JSON string verbatim (embedded as a JSON string value);
`day` is omitted (it is in the key). No new binary codec: the compressible
redundancy lives across rows, so zstd over JSONL captures the measured ratios while
the decompressed form stays human-inspectable.

Constants (not config): chunk size 4,096 rows; zstd default level. Dependency:
`github.com/klauspost/compress/zstd` — pure Go, consistent with the cgo-free
`modernc.org/sqlite` stack.

### 2. Sealing pass

`SealJournalDays()` in the store package. For every distinct `journal.day` strictly
older than the current ET day:

- Stream the day's rows in `seq` order with a cursor (never the whole day in
  memory); accumulate 4,096 rows → encode JSONL → compress → insert chunk.
- After the last chunk, delete the day's raw rows.
- All of the above in **one transaction per day**: a crash leaves the day fully raw
  or fully sealed, never split.

Triggers:

- **Boot:** in `cmd/etape/main.go`, immediately after `PruneJournal`, synchronously
  before the writer goroutine starts consuming `RecordEvent` (same pattern and
  placement as prune).
- **Day-roll:** if the engine stays up past midnight, a timer at ~00:30 ET enqueues
  the seal through the writer goroutine, serializing with normal writes at a moment
  when US markets are closed (20:00–04:00 ET; US-only scope).

The current ET day is never sealed: it is still being written, and today's tick
backfill reads raw rows.

### 3. Read paths

- `ReadJournalDay(day)`: decompress chunks in `chunk_no` order, decode rows, then
  append any raw `journal` rows for the day (normally present only for today; the
  per-day transaction guarantees no half-sealed overlap). Returns the identical
  ordered `[]JournalRow` as before — replay (`replay.NewFeed`) and everything above
  the store layer are unchanged.
- `JournalDays()`: union of distinct days from `journal` and `journal_chunks`.
- `ReadJournalTicks`: unchanged, raw-table only; its today-only contract gets
  documented at the declaration (its only caller is today's backfill).
- `demojournal` and e2e replay DBs work unmodified — unsealed raw days remain fully
  readable through the merged read path.

### 4. Prune and file shrink

- `PruneJournal(retentionDays)` extends to `DELETE FROM journal_chunks WHERE day <
  cutoff` alongside the existing raw delete.
- After boot-time prune + seal, if `PRAGMA freelist_count` × `page_size` exceeds
  ~64 MB, run `VACUUM` — still before the writer starts, so no contention. This
  closes the pre-existing "retention never shrinks the file" gap.

### 5. Migration / first boot

No schema migration. On first boot with this change, all existing raw days
(~5 GB today) seal and vacuum in one pass — a one-time boot of roughly 1–3
minutes. Progress goes to stdout; a summary line (days sealed, bytes before/after)
goes to `sys_events`, matching the prune/honesty-policy style. Sealing failures
degrade with a `sys_events` banner and leave days raw — never block market data.

### 6. Testing

1. **Round-trip golden:** build a synthetic day via `demojournal`;
   `ReadJournalDay` before vs. after sealing must be deep-equal.
2. **Replay determinism:** run the sealed day through `md.Core`; bars/indicators
   must be identical to the raw-day run. This is the load-bearing invariant.
3. **Crash safety:** force an error mid-seal; the day must remain fully raw.
4. **Ratio floor:** loose assertion that sealed size < 25% of raw on synthetic
   data, so a codec regression fails loudly.

## Trade-offs

- The live day only compresses at the next boot or the 00:30 ET timer — the disk
  high-water mark still includes one raw day (~1.8 GB hot). Accepted to keep the
  hot write path untouched.
- Sealed days lose row-level SQL access to `journal` (must decompress a chunk to
  inspect). Accepted: the only production readers are day-granularity.

## Rejected alternatives

- **Per-row payload compression at write time:** can't see cross-row redundancy —
  measured basis says ~2.5–4× overall (12–20 GB/30 days); reaching further needs
  trained zstd dictionaries, and migrating existing days requires building the
  rewrite pass anyway.
- **Typed binary re-encode (protobuf) + delta-encoded books:** comparable ratio at
  much higher effort; delta chains are corruption-fragile and every `feed.Event`
  change becomes proto evolution.
- **Tiered retention / depth truncation / bars-only aging:** ruled out by the
  full-fidelity-for-30-days requirement.
