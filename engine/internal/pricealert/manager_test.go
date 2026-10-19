package pricealert_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/pricealert"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

type availableFeed struct{}

type failingRepository struct {
	pricealert.Repository
	fail bool
}

func (r *failingRepository) CommitPriceAlerts(c pricealert.Change) error {
	if r.fail {
		return errors.New("disk unavailable")
	}
	return r.Repository.CommitPriceAlerts(c)
}

func TestStorageFailureRetainsTouchUntilDurableRecovery(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := &failingRepository{Repository: db}
	notifications := 0
	manager, err := pricealert.New(pricealert.Options{Clock: clk, Repository: repo, Demand: availableFeed{}, OnNotification: func(pricealert.Notification) { notifications++ }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Upsert(pricealert.Draft{Symbol: "NVDA", Price: 139, Condition: pricealert.AtOrAbove, Sessions: pricealert.Sessions{RTH: true}})
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	clk.Advance(time.Millisecond)
	now := clk.Now().UnixMilli()
	if manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 1}) == nil {
		t.Fatal("storage error hidden")
	}
	if notifications != 0 || len(manager.Snapshot().History) != 0 {
		t.Fatal("undurable event announced")
	}
	repo.fail = false
	manager.Refresh()
	got := manager.Snapshot()
	if len(got.History) != 1 || got.History[0].ActualPrice != 140 || got.Alerts[0].State != pricealert.Triggered {
		t.Fatalf("lost failed touch: %+v", got)
	}
	if notifications != 0 {
		t.Fatal("recovered history replayed sound")
	}
}

func (availableFeed) Ensure(string)                               {}
func (availableFeed) Release(string)                              {}
func (availableFeed) Availability(string) pricealert.Availability { return pricealert.Available }

type controlledFeed struct {
	state  pricealert.Availability
	leases map[string]bool
}

func (f *controlledFeed) Ensure(symbol string)                        { f.leases[symbol] = true }
func (f *controlledFeed) Release(symbol string)                       { delete(f.leases, symbol) }
func (f *controlledFeed) Availability(string) pricealert.Availability { return f.state }

func TestTriggerIsOneShotAndDurableAcrossRestart(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	options := pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{}}
	manager, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	alert, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139,
		Sessions: pricealert.Sessions{Pre: true, RTH: true, Post: true}})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	print := pricealert.Print{Symbol: "NVDA", Price: 139.25, TsMs: clk.Now().UnixMilli(), RecvTsMs: clk.Now().UnixMilli(), Seq: 1}
	if err := manager.Ingest(print); err != nil {
		t.Fatal(err)
	}
	print.Seq++
	if err := manager.Ingest(print); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot.History) != 1 || snapshot.Alerts[0].State != pricealert.Triggered {
		t.Fatalf("one-shot alert: %+v", snapshot)
	}
	if trigger := snapshot.History[0]; trigger.AlertID != alert.ID || trigger.Price != 139 || trigger.ActualPrice != 139.25 {
		t.Fatalf("wrong trigger snapshot: %+v", trigger)
	}
	restarted, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Snapshot(); len(got.History) != 1 || got.Alerts[0].State != pricealert.Triggered {
		t.Fatalf("restart lost the one-shot trigger: %+v", got)
	}
}

func TestOnlyFreshTradesAfterArmingAndDataGapsCanTrigger(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := pricealert.New(pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139, Sessions: pricealert.Sessions{RTH: true}})
	if err != nil {
		t.Fatal(err)
	}
	now := clk.Now().UnixMilli()
	for _, print := range []pricealert.Print{
		{Symbol: "NVDA", Price: 140, TsMs: now - 1, RecvTsMs: now - 1, Seq: 1},
		{Symbol: "NVDA", Price: 140, TsMs: now - 3_000, RecvTsMs: now - 3_000, Seq: 2},
		{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 0},
	} {
		if err := manager.Ingest(print); err != nil {
			t.Fatal(err)
		}
	}
	if got := manager.Snapshot(); len(got.History) != 0 {
		t.Fatalf("pre-arm/stale/seed input triggered: %+v", got)
	}
	clk.Advance(time.Millisecond)
	gapAt := clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Gap: true, RecvTsMs: gapAt}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: gapAt, RecvTsMs: gapAt, Seq: 3}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().History) != 0 {
		t.Fatal("pre-gap queued data triggered")
	}
	clk.Advance(time.Millisecond)
	fresh := clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: fresh, RecvTsMs: fresh, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot(); len(got.History) != 1 || got.History[0].Reason != "Target met on resume" {
		t.Fatalf("fresh recovery: %+v", got)
	}
}

