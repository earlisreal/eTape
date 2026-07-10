package quota

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	getsubinfo "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsubinfo"
	histquota "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
)

// fakeReq returns a canned Frame body per protoID.
type fakeReq struct {
	bodies     map[uint32][]byte
	err        error
	gotAllConn *bool
}

func (f *fakeReq) Request(_ context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	if f.err != nil {
		return opend.Frame{}, f.err
	}
	if protoID == opend.ProtoQotGetSubInfo {
		f.gotAllConn = req.(*getsubinfo.Request).GetC2S().IsReqAllConn
	}
	return opend.Frame{ProtoID: protoID, Body: f.bodies[protoID]}, nil
}

func conn(own bool, used int32) *qotcommon.ConnSubInfo {
	return &qotcommon.ConnSubInfo{IsOwnConnData: proto.Bool(own), UsedQuota: proto.Int32(used)}
}

func subInfoBody(t *testing.T, total, remain int32, conns ...*qotcommon.ConnSubInfo) []byte {
	t.Helper()
	b, err := proto.Marshal(&getsubinfo.Response{
		RetType: proto.Int32(0),
		S2C: &getsubinfo.S2C{
			TotalUsedQuota:  proto.Int32(total),
			RemainQuota:     proto.Int32(remain),
			ConnSubInfoList: conns,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadSubInfoRequestsAllConn(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo: subInfoBody(t, 47, 53, conn(true, 47)),
	}}
	if _, err := readSubInfo(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if f.gotAllConn == nil || !*f.gotAllConn {
		t.Fatal("must send IsReqAllConn=true")
	}
}

// Divergent watchlists visible in the conn list: foreign from the list.
func TestReadSubInfoForeignFromConnList(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo: subInfoBody(t, 62, 38, conn(true, 47), conn(false, 15)),
	}}
	si, err := readSubInfo(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if si.own != 47 || si.foreign != 15 || si.totalUsed != 62 || si.remain != 38 {
		t.Fatalf("got %+v", si)
	}
}

// Remote conn invisible but totals account-global: foreign from arithmetic.
func TestReadSubInfoForeignFromTotals(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo: subInfoBody(t, 62, 38, conn(true, 47)),
	}}
	si, _ := readSubInfo(context.Background(), f)
	if si.foreign != 15 { // 62 total - 47 own
		t.Fatalf("foreign should fall back to totals arithmetic: %+v", si)
	}
}

// Identical watchlists (dedupe): totals blind (total==own) but conn list shows it.
func TestReadSubInfoForeignIdenticalWatchlist(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo: subInfoBody(t, 47, 53, conn(true, 47), conn(false, 47)),
	}}
	si, _ := readSubInfo(context.Background(), f)
	if si.foreign != 47 { // list wins over arithmetic (62-47=0)
		t.Fatalf("identical-watchlist foreign must come from conn list: %+v", si)
	}
}

func TestReadHistoryQuota(t *testing.T) {
	b, _ := proto.Marshal(&histquota.Response{
		RetType: proto.Int32(0),
		S2C:     &histquota.S2C{UsedQuota: proto.Int32(41), RemainQuota: proto.Int32(59)},
	})
	f := &fakeReq{bodies: map[uint32][]byte{opend.ProtoQotRequestHistoryKLQuota: b}}
	used, remain, err := readHistoryQuota(context.Background(), f)
	if err != nil || used != 41 || remain != 59 {
		t.Fatalf("used=%d remain=%d err=%v", used, remain, err)
	}
}
