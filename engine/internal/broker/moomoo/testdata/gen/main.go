// Command gen (run once via `go run internal/broker/moomoo/testdata/gen/main.go`
// from the engine module root) hand-crafts the synthetic golden-frame fixtures
// for moomoo's two trade push protocols, Trd_UpdateOrder (2208) and
// Trd_UpdateOrderFill (2218), used by internal/broker/moomoo's
// TestPushDecoder_OrderPushGoldenFrames/TestPushDecoder_FillPushGoldenFrames.
//
// There was no live OpenD trade connection to capture these from at this point
// in the plan (Task 4). Task 7 has now superseded testdata/golden/trd_update_order.jsonl
// with a real paper capture (engine/scripts/capture_golden_frames.py's
// capture_trd_paper/--trd-paper) -- NOT with zero test code changes as originally
// expected here: a single real order only produces a handful of real pushes
// (no TimeOut/resubmit-dedup/PartiallyFilled/Rejected scenarios on demand), so
// TestPushDecoder_OrderPushGoldenFrames was rewritten to match the real fixture's
// actual (smaller) shape, and those now-uncovered edge cases were preserved as
// TestPushDecoder_OrderPushEdgeCases via a new hcOrderPush() helper that builds
// the same *trdupdateorder.Response literals this generator used to write to
// disk, directly in Go. TestPushDecoder_FillPushGoldenFrames also had to stop
// borrowing its OrderA seed from trd_update_order.jsonl (now a real, unrelated
// order) and construct it via hcOrderPush() instead -- Task 8, expect the same
// class of issue when superseding trd_update_orderfill.jsonl (2218 is LIVE-only,
// still hand-crafted here): a real live capture will not naturally reproduce the
// multi-fill/dedup/unknown-correlation narrative below either.
//
// This generator is committed for reproducibility/documentation of exactly how
// trd_update_orderfill.jsonl (and, historically, trd_update_order.jsonl) was
// built; it is not part of any normal build (it lives under a "testdata"
// directory, which the go tool always ignores for ./... package patterns) and
// is not expected to run again for trd_update_order.jsonl now that Task 7 has
// landed a real capture there.
//
// Output format mirrors engine/internal/feed/opend/golden_test.go's goldenFrame
// struct exactly (proto_id, direction, serial_no, body_len, frame_hex, body_hex)
// so a real capture (which may carry additional fields, e.g. is_push,
// proto_fmt_type, body_sha1_hex, decoded_json, per
// scripts/capture_golden_frames.py's fuller output) still unmarshals cleanly --
// json.Unmarshal ignores fields the destination struct doesn't declare.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

const (
	genAccID = uint64(28881234)

	// Domain client order ids, "ET"+ULID-shaped, matching every other
	// adapter's convention (Alpaca/TradeZero always key fills by domain id).
	remarkA = "ET01J9Z4KZ8N3H6VXG2Q7T5WYCMF" // AAPL buy limit, lives through Accepted -> two fills -> PartiallyFilled (no event)
	remarkB = "ET01J9Z4M8QNVD2K7H4RTXWG3BPS" // MSFT buy limit, Accepted -> Canceled
	remarkC = "ET01J9Z4NPXQ7VD4K2H8RTWG6MBS" // TSLA buy limit, straight to Rejected

	orderIDA uint64 = 620193847
	orderIDB uint64 = 620193855
	orderIDC uint64 = 620193861

	orderIDUnknown uint64 = 999999999 // never appears in any order push -- the "unknown correlation" fill case
)

type goldenFrame struct {
	ProtoID  uint32 `json:"proto_id"`
	Dir      string `json:"direction"`
	SerialNo uint32 `json:"serial_no"`
	BodyLen  int    `json:"body_len"`
	FrameHex string `json:"frame_hex"`
	BodyHex  string `json:"body_hex"`
}

func hdr() *trdcommon.TrdHeader {
	return &trdcommon.TrdHeader{
		TrdEnv:    proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)),
		AccID:     proto.Uint64(genAccID),
		TrdMarket: proto.Int32(int32(trdcommon.TrdMarket_TrdMarket_US)),
	}
}

// baseOrder builds a fully-populated Order with every "req" field set (this
// package's generated proto is proto2-shaped: several Order/OrderFill fields
// are marked required, and an unset required field can fail proto.Marshal),
// then the caller overrides OrderStatus/UpdateTimestamp/etc. per frame.
func baseOrder(orderID uint64, remark, code, name string, qty, price float64, createTime string) *trdcommon.Order {
	return &trdcommon.Order{
		TrdSide:     proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
		OrderType:   proto.Int32(int32(trdcommon.OrderType_OrderType_Normal)),
		OrderID:     proto.Uint64(orderID),
		OrderIDEx:   proto.String(fmt.Sprint(orderID)),
		Code:        proto.String(code),
		Name:        proto.String(name),
		Qty:         proto.Float64(qty),
		Price:       proto.Float64(price),
		CreateTime:  proto.String(createTime),
		UpdateTime:  proto.String(createTime),
		SecMarket:   proto.Int32(int32(trdcommon.TrdSecMarket_TrdSecMarket_US)),
		Remark:      proto.String(remark),
		TimeInForce: proto.Int32(int32(trdcommon.TimeInForce_TimeInForce_DAY)),
		Session:     proto.Int32(0), // Session_Session_NONE
	}
}

