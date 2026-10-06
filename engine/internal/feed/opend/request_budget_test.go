package opend

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestRequestBudgetPacesSharedFamilies(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	pacer := newRequestPacer(clock.NewFake(start))
	first, pace := marketDataRequestPacing(ProtoQotStockFilter)
	second, secondPace := marketDataRequestPacing(ProtoQotGetStockScreen)
	if first != second || pace != 5*time.Second || secondPace != pace {
		t.Fatalf("StockFilter/V2 pacing = (%q, %v)/(%q, %v), want one 5s family", first, pace, second, secondPace)
	}
	one := pacer.reserve(first, pace)
	two := pacer.reserve(second, secondPace)
	if got := two.Sub(one); got < pace {
		t.Fatalf("second request spacing = %v, want at least %v", got, pace)
	}
	if got := one.Sub(start); got < openDStartupQuiet {
		t.Fatalf("first request waited %v, want startup quiet period %v", got, openDStartupQuiet)
	}
	if family, spacing := marketDataRequestPacing(ProtoQotSub); family != "opend-subscription" || spacing != time.Second {
		t.Fatalf("subscription pacing = (%q, %v), want a separate one-second gate", family, spacing)
	}
}

func TestRequestBudgetRanksHaveSeparateRollingBudgets(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	pacer := newRequestPacer(clock.NewFake(start))
	one, pace := marketDataRequestPacing(ProtoQotGetUSAfterHoursRank)
	two, _ := marketDataRequestPacing(ProtoQotGetUSOvernightRank)
	if one == two || pace != time.Second {
		t.Fatalf("native rank budget = (%q, %q, %v), want separate one-second gates", one, two, pace)
	}
	first := pacer.reserve(one, pace)
	second := pacer.reserve(two, pace)
	if first != second {
		t.Fatalf("independent rank families did not share an available slot: %v != %v", first, second)
	}
}

func TestRequestBudgetDoesNotRestartQuietPeriodAfterIdle(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	pacer := newRequestPacer(clock.NewFake(start))
	first, pace := marketDataRequestPacing(ProtoQotGetSecuritySnapshot)
	initial := pacer.reserve(first, pace)
	if !initial.Equal(start.Add(openDStartupQuiet)) {
		t.Fatalf("initial slot = %v, want exactly %v", initial, start.Add(openDStartupQuiet))
	}
	// Idle beyond the next reserved slot; startup quiet applies once per
	// client process, not once after every quiet stretch.
	pacer.clk.(*clock.Fake).Advance(2 * time.Minute)
	next := pacer.reserve(first, pace)
	if want := start.Add(2 * time.Minute); !next.Equal(want) {
		t.Fatalf("post-idle slot = %v, want immediate slot %v", next, want)
	}
}

func TestRequestBudgetKeepsQuotaCountersAvailableDuringStartupQuiet(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	pacer := newRequestPacer(clock.NewFake(start))
	family, spacing := marketDataRequestPacing(ProtoQotGetSubInfo)
	if family != "opend-subscription-quota" {
		t.Fatalf("quota counter family = %q, want opend-subscription-quota", family)
	}
	if got := pacer.reserve(family, spacing); !got.Equal(start) {
		t.Fatalf("quota counter waited until %v, want immediate read at %v", got, start)
	}
	for _, id := range []uint32{ProtoQotGetStaticInfo, ProtoQotRequestHistoryKLQuota} {
		other, pace := marketDataRequestPacing(id)
		if got := pacer.reserve(other, pace); !got.Equal(start) {
			t.Fatalf("protocol %d waited behind unrelated quota counter until %v", id, got)
		}
	}
	if got := pacer.reserve(family, spacing); !got.Equal(start.Add(spacing)) {
		t.Fatalf("same quota endpoint lost its spacing: %v", got)
	}
}

func TestSubscriptionStartsDuringRollingWindowCooldown(t *testing.T) {
	pacer := newRequestPacer(clock.NewFake(time.Now()))
	family, spacing := marketDataRequestPacing(ProtoQotSub)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := pacer.wait(ctx, family, spacing); err != nil {
		t.Fatalf("initial subscription waited for unrelated rolling budgets: %v", err)
	}
}

func TestRequestBudgetWaitCanBeCanceled(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	pacer := newRequestPacer(clock.NewFake(start))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pacer.wait(ctx, "snapshot", time.Millisecond); err != context.Canceled {
		t.Fatalf("wait error = %v, want context canceled", err)
	}
}

func TestForegroundRequestOvertakesQueuedScannerWork(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	pacer := newRequestPacer(clk)
	pacer.readyAt = start.Add(10 * time.Second)
	backgroundDone := make(chan error, 1)
	go func() {
		backgroundDone <- pacer.waitPriority(WithBackgroundRequest(context.Background()), "snapshot", time.Second, true)
	}()
	waitForPacerQueue(t, pacer, 1)
	foregroundDone := make(chan error, 1)
	go func() { foregroundDone <- pacer.waitPriority(context.Background(), "snapshot", time.Second, false) }()
	waitForPacerQueue(t, pacer, 2)
	clk.Advance(10 * time.Second)
	select {
	case err := <-foregroundDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground request did not take the available slot")
	}
	select {
	case err := <-backgroundDone:
		t.Fatalf("Scanner request passed the foreground request: %v", err)
	default:
	}
	clk.Advance(time.Second)
	select {
	case err := <-backgroundDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("background request did not resume after the reserved spacing")
	}
}

func waitForPacerQueue(t *testing.T, p *requestPacer, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := len(p.queues["snapshot"])
		p.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pacer queue did not reach %d waiters", want)
}
