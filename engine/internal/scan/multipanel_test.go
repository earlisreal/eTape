package scan

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockscreen"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestFetchSessionVolumeUsesUSSessionFieldAndKeepsFlatStocks(t *testing.T) {
	var captured *qotstockscreen.Request
	r := requesterFunc(func(_ context.Context, id uint32, request proto.Message) (opend.Frame, error) {
		if id != opend.ProtoQotGetStockScreen {
			t.Fatalf("protocol = %d, want StockScreen V2", id)
		}
		captured = request.(*qotstockscreen.Request)
		return frameOf(&qotstockscreen.Response{RetType: proto.Int32(0), S2C: &qotstockscreen.S2C{DataList: []*qotstockscreen.StockScreenItem{
			{Results: []*qotstockscreen.RspItemResult{
				{BasicPropertyResult: &qotstockscreen.ResultPropertyBasic{Property: &qotstockscreen.PropertyBasic{Name: proto.Int32(1101)}, Sval: proto.String("FLAT")}},
				{SimplePropertyResult: &qotstockscreen.ResultPropertySimple{Property: &qotstockscreen.PropertySimple{Name: proto.Int32(2409)}, Ival: proto.Int64(0)}},
			}},
			{Results: []*qotstockscreen.RspItemResult{
				{BasicPropertyResult: &qotstockscreen.ResultPropertyBasic{Property: &qotstockscreen.PropertyBasic{Name: proto.Int32(1101)}, Sval: proto.String("ACTIVE")}},
				{SimplePropertyResult: &qotstockscreen.ResultPropertySimple{Property: &qotstockscreen.PropertySimple{Name: proto.Int32(2409)}, Dval: proto.Float64(125_000.9)}},
			}},
			{Results: []*qotstockscreen.RspItemResult{
				{BasicPropertyResult: &qotstockscreen.ResultPropertyBasic{Property: &qotstockscreen.PropertyBasic{Name: proto.Int32(1101)}, Sval: proto.String("MISSING")}},
			}},
		}}}), nil
	})
	poller := New(config.Scan{}, r, nil, clock.NewFake(time.Unix(1_800_000_000, 0)), nil, nil, nil)
	items, err := poller.fetchSessionVolume(context.Background(), session.PostMarket)
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.GetC2S().GetPageCount() != 200 || captured.GetC2S().GetPageFrom() != 0 {
		t.Fatalf("screen request = %+v, want first page of 200", captured)
	}
	if len(captured.GetC2S().GetFilterList()) != 1 || captured.GetC2S().GetFilterList()[0].GetSimpleFieldQuery().GetScreenValueList()[0] != 2 {
		t.Fatalf("market filter = %+v, want US market enum 2", captured.GetC2S().GetFilterList())
	}
	if captured.GetC2S().GetSort().GetSimpleProperty().GetName() != 2409 || captured.GetC2S().GetSort().GetDirection() != 2 {
		t.Fatalf("sort = %+v, want after-hours volume 2409 descending", captured.GetC2S().GetSort())
	}
	if len(items) != 2 || items[0].Symbol != "US.FLAT" || items[0].sessionVolume == nil || *items[0].sessionVolume != 0 {
		t.Fatalf("flat or unavailable volume decode = %+v, want zero-volume flat stock plus active stock", items)
	}
	if items[1].Symbol != "US.ACTIVE" || items[1].Volume != 125_000 || items[1].sessionVolumePhase != session.PostMarket {
		t.Fatalf("session volume decode = %+v", items[1])
	}
}

func TestCanceledScannerRankFetchDoesNotDelayRetry(t *testing.T) {
	poller := New(config.Scan{}, nil, nil, clock.NewFake(time.Unix(1_800_000_000, 0)), nil, nil, nil)
	now := time.Unix(1_800_000_000, 0)
	called := 0
	fetch := func() ([]rankItem, error) {
		called++
		if called == 1 {
			return nil, context.Canceled
		}
		return []rankItem{{Symbol: "US.A", ChangePct: 1}}, nil
	}
	if _, _, err := poller.cachedPanelRank(context.Background(), "rth/gainers", now, scannerRankCadence, fetch); !errors.Is(err, context.Canceled) {
		t.Fatalf("first fetch error = %v, want cancellation", err)
	}
	items, _, err := poller.cachedPanelRank(context.Background(), "rth/gainers", now, scannerRankCadence, fetch)
	if err != nil || called != 2 || len(items) != 1 || items[0].Symbol != "US.A" {
		t.Fatalf("retry = items:%+v err:%v calls:%d, want successful immediate retry", items, err, called)
	}
}

