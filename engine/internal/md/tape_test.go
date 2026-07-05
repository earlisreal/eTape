package md

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestRingWrapsAndSnapshots(t *testing.T) {
	r := newRing(4)
	for i := int64(1); i <= 6; i++ {
		r.append(feed.Tick{Seq: i})
	}
	snap := r.snapshot()
	if len(snap) != 4 || snap[0].Seq != 3 || snap[3].Seq != 6 {
		t.Fatalf("snapshot = %+v, want seqs 3..6", snap)
	}
}
