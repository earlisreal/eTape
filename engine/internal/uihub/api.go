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

// ExecCore is the exec.Core surface uihub commands need (satisfied by *exec.Core).
type ExecCore interface {
	Do(exec.Command) exec.CmdAck
}

// Stores is the store surface uihub needs (satisfied by *store.Store).
type Stores interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
}

// Indicators is the md.Core surface uihub needs (satisfied by *md.Core).
type Indicators interface {
	EnsureIndicator(id string, spec md.IndicatorSpec)
	ReleaseIndicator(id string)
}

// Feed is the market-data control surface uihub needs for on-demand symbol
// subscription (satisfied by *opend.OpenDFeed). It is injected after
// construction via Hub.SetFeed because the OpenD feed is created only after
// the hub is already listening; replay/tests leave it nil.
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
	ID      string
	Broker  string
	AutoArm bool   // boot this venue armed (paper); reflected in the initial exec.status
	Note    string // e.g. "execution v1.x" for the moomoo stub
	Gate    GateLimits
}

type Config struct {
	Venues                []VenueMeta
	Global                GlobalLimits
	MD, Account, Position time.Duration
	Buf                   int
	TapeCap, NewsCap      int
	FillsCap, EventsCap   int
	OutBuf                int
	DistDir               string
}

// New builds the mirror, hub, and server from the cores. Caller runs h.Run(ctx)
// and serves srv.Handler(); uses h.PublishMD/PublishExec/Publish for fan-in.
func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators, va venueAdmin) (*Hub, *Server) {
	vms := make([]venueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		vms = append(vms, venueMeta{
			ID:      v.ID,
			Broker:  wsmsg.Broker(v.Broker),
			AutoArm: v.AutoArm,
			Note:    v.Note,
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
	m := newMirror(vms, global, cfg.TapeCap, cfg.NewsCap, cfg.FillsCap, cfg.EventsCap)
	h := NewHub(clk, HubConfig{MDInterval: cfg.MD, AccountInterval: cfg.Account, PositionInterval: cfg.Position, Buf: cfg.Buf}, m)
	cmd := newCommands(ex, st, ind, h, va, h.feed)
	qry := newQueries(st)
	srv := NewServer(h, cmd, qry, ServerConfig{DistDir: cfg.DistDir, OutBuf: cfg.OutBuf})
	return h, srv
}
