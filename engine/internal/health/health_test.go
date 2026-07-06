package health

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestBuildHealthStatuses(t *testing.T) {
	ok := 20 * time.Millisecond
	slow := 800 * time.Millisecond
	snap := buildHealth(&ok, &slow, true)
	byLink := map[string]wsmsg.HealthLink{}
	for _, l := range snap.Links {
		byLink[string(l.Link)] = l
	}
	if byLink["ui-engine"].Status != "ok" {
		t.Fatalf("20ms ui-engine should be ok: %+v", byLink["ui-engine"])
	}
	if byLink["engine-moomoo"].Status != "degraded" {
		t.Fatalf("800ms moomoo should be degraded: %+v", byLink["engine-moomoo"])
	}
	if _, hasTZ := byLink["engine-tz"]; !hasTZ {
		t.Fatal("engine-tz link must be present when hasTZ=true")
	}
}

func TestBuildHealthDownWhenNil(t *testing.T) {
	snap := buildHealth(nil, nil, false)
	byLink := map[string]wsmsg.HealthLink{}
	for _, l := range snap.Links {
		byLink[string(l.Link)] = l
	}
	if byLink["engine-moomoo"].Status != "down" || byLink["engine-moomoo"].Ms != nil {
		t.Fatalf("nil RTT => down with null ms: %+v", byLink["engine-moomoo"])
	}
	if _, hasTZ := byLink["engine-tz"]; hasTZ {
		t.Fatal("engine-tz must be absent when hasTZ=false")
	}
}

func TestBuildHealthDownThreshold(t *testing.T) {
	// Verify that >= 2000ms is "down", not "degraded" (order matters)
	down := 3000 * time.Millisecond
	snap := buildHealth(&down, nil, false)
	byLink := map[string]wsmsg.HealthLink{}
	for _, l := range snap.Links {
		byLink[string(l.Link)] = l
	}
	if byLink["ui-engine"].Status != "down" {
		t.Fatalf("3000ms should be down (not degraded): %+v", byLink["ui-engine"])
	}
}
