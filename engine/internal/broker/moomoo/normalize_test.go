package moomoo

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

// Expected content of the hand-crafted fixtures written by
// testdata/gen/main.go -- kept here as literals (not imported: the generator
// is package main, and its constants describe fixture INPUT, which is frozen
// wire bytes once written) so a reviewer can see exactly what each assertion
// expects without cross-referencing the generator. If Task 7/8 supersede
// testdata/golden/trd_update_order.jsonl or trd_update_orderfill.jsonl with a
// real capture, these tests -- and these literals -- get superseded too.
const (
	testVenue = exec.VenueID("moomoo")

	goldenRemarkA   = "ET01J9Z4KZ8N3H6VXG2Q7T5WYCMF" // AAPL buy limit
	goldenOrderIDA  = uint64(620193847)
	goldenRemarkB   = "ET01J9Z4M8QNVD2K7H4RTXWG3BPS" // MSFT buy limit
	goldenRemarkC   = "ET01J9Z4NPXQ7VD4K2H8RTWG6MBS" // TSLA buy limit
	goldenRejectMsg = "Insufficient buying power"
)

// goldenFrame mirrors engine/internal/feed/opend/golden_test.go's goldenFrame
// struct exactly (same field set), so a real capture that lands with extra
// fields (is_push, proto_fmt_type, body_sha1_hex, decoded_json, per
// scripts/capture_golden_frames.py's fuller output) still unmarshals cleanly.
type goldenFrame struct {
	ProtoID  uint32 `json:"proto_id"`
	Dir      string `json:"direction"`
	SerialNo uint32 `json:"serial_no"`
	BodyLen  int    `json:"body_len"`
	FrameHex string `json:"frame_hex"`
	BodyHex  string `json:"body_hex"`
}

// loadGoldenFrames parses testdata/golden/<name>.jsonl.
func loadGoldenFrames(t *testing.T, name string) []goldenFrame {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var out []goldenFrame
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var g goldenFrame
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("%s: golden line: %v", name, err)
		}
		out = append(out, g)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: scan: %v", name, err)
	}
	return out
}

// decodeHeader hex-decodes g's stored frame and verifies the WIRE round-trip
// via opend.Decode against the header fields the fixture also recorded
// (proto id, serial, body length) -- proving the frame/body bytes are
// self-consistent, not just trusting the stored body_hex blindly.
func decodeHeader(t *testing.T, g goldenFrame) opend.Frame {
	t.Helper()
	raw, err := hex.DecodeString(g.FrameHex)
	if err != nil {
		t.Fatalf("decode frame_hex: %v", err)
	}
	f, err := opend.Decode(raw)
	if err != nil {
		t.Fatalf("opend.Decode: %v", err)
	}
	if f.ProtoID != g.ProtoID || f.SerialNo != g.SerialNo || len(f.Body) != g.BodyLen {
		t.Fatalf("header mismatch: got protoID=%d serial=%d bodyLen=%d, want protoID=%d serial=%d bodyLen=%d",
			f.ProtoID, f.SerialNo, len(f.Body), g.ProtoID, g.SerialNo, g.BodyLen)
	}
	return f
}

func decodeOrderPushFrame(t *testing.T, g goldenFrame) *trdupdateorder.Response {
	t.Helper()
	f := decodeHeader(t, g)
	var resp trdupdateorder.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		t.Fatalf("proto.Unmarshal Trd_UpdateOrder body: %v", err)
	}
	return &resp
}

func decodeFillPushFrame(t *testing.T, g goldenFrame) *trdupdateorderfill.Response {
	t.Helper()
	f := decodeHeader(t, g)
	var resp trdupdateorderfill.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		t.Fatalf("proto.Unmarshal Trd_UpdateOrderFill body: %v", err)
	}
	return &resp
}

