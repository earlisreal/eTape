// Package config loads eTape's bootstrap TOML config (~/.eTape/config.toml).
// Only the sections the current plan needs are defined; the struct grows in
// later plans. A missing file yields defaults; a malformed file is an error.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

// OpenD locates the local OpenD gateway.
type OpenD struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

// Addr returns host:port for net.Dial.
func (o OpenD) Addr() string { return net.JoinHostPort(o.Host, strconv.Itoa(o.Port)) }

// Feed configures the market-data feed adapter.
type Feed struct {
	Watchlist           []string `toml:"watchlist"`
	ExtendedTime        bool     `toml:"extended_time"`
	UnsubHysteresisSecs int      `toml:"unsub_hysteresis_secs"`
	QuotaSlots          int      `toml:"quota_slots"`
}

// MD configures the market-data core.
type MD struct {
	TapeRing      int    `toml:"tape_ring"`
	SessionAnchor string `toml:"session_anchor"` // "HH:MM" ET
}

// AnchorSecs parses SessionAnchor into seconds-since-ET-midnight.
func (m MD) AnchorSecs() (int64, error) {
	t, err := time.Parse("15:04", m.SessionAnchor)
	if err != nil {
		return 0, fmt.Errorf("config: session_anchor %q must be HH:MM: %w", m.SessionAnchor, err)
	}
	return int64(t.Hour())*3600 + int64(t.Minute())*60, nil
}

// Store configures SQLite persistence (journal, bar archives, config, sys_events).
type Store struct {
	DBPath        string `toml:"db_path"`        // empty → resolved to ~/.eTape/etape.db by main
	RetentionDays int    `toml:"retention_days"` // journal pruned older than this many days
	FlushMs       int    `toml:"flush_ms"`       // writer batch-flush interval
}

// Venue is one configured execution venue.  ->  [[venue]]
type Venue struct {
	ID          string `toml:"id"`          // slug used in events, topics, commands, gate config
	Broker      string `toml:"broker"`      // tradezero | alpaca | moomoo | sim
	Env         string `toml:"env"`         // paper | live
	Credentials string `toml:"credentials"` // key into ~/.eJournal/credentials.json
	AccountID   string `toml:"account_id"`  // broker-specific (TZ accountId, moomoo accID)
}

// GateGlobal caps aggregate risk across all venues.  ->  [gate.global]
type GateGlobal struct {
	MaxDayLoss              float64 `toml:"max_day_loss"`
	MaxSymbolPositionValue  float64 `toml:"max_symbol_position_value"`
	MaxSymbolPositionShares float64 `toml:"max_symbol_position_shares"`
}

// GateVenue caps one venue's risk.  ->  [gate.venue.<id>]
type GateVenue struct {
	MaxOrderValue     float64 `toml:"max_order_value"`
	MaxPositionValue  float64 `toml:"max_position_value"`
	MaxPositionShares float64 `toml:"max_position_shares"`
	MaxOpenOrders     int     `toml:"max_open_orders"`
}

// Gate is the two-layer safety-gate config.  ->  [gate]
type Gate struct {
	Global GateGlobal           `toml:"global"`
	Venue  map[string]GateVenue `toml:"venue"`
}

// UIHub is the [uihub] section: the WS/HTTP server the UI connects to.
type UIHub struct {
	Host          string  `toml:"host"`
	Port          int     `toml:"port"`
	DistDir       string  `toml:"dist_dir"`        // path to built ui/dist; empty => no static file serving (dev proxies /ws)
	OutboundQueue int     `toml:"outbound_queue"`  // per-connection outbound buffer depth; overflow => drop + force re-snapshot
	MDRateHz      float64 `toml:"md_rate_hz"`      // flush rate for md.quote/book/bars/tape/indicator
	AccountRateHz float64 `toml:"account_rate_hz"` // flush rate for exec.account
	PositionMs    int     `toml:"position_ms"`     // batch interval for exec.positions
	TapeSnapshot  int     `toml:"tape_snapshot"`   // recent ticks retained per symbol for the tape snapshot
}

