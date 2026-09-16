package scan

import "time"

const scannerAlertCooldown = time.Minute

type alertState struct {
	ready      bool
	qualified  bool
	lastAlert  time.Time
	silent     bool
	newArrival bool
	seq        int64
	lastValue  float64
	hasValue   bool
}

type alertEngine struct {
	states map[string]alertState
}

func newAlertEngine() alertEngine { return alertEngine{states: map[string]alertState{}} }

func (e *alertEngine) reset(silent bool) {
	for symbol, state := range e.states {
		state.ready, state.qualified, state.hasValue, state.newArrival = false, false, false, false
		state.silent = silent
		e.states[symbol] = state
	}
}

func (e *alertEngine) resetSymbol(symbol string, silent bool) {
	state := e.states[symbol]
	state.ready, state.qualified, state.hasValue, state.newArrival = false, false, false, false
	state.silent = silent
	e.states[symbol] = state
}

// observe returns a new process-local revision only when a threshold crossing
// (or a healthy new arrival) is allowed to emit. Qualification is updated even
// when another filter rejects the row, so crossing state cannot replay later.
func (e *alertEngine) observe(symbol, mode string, threshold float64, value *float64, eligible, newArrival, silent bool, now time.Time) int64 {
	state := e.states[symbol]
	if value == nil {
		state.newArrival = state.newArrival || newArrival
		state.silent = state.silent || silent
		e.states[symbol] = state
		return 0
	}
	qualified := qualifies(mode, threshold, *value)
	if !state.ready {
		state.ready = true
		state.qualified = qualified
		state.hasValue = true
		state.lastValue = *value
		arrival := newArrival || state.newArrival
		quiet := silent || state.silent
		state.newArrival = false
		state.silent = false
		if !quiet && arrival && eligible && (mode == "most_active" || threshold <= 0 || qualified) {
			return e.emit(&state, now, symbol)
		}
		e.states[symbol] = state
		return 0
	}
	crossed := !state.qualified && qualified
	state.qualified, state.lastValue, state.hasValue = qualified, *value, true
	if silent || state.silent || !crossed || !eligible || threshold <= 0 || mode == "most_active" {
		state.silent = false
		e.states[symbol] = state
		return 0
	}
	return e.emit(&state, now, symbol)
}

func (e *alertEngine) revision(symbol string) int64 { return e.states[symbol].seq }

func qualifies(mode string, threshold, value float64) bool {
	if threshold <= 0 {
		return false
	}
	if mode == "losers" {
		return value <= -threshold
	}
	return value >= threshold
}

func (e *alertEngine) emit(state *alertState, now time.Time, symbol string) int64 {
	if !state.lastAlert.IsZero() && now.Sub(state.lastAlert) < scannerAlertCooldown {
		e.states[symbol] = *state
		return 0
	}
	state.seq++
	state.lastAlert = now
	e.states[symbol] = *state
	return state.seq
}