// TestPushDecoder_OrderPushGoldenFrames walks the 7 hand-crafted
// Trd_UpdateOrder (2208) golden frames in order through one shared
// pushDecoder, covering: an Accepted transition, a TimeOut push that must
// leave lastKnownStatus untouched, a repeated push at the SAME domain status
// producing no event (proving the TimeOut push above really didn't clobber
// anything), a genuine PartiallyFilled transition that still produces no
// event (2208 never signals fills), a second order's Accepted->Canceled
// transition, and a straight-to-Rejected transition carrying LastErrMsg
// through as Reason.
func TestPushDecoder_OrderPushGoldenFrames(t *testing.T) {
	frames := loadGoldenFrames(t, "trd_update_order.jsonl")
	if len(frames) != 7 {
		t.Fatalf("expected 7 golden order-push frames, got %d", len(frames))
	}
	p := newPushDecoder()

	// Frame 0: OrderA Submitted (wire) -> domain Accepted.
	evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[0]))
	if len(evs) != 1 {
		t.Fatalf("frame 0: got %d events, want 1: %+v", len(evs), evs)
	}
	acc, ok := evs[0].(exec.OrderAccepted)
	if !ok {
		t.Fatalf("frame 0: got %T, want exec.OrderAccepted", evs[0])
	}
	if acc.V != testVenue || acc.OID != goldenRemarkA || acc.BrokerOrderID != fmt.Sprint(goldenOrderIDA) {
		t.Fatalf("frame 0: unexpected OrderAccepted %+v", acc)
	}
	if acc.Ts == 0 {
		t.Fatal("frame 0: Ts should not be 0 (fixture sets a non-zero UpdateTimestamp)")
	}

	// Frame 1: OrderA TimeOut -> no event, and must not clobber lastKnownStatus.
	if evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[1])); evs != nil {
		t.Fatalf("frame 1 (TimeOut): got %d events, want 0: %+v", len(evs), evs)
	}

	// Frame 2: OrderA Submitted again (same domain status as frame 0) -> no
	// event. If frame 1's TimeOut had incorrectly overwritten
	// lastKnownStatus, this would wrongly look like a fresh transition and
	// emit a second OrderAccepted here.
	if evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[2])); evs != nil {
		t.Fatalf("frame 2 (resubmit, same status): got %d events, want 0: %+v", len(evs), evs)
	}

	// Frame 3: OrderA Filled_Part -> a genuine NEW status transition
	// (Accepted -> PartiallyFilled) that must STILL produce no event: 2208
	// never signals Filled/PartiallyFilled, that's exclusively 2218's job.
	if evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[3])); evs != nil {
		t.Fatalf("frame 3 (PartiallyFilled): got %d events, want 0: %+v", len(evs), evs)
	}

	// Frame 4: OrderB Submitted -> Accepted.
	evs = p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[4]))
	if len(evs) != 1 {
		t.Fatalf("frame 4: got %d events, want 1: %+v", len(evs), evs)
	}
	if accB, ok := evs[0].(exec.OrderAccepted); !ok || accB.OID != goldenRemarkB {
		t.Fatalf("frame 4: unexpected event %+v (%T)", evs[0], evs[0])
	}

	// Frame 5: OrderB Cancelled_All -> Canceled.
	evs = p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[5]))
	if len(evs) != 1 {
		t.Fatalf("frame 5: got %d events, want 1: %+v", len(evs), evs)
	}
	can, ok := evs[0].(exec.OrderCanceled)
	if !ok || can.OID != goldenRemarkB || can.V != testVenue {
		t.Fatalf("frame 5: unexpected event %+v (%T)", evs[0], evs[0])
	}

	// Frame 6: OrderC straight to SubmitFailed -> Rejected, LastErrMsg carried as Reason.
	evs = p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[6]))
	if len(evs) != 1 {
		t.Fatalf("frame 6: got %d events, want 1: %+v", len(evs), evs)
	}
	rej, ok := evs[0].(exec.OrderRejected)
	if !ok || rej.OID != goldenRemarkC || rej.Reason != goldenRejectMsg {
		t.Fatalf("frame 6: unexpected event %+v (%T)", evs[0], evs[0])
	}
}

