package md

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// VolumeProfileMaxPrints reserves room for input, identity maps and SQLite's
// bounded page cache within the reader's 64 MiB working-data limit.
const VolumeProfileMaxPrints = 96000

var ErrVolumeProfileTooLarge = errors.New("volume profile too large; zoom in")

type VolumeProfileRow struct {
	Lower, Upper float64
	Volume       int64
}

type VolumeProfile struct {
	Rows          []VolumeProfileRow
	Total         int64
	POC, VAH, VAL *float64
	Reasons       []string
}

// VolumeEligibleCondition reuses the live condition policy independently of
// last-sale eligibility, candle clamps and live sequence high-water state.
func VolumeEligibleCondition(c feed.TradeReportCondition) bool {
	return conditionPolicyFor(c).volumeEligible
}

// CalculateVolumeProfile consumes reports for all relevant exchange dates,
// including reports outside the viewport that can conflict with its identities.
func CalculateVolumeProfile(ctx context.Context, ticks []feed.Tick, from, to int64, rowCount, valueArea int) (VolumeProfile, error) {
	out := VolumeProfile{Rows: []VolumeProfileRow{}, Reasons: []string{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if from <= 0 || to <= from || rowCount < 1 || rowCount > 200 || valueArea < 1 || valueArea > 100 {
		return out, errors.New("invalid volume profile selection")
	}
	if len(ticks) > VolumeProfileMaxPrints {
		return out, ErrVolumeProfileTooLarge
	}
	type identity struct {
		symbol        string
		day, sequence int64
	}
	type report struct {
		tick struct {
			TsMs      int64
			Price     float64
			Volume    int64
			Condition feed.TradeReportCondition
		}
		eligible, conflict bool
	}
	reports := make(map[identity]report, len(ticks))
	reason := func(s string) {
		if !slices.Contains(out.Reasons, s) {
			out.Reasons = append(out.Reasons, s)
		}
	}
	for i, tick := range ticks {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return out, err
			}
		}
		inRange := tick.TsMs >= from && tick.TsMs < to
		if tick.Seq <= 0 {
			if inRange {
				reason("missing_sequence")
			}
			continue
		}
		if tick.TsMs <= 0 || tick.TsMs > 253402300799999 {
			reason("invalid_report")
			continue
		}
		key := identity{tick.Symbol, session.DayMs(tick.TsMs), tick.Seq}
		eligible := VolumeEligibleCondition(tick.Condition)
		if prev, ok := reports[key]; ok {
			prev.conflict = prev.conflict || prev.tick.TsMs != tick.TsMs || prev.tick.Price != tick.Price || prev.tick.Volume != tick.Volume || prev.eligible != eligible
			reports[key] = prev
		} else {
			r := report{eligible: eligible}
			r.tick.TsMs, r.tick.Price, r.tick.Volume, r.tick.Condition = tick.TsMs, tick.Price, tick.Volume, tick.Condition
			reports[key] = r
		}
	}
	type sample struct {
		Price  float64
		Volume int64
	}
	selected := make([]sample, 0, len(reports))
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range reports {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if r.conflict {
			reason("conflicting_reports")
			continue
		}
		t := r.tick
		if t.TsMs < from || t.TsMs >= to {
			continue
		}
		if !r.eligible {
			if t.Condition == feed.TradeConditionUnknown {
				reason("unknown_condition")
			}
			continue
		}
		if t.Volume <= 0 || t.Price <= 0 || math.IsNaN(t.Price) || math.IsInf(t.Price, 0) {
			reason("invalid_report")
			continue
		}
		if t.Volume > 9007199254740991-out.Total {
			return out, ErrVolumeProfileTooLarge
		}
		out.Total += t.Volume
		lo, hi = min(lo, t.Price), max(hi, t.Price)
		selected = append(selected, sample{t.Price, t.Volume})
	}
	slices.Sort(out.Reasons)
	if out.Total == 0 {
		return out, nil
	}
	width := (hi - lo) / float64(rowCount)
	if hi == lo || width == 0 {
		rowCount, width = 1, 0
	}
	out.Rows = make([]VolumeProfileRow, rowCount)
	for i := range out.Rows {
		out.Rows[i].Lower = lo + float64(i)*width
		out.Rows[i].Upper = lo + float64(i+1)*width
	}
	out.Rows[rowCount-1].Upper = hi
	for _, tick := range selected {
		index := 0
		if width > 0 {
			index = min(rowCount-1, max(0, int(math.Floor((tick.Price-lo)/width))))
		}
		out.Rows[index].Volume += tick.Volume
	}
	mid := lo + (hi-lo)/2
	center := func(i int) float64 { return out.Rows[i].Lower + (out.Rows[i].Upper-out.Rows[i].Lower)/2 }
	poc := 0
	for i := 1; i < rowCount; i++ {
		if out.Rows[i].Volume > out.Rows[poc].Volume || (out.Rows[i].Volume == out.Rows[poc].Volume && math.Abs(center(i)-mid) < math.Abs(center(poc)-mid)) {
			poc = i
		}
	}
	left, right, volume := poc, poc, out.Rows[poc].Volume
	target := out.Total/100*int64(valueArea) + (out.Total%100*int64(valueArea)+99)/100
	for volume < target {
		chooseLeft := right == rowCount-1
		if left > 0 && right < rowCount-1 {
			a, b := out.Rows[left-1].Volume, out.Rows[right+1].Volume
			chooseLeft = a > b || (a == b && poc-(left-1) <= right+1-poc)
		}
		if left > 0 && chooseLeft {
			left--
			volume += out.Rows[left].Volume
		} else {
			right++
			volume += out.Rows[right].Volume
		}
	}
	pocPrice, vah, val := center(poc), out.Rows[right].Upper, out.Rows[left].Lower
	out.POC, out.VAH, out.VAL = &pocPrice, &vah, &val
	return out, nil
}
