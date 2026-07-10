package quota

import "testing"

func step(m *machine, subRemain, foreign, histRemain int) []transition {
	return m.step(reading{subRemain: subRemain, foreign: foreign, histRemain: histRemain})
}

func TestForeignRequiresTwoConsecutivePolls(t *testing.T) {
	m := newMachine(12, 10)
	if evs := step(m, 90, 5, 100); len(evs) != 0 || m.sub != stateOK {
		t.Fatalf("first foreign poll must not enter FOREIGN: %v %s", evs, m.sub)
	}
	evs := step(m, 90, 5, 100)
	if m.sub != stateForeign || len(evs) != 1 || evs[0].level != "info" {
		t.Fatalf("second consecutive foreign poll enters FOREIGN(info): %v %s", evs, m.sub)
	}
}

func TestForeignStreakResetsOnCleanPoll(t *testing.T) {
	m := newMachine(12, 10)
	step(m, 90, 5, 100) // streak 1
	step(m, 90, 0, 100) // reset
	if evs := step(m, 90, 5, 100); m.sub != stateOK || len(evs) != 0 {
		t.Fatalf("streak must reset; one more foreign poll should NOT be FOREIGN yet: %v %s", evs, m.sub)
	}
}

func TestLowAndExhaustedAreImmediateAndOutrankForeign(t *testing.T) {
	m := newMachine(12, 10)
	if evs := step(m, 8, 30, 100); m.sub != stateLow || evs[0].level != "warn" {
		t.Fatalf("remain<headroom => LOW(warn) immediately: %v %s", evs, m.sub)
	}
	if evs := step(m, 0, 30, 100); m.sub != stateExhausted || evs[0].level != "danger" {
		t.Fatalf("remain==0 => EXHAUSTED(danger): %v %s", evs, m.sub)
	}
}

func TestRecoveryToOKEmitsInfo(t *testing.T) {
	m := newMachine(12, 10)
	step(m, 8, 0, 100) // LOW
	evs := step(m, 90, 0, 100)
	if m.sub != stateOK || len(evs) != 1 || evs[0].level != "info" {
		t.Fatalf("LOW->OK is info: %v %s", evs, m.sub)
	}
}

func TestForeignToOKWording(t *testing.T) {
	m := newMachine(12, 10)
	step(m, 90, 5, 100)
	step(m, 90, 5, 100) // FOREIGN
	evs := step(m, 90, 0, 100)
	if m.sub != stateOK || evs[0].detail != "other OpenD client released its subscriptions" {
		t.Fatalf("FOREIGN->OK wording: %v %s", evs, m.sub)
	}
}

func TestHistLowIsIndependentAndWarn(t *testing.T) {
	m := newMachine(12, 10)
	evs := step(m, 90, 0, 7) // healthy sub, low hist
	if m.hist != histLow || len(evs) != 1 || evs[0].level != "warn" {
		t.Fatalf("hist<remain-threshold => histLOW(warn), sub unchanged: %v hist=%s sub=%s", evs, m.hist, m.sub)
	}
	if evs := step(m, 90, 0, 50); m.hist != histOK || evs[0].level != "info" {
		t.Fatalf("hist recovery => info: %v %s", evs, m.hist)
	}
}

func TestNoEventWhenStateUnchanged(t *testing.T) {
	m := newMachine(12, 10)
	step(m, 8, 0, 100) // LOW
	if evs := step(m, 7, 0, 100); len(evs) != 0 {
		t.Fatalf("still LOW => no repeat event: %v", evs)
	}
}
