package config

import (
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// DataSettings is the bootstrap subset editable from Settings. The matching
// wire type lives in uihub/wsmsg; these values never include credentials.
type DataSettings struct {
	OpenDHost              string `json:"openDHost"`
	OpenDPort              int    `json:"openDPort"`
	QuotaSlots             int    `json:"quotaSlots"`
	ScannerPremarketMs     int    `json:"scannerPremarketMs"`
	ScannerRTHMs           int    `json:"scannerRTHMs"`
	YahooNews              bool   `json:"yahooNews"`
	TenSecondDays          int    `json:"tenSecondDays"`
	IntradayDays           int    `json:"intradayDays"`
	DailyYears             int    `json:"dailyYears"`
	BarRetentionDays       int    `json:"barRetentionDays"`
	RecordingEnabled       bool   `json:"recordingEnabled"`
	RecordingRetentionDays int    `json:"recordingRetentionDays"`
	RecordingMaxBytes      int64  `json:"recordingMaxBytes"`
	RecordingMinFreeBytes  int64  `json:"recordingMinFreeBytes"`
}

func (c Config) DataSettings() DataSettings {
	return DataSettings{
		OpenDHost: c.OpenD.Host, OpenDPort: c.OpenD.Port, QuotaSlots: c.Feed.QuotaSlots,
		ScannerPremarketMs: c.Scan.PremarketMs, ScannerRTHMs: c.Scan.RTHMs,
		YahooNews: c.News.YahooEnabled, TenSecondDays: c.Backfill.TenSecondDays,
		IntradayDays: c.Backfill.IntradayDays, DailyYears: c.Backfill.DailyYears,
		BarRetentionDays: c.Store.RetentionDays, RecordingEnabled: c.TickRecording.Enabled,
		RecordingRetentionDays: c.TickRecording.RetentionDays, RecordingMaxBytes: c.TickRecording.MaxBytes,
		RecordingMinFreeBytes: c.TickRecording.MinFreeBytes,
	}
}

func (s DataSettings) Validate() error {
	if s.OpenDHost == "" || strings.TrimSpace(s.OpenDHost) != s.OpenDHost ||
		strings.ContainsAny(s.OpenDHost, " /[]\t\r\n") ||
		(strings.Contains(s.OpenDHost, ":") && net.ParseIP(s.OpenDHost) == nil) {
		return fmt.Errorf("OpenD host must be a hostname or IP address")
	}
	if s.OpenDPort < 1 || s.OpenDPort > 65535 {
		return fmt.Errorf("OpenD port must be between 1 and 65535")
	}
	if s.QuotaSlots < 1 || s.QuotaSlots > 1_000_000 {
		return fmt.Errorf("subscription budget must be between 1 and 1000000")
	}
	maxMs := int64(math.MaxInt64 / int64(time.Millisecond))
	if s.ScannerPremarketMs < 1 || int64(s.ScannerPremarketMs) > maxMs || s.ScannerRTHMs < 1 || int64(s.ScannerRTHMs) > maxMs {
		return fmt.Errorf("scanner intervals must be positive and within the supported duration")
	}
	if s.TenSecondDays < 0 || s.TenSecondDays > 365000 || s.IntradayDays < 0 || s.IntradayDays > 365000 ||
		s.DailyYears < 0 || s.DailyYears > 1000 || s.BarRetentionDays < 0 || s.BarRetentionDays > 365000 ||
		s.RecordingRetentionDays < 1 || s.RecordingRetentionDays > 365000 {
		return fmt.Errorf("history and retention periods are out of range")
	}
	const maxExactJSInteger = 1<<53 - 1
	if s.RecordingMaxBytes < 2<<20 || s.RecordingMaxBytes > maxExactJSInteger ||
		s.RecordingMinFreeBytes < 1 || s.RecordingMinFreeBytes > maxExactJSInteger {
		return fmt.Errorf("recording cap must be at least 2 MiB and free-space reserve must be positive")
	}
	return nil
}

// WriteDataSettings checks the original snapshot so another window's edits are
// never silently overwritten. Other config sections are loaded fresh.
func WriteDataSettings(path string, expected, next DataSettings, migrated *Scan) error {
	c, err := Load(path)
	if err != nil {
		return err
	}
	if c.DataSettings() != expected {
		return fmt.Errorf("settings changed elsewhere; reload before saving")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	c.OpenD.Host, c.OpenD.Port = next.OpenDHost, next.OpenDPort
	c.Feed.QuotaSlots = next.QuotaSlots
	c.Scan.PremarketMs, c.Scan.RTHMs = next.ScannerPremarketMs, next.ScannerRTHMs
	c.News.YahooEnabled = next.YahooNews
	c.Backfill.TenSecondDays, c.Backfill.IntradayDays, c.Backfill.DailyYears = next.TenSecondDays, next.IntradayDays, next.DailyYears
	c.Store.RetentionDays = next.BarRetentionDays
	c.TickRecording = TickRecording{Enabled: next.RecordingEnabled, RetentionDays: next.RecordingRetentionDays,
		MaxBytes: next.RecordingMaxBytes, MinFreeBytes: next.RecordingMinFreeBytes}
	return writeConfig(path, c, migrated)
}
