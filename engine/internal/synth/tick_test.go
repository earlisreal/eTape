package synth

import (
	"math/rand"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestGenTicks_ExecuteAtTouchAndTurnover(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	ps.Reg = RegTrendUp
	ps.Mid, ps.Anchor = 100, 100 // keep price state consistent with the book's center below
	b := newBook(rng, s, 100)
	var sess sessionAgg
	ticks := genTicks(rng, s, ps, b, &sess, s.Code, 0, 10_000, 1)
	if len(ticks) == 0 {
		t.Fatal("no ticks generated over 10s at lambda>0")
	}
	var seq int64
	for _, tk := range ticks {
		if tk.Seq <= seq {
			t.Errorf("seq not increasing: %d after %d", tk.Seq, seq)
		}
		seq = tk.Seq
		if tk.Turnover != tk.Price*float64(tk.Volume) {
			t.Errorf("turnover %.4f != price*vol %.4f", tk.Turnover, tk.Price*float64(tk.Volume))
		}
		if tk.Dir == feed.Buy && tk.Price < 100 { // buys lift the ask (>= mid)
			t.Errorf("buy printed below mid: %.2f", tk.Price)
		}
	}
	// up-regime should skew buy-heavy
	var buys int
	for _, tk := range ticks {
		if tk.Dir == feed.Buy {
			buys++
		}
	}
	if buys*2 < len(ticks) {
		t.Errorf("up-regime not buy-heavy: %d/%d buys", buys, len(ticks))
	}
}

func TestGenTicks_SilentDuringHalt(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	s := spec(PersRunner)
	ps := newPriceState(s)
	ps.HaltUntilMs = 999_999
	b := newBook(rng, s, 5)
	var sess sessionAgg
	if got := genTicks(rng, s, ps, b, &sess, s.Code, 0, 60_000, 1); len(got) != 0 {
		t.Fatalf("halt should silence ticks, got %d", len(got))
	}
}
