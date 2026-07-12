package venueseed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
)

// waitTimeout bounds every channel wait below. Generous relative to the
// in-memory, no-IO work under test -- these tests synchronize on real signals
// (fakeAdmin/notifyRecorder channels), never bare sleeps; this is only the
// "something is wrong" backstop.
const waitTimeout = 2 * time.Second

// fakeAdmin implements Admin for tests. Every method signals on its own
// buffered channel right after recording the call, so a test can wait for the
// LAST call a given code path makes rather than sleeping.
type fakeAdmin struct {
	mu sync.Mutex

	attempted   bool
	venueExists bool
	stateErr    error
	markErr     error
	seedCreated bool
	seedErr     error

	stateCalls int
	markCalls  int
	seedCalls  []uint64

	stateSig chan struct{}
	markSig  chan struct{}
	seedSig  chan struct{}
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{
		stateSig: make(chan struct{}, 8),
		markSig:  make(chan struct{}, 8),
		seedSig:  make(chan struct{}, 8),
	}
}

func (f *fakeAdmin) MoomooSeedState() (attempted, venueExists bool, err error) {
	f.mu.Lock()
	f.stateCalls++
	attempted, venueExists, err = f.attempted, f.venueExists, f.stateErr
	f.mu.Unlock()
	f.stateSig <- struct{}{}
	return attempted, venueExists, err
}

func (f *fakeAdmin) MarkMoomooSeedAttempted() error {
	f.mu.Lock()
	f.markCalls++
	err := f.markErr
	f.mu.Unlock()
	f.markSig <- struct{}{}
	return err
}

func (f *fakeAdmin) SeedMoomooVenue(accID uint64) (created bool, err error) {
	f.mu.Lock()
	f.seedCalls = append(f.seedCalls, accID)
	created, err = f.seedCreated, f.seedErr
	f.mu.Unlock()
	f.seedSig <- struct{}{}
	return created, err
}

func (f *fakeAdmin) counts() (state, mark int, seeds []uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stateCalls, f.markCalls, append([]uint64(nil), f.seedCalls...)
}

// notifyCall records one Config.Notify invocation.
type notifyCall struct{ kind, detail, level string }

// notifyRecorder is the Config.Notify test double: it records every call and
// signals on sig so a test can wait for the call rather than sleep.
type notifyRecorder struct {
	mu    sync.Mutex
	calls []notifyCall
	sig   chan struct{}
}

func newNotifyRecorder() *notifyRecorder {
	return &notifyRecorder{sig: make(chan struct{}, 8)}
}

func (r *notifyRecorder) fn(kind, detail, level string) {
	r.mu.Lock()
	r.calls = append(r.calls, notifyCall{kind, detail, level})
	r.mu.Unlock()
	r.sig <- struct{}{}
}

func (r *notifyRecorder) get() []notifyCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notifyCall(nil), r.calls...)
}

// waitSig blocks until ch fires or waitTimeout elapses (test failure).
func waitSig(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for expected call")
	}
}

// eligibleAcc/masterAcc/disabledAcc/nonUSAcc build just enough of a TrdAcc for
// moomoo.EligibleLiveUS's checks; venueseed can't reach the moomoo package's
// own unexported test helpers (different package), so this package builds its
// own minimal fixtures directly against trdcommon.
func eligibleAcc(accID uint64) *trdcommon.TrdAcc {
	return &trdcommon.TrdAcc{
		TrdEnv:            proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Real)),
		AccID:             proto.Uint64(accID),
		TrdMarketAuthList: []int32{int32(trdcommon.TrdMarket_TrdMarket_US)},
		AccStatus:         proto.Int32(int32(trdcommon.TrdAccStatus_TrdAccStatus_Active)),
		AccRole:           proto.Int32(int32(trdcommon.TrdAccRole_TrdAccRole_Normal)),
	}
}

func masterAcc(accID uint64) *trdcommon.TrdAcc {
	a := eligibleAcc(accID)
	a.AccRole = proto.Int32(int32(trdcommon.TrdAccRole_TrdAccRole_Master))
	return a
}

func nonUSAcc(accID uint64) *trdcommon.TrdAcc {
	a := eligibleAcc(accID)
	a.TrdMarketAuthList = []int32{int32(trdcommon.TrdMarket_TrdMarket_HK)}
	return a
}

// discoverFunc returns a Discover test double that returns accs/err and
// counts + signals every call on sig.
func discoverFunc(accs []*trdcommon.TrdAcc, err error) (fn func(ctx context.Context) ([]*trdcommon.TrdAcc, error), calls func() int, sig chan struct{}) {
	var mu sync.Mutex
	var n int
	ch := make(chan struct{}, 8)
	fn = func(ctx context.Context) ([]*trdcommon.TrdAcc, error) {
		mu.Lock()
		n++
		mu.Unlock()
		ch <- struct{}{}
		return accs, err
	}
	calls = func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
	return fn, calls, ch
}

