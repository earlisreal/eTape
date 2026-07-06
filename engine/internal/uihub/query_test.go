package uihub

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type spyFills struct {
	rows []exec.FillRow
	err  error
	sym  string
}

func (s *spyFills) QueryFills(symbol string, _, _ int64) ([]exec.FillRow, error) {
	s.sym = symbol
	return s.rows, s.err
}

func TestQueryFillsReturnsFills(t *testing.T) {
	f := &spyFills{rows: []exec.FillRow{{OrderID: "ET1", Symbol: "US.AAPL", Side: "BUY", Qty: 100, Price: 3.47, TsMs: 5, Venue: "sim"}}}
	q := newQueries(f)
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"US.AAPL","fromMs":0,"toMs":9}`))
	fills, ok := out.([]wsmsg.Fill)
	if !ok || len(fills) != 1 {
		t.Fatalf("expected []wsmsg.Fill of len 1, got %T %v", out, out)
	}
	if fills[0].Side != wsmsg.SideBuy || fills[0].OrderID != "ET1" || f.sym != "US.AAPL" {
		t.Fatalf("fill map wrong: %+v (queried %q)", fills[0], f.sym)
	}
}

func TestQueryFillsEmptyOnError(t *testing.T) {
	q := newQueries(&spyFills{err: errors.New("boom")})
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"X","fromMs":0,"toMs":1}`))
	if fills, ok := out.([]wsmsg.Fill); !ok || len(fills) != 0 {
		t.Fatalf("error must yield empty []wsmsg.Fill (never nil/hang): %T %v", out, out)
	}
}

func TestQueryUnknownReturnsEmptySlice(t *testing.T) {
	q := newQueries(&spyFills{})
	out := q.handle("Nope", json.RawMessage(`{}`))
	// must be a non-nil, JSON-marshals-to-[] value so the UI promise resolves to []
	b, _ := json.Marshal(out)
	if string(b) != "[]" {
		t.Fatalf("unknown query must resolve to []; marshaled to %s", b)
	}
}