func TestRearmAndDeleteKeepIndependentTriggerHistory(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	options := pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{}}
	manager, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	draft := pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139, Label: "First", Sessions: pricealert.Sessions{RTH: true}}
	alert, err := manager.Upsert(draft)
	if err != nil {
		t.Fatal(err)
	}
	trade := func(seq int64) {
		t.Helper()
		clk.Advance(time.Millisecond)
		now := clk.Now().UnixMilli()
		if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 139.5, TsMs: now, RecvTsMs: now, Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	trade(1)
	triggered := manager.Snapshot().Alerts[0]
	draft.ID, draft.Version, draft.Label = alert.ID, triggered.Version, "Second"
	edited, err := manager.Upsert(draft)
	if err != nil {
		t.Fatal(err)
	}
	trade(2)
	if edited.State != pricealert.Triggered || len(manager.Snapshot().History) != 1 {
		t.Fatal("editing silently rearmed the alert")
	}
	draft.Version, draft.Rearm = edited.Version, true
	if _, err := manager.Upsert(draft); err != nil {
		t.Fatal(err)
	}
	trade(3)
	got := manager.Snapshot()
	if len(got.History) != 2 || got.History[0].Label != "Second" || got.History[1].Label != "First" {
		t.Fatalf("history was replaced: %+v", got)
	}
	if err := manager.Delete(alert.ID, got.Alerts[0].Version); err != nil {
		t.Fatal(err)
	}
	restarted, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Snapshot(); len(got.Alerts) != 0 || len(got.History) != 2 {
		t.Fatalf("deleting definition erased history: %+v", got)
	}
	if err := restarted.DeleteTrigger(got.History[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(restarted.Snapshot().History) != 1 {
		t.Fatal("history deletion removed the wrong number of occurrences")
	}
}

func TestSelectedSessionsAndExplicitPauseSurviveDataRecovery(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 8, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := pricealert.New(pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{}})
	if err != nil {
		t.Fatal(err)
	}
	alert, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139, Sessions: pricealert.Sessions{RTH: true}})
	if err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot().Alerts[0].State != pricealert.OutsideSession {
		t.Fatal("RTH-only alert is monitoring premarket")
	}
	clk.Advance(time.Millisecond)
	now := clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().History) != 0 {
		t.Fatal("excluded session triggered")
	}
	clk.Advance(2 * time.Hour)
	manager.Refresh()
	paused, err := manager.SetPaused(alert.ID, alert.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Ingest(pricealert.Print{Gap: true, RecvTsMs: clk.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	now = clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot(); got.Alerts[0].State != pricealert.Paused || len(got.History) != 0 {
		t.Fatalf("gap recovery resumed a paused alert: %+v", got)
	}
	if _, err := manager.SetPaused(alert.ID, paused.Version, false); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot(); got.Alerts[0].State != pricealert.WaitingData || len(got.History) != 0 {
		t.Fatalf("resume reused cached price: %+v", got)
	}
	clk.Advance(time.Millisecond)
	now = clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 3}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().History) != 1 {
		t.Fatal("fresh RTH input did not resume the alert")
	}
}

