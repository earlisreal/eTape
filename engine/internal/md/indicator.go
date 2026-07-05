// indicator.go (Task 12 replaces this file; the exported types are already
// final — core.go's API references them)
package md

import "github.com/earlisreal/eTape/engine/internal/session"

type IndicatorType string

type IndicatorSpec struct {
	Symbol string
	TF     session.Timeframe
	Type   IndicatorType
	Params map[string]float64
}

type indicatorSet struct{}

func newIndicatorSet() *indicatorSet                                  { return &indicatorSet{} }
func (s *indicatorSet) ensure(c *Core, id string, spec IndicatorSpec) {}
func (s *indicatorSet) release(id string)                             {}

//nolint:unused // wired up by Task 11's real bar engine, which calls this on every closed bar
func (s *indicatorSet) onBar(c *Core, b Bar) {}
