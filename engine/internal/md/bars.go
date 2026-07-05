// bars.go (Task 11 replaces this file)
package md

import "github.com/earlisreal/eTape/engine/internal/feed"

type barEngine struct{ anchorSecs int64 }

func newBarEngine(anchorSecs int64) *barEngine                      { return &barEngine{anchorSecs: anchorSecs} }
func (e *barEngine) applyTicks(c *Core, ts []feed.Tick)             {}
func (e *barEngine) apply1m(c *Core, bs []feed.Bar)                 {}
func (e *barEngine) seedHistory1m(c *Core, s string, bs []feed.Bar) {}
func (e *barEngine) seedDaily(c *Core, s string, bs []feed.Bar)     {}
func (e *barEngine) markGaps()                                      {}