func TestOnFeedUp_AlreadyAttempted_NoFurtherAction(t *testing.T) {
	admin := newFakeAdmin()
	admin.attempted = true
	nr := newNotifyRecorder()
	discover, discoverCalls, _ := discoverFunc(nil, errors.New("must not be called"))

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, admin.stateSig)
	stateCalls, markCalls, seeds := admin.counts()
	if stateCalls != 1 || markCalls != 0 || len(seeds) != 0 {
		t.Fatalf("admin calls = state:%d mark:%d seeds:%v, want state:1 mark:0 seeds:[]", stateCalls, markCalls, seeds)
	}
	if got := discoverCalls(); got != 0 {
		t.Fatalf("discover called %d times, want 0 (removal-sticks path must never probe)", got)
	}
	if calls := nr.get(); len(calls) != 0 {
		t.Fatalf("notify calls = %v, want none", calls)
	}
}

func TestOnFeedUp_VenueAlreadyExists_MarksAttemptedNoDiscover(t *testing.T) {
	admin := newFakeAdmin()
	admin.venueExists = true
	nr := newNotifyRecorder()
	discover, discoverCalls, _ := discoverFunc(nil, errors.New("must not be called"))

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, admin.markSig)
	_, markCalls, seeds := admin.counts()
	if markCalls != 1 || len(seeds) != 0 {
		t.Fatalf("admin calls = mark:%d seeds:%v, want mark:1 seeds:[]", markCalls, seeds)
	}
	if got := discoverCalls(); got != 0 {
		t.Fatalf("discover called %d times, want 0 (pre-existing venue must never probe)", got)
	}
	if calls := nr.get(); len(calls) != 0 {
		t.Fatalf("notify calls = %v, want none (log only, no toast)", calls)
	}
}

func TestOnFeedUp_DiscoverError_NoMarkerNoNotify(t *testing.T) {
	admin := newFakeAdmin()
	nr := newNotifyRecorder()
	discover, discoverCalls, discoverSig := discoverFunc(nil, errors.New("opend: request timed out"))

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, discoverSig)
	if got := discoverCalls(); got != 1 {
		t.Fatalf("discover called %d times, want 1", got)
	}
	_, markCalls, seeds := admin.counts()
	if markCalls != 0 || len(seeds) != 0 {
		t.Fatalf("admin calls = mark:%d seeds:%v, want none written on a probe error (retry next boot)", markCalls, seeds)
	}
	if calls := nr.get(); len(calls) != 0 {
		t.Fatalf("notify calls = %v, want none (no toast on a transport/login error)", calls)
	}
}

func TestOnFeedUp_ZeroEligible_MarksAttemptedAndDeclinesInfo(t *testing.T) {
	admin := newFakeAdmin()
	nr := newNotifyRecorder()
	// A mix of ineligible accounts (master, non-US) -- zero of them eligible.
	discover, discoverCalls, _ := discoverFunc([]*trdcommon.TrdAcc{masterAcc(1), nonUSAcc(2)}, nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, nr.sig)
	if got := discoverCalls(); got != 1 {
		t.Fatalf("discover called %d times, want 1", got)
	}
	_, markCalls, seeds := admin.counts()
	if markCalls != 1 || len(seeds) != 0 {
		t.Fatalf("admin calls = mark:%d seeds:%v, want mark:1 seeds:[]", markCalls, seeds)
	}
	calls := nr.get()
	if len(calls) != 1 {
		t.Fatalf("notify calls = %v, want exactly 1", calls)
	}
	if calls[0].kind != "venue.seed_declined" || calls[0].level != "info" {
		t.Fatalf("notify = %+v, want kind=venue.seed_declined level=info", calls[0])
	}
	if calls[0].detail != "moomoo: no live US-authorized account on this OpenD login." {
		t.Fatalf("notify detail = %q, want exact copy string", calls[0].detail)
	}
}

func TestOnFeedUp_MultipleEligible_MarksAttemptedAndDeclinesWarn(t *testing.T) {
	admin := newFakeAdmin()
	nr := newNotifyRecorder()
	discover, _, _ := discoverFunc([]*trdcommon.TrdAcc{eligibleAcc(1), eligibleAcc(2)}, nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, nr.sig)
	_, markCalls, seeds := admin.counts()
	if markCalls != 1 || len(seeds) != 0 {
		t.Fatalf("admin calls = mark:%d seeds:%v, want mark:1 seeds:[]", markCalls, seeds)
	}
	calls := nr.get()
	if len(calls) != 1 {
		t.Fatalf("notify calls = %v, want exactly 1", calls)
	}
	if calls[0].kind != "venue.seed_declined" || calls[0].level != "warn" {
		t.Fatalf("notify = %+v, want kind=venue.seed_declined level=warn", calls[0])
	}
	want := "moomoo: 2 live accounts found — pick one in Settings → Venues."
	if calls[0].detail != want {
		t.Fatalf("notify detail = %q, want %q", calls[0].detail, want)
	}
}

