package synth

import (
	"math"
	"math/rand"
	"testing"
)

// spec returns a minimal SymbolSpec for exercising stepPrice. Shared by
// other _test.go files in this package.
func spec(p Personality) SymbolSpec {
	return SymbolSpec{Code: "US.TST", Pers: p, Open: 10, PrevClose: 10, Vol: 0.5,
		Spread: SpreadProfile{1, 5, 4}, LambdaMin: 1, LambdaMax: 10}
}

// stepForceMove is a test-only helper that injects a synthetic print into ps
// without running stepPrice's full regime/drift machinery, so halt detection
// can be exercised deterministically and independently of the price walk.
func stepForceMove(ps *priceState, tsMs int64, px float64) {
	ps.Mid = px
	ps.win = append(ps.win, pricePoint{tsMs: tsMs, px: px})
}

func TestStepPrice_DeterministicAndCentSnapped(t *testing.T) {
	run := func() float64 {
		rng := rand.New(rand.NewSource(7))
		s := spec(PersLargeCap)
		ps := newPriceState(s)
		now := int64(0)
		for i := 0; i < 5000; i++ {
			now += 200
			stepPrice(rng, s, ps, now, 200)
		}
		return ps.Mid
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("nondeterministic: %v vs %v", a, b)
	}
	if math.Abs(a*100-math.Round(a*100)) > 1e-9 {
		t.Errorf("mid %.4f not snapped to cent", a)
	}
}

func TestStepPrice_BoundedDriftOverHours(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	now := int64(0)
	for i := 0; i < 6*60*60*5; i++ { // ~6h at 200ms steps
		now += 200
		stepPrice(rng, s, ps, now, 200)
		if ps.Mid <= 0 {
			t.Fatalf("price went non-positive: %v", ps.Mid)
		}
	}
	// mean-reversion keeps a large cap within a sane band of its open
	if ps.Mid < s.Open*0.3 || ps.Mid > s.Open*3 {
		t.Errorf("unbounded drift: open %.2f -> %.2f", s.Open, ps.Mid)
	}
}

func TestStepPrice_RunnerHaltFreezes(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := spec(PersRunner)
	ps := newPriceState(s)
	// force a >10% jump inside the window by seeding the window then a big move
	ps.Mid = 10
	ps.win = []pricePoint{{tsMs: 0, px: 10}}
	stepForceMove(ps, 1000, 11.5) // test-only helper: injects an 11.5 print at t=1000ms
	detectHalt(s, ps, 1000)       // package-private halt check, also used internally by stepPrice
	if !ps.Halted(1000) {
		t.Fatal("expected halt after >10% move in 5min")
	}
	before := ps.Mid
	stepPrice(rng, s, ps, 2000, 1000)
	if ps.Mid != before {
		t.Errorf("price moved during halt: %v -> %v", before, ps.Mid)
	}
}

