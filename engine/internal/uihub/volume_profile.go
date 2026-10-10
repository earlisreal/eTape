package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func (q *queries) volumeProfile(ctx context.Context, raw json.RawMessage) wsmsg.QueryVolumeProfileResult {
	var a wsmsg.QueryVolumeProfileArgs
	out := wsmsg.QueryVolumeProfileResult{Status: "invalid", Source: "captured", Rows: []wsmsg.VolumeProfileRow{}, Reasons: []string{}}
	err := json.Unmarshal(raw, &a)
	out.Selection = a
	if err != nil || !strings.HasPrefix(a.Symbol, "US.") || len(a.Symbol) > 32 || a.FromMs <= 0 || a.ToMs <= a.FromMs || a.ToMs > 253402300799999 || a.Rows < 1 || a.Rows > 200 || a.ValueArea < 1 || a.ValueArea > 100 || !slices.Contains([]string{"10s", "1m", "5m", "15m", "30m", "60m"}, a.Timeframe) {
		out.Reasons = []string{"invalid_selection"}
		return out
	}
	if q.profileReader == nil {
		out.Status = "unavailable"
		out.Reasons = []string{"archive_unavailable"}
		return out
	}
	select {
	case q.profileSlots <- struct{}{}:
		defer func() { <-q.profileSlots }()
	default:
		out.Status = "busy"
		out.Reasons = []string{"reader_busy"}
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	to := min(a.ToMs, q.clk.Now().UnixMilli())
	if to <= a.FromMs {
		out.Status = "empty"
		return out
	}
	read, err := q.profileReader.Read(ctx, a.Symbol, a.FromMs, to)
	if err == nil {
		var profile md.VolumeProfile
		profile, err = md.CalculateVolumeProfile(ctx, read.Prints, a.FromMs, to, a.Rows, a.ValueArea)
		if err == nil {
			out.Status = "ready"
			if profile.Total == 0 {
				out.Status = "empty"
			}
			out.CapturedVolume, out.POC, out.VAH, out.VAL = profile.Total, profile.POC, profile.VAH, profile.VAL
			out.AsOfMs, out.FirstPrintMs, out.LastPrintMs = read.AsOfMs, read.FirstPrintMs, read.LastPrintMs
			out.Reasons = append(read.Reasons, profile.Reasons...)
			slices.Sort(out.Reasons)
			out.Reasons = slices.Compact(out.Reasons)
			out.Partial = len(out.Reasons) > 0
			for _, row := range profile.Rows {
				out.Rows = append(out.Rows, wsmsg.VolumeProfileRow{Lower: row.Lower, Upper: row.Upper, Volume: row.Volume})
			}
			return out
		}
	}
	switch {
	case errors.Is(err, md.ErrVolumeProfileTooLarge), errors.Is(err, context.DeadlineExceeded):
		out.Status = "too_large"
		out.Reasons = []string{"zoom_in"}
	case errors.Is(err, tickstore.ErrProfileBusy):
		out.Status = "busy"
		out.Reasons = []string{"reader_busy"}
	default:
		out.Status = "error"
		out.Reasons = []string{"archive_read_failed"}
	}
	return out
}
