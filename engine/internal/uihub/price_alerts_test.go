package uihub

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/pricealert"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestPriceAlertCommandsAndSnapshotDoNotReplayNotifications(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "alerts.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager, err := pricealert.New(pricealert.Options{Repository: db, Clock: clock.NewFake(time.Date(2026, 10, 19, 10, 0, 0, 0, session.Loc()))})
	if err != nil {
		t.Fatal(err)
	}
	hub, _ := New(clock.System{}, Config{}, &spyExec{}, db, &spyInd{}, nil, nil, nil, nil)
	hub.SetPriceAlerts(manager)
	ack, _ := hub.cmd.handle(context.Background(), "UpsertPriceAlert", mustJSON(t, wsmsg.UpsertPriceAlertArgs{Symbol: "NVDA", Condition: "at-or-above", Price: 139, Sessions: wsmsg.PriceAlertSessions{RTH: true}}), 1, nil)
	if ack.Status != wsmsg.AckAccepted {
		t.Fatalf("create=%+v", ack)
	}
	snapshot := MapPriceAlertState(manager.Snapshot())
	if len(snapshot.Alerts) != 1 || snapshot.Alerts[0].State != "waiting-data" {
		t.Fatalf("state=%+v", snapshot)
	}
	hub.m.applyPub(staged{Topic: wsmsg.TopicAlertsState, Payload: snapshot})
	if frames := hub.m.snapshotFrames(wsmsg.TopicAlertsState); len(frames) != 1 {
		t.Fatalf("snapshot=%v", frames)
	}
	if frames := hub.m.snapshotFrames(wsmsg.TopicAlertsTrigger); len(frames) != 0 {
		t.Fatalf("replayed=%v", frames)
	}
	ack, _ = hub.cmd.handle(context.Background(), "ClaimPriceAlertNotification", []byte(`{"id":"old","kind":"sound"}`), 2, nil)
	if ack.Status != wsmsg.AckAccepted || ack.Value != false {
		t.Fatalf("stale claim=%+v", ack)
	}
}