func TestScannerPanelsKeepFiltersAndConnectionsIndependent(t *testing.T) {
	filtersA := wsmsg.ScannerFilters{Mode: "gainers", MinChangePct: 8, FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	filtersB := wsmsg.ScannerFilters{Mode: "session_volume", MinSessionVolume: 500_000, FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	poller := New(config.Scan{}, nil, nil, clock.NewFake(time.Unix(1_800_000_000, 0)), nil, nil, nil)
	args := wsmsg.SetScannerWorkspaceArgs{WorkspaceID: "main", Panels: []wsmsg.ScannerPanelSettings{
		{PanelID: "scanner-a", Filters: filtersA},
		{PanelID: "scanner-b", Filters: filtersB},
	}}
	if err := poller.SetScannerWorkspace(1, args); err != nil {
		t.Fatal(err)
	}
	if err := poller.SetScannerWorkspace(2, args); err != nil {
		t.Fatal(err)
	}
	panels := poller.activeScannerPanels()
	if len(panels) != 2 || !reflect.DeepEqual(panels[0].filters, filtersA) && !reflect.DeepEqual(panels[1].filters, filtersA) {
		t.Fatalf("active panels = %+v", panels)
	}
	if err := poller.SetPanelFilters("main", "scanner-a", wsmsg.ScannerFilters{Mode: "losers", MinChangePct: 4, FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(poller.scannerPanels[scannerPanelID("main", "scanner-b")].filters, filtersB) {
		t.Fatal("changing one panel's filters changed its peer")
	}
	poller.ReleaseScannerConnection(1)
	if got := len(poller.activeScannerPanels()); got != 2 {
		t.Fatalf("duplicate window close left %d panels active, want 2", got)
	}
	poller.ReleaseScannerConnection(2)
	if got := len(poller.activeScannerPanels()); got != 0 {
		t.Fatalf("closed workspace left %d panels active", got)
	}
}

func TestScannerSourceStaysActiveAfterItsWorkspaceCloses(t *testing.T) {
	filters := wsmsg.ScannerFilters{Mode: "session_volume", FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	pub := &capturePub{}
	poller := New(config.Scan{}, nil, pub, clock.NewFake(time.Unix(1_800_000_000, 0)), nil, nil, nil)
	if err := poller.SetScannerWorkspace(1, wsmsg.SetScannerWorkspaceArgs{
		WorkspaceID: "monitoring", SourceEnabled: true,
		SourceWorkspaceID: "main", SourcePanelID: "scanner-source", SourceFilters: &filters,
	}); err != nil {
		t.Fatal(err)
	}
	state := poller.scannerPanels[scannerPanelID("main", "scanner-source")]
	state.board["US.OLD"] = rankItem{Symbol: "US.OLD"}
	state.seen["rth"] = map[string]bool{"US.OLD": true}
	state.baseline = false
	if got := len(poller.activeScannerPanels()); got != 1 {
		t.Fatalf("active panels = %d, want selected source only", got)
	}
	if err := poller.SetScannerWorkspace(2, wsmsg.SetScannerWorkspaceArgs{WorkspaceID: "main"}); err != nil {
		t.Fatal(err)
	}
	poller.ReleaseScannerConnection(2)
	if got := len(poller.activeScannerPanels()); got != 1 {
		t.Fatalf("closing the source window disabled its selected board; active=%d", got)
	}
	if len(state.board) != 1 || len(state.seen) != 1 || state.baseline {
		t.Fatalf("selected board was cleared when its host closed: %+v", state)
	}
	if err := poller.SetScannerWorkspace(1, wsmsg.SetScannerWorkspaceArgs{WorkspaceID: "monitoring"}); err != nil {
		t.Fatal(err)
	}
	if got := len(poller.activeScannerPanels()); got != 0 {
		t.Fatalf("closed Monitoring left %d scanners active", got)
	}
	if len(state.board) != 0 || len(state.seen) != 0 || !state.baseline || state.status != "paused" {
		t.Fatalf("deselected source retained stale board state: %+v", state)
	}
	if len(pub.ranks) != 4 {
		t.Fatalf("source deactivation published %d rank updates, want one paused baseline per session", len(pub.ranks))
	}
}

func TestScannerPanelFreshnessUsesOldestVisibleRowSnapshot(t *testing.T) {
	first := time.Unix(1_800_000_000, 0)
	second := first.Add(time.Second)
	fallback := second.Add(time.Second)
	board := map[string]rankItem{
		"US.A": {Symbol: "US.A", snapshotAt: second},
		"US.B": {Symbol: "US.B", snapshotAt: first},
	}
	rows := []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.B"}}
	if got := panelSnapshotTime(rows, board, fallback); !got.Equal(first) {
		t.Fatalf("panel freshness = %v, want oldest row snapshot %v", got, first)
	}
	board["US.B"] = rankItem{Symbol: "US.B"}
	if got := panelSnapshotTime(rows, board, fallback); !got.IsZero() {
		t.Fatalf("panel freshness = %v, want unknown when a visible row lacks a snapshot", got)
	}
	if got := panelSnapshotTime(nil, board, fallback); !got.Equal(fallback) {
		t.Fatalf("empty-board freshness = %v, want latest empty-result fallback %v", got, fallback)
	}
}

func TestClosingLastScannerCancelsPollBeforeItCanRepublish(t *testing.T) {
	requestStarted := make(chan struct{})
	requester := requesterFunc(func(ctx context.Context, _ uint32, _ proto.Message) (opend.Frame, error) {
		close(requestStarted)
		<-ctx.Done()
		return opend.Frame{}, ctx.Err()
	})
	pub := &capturePub{}
	feed := &spyFeed{}
	poller := New(config.Scan{}, requester, pub, clock.NewFake(time.Unix(1_800_000_000, 0)), feed, nil, nil)
	filters := wsmsg.ScannerFilters{Mode: "gainers", FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	if err := poller.SetScannerWorkspace(1, wsmsg.SetScannerWorkspaceArgs{
		WorkspaceID: "main", Panels: []wsmsg.ScannerPanelSettings{{PanelID: "scanner-a", Filters: filters}},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		poller.pollOnce(context.Background(), poller.clk.Now())
		close(done)
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("scanner poll did not start its provider request")
	}
	poller.ReleaseScannerConnection(1)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing the last scanner did not cancel its in-flight request")
	}
	if len(pub.ranks) != 4 {
		t.Fatalf("rank publications = %d, want only four paused baselines", len(pub.ranks))
	}
	for _, payload := range pub.ranks {
		if payload.Status != "paused" || len(payload.Rows) != 0 {
			t.Fatalf("closed scanner was republished: %+v", payload)
		}
	}
	if len(feed.ensured) != 0 || len(poller.PoolSymbols()) != 0 {
		t.Fatalf("closed scanner reactivated pool: ensured=%v pool=%v", feed.ensured, poller.PoolSymbols())
	}
}

func TestClosedScannerWorkspacesReleaseSharedWarmPool(t *testing.T) {
	feed := &spyFeed{}
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	poller := New(config.Scan{}, nil, nil, clk, feed, nil, nil)
	poller.updatePool(clk.Now(), rows("US.A", "US.B"))
	if got := poller.PoolSymbols(); len(got) != 2 {
		t.Fatalf("initial pool = %v, want both scanner rows", got)
	}
	filters := wsmsg.ScannerFilters{Mode: "gainers", FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	if err := poller.SetScannerWorkspace(1, wsmsg.SetScannerWorkspaceArgs{
		WorkspaceID: "main", Panels: []wsmsg.ScannerPanelSettings{{PanelID: "scanner-a", Filters: filters}},
	}); err != nil {
		t.Fatal(err)
	}
	poller.ReleaseScannerConnection(1)
	poller.pollOnce(context.Background(), clk.Now())
	if got := poller.PoolSymbols(); len(got) != 0 {
		t.Fatalf("closed workspace left pool symbols: %v", got)
	}
	if !reflect.DeepEqual(feed.released, []string{"scan:US.A", "scan:US.B"}) {
		t.Fatalf("released demands = %v, want both scanner-only demands", feed.released)
	}
}

func TestClosingLastScannerClearsItsBoardAndPublishesPausedBaseline(t *testing.T) {
	pub := &capturePub{}
	filters := wsmsg.ScannerFilters{Mode: "gainers", FloatUnit: "M", VolumeUnit: "K", SessionVolumeUnit: "K"}
	poller := New(config.Scan{}, nil, pub, clock.NewFake(time.Unix(1_800_000_000, 0)), nil, nil, nil)
	if err := poller.SetScannerWorkspace(1, wsmsg.SetScannerWorkspaceArgs{
		WorkspaceID: "main", Panels: []wsmsg.ScannerPanelSettings{{PanelID: "scanner-a", Filters: filters}},
	}); err != nil {
		t.Fatal(err)
	}
	id := scannerPanelID("main", "scanner-a")
	state := poller.scannerPanels[id]
	state.board["US.OLD"] = rankItem{Symbol: "US.OLD"}
	state.seen["rth"] = map[string]bool{"US.OLD": true}
	state.baseline = false

	poller.ReleaseScannerConnection(1)
	if len(state.board) != 0 || len(state.seen) != 0 || !state.baseline {
		t.Fatalf("closed board state = %+v, want cleared board/seen and a pending baseline", state)
	}
	if len(pub.ranks) != 4 {
		t.Fatalf("paused publications = %d, want one per session", len(pub.ranks))
	}
	for _, payload := range pub.ranks {
		if payload.ScannerID != id || payload.Status != "paused" || !payload.Baseline || len(payload.Rows) != 0 {
			t.Fatalf("paused publication = %+v", payload)
		}
	}
}
