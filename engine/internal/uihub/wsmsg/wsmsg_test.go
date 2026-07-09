package wsmsg_test

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestOrderJSONFieldNames(t *testing.T) {
	o := wsmsg.Order{
		Venue: "sim", ID: "ET1", Symbol: "US.AAPL",
		Side: wsmsg.SideBuy, Type: wsmsg.OrderLimit, TIF: wsmsg.TIFDay, Session: wsmsg.SessionExtended,
		Qty: 100, LimitPrice: 3.47, Status: wsmsg.StatusSubmitted,
		LeavesQty: 100, ReplacesID: "", CreatedMs: 1_700_000_000_000, UpdatedMs: 1_700_000_000_000,
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"venue", "id", "symbol", "side", "type", "tif", "session", "qty",
		"limitPrice", "stopPrice", "status", "executedQty", "leavesQty", "avgFillPrice",
		"rejectReason", "replacesId", "createdMs", "updatedMs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Order JSON missing key %q; got %v", k, m)
		}
	}
	if m["side"] != "BUY" || m["type"] != "LIMIT" || m["status"] != "SUBMITTED" || m["session"] != "EXTENDED" {
		t.Errorf("enum strings wrong: side=%v type=%v status=%v session=%v", m["side"], m["type"], m["status"], m["session"])
	}
}

func TestEnvelopeAndPositionNullVenue(t *testing.T) {
	snap := wsmsg.SnapshotMsg{Kind: "snapshot", Topic: wsmsg.TopicExecPositions,
		Payload: []wsmsg.PositionRow{{Venue: nil, Symbol: "US.AAPL", Qty: 50, AvgPrice: 3.5}}}
	b, _ := json.Marshal(snap)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["kind"] != "snapshot" || got["topic"] != "exec.positions" {
		t.Fatalf("envelope wrong: %v", got)
	}
	rows := got["payload"].([]any)
	row := rows[0].(map[string]any)
	if v, ok := row["venue"]; !ok || v != nil {
		t.Fatalf("cross-venue row must serialize venue:null, got %v (present=%v)", v, ok)
	}
}

func TestQuoteAndScannerNullables(t *testing.T) {
	q := wsmsg.Quote{Symbol: "US.AAPL", Bid: 3.46, Ask: 3.48, Last: 3.47, Ts: "2026-07-06T13:31:00.000Z"}
	b, _ := json.Marshal(q)
	var qm map[string]any
	_ = json.Unmarshal(b, &qm)
	for _, k := range []string{"symbol", "bid", "ask", "last", "ts"} {
		if _, ok := qm[k]; !ok {
			t.Errorf("Quote missing %q", k)
		}
	}
	row := wsmsg.ScannerRow{Symbol: "US.XYZ", ChangePct: nil, Last: nil, FloatShares: nil, Volume: 0}
	rb, _ := json.Marshal(row)
	var rm map[string]any
	_ = json.Unmarshal(rb, &rm)
	if rm["changePct"] != nil || rm["last"] != nil || rm["floatShares"] != nil {
		t.Errorf("scanner nullables must serialize as null, got %v", rm)
	}
	if rm["volume"] != float64(0) {
		t.Errorf("volume 0 is legitimate, must be present: %v", rm)
	}
}
