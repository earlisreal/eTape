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
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

const testVenue = exec.VenueID("moomoo")

// Task 7: identifies the REAL order captured by
// engine/scripts/capture_golden_frames.py's capture_trd_paper into
// testdata/golden/trd_update_order.jsonl -- one tiny (1 share), far-from-
// market US.F paper (SIMULATE) limit order, placed then cancelled. Its
// account id was redacted before commit (public repo); order id and remark
// are not account-identifying and are kept as captured. This REAL fixture
// supersedes Task 4's hand-crafted one at the same file path -- see
// TestPushDecoder_OrderPushGoldenFrames.
const (
	goldenRemark  = "ET7CAPTURE"
	goldenOrderID = uint64(8476557239106489402)
)

// Fill pushes (2218) are LIVE-only, so testdata/golden/trd_update_orderfill.jsonl
// is still Task 4's hand-crafted fixture (Task 8 supersedes it with a real
// LIVE capture). These identify the specific hand-crafted "OrderA" that file
// correlates against -- testdata/gen/main.go's orderIDA/remarkA. Now that
// trd_update_order.jsonl is a real, unrelated order, TestPushDecoder_FillPushGoldenFrames
// can no longer borrow its correlation seed from that file, so
// hcOrderPush() below re-derives, in Go, the one order-push fact it needs
// from that same hand-crafted identity.
const (
	handCraftedOrderIDA = uint64(620193847)
	handCraftedRemarkA  = "ET01J9Z4KZ8N3H6VXG2Q7T5WYCMF"
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

// TestPushDecoder_OrderPushGoldenFrames walks the 3 REAL Trd_UpdateOrder
// (2208) golden frames captured (Task 7) from one live OpenD paper
// (SIMULATE) order's actual full lifecycle -- see
// engine/scripts/capture_golden_frames.py's capture_trd_paper: a 1-share
// US.F limit placed far below market, then cancelled. This is the real-wire
// proof that Task 4's own design goal holds ("decoding logic must not depend
// on which source produced the fixture"): the same pushDecoder that passed
// against Task 4's hand-crafted frames also passes against a real capture.
//
// The real order's own status sequence was Submitting(2) -> Submitted(5) ->
// Cancelled_All(15): statusDomain maps Submitting to StatusSubmitted (no
// one-shot BrokerEvent), so frame 0 produces no event -- the transition to
// domain Accepted only happens at frame 1 (Submitted), and the transition to
// domain Canceled at frame 2 (Cancelled_All).
func TestPushDecoder_OrderPushGoldenFrames(t *testing.T) {
	frames := loadGoldenFrames(t, "trd_update_order.jsonl")
	if len(frames) != 3 {
		t.Fatalf("expected 3 golden order-push frames, got %d", len(frames))
	}
	p := newPushDecoder()

	// Frame 0: Submitting (wire) -> domain StatusSubmitted -- no one-shot
	// BrokerEvent, but lastKnownStatus must now be tracked as StatusSubmitted.
	if evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[0])); evs != nil {
		t.Fatalf("frame 0 (Submitting): got %d events, want 0: %+v", len(evs), evs)
	}

	// Frame 1: Submitted (wire) -> domain StatusAccepted, a genuine
	// transition from StatusSubmitted -> exec.OrderAccepted.
	evs := p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[1]))
	if len(evs) != 1 {
		t.Fatalf("frame 1: got %d events, want 1: %+v", len(evs), evs)
	}
	acc, ok := evs[0].(exec.OrderAccepted)
	if !ok {
		t.Fatalf("frame 1: got %T, want exec.OrderAccepted", evs[0])
	}
	if acc.V != testVenue || acc.OID != goldenRemark || acc.BrokerOrderID != fmt.Sprint(goldenOrderID) {
		t.Fatalf("frame 1: unexpected OrderAccepted %+v", acc)
	}
	if acc.Ts == 0 {
		t.Fatal("frame 1: Ts should not be 0 (the real fixture carries a non-zero UpdateTimestamp)")
	}

	// Frame 2: Cancelled_All (wire) -> domain StatusCanceled, a genuine
	// transition from StatusAccepted -> exec.OrderCanceled.
	evs = p.decodeOrderPush(testVenue, decodeOrderPushFrame(t, frames[2]))
	if len(evs) != 1 {
		t.Fatalf("frame 2: got %d events, want 1: %+v", len(evs), evs)
	}
	can, ok := evs[0].(exec.OrderCanceled)
	if !ok || can.OID != goldenRemark || can.V != testVenue {
		t.Fatalf("frame 2: unexpected event %+v (%T)", evs[0], evs[0])
	}
}

