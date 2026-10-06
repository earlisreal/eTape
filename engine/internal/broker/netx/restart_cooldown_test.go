package netx

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestRestartCooldownRetainsOnlyRemainingRequestWindow(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)
	path := filepath.Join(t.TempDir(), "cooldown.json")
	const window = 31 * time.Second
	guard := NewRestartCooldown(path, "opend", window, clk)
	if !guard.ReadyAt().Equal(now.Add(window)) {
		t.Fatal("unknown previous request history must retain the full cooldown")
	}
	clk.Advance(window)
	if err := guard.Record(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(20 * time.Second)
	restarted := NewRestartCooldown(path, "opend", window, clk)
	if got := restarted.ReadyAt().Sub(clk.Now()); got != 11*time.Second {
		t.Fatalf("restart cooldown = %v, want 11s", got)
	}
	clk.Advance(12 * time.Second)
	if NewRestartCooldown(path, "opend", window, clk).ReadyAt().After(clk.Now()) {
		t.Fatal("launch after an idle request window must proceed immediately")
	}
	deferred := clk.Now().Add(2 * time.Minute)
	if err := guard.DeferUntil(deferred); err != nil {
		t.Fatal(err)
	}
	if err := guard.Record(); err != nil {
		t.Fatal(err)
	}
	if !NewRestartCooldown(path, "opend", window, clk).ReadyAt().Equal(deferred) {
		t.Fatal("provider Retry-After must survive restart and newer request checkpoints")
	}
	if !NewRestartCooldown(path, "different-provider", window, clk).ReadyAt().Equal(clk.Now().Add(window)) {
		t.Fatal("different provider must not reuse another provider's request history")
	}
	clk.Advance(-time.Minute)
	if !NewRestartCooldown(path, "opend", window, clk).ReadyAt().Equal(clk.Now().Add(window)) {
		t.Fatal("clock rollback must use a bounded conservative cooldown")
	}
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NewRestartCooldown(path, "opend", window, clk).ReadyAt().Equal(clk.Now().Add(window)) {
		t.Fatal("corrupt request history must retain the full cooldown")
	}
}

func TestRestartCooldownSerializesConcurrentRecordsAndReportsWriteFailure(t *testing.T) {
	clk := clock.NewFake(time.Now())
	path := filepath.Join(t.TempDir(), "cooldown.json")
	guard := NewRestartCooldown(path, "opend", time.Minute, clk)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := guard.Record(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := NewRestartCooldown(path, "opend", time.Minute, clk).ReadyAt(); !got.Equal(clk.Now().Add(time.Minute)) {
		t.Fatalf("concurrent checkpoint = %v", got)
	}
	blocked := NewRestartCooldown(filepath.Join(t.TempDir(), "missing", "state.json"), "opend", time.Minute, clk)
	if err := blocked.Record(); err == nil {
		t.Fatal("durability failure must prevent an unrecorded provider request")
	}
}
