package uihub_test

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

type apiExec struct{}

func (apiExec) Do(exec.Command) exec.CmdAck { return exec.CmdAck{Accepted: true} }

type apiStores struct{}

func (apiStores) GetConfig(string) (string, bool, error)                  { return "", false, nil }
func (apiStores) SetConfig(string, string)                                {}
func (apiStores) QueryFills(string, int64, int64) ([]exec.FillRow, error) { return nil, nil }

type apiInd struct{}

func (apiInd) EnsureIndicator(string, md.IndicatorSpec) {}
func (apiInd) ReleaseIndicator(string)                  {}

func TestUIHubNewBuildsRunnableHubAndServer(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h, srv := uihub.New(clk, uihub.Config{
		Venues: []uihub.VenueMeta{{ID: "sim", Broker: "alpaca", Gate: uihub.GateLimits{MaxOrderValue: 1000}}},
		Global: uihub.GlobalLimits{MaxDayLoss: 500},
		MD:     20 * time.Millisecond, Account: 250 * time.Millisecond, Position: 100 * time.Millisecond,
		Buf: 128, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 64,
	}, apiExec{}, apiStores{}, apiInd{})
	if h == nil || srv == nil {
		t.Fatal("New must return a hub and server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	// smoke: publish an exec update; no panic, mirror knows the venue for exec.status
	h.PublishExec(exec.StatusUpdate{Venue: "sim", Connected: true})
	h.Publish("sys.events", "", nil) // generic publish path works
}
