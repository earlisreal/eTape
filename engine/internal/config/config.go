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

// Config is the engine's bootstrap configuration.
type Config struct {
	OpenD OpenD `toml:"opend"`
	Feed  Feed  `toml:"feed"`
	MD    MD    `toml:"md"`
	Store Store `toml:"store"`
}

// Default returns the built-in defaults used when a field or the whole file is absent.
func Default() Config {
	return Config{
		OpenD: OpenD{Host: "127.0.0.1", Port: 11111},
		Feed:  Feed{ExtendedTime: true, UnsubHysteresisSecs: 300, QuotaSlots: 100},
		MD:    MD{TapeRing: 65536, SessionAnchor: "09:30"},
		Store: Store{DBPath: "", RetentionDays: 30, FlushMs: 250},
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
