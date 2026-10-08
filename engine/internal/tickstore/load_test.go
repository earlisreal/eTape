package tickstore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
	"google.golang.org/protobuf/proto"
)

// This declared workload is evidence, not an all-market burst guarantee.
func TestRecordingLoadComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock capacity measurement; run without -short")
	}
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("recording=%v", enabled), func(t *testing.T) {
			dir := t.TempDir()
			var archive *tickstore.Store
			var recorder feed.Recorder
			if enabled {
				var err error
				archive, err = tickstore.Open(tickstore.Options{Directory: filepath.Join(dir, "ticks"), MinFreeBytes: 1, Decode: opend.DecodeRecording})
				if err != nil {
					t.Fatal(err)
				}
				recorder = archive
			}
			journal, err := store.Open(store.Options{Path: filepath.Join(dir, "execution.sqlite")})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := journal.Close(); err != nil {
					t.Error(err)
				}
			}()
			core := md.New(md.Config{Recorder: recorder})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _ = core.Run(ctx) }()
			var drainDone = make(chan struct{})
			go func() {
				defer close(drainDone)
				for {
					select {
					case <-ctx.Done():
						return
					case <-core.Updates():
					case <-core.EligiblePrints():
					case <-core.Books():
					}
				}
			}()
			defer func() {
				cancel()
				<-done
				<-drainDone
				if archive != nil {
					closeArchive(t, archive)
				}
			}()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			cpu := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
			metrics.Read(cpu)
			cpuBefore := cpu[0].Value.Float64() + cpu[1].Value.Float64()
			start := time.Now()
			var maxWork, maxChart, maxJournal int64
			for frame := range 550 {
				if frame < 500 {
					target := start.Add(time.Duration(frame) * 20 * time.Millisecond)
					time.Sleep(time.Until(target))
				}
				symbol := fmt.Sprintf("US.LOAD%02d", frame%50)
				var raw []*qotcommon.Ticker
				var ticks []feed.Tick
				ref := feed.SourceRef{Run: "load-disabled", Connection: 1, Ingress: uint64(frame + 1), Index: -1, ReceiptMs: time.Now().UnixMilli()}
				if archive != nil {
					ref.Run = archive.RunID()
				}
				for i := range 10 {
					seq := int64(frame*10 + i + 1)
					ts := int64(1791446772000) + int64(frame*20+i)
					price := 2.0 + float64(seq%100)/10000
					raw = append(raw, &qotcommon.Ticker{Time: proto.String("2026-10-08 04:06:12"), Timestamp: proto.Float64(float64(ts) / 1000), Sequence: proto.Int64(seq), Price: proto.Float64(price), Volume: proto.Int64(10), Turnover: proto.Float64(price * 10), Dir: proto.Int32(1), Type: proto.Int32(0)})
					itemRef := ref
					itemRef.Index = int32(i)
					ticks = append(ticks, feed.Tick{Source: &itemRef, Symbol: symbol, Seq: seq, TsMs: ts, Price: price, Volume: 10, Dir: feed.Buy, Type: feed.TransactionRegular, Condition: feed.TradeConditionAutomaticMatch, Delivery: feed.DeliveryRealtime})
				}
				body, err := proto.Marshal(&qotupdateticker.Response{RetType: proto.Int32(0), S2C: &qotupdateticker.S2C{Security: &qotcommon.Security{Market: proto.Int32(11), Code: proto.String(symbol[3:])}, TickerList: raw}})
				if err != nil {
					t.Fatal(err)
				}
				if archive != nil && !archive.Record(feed.Recording{Kind: "source", Source: ref, Body: body, Data: feed.SourceMessage{Protocol: opend.ProtoQotUpdateTicker, Origin: "push"}}) {
					t.Fatal("healthy source frame rejected")
				}
				chartStart := time.Now()
				core.Feed(feed.TicksEvent{Ticks: ticks})
				core.SeedChartHistory(symbol, nil, nil, nil)
				maxChart = max(maxChart, time.Since(chartStart).Microseconds())
				if frame%25 == 0 {
					journalStart := time.Now()
					journal.AppendSysEvent("load-fixture", "recording comparison")
					journal.Flush()
					maxJournal = max(maxJournal, time.Since(journalStart).Microseconds())
				}
				if archive != nil {
					maxWork = max(maxWork, archive.Stats().WorkingBytes)
				}
			}
			if core.DropStats().Inbox != 0 {
				t.Fatalf("MD inbox drops: %+v", core.DropStats())
			}
			if archive != nil {
				waitArchive(t, func() bool { return archive.Stats().WorkingItems == 0 })
				stats := archive.Stats()
				if stats.MaxCommitLagMs > 1000 || stats.Lost != [feed.RecordingLaneCount]uint64{} {
					t.Fatalf("healthy capacity: %+v", stats)
				}
			}
			cancel()
			<-done
			<-drainDone
			if archive != nil {
				closeArchive(t, archive)
			}
			runtime.ReadMemStats(&after)
			metrics.Read(cpu)
			cpuSeconds := cpu[0].Value.Float64() + cpu[1].Value.Float64() - cpuBefore
			var physical int64
			if archive != nil {
				entries, err := os.ReadDir(filepath.Join(dir, "ticks"))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					info, err := entry.Info()
					if err != nil {
						t.Fatal(err)
					}
					physical += info.Size()
				}
			}
			var commitLag int64
			if archive != nil {
				commitLag = archive.Stats().MaxCommitLagMs
			}
			t.Logf("50 symbols; sustained 500 prints/s for 10s + 500-print burst; elapsed=%s Go-user+GC-CPU=%.3fs allocated=%d heap=%d work_peak=%d receipt_commit_max=%dms MD-chart-barrier_max=%dus execution-journal-flush_max=%dus physical=%d", time.Since(start).Round(time.Millisecond), cpuSeconds, after.TotalAlloc-before.TotalAlloc, after.HeapInuse, maxWork, commitLag, maxChart, maxJournal, physical)
		})
	}
}
