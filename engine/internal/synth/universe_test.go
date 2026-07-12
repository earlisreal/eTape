package synth

import (
	"math/rand"
	"strings"
	"testing"
)

func TestDrawUniverse_DeterministicAndWellFormed(t *testing.T) {
	a := DrawUniverse(rand.New(rand.NewSource(42)))
	b := DrawUniverse(rand.New(rand.NewSource(42)))
	if len(a) != 12 {
		t.Fatalf("want 12 symbols, got %d", len(a))
	}
	// same seed -> identical universe
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
	// counts per personality
	var run, lc, mc int
	seen := map[string]bool{}
	for _, s := range a {
		if !strings.HasPrefix(s.Code, "US.") {
			t.Errorf("symbol %q missing US. prefix", s.Code)
		}
		if seen[s.Code] {
			t.Errorf("duplicate symbol %q", s.Code)
		}
		seen[s.Code] = true
		switch s.Pers {
		case PersRunner:
			run++
			if s.Open < 2 || s.Open > 15 {
				t.Errorf("runner %s open %.2f out of $2-15", s.Code, s.Open)
			}
			if s.FloatShares < 5_000_000 || s.FloatShares > 20_000_000 {
				t.Errorf("runner %s float %d out of 5-20M", s.Code, s.FloatShares)
			}
		case PersLargeCap:
			lc++
			if s.Open < 80 || s.Open > 500 {
				t.Errorf("largecap %s open %.2f out of $80-500", s.Code, s.Open)
			}
		case PersMidCap:
			mc++
		}
	}
	if run != 2 || lc != 5 || mc != 5 {
		t.Fatalf("personality mix = runner:%d large:%d mid:%d, want 2/5/5", run, lc, mc)
	}
}

func TestDrawUniverse_DiffersAcrossSeeds(t *testing.T) {
	a := DrawUniverse(rand.New(rand.NewSource(1)))
	b := DrawUniverse(rand.New(rand.NewSource(2)))
	same := true
	for i := range a {
		if a[i].Code != b[i].Code || a[i].Pers != b[i].Pers {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds produced identical universe assignment")
	}
}
