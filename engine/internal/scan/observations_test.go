package scan

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestNormalizeSnapshotRequiresTheActiveExtendedBlock(t *testing.T) {
	now := et(2026, 7, 8, 8, 0)
	basic := snapshotBasic("A")
	if got := normalizeSnapshot(basic, session.PreMarket, now); got.usable {
		t.Fatal("regular fields were accepted as a pre-market observation")
	}
	basic.PreMarket = &qotcommon.PreAfterMarketData{Price: proto.Float64(11), ChangeRate: proto.Float64(10), Volume: proto.Int64(3)}
	if got := normalizeSnapshotWithClose(basic, session.PreMarket, now, proto.Float64(10)); got.usable {
		t.Fatal("extended snapshot without an overall update timestamp was accepted")
	}
	basic.UpdateTimestamp = proto.Float64(float64(now.Unix()))
	if got := normalizeSnapshotWithClose(basic, session.PreMarket, now, proto.Float64(10)); !got.usable || got.price != 11 || !got.hasClose {
		t.Fatalf("active pre-market block was not normalized: %+v", got)
	}
	if got := normalizeSnapshot(basic, session.PreMarket, now); got.hasClose {
		t.Fatal("extended snapshot invented a previous close without provenance")
	}
	basic.UpdateTimestamp = proto.Float64(float64(session.TradingCycleStart(now).Add(-time.Second).Unix()))
	if got := normalizeSnapshot(basic, session.PreMarket, now); got.usable {
		t.Fatal("stale previous-cycle observation was accepted")
	}
}

func TestNormalizeSnapshotRejectsPreOpenTimestampDuringRTH(t *testing.T) {
	now := et(2026, 7, 8, 10, 0)
	basic := snapshotBasic("A")
	basic.CurPrice = proto.Float64(11)
	basic.LastClosePrice = proto.Float64(10)
	basic.UpdateTimestamp = proto.Float64(float64(session.Schedule(now).Open.Add(-time.Minute).Unix()))
	if got := normalizeSnapshot(basic, session.RTH, now); got.usable {
		t.Fatal("pre-open snapshot was accepted as an RTH observation")
	}
}

func TestNormalizeSnapshotDoesNotInventClosedMarketObservations(t *testing.T) {
	basic := snapshotBasic("A")
	basic.PreMarket = &qotcommon.PreAfterMarketData{Price: proto.Float64(11), ChangeRate: proto.Float64(10)}
	if got := normalizeSnapshot(basic, session.Closed, et(2026, 7, 11, 12, 0)); got.usable {
		t.Fatal("closed market accepted a synthetic pre-market observation")
	}
}