// TestStepPrice_DayScaleReversionConverges is a regression test for a bug
// surfaced by Task 9's boot-time history seeder: that seeder legitimately
// calls stepPrice once per day (dtMs on the order of a day or more,
// unclamped) for its coarse ~1-year history pass. The original linear-Euler
// reversion (`Mid += (Anchor-Mid)*reversion(reg)*dtSec`) is only a bounded
// correction for small dtSec; at day-scale dtSec the coefficient reaches
// into the thousands, so the linear formula overshot wildly past Anchor and
// got trapped at the $0.01 floor clamp — reproduced independently as ~51% of
// symbol-runs floor-pinned after a single coarse pass. The fix replaces it
// with exponential decay (`Mid = Anchor + (Mid-Anchor)*exp(-rate*dtSec)`),
// which is stable for any dtSec and converges Mid smoothly to Anchor at
// large dtSec instead of overshooting.
func TestStepPrice_DayScaleReversionConverges(t *testing.T) {
	const dayMs = 86_400_000 // one day
	for _, days := range []int64{1, 3, 7, 30, 365} {
		for _, seed := range []int64{1, 11, 42} {
			rng := rand.New(rand.NewSource(seed))
			s := spec(PersLargeCap)
			ps := newPriceState(s)
			ps.Anchor = 100
			ps.Mid = 50 // displaced far from Anchor, as at the start of a coarse seed pass
			anchorBefore := ps.Anchor
			dtMs := days * dayMs

			stepPrice(rng, s, ps, dtMs, dtMs)

			if math.IsNaN(ps.Mid) || math.IsInf(ps.Mid, 0) {
				t.Fatalf("days=%d seed=%d: Mid not finite: %v", days, seed, ps.Mid)
			}
			if ps.Mid <= 0 {
				t.Fatalf("days=%d seed=%d: Mid non-positive: %v", days, seed, ps.Mid)
			}
			if ps.Mid <= 0.01+1e-9 {
				t.Errorf("days=%d seed=%d: Mid floor-pinned at %.4f instead of converging toward Anchor %.2f",
					days, seed, ps.Mid, anchorBefore)
			}
			// Should converge close to (not overshoot past, not oscillate
			// around) the pre-step Anchor — not just "somewhere positive".
			if math.Abs(ps.Mid-anchorBefore) > anchorBefore*0.02 {
				t.Errorf("days=%d seed=%d: Mid=%.4f did not converge near Anchor=%.4f (started at Mid=50)",
					days, seed, ps.Mid, anchorBefore)
			}
		}
	}
}

// TestStepPrice_ExtremeDrawCapped is a regression test for a second bug
// surfaced by Task 9's history seeder: noise scales with sqrt(dtSec), so on
// a sufficiently extreme rng.NormFloat64() draw (more likely at large dtSec,
// but possible at any dtSec) the uncapped drift+noise fractional change
// could send Mid to the $0.01 price floor or to an implausible multiple of
// its prior value in a single stepPrice call. Independent measurement
// across 200 real Seed() runs found this in 27% of symbol-runs and on every
// single boot (200/200) before the maxStepChange cap was added — see the
// "Fix round 2" section of task-2-report.md for the before/after frequency
// sweep against the real Generator/Seed() pipeline (the authoritative
// verification; this test only proves the cap's own arithmetic).
//
// Rather than searching for a "magic" seed whose natural rng.NormFloat64()
// draw happens to be extreme (checked empirically: a 500-seed x 5-dtMs-scale
// sweep with a natural draw never got close to the cap boundary, because
// Fix Round 1's exponential reversion already neutralizes most large-dtSec
// blowups on its own — the two fixes are complementary, not redundant, and
// this test needs to isolate the cap specifically), this test forces the
// pre-clamp magnitude to be extreme deterministically by using an
// unrealistically large spec.Vol (noise scales linearly with Vol, so a huge
// Vol guarantees |drift+noise|/100 dwarfs maxStepChange for essentially any
// rng draw). That isolates exactly what maxStepChange is responsible for:
// given an arbitrarily large pre-clamp change, Mid must still land within
// before*[1-maxStepChange, 1+maxStepChange] (plus trivial cent-rounding
// slack) — the same convex-combination argument as
// TestStepPrice_DayScaleReversionConverges shows the later mean-reversion
// step can only pull the result closer to Anchor, never push it further
// outside that range.
func TestStepPrice_ExtremeDrawCapped(t *testing.T) {
	const before = 100.0
	dtScales := []int64{200, 60_000, 30 * 60_000, 86_400_000, 365 * 86_400_000}
	lo := before*(1-maxStepChange) - 0.01
	hi := before*(1+maxStepChange) + 0.01

	for seed := int64(0); seed < 50; seed++ {
		for _, dtMs := range dtScales {
			rng := rand.New(rand.NewSource(seed))
			s := spec(PersLargeCap)
			s.Vol = 1000 // deliberately absurd: forces an uncapped blowup regardless of rng draw
			ps := newPriceState(s)
			ps.Mid = before
			ps.Anchor = before

			stepPrice(rng, s, ps, dtMs, dtMs)

			if ps.Mid < lo || ps.Mid > hi {
				t.Fatalf("seed=%d dtMs=%d: Mid=%.4f outside capped range [%.4f, %.4f]",
					seed, dtMs, ps.Mid, lo, hi)
			}
		}
	}
}
