// Package quota watches moomoo's account-level subscription and history
// K-line quotas via Qot_GetSubInfo (3003) and Qot_RequestHistoryKLQuota
// (3104), detects contention from a second OpenD client on the same account,
// and emits leveled sys.events on state transitions. It changes no feed
// behavior — it only warns. Spec:
// docs/superpowers/specs/2026-07-10-moomoo-quota-contention-awareness-design.md
package quota

import "fmt"

const eventKind = "quota"

// subState is the account-wide subscription-quota condition (severity-ordered).
type subState string

const (
	stateOK        subState = "ok"
	stateForeign   subState = "foreign"
	stateLow       subState = "low"
	stateExhausted subState = "exhausted"
)

// histState is the history-K-line-quota condition, independent of subState.
type histState string

const (
	histOK  histState = "ok"
	histLow histState = "low"
)

// reading is one poll's computed inputs to the state machine.
type reading struct {
	subRemain  int // account-wide subscription slots remaining
	foreign    int // subscription slots used by other OpenD clients
	histRemain int // history K-line slots remaining (30-day window)
}

// transition is a pending sys.event describing one state change.
type transition struct {
	level  string // "info" | "warn" | "danger"
	detail string
}

// machine holds the debounced sub/hist states between polls. Driven from a
// single goroutine; not safe for concurrent use.
type machine struct {
	subWarnHeadroom int
	histWarnRemain  int

	sub           subState
	hist          histState
	foreignStreak int // consecutive polls with foreign>0 (2-poll debounce on FOREIGN entry)
}

func newMachine(subWarnHeadroom, histWarnRemain int) *machine {
	return &machine{
		subWarnHeadroom: subWarnHeadroom,
		histWarnRemain:  histWarnRemain,
		sub:             stateOK,
		hist:            histOK,
	}
}

// step advances both state tracks for one poll and returns a transition for
// each track that changed (0, 1, or 2 events).
func (m *machine) step(r reading) []transition {
	var out []transition

	foreignPresent := r.foreign > 0
	if foreignPresent {
		m.foreignStreak++
	} else {
		m.foreignStreak = 0
	}
	if next := m.nextSub(r, foreignPresent); next != m.sub {
		out = append(out, subTransition(m.sub, next, r))
		m.sub = next
	}

	nextHist := histOK
	if r.histRemain < m.histWarnRemain {
		nextHist = histLow
	}
	if nextHist != m.hist {
		out = append(out, histTransition(nextHist, r))
		m.hist = nextHist
	}
	return out
}

// nextSub computes the incoming sub-quota state. Severity wins: EXHAUSTED and
// LOW (both authoritative on RemainQuota, no debounce) outrank FOREIGN, which
// needs 2 consecutive foreign polls to enter (rides out moomoo's <=1-minute
// quota-release lag after a connection closes).
func (m *machine) nextSub(r reading, foreignPresent bool) subState {
	switch {
	case r.subRemain == 0:
		return stateExhausted
	case r.subRemain < m.subWarnHeadroom:
		return stateLow
	case foreignPresent && m.foreignStreak >= 2:
		return stateForeign
	default:
		return stateOK
	}
}

func subTransition(prev, next subState, r reading) transition {
	switch next {
	case stateExhausted:
		return transition{level: "danger", detail: "subscription quota exhausted account-wide"}
	case stateLow:
		return transition{level: "warn", detail: fmt.Sprintf("%d subscription slots remaining account-wide", r.subRemain)}
	case stateForeign:
		return transition{level: "info", detail: fmt.Sprintf("another OpenD client is using %d subscription slots", r.foreign)}
	default: // stateOK
		if prev == stateForeign {
			return transition{level: "info", detail: "other OpenD client released its subscriptions"}
		}
		return transition{level: "info", detail: fmt.Sprintf("subscription quota recovered (%d slots free account-wide)", r.subRemain)}
	}
}

func histTransition(next histState, r reading) transition {
	if next == histLow {
		return transition{level: "warn", detail: fmt.Sprintf("%d historical K-line slots remaining (30-day window)", r.histRemain)}
	}
	return transition{level: "info", detail: fmt.Sprintf("historical K-line quota recovered (%d slots free)", r.histRemain)}
}
