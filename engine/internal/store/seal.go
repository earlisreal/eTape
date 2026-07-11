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