func (u UIHub) Addr() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// Scan is the [scan] section: pre-market/RTH rank scanner + low-float universe.
type Scan struct {
	Enabled          bool    `toml:"enabled"`
	PremarketMs      int     `toml:"premarket_ms"`       // rank poll interval before 09:30 ET
	RTHMs            int     `toml:"rth_ms"`             // rank poll interval during RTH
	RankPages        int     `toml:"rank_pages"`         // pages of <=35 to pull per rank refresh
	MinChangePct     float64 `toml:"min_change_pct"`     // client-side gainer threshold (%)
	MaxFloatShares   float64 `toml:"max_float_shares"`   // float cap in ACTUAL shares (not thousands)
	MinVolume        int64   `toml:"min_volume"`         // session cumulative volume floor
	UniverseRefreshH int     `toml:"universe_refresh_h"` // low-float universe refresh interval (hours)
}

// News is the [news] section: Qot_GetSearchNews polling.
type News struct {
	Enabled   bool `toml:"enabled"`
	FocusedMs int  `toml:"focused_ms"` // poll interval for focused symbols
	WatchMs   int  `toml:"watch_ms"`   // step interval for the watchlist rotation
	MaxPerReq int  `toml:"max_per_req"`
}

// Health is the [health] section: moomoo probe RTT + sys.health/sys.events emission.
type Health struct {
	Enabled bool `toml:"enabled"`
	ProbeMs int  `toml:"probe_ms"` // probe + sys.health emit interval
}

// Backfill is the [backfill] section: deep-history warm-start + gap-fill at boot.
type Backfill struct {
	Enabled      bool `toml:"enabled"`
	IntradayDays int  `toml:"intraday_days"` // trading days of 1m history to backfill
	DailyYears   int  `toml:"daily_years"`   // 0 = all available daily history
	Concurrency  int  `toml:"concurrency"`   // bounded boot worker pool
	SeedChunk    int  `toml:"seed_chunk"`    // max bars per Seed* call (drop-on-full guard)
}

// Config is the engine's bootstrap configuration.
type Config struct {
	OpenD    OpenD    `toml:"opend"`
	Feed     Feed     `toml:"feed"`
	MD       MD       `toml:"md"`
	Store    Store    `toml:"store"`
	Venues   []Venue  `toml:"venue"`
	Gate     Gate     `toml:"gate"`
	UIHub    UIHub    `toml:"uihub"`
	Scan     Scan     `toml:"scan"`
	News     News     `toml:"news"`
	Health   Health   `toml:"health"`
	Backfill Backfill `toml:"backfill"`
}

// Default returns the built-in defaults used when a field or the whole file is absent.
func Default() Config {
	return Config{
		OpenD: OpenD{Host: "127.0.0.1", Port: 11111},
		Feed:  Feed{ExtendedTime: true, UnsubHysteresisSecs: 300, QuotaSlots: 100},
		MD:    MD{TapeRing: 65536, SessionAnchor: "09:30"},
		Store: Store{DBPath: "", RetentionDays: 30, FlushMs: 250},
		UIHub: UIHub{
			Host: "127.0.0.1", Port: 8686, DistDir: "",
			OutboundQueue: 1024, MDRateHz: 30, AccountRateHz: 4, PositionMs: 100, TapeSnapshot: 200,
		},
		Scan: Scan{
			Enabled: true, PremarketMs: 2000, RTHMs: 3000, RankPages: 2,
			MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000, UniverseRefreshH: 24,
		},
		News:     News{Enabled: true, FocusedMs: 20000, WatchMs: 3000, MaxPerReq: 50},
		Health:   Health{Enabled: true, ProbeMs: 5000},
		Backfill: Backfill{Enabled: true, IntradayDays: 20, DailyYears: 0, Concurrency: 3, SeedChunk: 500},
	}
}

// Load reads the TOML file at path over the defaults. A non-existent file is
// not an error (defaults are returned); a malformed file is.
func Load(path string) (Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}
