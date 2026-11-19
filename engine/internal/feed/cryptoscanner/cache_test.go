package cryptoscanner

import (
	"testing"
	"time"
)

func TestScannerChangeUsesFreshFiveMinuteBaseline(t *testing.T) {
	now := time.Unix(1800000000, 0)
	history := []scannerPrice{
		{at: now.Add(-6 * time.Minute), last: 12},
		{at: now.Add(-5*time.Minute - 2*time.Second), last: 100},
		{at: now.Add(-5*time.Minute + time.Second), last: 110},
	}
	next, change := scannerChange(history, scannerPrice{at: now, last: 125})
	if change == nil || *change != 25 || len(next) != 3 {
		t.Fatalf("history=%v change=%v", next, change)
	}
	if _, change := scannerChange(next, scannerPrice{at: now.Add(-time.Second), last: 120}); change != nil {
		t.Fatalf("out-of-order update supplied a move: %v", *change)
	}
	if _, change := scannerChange(next, scannerPrice{at: now.Add(6 * time.Minute), last: 120}); change != nil {
		t.Fatalf("gap supplied a move: %v", *change)
	}
}