func TestBackgroundDemandWaitsForCapacityAndReleasesWhenPaused(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider := &controlledFeed{state: pricealert.CapacityUnavailable, leases: make(map[string]bool)}
	manager, err := pricealert.New(pricealert.Options{Clock: clk, Repository: db, Demand: provider})
	if err != nil {
		t.Fatal(err)
	}
	alert, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139, Sessions: pricealert.Sessions{RTH: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.leases["NVDA"] || manager.Snapshot().Alerts[0].State != pricealert.WaitingCapacity {
		t.Fatal("off-chart alert did not retain a visibly waiting demand")
	}
	clk.Advance(time.Millisecond)
	now := clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140, TsMs: now, RecvTsMs: now, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	provider.state = pricealert.Available
	manager.Refresh()
	if got := manager.Snapshot(); got.Alerts[0].State != pricealert.WaitingData || len(got.History) != 0 {
		t.Fatalf("capacity recovery reused an unavailable price: %+v", got)
	}
	clk.Advance(time.Millisecond)
	now = clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 138, TsMs: now, RecvTsMs: now, Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot().Alerts[0].State != pricealert.Monitoring {
		t.Fatal("fresh data did not start monitoring")
	}
	paused, err := manager.SetPaused(alert.ID, alert.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if provider.leases["NVDA"] {
		t.Fatal("paused alert retained its feed lease")
	}
	if _, err := manager.SetPaused(alert.ID, paused.Version, false); err != nil {
		t.Fatal(err)
	}
	if !provider.leases["NVDA"] {
		t.Fatal("resume did not restore engine-owned demand")
	}
}

func TestNotificationClaimsAreEngineWideAndNeverReplayedOnRestart(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var notifications []pricealert.Notification
	options := pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{},
		OnNotification: func(notification pricealert.Notification) { notifications = append(notifications, notification) }}
	manager, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []float64{139, 140} {
		if _, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: target, Sessions: pricealert.Sessions{RTH: true}}); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(time.Millisecond)
	now := clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140.25, TsMs: now, RecvTsMs: now, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || len(notifications[0].Triggers) != 2 {
		t.Fatalf("burst did not preserve both occurrences: %+v", notifications)
	}
	burst := notifications[0]
	if !manager.ClaimNotification(burst.ID, "sound", 1) || manager.ClaimNotification(burst.ID, "sound", 2) || manager.ClaimNotification(burst.ID, "sound", 1) {
		t.Fatal("sound was not claimed once across connections")
	}
	if !manager.ClaimNotification(burst.ID, "popup", 1) || manager.ClaimNotification(burst.ID, "popup", 2) || !manager.ClaimNotification(burst.ID, "popup", 1) {
		t.Fatal("popup ownership did not remain with one connection")
	}
	if _, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 140, Sessions: pricealert.Sessions{RTH: true}}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	now = clk.Now().UnixMilli()
	if err := manager.Ingest(pricealert.Print{Symbol: "NVDA", Price: 140.5, TsMs: now, RecvTsMs: now, Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 || notifications[1].ID != burst.ID || len(notifications[1].Triggers) != 3 {
		t.Fatalf("in-flight burst was replaced: %+v", notifications)
	}
	restarted, err := pricealert.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 || len(restarted.Snapshot().History) != 3 || restarted.ClaimNotification(burst.ID, "sound", 3) {
		t.Fatal("restart replayed or reclaimed a historical notification")
	}
	clk.Advance(6 * time.Second)
	if manager.ClaimNotification(burst.ID, "popup", 1) {
		t.Fatal("stale notification remained claimable")
	}
}

func TestRunPublishesChangesWithoutStreamingEveryTrade(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	states := make(chan pricealert.Snapshot, 32)
	manager, err := pricealert.New(pricealert.Options{Clock: clk, Repository: db, Demand: availableFeed{}, OnSnapshot: func(s pricealert.Snapshot) { states <- s }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	next := func() pricealert.Snapshot {
		t.Helper()
		select {
		case state := <-states:
			return state
		case <-time.After(5 * time.Second):
			t.Fatal("missing published state")
			return pricealert.Snapshot{}
		}
	}
	if len(next().Alerts) != 0 {
		t.Fatal("initial snapshot is not empty")
	}
	if _, err := manager.Upsert(pricealert.Draft{Symbol: "NVDA", Condition: pricealert.AtOrAbove, Price: 139, Sessions: pricealert.Sessions{RTH: true}}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(250 * time.Millisecond)
	if next().Alerts[0].State != pricealert.WaitingData {
		t.Fatal("creation state was not published")
	}
	clk.Advance(time.Millisecond)
	now := clk.Now().UnixMilli()
	manager.Feed(pricealert.Print{Symbol: "NVDA", Price: 138, TsMs: now, RecvTsMs: now, Seq: 1})
	if next().Alerts[0].State != pricealert.Monitoring {
		t.Fatal("monitoring readiness was not published")
	}
	for seq := int64(2); seq < 22; seq++ {
		manager.Feed(pricealert.Print{Symbol: "NVDA", Price: 138.5, TsMs: now, RecvTsMs: now, Seq: seq})
	}
	manager.Feed(pricealert.Print{Symbol: "NVDA", Price: 139, TsMs: now, RecvTsMs: now, Seq: 22})
	if next().Alerts[0].State != pricealert.Triggered {
		t.Fatal("ordinary trades leaked into the low-rate state stream")
	}
	select {
	case unexpected := <-states:
		t.Fatalf("unexpected trade-rate snapshot: %+v", unexpected)
	default:
	}
}