func writeFrame(w *os.File, protoID, serialNo uint32, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("marshal proto %d serial %d: %v", protoID, serialNo, err))
	}
	frame := opend.Encode(protoID, serialNo, body)
	g := goldenFrame{
		ProtoID:  protoID,
		Dir:      "s2c",
		SerialNo: serialNo,
		BodyLen:  len(body),
		FrameHex: hex.EncodeToString(frame),
		BodyHex:  hex.EncodeToString(body),
	}
	line, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		panic(err)
	}
}

func orderResp(o *trdcommon.Order) *trdupdateorder.Response {
	return &trdupdateorder.Response{
		RetType: proto.Int32(0), // RetType_RetType_Succeed
		S2C:     &trdupdateorder.S2C{Header: hdr(), Order: o},
	}
}

func fillResp(f *trdcommon.OrderFill) *trdupdateorderfill.Response {
	return &trdupdateorderfill.Response{
		RetType: proto.Int32(0),
		S2C:     &trdupdateorderfill.S2C{Header: hdr(), OrderFill: f},
	}
}

func main() {
	outDir := filepath.Join("internal", "broker", "moomoo", "testdata", "golden")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}

	// ---- Trd_UpdateOrderFill (2218) ----
	// (This section runs FIRST, ahead of the disabled Trd_UpdateOrder block
	// below, so that block's guard -- an unconditional panic -- can be this
	// function's last statement without leaving any of this legitimately-
	// still-generated code unreachable after it.)
	fillFile, err := os.Create(filepath.Join(outDir, "trd_update_orderfill.jsonl"))
	if err != nil {
		panic(err)
	}
	defer fillFile.Close()

	fillSerial := uint32(200)
	nextFill := func() uint32 { fillSerial++; return fillSerial }

	baseFill := func(orderID, fillID uint64, code, name string, qty, price float64, createTime string) *trdcommon.OrderFill {
		return &trdcommon.OrderFill{
			TrdSide:         proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
			FillID:          proto.Uint64(fillID),
			FillIDEx:        proto.String(fmt.Sprint(fillID)),
			OrderID:         proto.Uint64(orderID),
			OrderIDEx:       proto.String(fmt.Sprint(orderID)),
			Code:            proto.String(code),
			Name:            proto.String(name),
			Qty:             proto.Float64(qty),
			Price:           proto.Float64(price),
			CreateTime:      proto.String(createTime),
			SecMarket:       proto.Int32(int32(trdcommon.TrdSecMarket_TrdSecMarket_US)),
			CreateTimestamp: proto.Float64(0), // set per-frame below
		}
	}

	// OrderA fill #1: 40 @ 150.10. OrderA's total Qty (100, from
	// testdata/gen's historical order pushes -- see the disabled block
	// below) makes LeavesQty = 60 after this fill.
	fill1 := baseFill(orderIDA, 990001001, "AAPL", "Apple Inc.", 40, 150.10, "2026-07-11 09:31:20")
	fill1.CreateTimestamp = proto.Float64(1783928480.0)
	writeFrame(fillFile, opend.ProtoTrdUpdateOrderFill, nextFill(), fillResp(fill1))

	// OrderA fill #2: 60 @ 150.40. CumQty=100, LeavesQty=0,
	// AvgPrice = (40*150.10 + 60*150.40) / 100 = 150.28.
	fill2 := baseFill(orderIDA, 990001002, "AAPL", "Apple Inc.", 60, 150.40, "2026-07-11 09:31:25")
	fill2.CreateTimestamp = proto.Float64(1783928485.0)
	writeFrame(fillFile, opend.ProtoTrdUpdateOrderFill, nextFill(), fillResp(fill2))

	// Redelivery of fill #2 (same FillID, moomoo docs note fills can
	// theoretically redeliver) -- must dedup, no second OrderFilled.
	writeFrame(fillFile, opend.ProtoTrdUpdateOrderFill, nextFill(), fillResp(fill2))

	// A fill for an OrderID this decoder never saw via ANY order push --
	// the "unknown correlation" race. Must produce no event and not panic.
	fillUnknown := baseFill(orderIDUnknown, 990009999, "NVDA", "NVIDIA Corp.", 10, 99.99, "2026-07-11 09:34:00")
	fillUnknown.CreateTimestamp = proto.Float64(1783928640.0)
	writeFrame(fillFile, opend.ProtoTrdUpdateOrderFill, nextFill(), fillResp(fillUnknown))

	fmt.Println("wrote", filepath.Join(outDir, "trd_update_orderfill.jsonl"))

	// ---- Trd_UpdateOrder (2208) ----
	// DISABLED (final review, Minor hygiene fix): trd_update_order.jsonl is
	// now Task 7's REAL captured paper-trading fixture (see the package doc
	// comment above), and TestPushDecoder_OrderPushGoldenFrames asserts on
	// exactly those 3 real frames. This generator must never regenerate/
	// overwrite that file with synthetic data again -- a manual re-run used
	// to silently clobber the real capture and break that test. The
	// baseOrder/orderResp helpers above are kept, deliberately unused, purely
	// as documentation of how the original Task 4 synthetic fixture was
	// constructed (the exact frame narrative is spelled out in the package
	// doc comment); TestPushDecoder_OrderPushEdgeCases (normalize_test.go)
	// covers the same scenarios today via its own hcOrderPush helper. Do not
	// remove this guard and re-wire a write to trd_update_order.jsonl -- and
	// keep this panic as this function's LAST statement (nothing may follow
	// it in this block, or that code becomes unreachable).
	panic("gen: writing trd_update_order.jsonl is disabled -- that file is now Task 7's real captured fixture; see the comment above this panic")
}
