package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

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
