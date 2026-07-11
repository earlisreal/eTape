// Command gen (run once via `go run internal/broker/moomoo/testdata/gen/main.go`
// from the engine module root) hand-crafts the synthetic golden-frame fixtures
// for moomoo's two trade push protocols, Trd_UpdateOrder (2208) and
// Trd_UpdateOrderFill (2218), used by internal/broker/moomoo's
// TestGoldenTrdUpdateOrderPushesDecode/TestGoldenTrdUpdateOrderFillPushesDecode.
//
// There is no live OpenD trade connection to capture these from at this point
// in the plan (Task 4) -- Task 7 supersedes testdata/golden/trd_update_order.jsonl
// with a real paper capture and Task 8 supersedes trd_update_orderfill.jsonl with
// a real live capture, dropped in at the exact same file paths with zero test
// code changes (the decode logic under test must not care which source produced
// the bytes it's fed). This generator is committed for reproducibility/
// documentation of exactly how these fixtures were built; it is not part of any
// normal build (it lives under a "testdata" directory, which the go tool always
// ignores for ./... package patterns) and is not expected to run again once the
// real captures land.
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

	// ---- Trd_UpdateOrder (2208) ----
	orderFile, err := os.Create(filepath.Join(outDir, "trd_update_order.jsonl"))
	if err != nil {
		panic(err)
	}
	defer orderFile.Close()

	serial := uint32(100)
	next := func() uint32 { serial++; return serial }

	// OrderA: Submitted (wire) -> domain Accepted (event #1).
	oA := baseOrder(orderIDA, remarkA, "AAPL", "Apple Inc.", 100, 150.25, "2026-07-11 09:31:05")
	oA.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_Submitted))
	oA.UpdateTimestamp = proto.Float64(1783928465.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oA))

	// OrderA: TimeOut -- must produce no event and must NOT clobber the
	// last CONFIRMED status (Accepted, from the frame above).
	oATimeout := baseOrder(orderIDA, remarkA, "AAPL", "Apple Inc.", 100, 150.25, "2026-07-11 09:31:05")
	oATimeout.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_TimeOut))
	oATimeout.UpdateTimestamp = proto.Float64(1783928466.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oATimeout))

	// OrderA: Submitted again (wire) -> domain Accepted again -- same status
	// as before the TimeOut frame, so this must produce NO event. If the
	// TimeOut frame had incorrectly clobbered lastKnownStatus, this would
	// wrongly look like a fresh transition and emit a second OrderAccepted.
	oAResubmit := baseOrder(orderIDA, remarkA, "AAPL", "Apple Inc.", 100, 150.25, "2026-07-11 09:31:05")
	oAResubmit.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_Submitted))
	oAResubmit.UpdateTimestamp = proto.Float64(1783928467.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oAResubmit))

	// OrderA: Filled_Part (domain PartiallyFilled) -- a genuine NEW status
	// transition that must still produce NO event: 2208 never signals
	// Filled/PartiallyFilled, that is exclusively 2218's job.
	oAPartial := baseOrder(orderIDA, remarkA, "AAPL", "Apple Inc.", 100, 150.25, "2026-07-11 09:31:05")
	oAPartial.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_Filled_Part))
	oAPartial.FillQty = proto.Float64(40)
	oAPartial.FillAvgPrice = proto.Float64(150.10)
	oAPartial.UpdateTimestamp = proto.Float64(1783928470.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oAPartial))

	// OrderB: Submitted -> Accepted (event), then Cancelled_All -> Canceled (event).
	oB := baseOrder(orderIDB, remarkB, "MSFT", "Microsoft Corp.", 50, 310.00, "2026-07-11 09:32:10")
	oB.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_Submitted))
	oB.UpdateTimestamp = proto.Float64(1783928530.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oB))

	oBCancel := baseOrder(orderIDB, remarkB, "MSFT", "Microsoft Corp.", 50, 310.00, "2026-07-11 09:32:10")
	oBCancel.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_Cancelled_All))
	oBCancel.UpdateTimestamp = proto.Float64(1783928540.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oBCancel))

	// OrderC: straight to SubmitFailed -> domain Rejected (event, with LastErrMsg).
	oC := baseOrder(orderIDC, remarkC, "TSLA", "Tesla Inc.", 20, 250.00, "2026-07-11 09:33:00")
	oC.OrderStatus = proto.Int32(int32(trdcommon.OrderStatus_OrderStatus_SubmitFailed))
	oC.LastErrMsg = proto.String("Insufficient buying power")
	oC.UpdateTimestamp = proto.Float64(1783928580.0)
	writeFrame(orderFile, opend.ProtoTrdUpdateOrder, next(), orderResp(oC))

	// ---- Trd_UpdateOrderFill (2218) ----
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

	// OrderA fill #1: 40 @ 150.10. OrderA's total Qty (100, from the order
	// pushes above) makes LeavesQty = 60 after this fill.
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

	fmt.Println("wrote", filepath.Join(outDir, "trd_update_order.jsonl"))
	fmt.Println("wrote", filepath.Join(outDir, "trd_update_orderfill.jsonl"))
}
