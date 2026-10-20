package uihub

import (
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func trailConfigFromWire(c wsmsg.TrailConfig) exec.TrailConfig {
	return exec.TrailConfig{Mode: c.Mode, Value: c.Value, Sessions: exec.TrailSessions{Pre: c.Sessions.Pre, RTH: c.Sessions.RTH, Post: c.Sessions.Post}, Pricing: c.Pricing,
		Cushion: exec.LimitCushion{Value: c.Cushion.Value, Unit: c.Cushion.Unit}, QuoteSide: c.QuoteSide, Offset: exec.LimitCushion{Value: c.Offset.Value, Unit: c.Offset.Unit}, Timeframe: c.Timeframe, Buffer: exec.LimitCushion{Value: c.Buffer.Value, Unit: c.Buffer.Unit}}
}
func mapTrailConfig(c *exec.TrailConfig) *wsmsg.TrailConfig {
	if c == nil {
		return nil
	}
	return &wsmsg.TrailConfig{Mode: c.Mode, Value: c.Value, Sessions: wsmsg.TrailSessions{Pre: c.Sessions.Pre, RTH: c.Sessions.RTH, Post: c.Sessions.Post}, Pricing: c.Pricing,
		Cushion: wsmsg.LimitCushion{Value: c.Cushion.Value, Unit: c.Cushion.Unit}, QuoteSide: c.QuoteSide, Offset: wsmsg.LimitCushion{Value: c.Offset.Value, Unit: c.Offset.Unit}, Timeframe: c.Timeframe, Buffer: wsmsg.LimitCushion{Value: c.Buffer.Value, Unit: c.Buffer.Unit}}
}
func mapTrail(t *exec.TrailState) *wsmsg.TrailState {
	if t == nil {
		return nil
	}
	r := &wsmsg.TrailState{Config: *mapTrailConfig(&t.Config), Watermark: t.Watermark, Stop: t.Stop, Limit: t.Limit, Triggered: t.Triggered, Retired: t.Retired, ReservedQty: t.ReservedQty, Floor: t.Floor, PositionCycle: t.PositionCycle}
	if b := t.Reference; b != nil {
		r.Reference = &wsmsg.TrailCandle{Symbol: b.Symbol, Timeframe: b.Timeframe, StartMs: b.StartMs, EndMs: b.EndMs, Low: b.Low, High: b.High}
	}
	return r
}
