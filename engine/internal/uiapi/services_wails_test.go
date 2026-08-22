//go:build wails

package uiapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
)

func TestEngineServiceUsesSharedAdmissionGate(t *testing.T) {
	runtime := wailsruntime.New()
	service := NewEngineService(runtime)
	ConfigureEngineService(service, QuerySources{
		Fills: &querySourceFake{rows: []exec.FillRow{{OrderID: "ET1", Side: "BUY", Qty: 1, Venue: "sim"}}},
		Clock: clock.NewFake(time.UnixMilli(1)),
	})

	fills, err := service.QueryFills(context.Background(), QueryFillsArgs{Symbol: "US.AAPL"})
	if err != nil || len(fills) != 1 || fills[0].Side != SideBuy {
		t.Fatalf("fills = %#v, err=%v", fills, err)
	}
	if got := runtime.Gate().InFlight(); got != 0 {
		t.Fatalf("gate in-flight after completed binding = %d", got)
	}

	runtime.BeginStop()
	if _, err := service.QueryFills(context.Background(), QueryFillsArgs{}); !errors.Is(err, wailsruntime.ErrStopping) {
		t.Fatalf("post-stop query error = %v, want %v", err, wailsruntime.ErrStopping)
	}
}

func TestEngineServiceMutationUsesSharedAdmissionGate(t *testing.T) {
	runtime := wailsruntime.New()
	service := NewEngineService(runtime)
	ConfigureEngineMutations(service, MutationSources{Watchlist: &mutationWatchlist{}})

	result, err := service.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "US.AAPL"})
	if err != nil || result.Status != MutationAccepted || result.Revision != 1 {
		t.Fatalf("watchlist result = %#v, err=%v", result, err)
	}

	runtime.BeginStop()
	if _, err := service.GetScannerFilters(context.Background()); !errors.Is(err, wailsruntime.ErrStopping) {
		t.Fatalf("post-stop mutation error = %v, want %v", err, wailsruntime.ErrStopping)
	}
}