// hcOrderPush hand-constructs a *trdupdateorder.Response directly in Go
// (not via a captured golden frame) for order-push scenarios a live OpenD
// paper capture cannot reliably reproduce on demand: TimeOut, a same-status
// resubmit, a partial fill, or an outright rejection. Ported from
// testdata/gen/main.go's now-retired baseOrder/orderResp helpers (that
// generator produced the fixture file this replaces; these scenarios still
// need coverage, just not sourced from a wire-frame JSONL file anymore).
func hcOrderPush(orderID uint64, remark, code, name string, qty, price float64, status trdcommon.OrderStatus, updateTs float64) *trdupdateorder.Response {
	return &trdupdateorder.Response{
		RetType: proto.Int32(0), // RetType_RetType_Succeed
		S2C: &trdupdateorder.S2C{
			Header: &trdcommon.TrdHeader{
				TrdEnv:    proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)),
				AccID:     proto.Uint64(28881234), // placeholder, same as testdata/gen/main.go's genAccID
				TrdMarket: proto.Int32(int32(trdcommon.TrdMarket_TrdMarket_US)),
			},
			Order: &trdcommon.Order{
				TrdSide:         proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
				OrderType:       proto.Int32(int32(trdcommon.OrderType_OrderType_Normal)),
				OrderStatus:     proto.Int32(int32(status)),
				OrderID:         proto.Uint64(orderID),
				OrderIDEx:       proto.String(fmt.Sprint(orderID)),
				Code:            proto.String(code),
				Name:            proto.String(name),
				Qty:             proto.Float64(qty),
				Price:           proto.Float64(price),
				CreateTime:      proto.String("2026-07-11 09:31:05"),
				UpdateTime:      proto.String("2026-07-11 09:31:05"),
				UpdateTimestamp: proto.Float64(updateTs),
				Remark:          proto.String(remark),
			},
		},
	}
}

