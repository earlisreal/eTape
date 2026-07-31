package uihub

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type ExecCore interface {
	Do(exec.Command) exec.CmdAck
}

type Stores interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
	ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)
}

type Indicators interface {
	EnsureIndicator(connID uint64, id string, spec md.IndicatorSpec)
	ReleaseIndicator(connID uint64, id string)
}

type Feed interface {
	Validate(ctx context.Context, symbol string) error
	Ensure(d feed.Demand)
	Release(id string)
}

type GateLimits struct {
	MaxOrderValue     float64
	MaxPositionValue  float64
	MaxPositionShares float64
	MaxOpenOrders     int
}

type GlobalLimits struct {
	MaxDayLoss              float64
	MaxSymbolPositionValue  float64
	MaxSymbolPositionShares float64
}

type VenueMeta struct {
	ID     string
	Broker string
	Note   string
	Gate   GateLimits
}

type Config struct {
	Venues                         []VenueMeta
	Global                         GlobalLimits
	MD, Account, Position          time.Duration
	Buf                            int
	TapeCap, NewsCap               int
	FillsCap, EventsCap, TradesCap int
	OutBuf                         int
	DistDir                        string
	Demo                           bool
}

func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators, va venueAdmin, vt venueTester, requestRestart func(), startDemo func() error) (*Hub, *Server) {
	vms := make([]venueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		vms = append(vms, venueMeta{
			ID:     v.ID,
			Broker: wsmsg.Broker(v.Broker),
			Note:   v.Note,
			Gate: wsmsg.GateLimitsView{
				MaxOrderValue: v.Gate.MaxOrderValue, MaxPositionValue: v.Gate.MaxPositionValue,
				MaxPositionShares: v.Gate.MaxPositionShares, MaxOpenOrders: v.Gate.MaxOpenOrders,
			},
		})
	}
	global := wsmsg.GlobalLimitsView{
		MaxDayLoss: cfg.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Global.MaxSymbolPositionValue,
		MaxSymbolPositionShares: cfg.Global.MaxSymbolPositionShares,
	}
	m := newMirror(vms, global, cfg.TapeCap, cfg.NewsCap, cfg.FillsCap, cfg.EventsCap, cfg.TradesCap)
	m.session = wsmsg.SessionSnapshot{Mode: "live"}
	if cfg.Demo {
		m.session = wsmsg.SessionSnapshot{Mode: "demo"}
	}
	m.boot = wsmsg.BootStatus{Phase: "connecting"}
	h := NewHub(clk, HubConfig{MDInterval: cfg.MD, AccountInterval: cfg.Account, PositionInterval: cfg.Position, Buf: cfg.Buf}, m)
	h.SetIndicators(ind)
	cmd := newCommands(ex, st, h, h, va, h.feed, vt)
	h.cmd = cmd
	cmd.restart = requestRestart
	cmd.startDemo = startDemo
	qry := newQueries(st, clk)
	srv := NewServer(h, cmd, qry, ServerConfig{DistDir: cfg.DistDir, OutBuf: cfg.OutBuf})
	return h, srv
}
