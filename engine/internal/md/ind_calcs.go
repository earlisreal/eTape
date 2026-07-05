package md

import (
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// slotPoint is one slot's output for one bar. ok=false during warmup.
type slotPoint struct {
	slot  string
	value float64
	ok    bool
}

// calc is the streaming indicator contract. fold advances permanent state
// with a FINALIZED bar (O(1)); points computes output for any bar (forming
// or about-to-fold) from the last-folded state WITHOUT mutating anything —
// the forming bar's point is always recomputed from finalized state, so a
// live EMA never compounds partials. (go-engine-design §Indicators)
type calc interface {
	slots() []string
	fold(b Bar)
	points(b Bar) []slotPoint
}

func paramOr(p map[string]float64, key string, def float64) float64 {
	if v, ok := p[key]; ok {
		return v
	}
	return def
}

func intParam(p map[string]float64, key string, def float64) (int, error) {
	v := paramOr(p, key, def)
	n := int(v)
	if float64(n) != v || n < 1 || n > 400 {
		return 0, fmt.Errorf("md: indicator param %s=%v out of range [1,400]", key, v)
	}
	return n, nil
}

// newCalc builds a calculator; parameter names/defaults mirror the UI catalog
// (EMA period 9, SMA period 20, MACD 12/26/9).
func newCalc(spec IndicatorSpec) (calc, error) {
	switch spec.Type {
	case IndVWAP:
		return &vwapCalc{}, nil
	case IndSMA:
		n, err := intParam(spec.Params, "period", 20)
		if err != nil {
			return nil, err
		}
		return newSMACalc(n), nil
	case IndEMA:
		n, err := intParam(spec.Params, "period", 9)
		if err != nil {
			return nil, err
		}
		return &emaCalc{state: newEMAState(n)}, nil
	case IndMACD:
		fast, err := intParam(spec.Params, "fast", 12)
		if err != nil {
			return nil, err
		}
		slow, err := intParam(spec.Params, "slow", 26)
		if err != nil {
			return nil, err
		}
		sig, err := intParam(spec.Params, "signal", 9)
		if err != nil {
			return nil, err
		}
		return &macdCalc{fast: newEMAState(fast), slow: newEMAState(slow), sig: newEMAState(sig)}, nil
	case IndVolume:
		return statelessCalc(func(b Bar) float64 { return float64(b.V) }), nil
	case IndDelta:
		return statelessCalc(func(b Bar) float64 { return float64(b.BuyV - b.SellV) }), nil
	}
	return nil, fmt.Errorf("md: unknown indicator type %q", spec.Type)
}

// ---- stateless (VOLUME, DELTA) ----

type statelessCalc func(Bar) float64

func (statelessCalc) slots() []string { return []string{"hist"} }
func (statelessCalc) fold(Bar)        {}
func (f statelessCalc) points(b Bar) []slotPoint {
	return []slotPoint{{slot: "hist", value: f(b), ok: true}}
}

// ---- VWAP (session-anchored: resets each ET trading day; pre-market included) ----

type vwapCalc struct {
	day   int64
	cumPV float64
	cumV  float64
}

func (*vwapCalc) slots() []string { return []string{"line"} }

func typical(b Bar) float64 { return (b.H + b.L + b.C) / 3 }

func (v *vwapCalc) fold(b Bar) {
	if d := session.BucketStartMs(b.BucketMs, session.TFDay); d != v.day {
		v.day, v.cumPV, v.cumV = d, 0, 0
	}
	v.cumPV += typical(b) * float64(b.V)
	v.cumV += float64(b.V)
}

func (v *vwapCalc) points(b Bar) []slotPoint {
	pv, vol := v.cumPV, v.cumV
	if session.BucketStartMs(b.BucketMs, session.TFDay) != v.day {
		pv, vol = 0, 0 // bar opens a new day: preview a fresh session
	}
	pv += typical(b) * float64(b.V)
	vol += float64(b.V)
	if vol == 0 {
		return []slotPoint{{slot: "line"}}
	}
	return []slotPoint{{slot: "line", value: pv / vol, ok: true}}
}

// ---- SMA ----

type smaCalc struct {
	period int
	win    []float64 // last period-1 finalized closes
	sum    float64
}

func newSMACalc(period int) *smaCalc { return &smaCalc{period: period} }

func (*smaCalc) slots() []string { return []string{"line"} }

func (s *smaCalc) fold(b Bar) {
	s.win = append(s.win, b.C)
	s.sum += b.C
	if len(s.win) >= s.period { // keep exactly period-1 for the preview window
		s.sum -= s.win[0]
		s.win = s.win[1:]
	}
}

func (s *smaCalc) points(b Bar) []slotPoint {
	if len(s.win) < s.period-1 {
		return []slotPoint{{slot: "line"}}
	}
	return []slotPoint{{slot: "line", value: (s.sum + b.C) / float64(s.period), ok: true}}
}

// ---- EMA (seeded with the SMA of the first `period` closes) ----

type emaState struct {
	period int
	alpha  float64
	count  int
	seed   float64
	val    float64
}

func newEMAState(period int) *emaState {
	return &emaState{period: period, alpha: 2 / float64(period+1)}
}

func (e *emaState) fold(v float64) {
	e.count++
	switch {
	case e.count < e.period:
		e.seed += v
	case e.count == e.period:
		e.val = (e.seed + v) / float64(e.period)
	default:
		e.val = e.alpha*v + (1-e.alpha)*e.val
	}
}

// preview computes the EMA as if v were folded, without folding it.
func (e *emaState) preview(v float64) (float64, bool) {
	switch {
	case e.count+1 < e.period:
		return 0, false
	case e.count+1 == e.period:
		return (e.seed + v) / float64(e.period), true
	default:
		return e.alpha*v + (1-e.alpha)*e.val, true
	}
}

type emaCalc struct{ state *emaState }

func (*emaCalc) slots() []string { return []string{"line"} }
func (e *emaCalc) fold(b Bar)    { e.state.fold(b.C) }
func (e *emaCalc) points(b Bar) []slotPoint {
	v, ok := e.state.preview(b.C)
	return []slotPoint{{slot: "line", value: v, ok: ok}}
}

// ---- MACD (fast/slow EMAs of close; signal EMA of the macd line) ----

type macdCalc struct {
	fast, slow, sig *emaState
}

func (*macdCalc) slots() []string { return []string{"macd", "signal", "hist"} }

func (m *macdCalc) fold(b Bar) {
	m.fast.fold(b.C)
	m.slow.fold(b.C)
	if m.fast.count >= m.fast.period && m.slow.count >= m.slow.period {
		m.sig.fold(m.fast.val - m.slow.val)
	}
}

func (m *macdCalc) points(b Bar) []slotPoint {
	fv, fok := m.fast.preview(b.C)
	sv, sok := m.slow.preview(b.C)
	out := []slotPoint{{slot: "macd"}, {slot: "signal"}, {slot: "hist"}}
	if !fok || !sok {
		return out
	}
	macd := fv - sv
	out[0] = slotPoint{slot: "macd", value: macd, ok: true}
	if sigv, sigok := m.sig.preview(macd); sigok {
		out[1] = slotPoint{slot: "signal", value: sigv, ok: true}
		out[2] = slotPoint{slot: "hist", value: macd - sigv, ok: true}
	}
	return out
}