func TestOnFeedUp_OneEligible_Created_NotifiesWarnWithAccID(t *testing.T) {
	admin := newFakeAdmin()
	admin.seedCreated = true
	nr := newNotifyRecorder()
	// Mixed in with an ineligible account to prove the filter is applied
	// before counting, not just passed the raw list through.
	discover, _, _ := discoverFunc([]*trdcommon.TrdAcc{masterAcc(99), eligibleAcc(42)}, nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, admin.seedSig)
	waitSig(t, nr.sig)

	_, markCalls, seeds := admin.counts()
	if markCalls != 0 {
		t.Fatalf("markCalls = %d, want 0 (SeedMoomooVenue writes the marker itself)", markCalls)
	}
	if len(seeds) != 1 || seeds[0] != 42 {
		t.Fatalf("seeds = %v, want [42] (the master account must be filtered out)", seeds)
	}
	calls := nr.get()
	if len(calls) != 1 {
		t.Fatalf("notify calls = %v, want exactly 1", calls)
	}
	if calls[0].kind != "venue.seeded" || calls[0].level != "warn" {
		t.Fatalf("notify = %+v, want kind=venue.seeded level=warn", calls[0])
	}
	want := "moomoo venue configured from OpenD (account 42) — restart to activate."
	if calls[0].detail != want {
		t.Fatalf("notify detail = %q, want %q", calls[0].detail, want)
	}
}

func TestOnFeedUp_OneEligible_RacedManualSave_NoNotify(t *testing.T) {
	admin := newFakeAdmin()
	admin.seedCreated = false // created=false, err=nil: SeedMoomooVenue found nothing left to do
	nr := newNotifyRecorder()
	discover, _, _ := discoverFunc([]*trdcommon.TrdAcc{eligibleAcc(7)}, nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, admin.seedSig)
	if calls := nr.get(); len(calls) != 0 {
		t.Fatalf("notify calls = %v, want none", calls)
	}
}

func TestOnFeedUp_OneEligible_ValidationError_NoNotify(t *testing.T) {
	admin := newFakeAdmin()
	admin.seedErr = errors.New("moomoo: venue id \"moomoo\" already in use by a different broker")
	nr := newNotifyRecorder()
	discover, _, _ := discoverFunc([]*trdcommon.TrdAcc{eligibleAcc(7)}, nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, admin.seedSig)
	if calls := nr.get(); len(calls) != 0 {
		t.Fatalf("notify calls = %v, want none", calls)
	}
}

// TestOnFeedUp_OnceOnly proves the one-shot-per-process gate: two OnFeedUp
// calls (simulating OpenD connect then a later reconnect) must probe exactly
// once. Synchronizes on the notify signal rather than sleeping -- once notify
// has fired, the run() goroutine's only observable side effects for this path
// (MoomooSeedState -> MarkMoomooSeedAttempted -> Notify) have all already
// happened, in that order, on the same goroutine.
func TestOnFeedUp_OnceOnly(t *testing.T) {
	admin := newFakeAdmin()
	nr := newNotifyRecorder()
	discover, discoverCalls, _ := discoverFunc(nil, nil) // zero eligible -> declineSeed -> notify

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nr.fn, Discover: discover})
	ctx := context.Background()
	s.OnFeedUp(ctx)
	s.OnFeedUp(ctx) // must lose the CAS and return without spawning a second goroutine

	waitSig(t, nr.sig)
	if got := discoverCalls(); got != 1 {
		t.Fatalf("discover called %d times across two OnFeedUp calls, want exactly 1", got)
	}
	if _, markCalls, _ := admin.counts(); markCalls != 1 {
		t.Fatalf("markCalls = %d, want exactly 1", markCalls)
	}
}

// TestOnFeedUp_NilNotify_NoPanic covers Config.Notify's documented nil-safety
// by driving a path that would otherwise call it.
func TestOnFeedUp_NilNotify_NoPanic(t *testing.T) {
	admin := newFakeAdmin()
	discover, _, discoverSig := discoverFunc(nil, nil) // zero eligible -> declineSeed -> notify (nil)

	s := New(Config{Admin: admin, Clock: clock.NewFake(time.Unix(0, 0)), Notify: nil, Discover: discover})
	s.OnFeedUp(context.Background())

	waitSig(t, discoverSig)
	waitSig(t, admin.markSig) // declineSeed's own last call; if this fires, the nil Notify call above didn't panic
}
