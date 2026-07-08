package opend

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// snapRPC is a one-shot rpc seam returning a fixed frame/error for the probe.
type snapRPC struct {
	resp *qotgetsecuritysnapshot.Response
	err  error
	got  uint32 // protoID actually requested
}

func (s *snapRPC) Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error) {
	s.got = protoID
	if s.err != nil {
		return Frame{}, s.err
	}
	b, _ := proto.Marshal(s.resp)
	return Frame{ProtoID: protoID, Body: b}, nil
}

func snapshotResp(retType int32, retMsg string, rows int) *qotgetsecuritysnapshot.Response {
	list := make([]*qotgetsecuritysnapshot.Snapshot, rows)
	for i := range list {
		list[i] = &qotgetsecuritysnapshot.Snapshot{
			Basic: &qotgetsecuritysnapshot.SnapshotBasicData{
				Security:       &qotcommon.Security{Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), Code: proto.String("AAPL")},
				Type:           proto.Int32(0),
				IsSuspend:      proto.Bool(false),
				ListTime:       proto.String("1980-12-12"),
				LotSize:        proto.Int32(100),
				PriceSpread:    proto.Float64(0.01),
				UpdateTime:     proto.String("2026-07-08 09:30:00"),
				HighPrice:      proto.Float64(310),
				OpenPrice:      proto.Float64(308),
				LowPrice:       proto.Float64(307),
				LastClosePrice: proto.Float64(305),
				CurPrice:       proto.Float64(309),
				Volume:         proto.Int64(1000000),
				Turnover:       proto.Float64(300000000),
				TurnoverRate:   proto.Float64(1.5),
			},
		}
	}
	return &qotgetsecuritysnapshot.Response{
		RetType: proto.Int32(retType),
		RetMsg:  proto.String(retMsg),
		S2C:     &qotgetsecuritysnapshot.S2C{SnapshotList: list},
	}
}

func TestSecurityExists_Ok(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 1)}
	bf := newBackfill(r)
	if err := bf.securityExists(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if r.got != ProtoQotGetSecuritySnapshot {
		t.Fatalf("want protoID %d, got %d", ProtoQotGetSecuritySnapshot, r.got)
	}
}

func TestSecurityExists_UnknownStock(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "Unknown stock. ZZZZQQ", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.ZZZZQQ")
	if !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol, got %v", err)
	}
}

func TestSecurityExists_EmptyListIsUnknown(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.NADA")
	if !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol on empty list, got %v", err)
	}
}

func TestSecurityExists_TransportIsUnavailable(t *testing.T) {
	r := &snapRPC{err: ErrRequestTimeout}
	err := newBackfill(r).securityExists(context.Background(), "US.AAPL")
	if !errors.Is(err, feed.ErrFeedUnavailable) {
		t.Fatalf("want ErrFeedUnavailable, got %v", err)
	}
}

func TestSecurityExists_OtherRetTypeIsUnavailable(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "server busy", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.AAPL")
	if !errors.Is(err, feed.ErrFeedUnavailable) {
		t.Fatalf("want ErrFeedUnavailable for non-'unknown stock' failure, got %v", err)
	}
}
