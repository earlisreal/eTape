// Package config loads eTape's bootstrap TOML config (~/.eTape/config.toml).
// Only the sections the current plan needs are defined; the struct grows in
// later plans. A missing file yields defaults; a malformed file is an error.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/earlisreal/eTape/engine/internal/atomicfile"
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
	ExtendedTime        bool `toml:"extended_time"`
	UnsubHysteresisSecs int  `toml:"unsub_hysteresis_secs"`
	QuotaSlots          int  `toml:"quota_slots"`
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
	AutoArm     bool   `toml:"auto_arm"`    // boot this venue armed (paper); live venues keep the manual arm click
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
	OutboundQueue int     `toml:"outbound_queue"`  // per-connection outbound buffer depth; lossless lane overflow => drop + force re-snapshot; latest-wins topics (quotes, book, bars, account, positions, scanner rank, health) coalesce instead
	MDRateHz      float64 `toml:"md_rate_hz"`      // flush rate for md.quote/book/bars/tape/indicator
	AccountRateHz float64 `toml:"account_rate_hz"` // flush rate for exec.account
	PositionMs    int     `toml:"position_ms"`     // batch interval for exec.positions
	TapeSnapshot  int     `toml:"tape_snapshot"`   // recent ticks retained per symbol for the tape snapshot
}

func (u UIHub) Addr() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// Scan is the [scan] section: pre-market/RTH rank scanner + on-demand float cache.
type Scan struct {
	Enabled        bool    `toml:"enabled"`
	PremarketMs    int     `toml:"premarket_ms"`     // rank poll interval before 09:30 ET
	RTHMs          int     `toml:"rth_ms"`           // rank poll interval during RTH
	RankPages      int     `toml:"rank_pages"`       // pages of <=35 to pull per rank refresh
	MinChangePct   float64 `toml:"min_change_pct"`   // client-side gainer threshold (%)
	MaxFloatShares float64 `toml:"max_float_shares"` // float cap in ACTUAL shares (not thousands)
	MinVolume      int64   `toml:"min_volume"`       // session cumulative volume floor
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
	Enabled      bool           `toml:"enabled"`
	IntradayDays int            `toml:"intraday_days"` // trading days of 1m history to backfill
	DailyYears   int            `toml:"daily_years"`   // 0 = all available daily history
	Concurrency  int            `toml:"concurrency"`   // bounded boot worker pool
	SeedChunk    int            `toml:"seed_chunk"`    // vestigial: no longer read (see backfill.Config.SeedChunk); kept so an existing config.toml's seed_chunk key doesn't need editing
	Alpaca       BackfillAlpaca `toml:"alpaca"`
}

// BackfillAlpaca is the [backfill.alpaca] section: the optional 1m-depth
// fallback source. Uses the PAPER creds key (free data; live keys untouched).
type BackfillAlpaca struct {
	Enabled  bool   `toml:"enabled"`
	CredsKey string `toml:"creds_key"`
	Feed     string `toml:"feed"` // "iex" (free) | "sip"
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
			OutboundQueue: 4096, MDRateHz: 30, AccountRateHz: 4, PositionMs: 100, TapeSnapshot: 200,
		},
		Scan: Scan{
			Enabled: true, PremarketMs: 2000, RTHMs: 3000, RankPages: 2,
			MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000,
		},
		News:   News{Enabled: true, FocusedMs: 20000, WatchMs: 3000, MaxPerReq: 50},
		Health: Health{Enabled: true, ProbeMs: 5000},
		Backfill: Backfill{Enabled: true, IntradayDays: 20, DailyYears: 0, Concurrency: 3, SeedChunk: 500,
			Alpaca: BackfillAlpaca{Enabled: true, CredsKey: "alpaca", Feed: "iex"},
		},
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

// VenueConfig is the file-writable subset of Config the settings UI edits.
type VenueConfig struct {
	Venues []Venue
	Gate   Gate
}

var venueIDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

var validBrokers = map[string]bool{"tradezero": true, "alpaca": true, "moomoo": true, "sim": true}

// ReadVenueConfig parses the TOML file fresh and returns its venue+gate subset.
// A missing file yields the defaults (not an error), matching boot semantics.
func ReadVenueConfig(path string) (VenueConfig, error) {
	c, err := Load(path)
	if err != nil {
		return VenueConfig{}, err
	}
	return VenueConfig{Venues: c.Venues, Gate: c.Gate}, nil
}

// ValidateVenueConfig enforces the settings-UI write rules. credKeys is the set
// of credential names currently present in credentials.json. It returns a
// field-naming error on the first violation and writes nothing.
func ValidateVenueConfig(vc VenueConfig, credKeys []string) error {
	keys := map[string]bool{}
	for _, k := range credKeys {
		keys[k] = true
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, v := range vc.Venues {
		if v.ID == "" || !venueIDRe.MatchString(v.ID) {
			return fmt.Errorf("venue id %q: must be non-empty and match [a-z0-9-]", v.ID)
		}
		if seen[v.ID] {
			return fmt.Errorf("venue id %q: duplicate", v.ID)
		}
		seen[v.ID] = true
		ids[v.ID] = true
		if !validBrokers[v.Broker] {
			return fmt.Errorf("venue %q: broker %q must be one of tradezero, alpaca, moomoo, sim", v.ID, v.Broker)
		}
		if v.Env != "paper" && v.Env != "live" {
			return fmt.Errorf("venue %q: env %q must be paper or live", v.ID, v.Env)
		}
		if v.Env == "live" && v.AutoArm {
			return fmt.Errorf("venue %q: live venues cannot auto-arm", v.ID)
		}
		if v.Broker == "tradezero" || v.Broker == "alpaca" {
			if !keys[v.Credentials] {
				return fmt.Errorf("venue %q: credentials %q names no existing key", v.ID, v.Credentials)
			}
		}
		if v.Broker == "tradezero" && v.AccountID == "" {
			return fmt.Errorf("venue %q: tradezero requires account_id", v.ID)
		}
	}
	g := vc.Gate.Global
	if g.MaxDayLoss < 0 || g.MaxSymbolPositionValue < 0 || g.MaxSymbolPositionShares < 0 {
		return fmt.Errorf("gate.global: caps must be >= 0 (0 = off)")
	}
	for id, gv := range vc.Gate.Venue {
		if !ids[id] {
			return fmt.Errorf("gate.venue.%s: no venue with that id", id)
		}
		if gv.MaxOrderValue < 0 || gv.MaxPositionValue < 0 || gv.MaxPositionShares < 0 || gv.MaxOpenOrders < 0 {
			return fmt.Errorf("gate.venue.%s: caps must be >= 0 (0 = off)", id)
		}
	}
	return nil
}

// WriteVenueConfig re-reads path into a full Config, replaces its Venues and
// Gate, and re-encodes the whole file atomically. On the FIRST UI-driven write
// it copies the original file to path+".bak" (only if that .bak is absent), so
// the hand-written original — comments and all — is preserved forever.
// Decode→encode loses comments/ordering and any keys unknown to Config; that is
// the accepted trade-off (Config is the engine's entire config surface).
func WriteVenueConfig(path string, vc VenueConfig) error {
	if orig, err := os.ReadFile(path); err == nil {
		bak := path + ".bak"
		if _, statErr := os.Stat(bak); errors.Is(statErr, os.ErrNotExist) {
			if err := atomicfile.Write(bak, orig, 0o644); err != nil {
				return fmt.Errorf("config: write .bak: %w", err)
			}
		}
	}
	c, err := Load(path)
	if err != nil {
		return err
	}
	c.Venues = vc.Venues
	c.Gate = vc.Gate
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := atomicfile.Write(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return nil
}