// TestPushDecoder_UnknownOrderPushIsIgnored covers an order push with no
// Remark at all (an order placed via the moomoo app or another client, not
// by eTape) -- it must be ignored, not crash, and must not pollute
// lastKnownStatus/domainOIDByOrderID for anything.
func TestPushDecoder_UnknownOrderPushIsIgnored(t *testing.T) {
	frames := loadGoldenFrames(t, "trd_update_order.jsonl")
	resp := decodeOrderPushFrame(t, frames[0])
	resp.S2C.Order.Remark = nil // simulate: no Remark on the wire

	p := newPushDecoder()
	if evs := p.decodeOrderPush(testVenue, resp); evs != nil {
		t.Fatalf("order push with no Remark: got %d events, want 0: %+v", len(evs), evs)
	}
}

// TestPushDecoder_FillPushGoldenFrames walks the 4 hand-crafted
// Trd_UpdateOrderFill (2218) golden frames, proving: multi-fill CumQty/
// LeavesQty/AvgPrice accumulation across two sequential fills on the same
// order (not just a single-fill case), a duplicate FillID producing no
// second event, and a fill referencing an OrderID this decoder has never
// seen via any order push producing no event and no panic -- both BEFORE and
// AFTER the decoder has learned about a different, unrelated order.
func TestPushDecoder_FillPushGoldenFrames(t *testing.T) {
	orderFrames := loadGoldenFrames(t, "trd_update_order.jsonl")
	fillFrames := loadGoldenFrames(t, "trd_update_orderfill.jsonl")
	if len(fillFrames) != 4 {
		t.Fatalf("expected 4 golden fill-push frames, got %d", len(fillFrames))
	}

	p := newPushDecoder()

	// The unknown-correlation fill (frame 3), tried FIRST -- before this
	// decoder has processed any order push at all. Must not panic.
	if evs := p.decodeFillPush(testVenue, decodeFillPushFrame(t, fillFrames[3])); evs != nil {
		t.Fatalf("unknown-correlation fill (pre-seed): got %d events, want 0: %+v", len(evs), evs)
	}

	// Learn OrderA's numeric OrderID -> (domain OID, total qty) via its first
	// order push (frame 0 of trd_update_order.jsonl -- Qty=100).
	if evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, orderFrames[0])); len(evs) != 1 {
		t.Fatalf("seeding OrderA's order push: got %d events, want 1", len(evs))
	}

	// Fill #1: 40 @ 150.10 -> CumQty=40, LeavesQty=100-40=60, AvgPrice=150.10.
	evs := p.decodeFillPush(testVenue, decodeFillPushFrame(t, fillFrames[0]))
	if len(evs) != 1 {
		t.Fatalf("fill 1: got %d events, want 1: %+v", len(evs), evs)
	}
	f1, ok := evs[0].(exec.OrderFilled)
	if !ok {
		t.Fatalf("fill 1: got %T, want exec.OrderFilled", evs[0])
	}
	if f1.F.Venue != testVenue || f1.F.OrderID != goldenRemarkA {
		t.Fatalf("fill 1: unexpected Fill venue/orderID %+v", f1.F)
	}
	if f1.F.Symbol != "US.AAPL" {
		t.Fatalf("fill 1: Symbol = %q, want US.AAPL", f1.F.Symbol)
	}
	if f1.F.Side != exec.SideBuy {
		t.Fatalf("fill 1: Side = %v, want SideBuy", f1.F.Side)
	}
	if f1.F.Qty != 40 || f1.F.Price != 150.10 {
		t.Fatalf("fill 1: Qty/Price = %v/%v, want 40/150.10", f1.F.Qty, f1.F.Price)
	}
	if f1.F.TsMs == 0 {
		t.Fatal("fill 1: TsMs should not be 0 (fixture sets a non-zero CreateTimestamp)")
	}
	if f1.CumQty != 40 {
		t.Fatalf("fill 1: CumQty = %v, want 40", f1.CumQty)
	}
	if f1.LeavesQty != 60 {
		t.Fatalf("fill 1: LeavesQty = %v, want 60", f1.LeavesQty)
	}
	if math.Abs(f1.AvgPrice-150.10) > 1e-9 {
		t.Fatalf("fill 1: AvgPrice = %v, want 150.10", f1.AvgPrice)
	}

	// Fill #2: 60 @ 150.40 -> CumQty=100, LeavesQty=0,
	// AvgPrice = (40*150.10 + 60*150.40) / 100.
	evs = p.decodeFillPush(testVenue, decodeFillPushFrame(t, fillFrames[1]))
	if len(evs) != 1 {
		t.Fatalf("fill 2: got %d events, want 1: %+v", len(evs), evs)
	}
	f2, ok := evs[0].(exec.OrderFilled)
	if !ok {
		t.Fatalf("fill 2: got %T, want exec.OrderFilled", evs[0])
	}
	if f2.CumQty != 100 {
		t.Fatalf("fill 2: CumQty = %v, want 100", f2.CumQty)
	}
	if f2.LeavesQty != 0 {
		t.Fatalf("fill 2: LeavesQty = %v, want 0", f2.LeavesQty)
	}
	wantAvg := (40*150.10 + 60*150.40) / 100.0
	if math.Abs(f2.AvgPrice-wantAvg) > 1e-9 {
		t.Fatalf("fill 2: AvgPrice = %v, want %v", f2.AvgPrice, wantAvg)
	}

	// Redelivery of fill #2 (same FillID, moomoo docs note fills can
	// theoretically redeliver) -- must dedup, no second event.
	if evs := p.decodeFillPush(testVenue, decodeFillPushFrame(t, fillFrames[2])); evs != nil {
		t.Fatalf("fill 2 redelivery: got %d events, want 0 (dedup): %+v", len(evs), evs)
	}

	// The unknown-correlation fill, retried now that OrderA's state IS
	// known -- it targets a different, still-never-seen OrderID, so it must
	// still produce nothing (and still not panic).
	if evs := p.decodeFillPush(testVenue, decodeFillPushFrame(t, fillFrames[3])); evs != nil {
		t.Fatalf("unknown-correlation fill (post-seed): got %d events, want 0: %+v", len(evs), evs)
	}
}

