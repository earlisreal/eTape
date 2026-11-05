package screener

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	screenpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockscreen"
	"google.golang.org/protobuf/proto"
)

type requestFunc func(context.Context, uint32, proto.Message) (opend.Frame, error)

func (f requestFunc) Request(ctx context.Context, id uint32, m proto.Message) (opend.Frame, error) {
	return f(ctx, id, m)
}
func sourceFrame(t *testing.T, m proto.Message) opend.Frame {
	t.Helper()
	body, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return opend.Frame{Body: body}
}
func staticEntry(code string, exchange, secType int32) *qotcommon.SecurityStaticInfo {
	return &qotcommon.SecurityStaticInfo{Basic: &qotcommon.SecurityStaticBasic{Security: &qotcommon.Security{Market: proto.Int32(11), Code: proto.String(code)}, Id: proto.Int64(1), LotSize: proto.Int32(1), SecType: proto.Int32(secType), Name: proto.String(code), ListTime: proto.String("2026-01-02"), ExchType: proto.Int32(exchange)}}
}
func screenItem(code string) *screenpb.StockScreenItem {
	return &screenpb.StockScreenItem{Results: []*screenpb.RspItemResult{{BasicPropertyResult: &screenpb.ResultPropertyBasic{Property: &screenpb.PropertyBasic{Name: proto.Int32(1101)}, Sval: proto.String(code)}}}}
}

func TestDiscoveryPaginatesAndExcludesOTCAndFunds(t *testing.T) {
	var pages []int32
	r := requestFunc(func(_ context.Context, id uint32, m proto.Message) (opend.Frame, error) {
		if id == opend.ProtoQotGetSecuritySnapshot {
			return sourceFrame(t, &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{}}), nil
		}
		if id == opend.ProtoQotGetStaticInfo {
			return sourceFrame(t, &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{StaticInfoList: []*qotcommon.SecurityStaticInfo{staticEntry("AAA", 4, 3), staticEntry("BBB", 7, 3), staticEntry("PINK", 6, 3), staticEntry("ETF", 5, 4)}}}), nil
		}
		if id != opend.ProtoQotGetStockScreen {
			t.Fatalf("unexpected protocol %d", id)
		}
		q := m.(*screenpb.Request).GetC2S()
		pages = append(pages, q.GetPageFrom())
		if q.GetFilterList()[0].GetSimpleFieldQuery().GetSimpleField() != 1 {
			t.Fatal("wrong market enum")
		}
		if q.GetFilterList()[1].GetCumulativePropertyQuery().GetProperty().GetDays() != 60 {
			t.Fatal("wrong high period")
		}
		if q.GetPageFrom() == 0 {
			return sourceFrame(t, &screenpb.Response{RetType: proto.Int32(0), S2C: &screenpb.S2C{AllCount: proto.Int32(201), LastPage: proto.Int32(0), DataList: []*screenpb.StockScreenItem{screenItem("AAA")}}}), nil
		}
		return sourceFrame(t, &screenpb.Response{RetType: proto.Int32(0), S2C: &screenpb.S2C{LastPage: proto.Int32(1), DataList: []*screenpb.StockScreenItem{screenItem("US.BBB")}}}), nil
	})
	date := testDate("2026-11-04")
	discovery, err := NewMarketSource(r, nil, nil).Discover(t.Context(), testFilters(), date, date.Add(17*time.Hour))
	if err != nil || len(discovery.Stocks) != 2 || discovery.Universe != 2 || discovery.Warning != "" || len(pages) != 2 || pages[1] != 200 {
		t.Fatalf("discovery=%+v pages=%v err=%v", discovery, pages, err)
	}
}

func TestDiscoveryFallsBackWithoutTruncatingUniverse(t *testing.T) {
	for _, afterClose := range []bool{true, false} {
		t.Run(map[bool]string{true: "screen failure", false: "prior session"}[afterClose], func(t *testing.T) {
			calls := 0
			r := requestFunc(func(_ context.Context, id uint32, _ proto.Message) (opend.Frame, error) {
				if id == opend.ProtoQotGetSecuritySnapshot {
					return sourceFrame(t, &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{}}), nil
				}
				if id == opend.ProtoQotGetStockScreen {
					calls++
					return opend.Frame{}, errors.New("unavailable")
				}
				return sourceFrame(t, &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{StaticInfoList: []*qotcommon.SecurityStaticInfo{staticEntry("AAA", 5, 3)}}}), nil
			})
			date := testDate("2026-11-04")
			now := date.Add(17 * time.Hour)
			if !afterClose {
				now = date.AddDate(0, 0, 1).Add(12 * time.Hour)
			}
			discovery, err := NewMarketSource(r, nil, nil).Discover(t.Context(), testFilters(), date, now)
			if err != nil || len(discovery.Stocks) != 1 || discovery.Universe != 1 || (afterClose && (discovery.Warning == "" || calls != 1)) || (!afterClose && calls != 0) {
				t.Fatalf("discovery=%+v calls=%d err=%v", discovery, calls, err)
			}
		})
	}
}
