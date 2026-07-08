// indicator.go is the v1 indicator registry: refcounted (symbol, tf, type,
// params) instances keyed by the requester's instanceId, seeded from
// finalized bar history on create and streamed forward via barOut. The
// streaming contract itself (fold/points, non-mutating previews) lives in
// ind_calcs.go.
package md

import (
	"log/slog"
	"maps"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// IndicatorType names the v1 catalog. Values match the UI contract.
type IndicatorType string

const (
	IndVWAP   IndicatorType = "VWAP"
	IndEMA    IndicatorType = "EMA"
	IndSMA    IndicatorType = "SMA"
	IndMACD   IndicatorType = "MACD"
	IndVolume IndicatorType = "VOLUME"
)

// IndicatorSpec identifies what an instance computes.
type IndicatorSpec struct {
	Symbol string
	TF     session.Timeframe
	Type   IndicatorType
	Params map[string]float64
}

type symTF struct {
	symbol string
	tf     session.Timeframe
}

type instance struct {
	id         string
	spec       IndicatorSpec
	c          calc
	refs       int
	lastFolded int64
	series     map[string][]Point // stored FINAL points per slot (snapshot source)
}

// seriesKey follows the UI contract: single-slot instances stream under the
// instanceId itself; multi-slot ones under "instanceId#slot".
func (in *instance) seriesKey(slot string) string {
	if len(in.c.slots()) == 1 {
		return in.id
	}
	return in.id + "#" + slot
}

// indicatorSet is the per-core instance registry. All methods run on the
// Core goroutine.
type indicatorSet struct {
	byID    map[string]*instance
	bySymTF map[symTF][]*instance
}

func newIndicatorSet() *indicatorSet {
	return &indicatorSet{byID: make(map[string]*instance), bySymTF: make(map[symTF][]*instance)}
}

func (s *indicatorSet) ensure(c *Core, id string, spec IndicatorSpec) {
	if in, ok := s.byID[id]; ok {
		in.refs++
		if !specEqual(in.spec, spec) {
			// The UI re-subscribes the SAME instanceId with a new spec on every
			// symbol/timeframe switch (ChartController.resetForReload) and on a
			// param edit (updateIndicator's remove-then-add). Ignoring the new
			// spec left the instance computing the OLD (symbol, tf, params)
			// forever — the chart then draws a line in the old symbol's price
			// domain, off every visible scale ("VWAP not showing").
			s.respec(c, in, spec)
			return
		}
		s.emitSnapshots(c, in) // the new subscriber needs the series
		return
	}
	ca, err := newCalc(spec)
	if err != nil {
		slog.Warn("indicator spec rejected", "id", id, "type", spec.Type, "err", err)
		return
	}
	in := &instance{id: id, spec: spec, c: ca, refs: 1, lastFolded: -1,
		series: make(map[string][]Point)}
	s.byID[id] = in
	key := symTF{symbol: spec.Symbol, tf: spec.TF}
	s.bySymTF[key] = append(s.bySymTF[key], in)
	s.reseed(c, in)
}

func specEqual(a, b IndicatorSpec) bool {
	return a.Symbol == b.Symbol && a.TF == b.TF && a.Type == b.Type && maps.Equal(a.Params, b.Params)
}

// respec moves a live instance onto a new spec: validate the new calc first
// (an invalid re-spec must not destroy a working instance), re-bucket the
// symTF index, then rebuild from the new spec's finalized history and
// re-snapshot every slot.
func (s *indicatorSet) respec(c *Core, in *instance, spec IndicatorSpec) {
	if _, err := newCalc(spec); err != nil {
		slog.Warn("indicator respec rejected", "id", in.id, "type", spec.Type, "err", err)
		return
	}
	oldKey := symTF{symbol: in.spec.Symbol, tf: in.spec.TF}
	newKey := symTF{symbol: spec.Symbol, tf: spec.TF}
	if oldKey != newKey {
		list := s.bySymTF[oldKey]
		for i, x := range list {
			if x == in {
				s.bySymTF[oldKey] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(s.bySymTF[oldKey]) == 0 {
			delete(s.bySymTF, oldKey)
		}
		s.bySymTF[newKey] = append(s.bySymTF[newKey], in)
	}
	in.spec = spec
	s.reseed(c, in)
}

// reseed rebuilds an instance from finalized history and emits snapshots.
func (s *indicatorSet) reseed(c *Core, in *instance) {
	ca, err := newCalc(in.spec)
	if err != nil { // cannot happen after ensure validated once; stay safe
		return
	}
	in.c = ca
	in.lastFolded = -1
	in.series = make(map[string][]Point)
	for _, b := range c.bars.finalizedBars(in.spec.Symbol, in.spec.TF) {
		s.foldFinal(in, b)
	}
	s.emitSnapshots(c, in)
}

// foldFinal records the final points for b, then folds it.
func (s *indicatorSet) foldFinal(in *instance, b Bar) {
	for _, p := range in.c.points(b) {
		if p.ok {
			in.series[p.slot] = append(in.series[p.slot], Point{TimeMs: b.BucketMs, Value: p.value})
		}
	}
	in.c.fold(b)
	in.lastFolded = b.BucketMs
}

func (s *indicatorSet) emitSnapshots(c *Core, in *instance) {
	for _, slot := range in.c.slots() {
		pts := in.series[slot]
		c.emit(IndicatorUpdate{
			InstanceID: in.id,
			SeriesKey:  in.seriesKey(slot),
			Points:     append([]Point(nil), pts...),
			Snapshot:   true,
		})
	}
}

func (s *indicatorSet) release(id string) {
	in, ok := s.byID[id]
	if !ok {
		return
	}
	in.refs--
	if in.refs > 0 {
		return
	}
	delete(s.byID, id)
	key := symTF{symbol: in.spec.Symbol, tf: in.spec.TF}
	list := s.bySymTF[key]
	for i, x := range list {
		if x == in {
			s.bySymTF[key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(s.bySymTF[key]) == 0 {
		delete(s.bySymTF, key)
	}
}

// onBar routes a bar emission to matching instances (called from barOut).
func (s *indicatorSet) onBar(c *Core, b Bar) {
	list := s.bySymTF[symTF{symbol: b.Symbol, tf: b.TF}]
	for _, in := range list {
		switch {
		case b.InProgress:
			for _, p := range in.c.points(b) {
				if p.ok {
					c.emit(IndicatorUpdate{
						InstanceID: in.id, SeriesKey: in.seriesKey(p.slot),
						Points: []Point{{TimeMs: b.BucketMs, Value: p.value}},
					})
				}
			}
		case b.BucketMs > in.lastFolded:
			for _, p := range in.c.points(b) {
				if p.ok {
					in.series[p.slot] = append(in.series[p.slot], Point{TimeMs: b.BucketMs, Value: p.value})
					c.emit(IndicatorUpdate{
						InstanceID: in.id, SeriesKey: in.seriesKey(p.slot),
						Points: []Point{{TimeMs: b.BucketMs, Value: p.value}},
					})
				}
			}
			in.c.fold(b)
			in.lastFolded = b.BucketMs
		default:
			// A finalized bar rewrote the past (deep-history insert):
			// recompute from scratch and re-snapshot. Only backfill pays this.
			s.reseed(c, in)
		}
	}
}
