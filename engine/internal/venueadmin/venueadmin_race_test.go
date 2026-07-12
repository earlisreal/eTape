package venueadmin

import (
	"fmt"
	"sync"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
)

// TestConcurrentWritesSerializeWithoutTearing runs SetVenueSetup (with N
// distinct venue lists) interleaved with SeedMoomooVenue and
// MarkMoomooSeedAttempted from many goroutines. It exists to be run with
// `-race`: the mutex added to Admin must make every read-modify-write atomic,
// so there is no data race AND no torn/interleaved file content.
//
// Because the mutex fully serializes calls, the actual outcome is SOME serial
// ordering of the operations — which one lands last is up to the scheduler.
// So this asserts invariants that hold under ANY serial ordering, rather than
// one exact sequence:
//   - the final file still parses;
//   - the one-shot marker ends up set (at least one seed-related call always
//     completes a write that sets it, and no later write ever clears it —
//     WriteVenueConfig preserves whatever Seed value it re-reads);
//   - at most one moomoo venue exists, and if present it has the expected
//     shape;
//   - the non-moomoo venues in the final file exactly match ONE of the N
//     distinct SetVenueSetup inputs — never a mix of two different calls'
//     venues, which is what a torn/interleaved (non-atomic) write would look
//     like.
func TestConcurrentWritesSerializeWithoutTearing(t *testing.T) {
	a, cfgPath, _ := setup(t)

	const n = 8
	lists := make([][]config.Venue, n)
	for i := 0; i < n; i++ {
		lists[i] = []config.Venue{
			{ID: fmt.Sprintf("sim-%d-a", i), Broker: "sim", Env: "paper"},
			{ID: fmt.Sprintf("sim-%d-b", i), Broker: "sim", Env: "paper"},
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = a.SetVenueSetup(config.VenueConfig{Venues: lists[i]})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = a.SeedMoomooVenue(999999)
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.MarkMoomooSeedAttempted()
		}()
	}
	wg.Wait()

	// (a) the final file parses.
	file, err := config.ReadVenueConfig(cfgPath)
	if err != nil {
		t.Fatalf("final file must parse via ReadVenueConfig: %v", err)
	}
	full, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("final file must Load: %v", err)
	}

	// (b) the marker ends up set.
	if !full.Seed.MoomooAttempted {
		t.Fatalf("marker should be set after concurrent seed calls")
	}

	// (c) at most one moomoo venue, correct shape if present.
	var moomooCount int
	var nonMoomoo []config.Venue
	for _, v := range file.Venues {
		if v.Broker == "moomoo" {
			moomooCount++
			if v.ID != "moomoo" || v.Env != "live" || v.AccountID != "999999" {
				t.Fatalf("moomoo venue has unexpected shape: %+v", v)
			}
		} else {
			nonMoomoo = append(nonMoomoo, v)
		}
	}
	if moomooCount > 1 {
		t.Fatalf("expected at most one moomoo venue, got %d: %+v", moomooCount, file.Venues)
	}

	// (d) the non-moomoo venues exactly match ONE of the N distinct
	// SetVenueSetup inputs -- never a mix of two different calls' venues.
	matched := false
	for _, want := range lists {
		if venueIDSetEqual(nonMoomoo, want) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("final non-moomoo venues match no single SetVenueSetup input (torn write?): %+v", nonMoomoo)
	}
}

func venueIDSetEqual(got, want []config.Venue) bool {
	if len(got) != len(want) {
		return false
	}
	ids := map[string]bool{}
	for _, v := range got {
		ids[v.ID] = true
	}
	for _, v := range want {
		if !ids[v.ID] {
			return false
		}
	}
	return true
}