// TestPushDecoder_ConcurrentAccess exercises decodeOrderPush/decodeFillPush
// from multiple goroutines against one shared pushDecoder under -race:
// pushDecoder's mutex must actually guard every map it owns. Frames are
// decoded to *Response values up front, in the main test goroutine -- t's
// Fatalf must never be called from a spawned goroutine, so only the pure
// decodeOrderPush/decodeFillPush calls (which touch no *testing.T) run
// concurrently below.
func TestPushDecoder_ConcurrentAccess(t *testing.T) {
	orderFrames := loadGoldenFrames(t, "trd_update_order.jsonl")
	fillFrames := loadGoldenFrames(t, "trd_update_orderfill.jsonl")

	orderResps := make([]*trdupdateorder.Response, len(orderFrames))
	for i, g := range orderFrames {
		orderResps[i] = decodeOrderPushFrame(t, g)
	}
	fillResps := make([]*trdupdateorderfill.Response, len(fillFrames))
	for i, g := range fillFrames {
		fillResps[i] = decodeFillPushFrame(t, g)
	}

	p := newPushDecoder()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for _, r := range orderResps {
				p.decodeOrderPush(testVenue, r)
			}
		}()
		go func() {
			defer wg.Done()
			for _, r := range fillResps {
				p.decodeFillPush(testVenue, r)
			}
		}()
	}
	wg.Wait()
}