// TestPushDecoder_OrderPushEdgeCases covers the same scenarios Task 4's
// hand-crafted trd_update_order.jsonl used to (via hcOrderPush, since that
// file is now a real capture -- see TestPushDecoder_OrderPushGoldenFrames):
// a TimeOut push that must leave lastKnownStatus untouched, a repeated push
// at the SAME domain status producing no event (proving the TimeOut push
// above really didn't clobber anything), a genuine PartiallyFilled
// transition that still produces no event (2208 never signals fills), and a
// straight-to-Rejected transition carrying LastErrMsg through as Reason.
func TestPushDecoder_OrderPushEdgeCases(t *testing.T) {
	const (
		orderID = handCraftedOrderIDA
		remark  = handCraftedRemarkA
	)
	p := newPushDecoder()

	// Submitted -> Accepted, establishing a last-known-good status.
	evs := p.decodeOrderPush(testVenue, hcOrderPush(orderID, remark, "AAPL", "Apple Inc.", 100, 150.25, trdcommon.OrderStatus_OrderStatus_Submitted, 1))
	if len(evs) != 1 {
		t.Fatalf("Submitted: got %d events, want 1: %+v", len(evs), evs)
	}
	if _, ok := evs[0].(exec.OrderAccepted); !ok {
		t.Fatalf("Submitted: got %T, want exec.OrderAccepted", evs[0])
	}

	// TimeOut -> no event, and must not clobber lastKnownStatus.
	if evs := p.decodeOrderPush(testVenue, hcOrderPush(orderID, remark, "AAPL", "Apple Inc.", 100, 150.25, trdcommon.OrderStatus_OrderStatus_TimeOut, 2)); evs != nil {
		t.Fatalf("TimeOut: got %d events, want 0: %+v", len(evs), evs)
	}

	// Submitted again (same domain status as before) -> no event. If the
	// TimeOut push above had incorrectly overwritten lastKnownStatus, this
	// would wrongly look like a fresh transition and emit a second
	// OrderAccepted here.
	if evs := p.decodeOrderPush(testVenue, hcOrderPush(orderID, remark, "AAPL", "Apple Inc.", 100, 150.25, trdcommon.OrderStatus_OrderStatus_Submitted, 3)); evs != nil {
		t.Fatalf("resubmit (same status): got %d events, want 0: %+v", len(evs), evs)
	}

	// Filled_Part -> a genuine NEW status transition (Accepted ->
	// PartiallyFilled) that must STILL produce no event: 2208 never signals
	// Filled/PartiallyFilled, that's exclusively 2218's job.
	if evs := p.decodeOrderPush(testVenue, hcOrderPush(orderID, remark, "AAPL", "Apple Inc.", 100, 150.25, trdcommon.OrderStatus_OrderStatus_Filled_Part, 4)); evs != nil {
		t.Fatalf("PartiallyFilled: got %d events, want 0: %+v", len(evs), evs)
	}

	// A second, unrelated order, straight to SubmitFailed -> Rejected, with
	// LastErrMsg carried through as Reason.
	const (
		orderID2 = uint64(620193861)
		remark2  = "ET01J9Z4NPXQ7VD4K2H8RTWG6MBS"
	)
	rejResp := hcOrderPush(orderID2, remark2, "TSLA", "Tesla Inc.", 20, 250.00, trdcommon.OrderStatus_OrderStatus_SubmitFailed, 5)
	rejResp.S2C.Order.LastErrMsg = proto.String("Insufficient buying power")
	evs = p.decodeOrderPush(testVenue, rejResp)
	if len(evs) != 1 {
		t.Fatalf("Rejected: got %d events, want 1: %+v", len(evs), evs)
	}
	rej, ok := evs[0].(exec.OrderRejected)
	if !ok || rej.OID != remark2 || rej.Reason != "Insufficient buying power" {
		t.Fatalf("Rejected: unexpected event %+v (%T)", evs[0], evs[0])
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

	// Learn OrderA's numeric OrderID -> (domain OID, total qty=100) via a
	// hand-constructed order push carrying testdata/gen/main.go's exact
	// OrderA identity (see the handCraftedOrderIDA/handCraftedRemarkA
	// doc comment above) -- trd_update_order.jsonl is a real, unrelated
	// capture now (Task 7), so this seed can no longer be borrowed from that
	// file the way it could when both files were hand-crafted together.
	seed := hcOrderPush(handCraftedOrderIDA, handCraftedRemarkA, "AAPL", "Apple Inc.", 100, 150.25, trdcommon.OrderStatus_OrderStatus_Submitted, 1)
	if evs := p.decodeOrderPush(testVenue, seed); len(evs) != 1 {
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
	if f1.F.Venue != testVenue || f1.F.OrderID != handCraftedRemarkA {
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

// TestPushDecoder_ReconnectRaceCumQtyClamp proves the final-review bounding
// mitigation for the onConnUp reconnect race documented on reconcileOrder
// (normalize.go) and onConnUp (moomoo.go): subAccPush goes live BEFORE
// reconcile's getOrderList snapshot, so a fill landing in that window can be
// counted once via reconcileOrder's catch-up path (which sets cumQtyByOrderID
// directly from the snapshot's authoritative ExecutedQty) and AGAIN when that
// same fill's already-queued live push is processed by decodeFillPush
// afterward (which adds additively). Without the clamp, this test's raw sum
// (80 seeded + 40 live = 120) would exceed the order's own total qty (100);
// decodeFillPush must clamp CumQty (and LeavesQty) to the total instead.
func TestPushDecoder_ReconnectRaceCumQtyClamp(t *testing.T) {
	const (
		orderID = uint64(555000111)
		remark  = "ET-RACE-CLAMP"
		total   = 100.0
	)
	p := newPushDecoder()

	// Simulate reconcile's snapshot already showing 80/100 filled -- as if
	// the fill that's about to arrive as a live push below had already
	// landed inside the subAccPush-before-snapshot race window and gotten
	// folded into the snapshot's authoritative ExecutedQty.
	raw := reconcileOrderFixture(orderID, remark, "AAPL", trdcommon.OrderStatus_OrderStatus_Filled_Part, total, 80, 150.00)
	seedEvs := p.reconcileOrder(testVenue, raw)
	if len(seedEvs) != 1 {
		t.Fatalf("reconcile seed: got %d events, want 1 (the catch-up fill): %+v", len(seedEvs), seedEvs)
	}
	seedFill, ok := seedEvs[0].(exec.OrderFilled)
	if !ok || seedFill.CumQty != 80 {
		t.Fatalf("reconcile seed: got %+v, want an OrderFilled with CumQty=80", seedEvs[0])
	}

	// The SAME underlying fill also arrives as a live Trd_UpdateOrderFill
	// (2218) push -- queued during the race window, processed only after
	// onConnUp returns. decodeFillPush has no way to know this qty was
	// already folded into the snapshot above, so it adds additively:
	// 80 (seeded) + 40 (this push) = 120, more than the order's total of 100.
	fillPush := &trdupdateorderfill.Response{
		RetType: proto.Int32(0),
		S2C: &trdupdateorderfill.S2C{
			Header: &trdcommon.TrdHeader{
				TrdEnv:    proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)),
				AccID:     proto.Uint64(28881234),
				TrdMarket: proto.Int32(int32(trdcommon.TrdMarket_TrdMarket_US)),
			},
			OrderFill: &trdcommon.OrderFill{
				TrdSide:         proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
				FillID:          proto.Uint64(1),
				FillIDEx:        proto.String("1"),
				OrderID:         proto.Uint64(orderID),
				OrderIDEx:       proto.String(fmt.Sprint(orderID)),
				Code:            proto.String("AAPL"),
				Name:            proto.String("Apple Inc."),
				Qty:             proto.Float64(40),
				Price:           proto.Float64(150.00),
				CreateTime:      proto.String("2026-07-11 09:31:00"),
				CreateTimestamp: proto.Float64(1783928460.0),
				SecMarket:       proto.Int32(int32(trdcommon.TrdSecMarket_TrdSecMarket_US)),
			},
		},
	}

	evs := p.decodeFillPush(testVenue, fillPush)
	if len(evs) != 1 {
		t.Fatalf("racing live fill: got %d events, want 1: %+v", len(evs), evs)
	}
	f, ok := evs[0].(exec.OrderFilled)
	if !ok {
		t.Fatalf("racing live fill: got %T, want exec.OrderFilled", evs[0])
	}
	if f.CumQty != total {
		t.Fatalf("racing live fill: CumQty = %v, want %v clamped (raw unclamped sum would have been 120)", f.CumQty, total)
	}
	if f.LeavesQty != 0 {
		t.Fatalf("racing live fill: LeavesQty = %v, want 0", f.LeavesQty)
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
