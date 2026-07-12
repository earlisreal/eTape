// Package venueseed auto-configures the moomoo live venue the first time the
// engine connects to OpenD: moomoo needs no key/secret in eTape (OpenD is the
// local, already-authenticated gateway), so the only missing fact is the
// numeric account id, discovered via Trd_GetAccList. The [seed] marker in
// config.toml records the first definitive attempt so a user's later manual
// removal of the venue sticks -- no re-seeding.
//
// Every config mutation goes through the venueadmin.Admin methods injected as
// Config.Admin (locked, TOCTOU-safe) -- this package never touches config.toml
// directly, and it never sends Trd_UnlockTrade: Trd_GetAccList (via
// moomoo.ListAccounts) is read-only, and the seeder only ever writes a venue
// config entry, never an order or an arm state.
package venueseed

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/moomoo"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
)

// discoverTimeout bounds the read-only Trd_GetAccList probe OnFeedUp's
// goroutine issues. A login stuck quote-only (or an OpenD that never answers)
// must not hang this goroutine forever -- there is no retry-with-backoff here,
// the next process boot tries again naturally.
const discoverTimeout = 10 * time.Second

// Admin is the venueadmin.Admin subset the seeder mutates config through --
// never a raw config.toml write. Satisfied by *venueadmin.Admin in production.
type Admin interface {
	// MoomooSeedState reports the file's current auto-config state: whether
	// the one-shot marker is already set, and whether any broker=="moomoo"
	// venue exists (any id -- including one the user hand-added).
	MoomooSeedState() (attempted, venueExists bool, err error)
	// MarkMoomooSeedAttempted sets the one-shot marker without touching
	// venues. Idempotent.
	MarkMoomooSeedAttempted() error
	// SeedMoomooVenue appends the moomoo venue (accID) plus the marker in one
	// atomic write, re-checking state under its own lock first.
	SeedMoomooVenue(accID uint64) (created bool, err error)
}

// Config configures a Seeder.
type Config struct {
	Admin     Admin
	OpenDAddr string      // cfg.OpenD.Addr()
	Clock     clock.Clock // falls back to clock.System{}

	// Notify emits a sys.events frame (kind, human-readable detail, level --
	// "info"/"warn"/"danger"). Nil-safe: OnFeedUp's goroutine guards against a
	// nil Notify rather than requiring every caller (tests especially) to
	// supply one.
	Notify func(kind, detail, level string)

	// Discover is a test seam. Nil in production, where it defaults to
	// moomoo.ListAccounts(ctx, addr, "etape-seed", clk) -- a short-lived,
	// read-only trade connection dialed just for this probe (the feed
	// connection implements no Trd_* protocols, and the seeder must not touch
	// a live broker adapter's own connection).
	Discover func(ctx context.Context) ([]*trdcommon.TrdAcc, error)
}

// Seeder runs the one-shot moomoo auto-config probe. Construct with New and
// drive it from forwardMD's md.ConnUpdate{Up: true} handling via OnFeedUp.
type Seeder struct {
	admin    Admin
	clk      clock.Clock
	notifyFn func(kind, detail, level string)
	discover func(ctx context.Context) ([]*trdcommon.TrdAcc, error)

	// attempted is the IN-PROCESS one-shot gate: only the first OnFeedUp call
	// of this process's lifetime spawns the probe goroutine, so an OpenD
	// disconnect/reconnect flapping mid-session can never re-trigger it. This
	// is distinct from (and layered in front of) the FILE-level marker
	// Admin.MoomooSeedState reports, which survives across process restarts.
	attempted atomic.Bool
}

// New builds a Seeder from cfg. A nil cfg.Clock falls back to clock.System{};
// a nil cfg.Discover falls back to moomoo.ListAccounts against cfg.OpenDAddr
// using cfg.Clock and ClientID "etape-seed" -- the same throwaway-connection
// idiom VerifyAccount's "etape-trade-probe" already uses, so the feed
// connection (which implements no Trd_* protocols) is never touched.
func New(cfg Config) *Seeder {
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System{}
	}
	s := &Seeder{admin: cfg.Admin, clk: clk, notifyFn: cfg.Notify, discover: cfg.Discover}
	if s.discover == nil {
		addr := cfg.OpenDAddr
		s.discover = func(ctx context.Context) ([]*trdcommon.TrdAcc, error) {
			return moomoo.ListAccounts(ctx, addr, "etape-seed", clk)
		}
	}
	return s
}

// OnFeedUp is called from forwardMD on every md.ConnUpdate{Up: true} (feed
// connect AND every reconnect). It is non-blocking: the state-machine steps
// below (Part 2 of this package's design) run on the Seeder's own goroutine,
// never on forwardMD's. The CompareAndSwap makes this one-shot per PROCESS --
// only the very first call spawns run(); OpenD flapping up/down/up across the
// rest of the session cannot re-trigger the probe, regardless of what run()
// ultimately decides.
func (s *Seeder) OnFeedUp(ctx context.Context) {
	if !s.attempted.CompareAndSwap(false, true) {
		return
	}
	go s.run(ctx)
}

// run is the seeder's state machine, invoked at most once per process (see
// OnFeedUp). Steps mirror the design brief exactly:
//
//  1. Read the FILE-level state. attempted=true means a user (or the marker
//     itself) already settled this venue's fate -- most importantly, it means
//     a user's earlier manual removal of an auto-seeded venue must stick, so
//     this returns silently with no further action.
//  2. attempted=false but a moomoo venue already exists (upgrade path, or a
//     user hand-added one before this ever ran): mark attempted and return.
//     This is what makes THAT venue's later removal also stick -- no
//     duplicate is ever created either way.
//  3. Otherwise, probe OpenD for the account list, OUTSIDE any config lock
//     (Admin's methods take their own lock per call; this goroutine holds
//     none of its own). A probe error (transport, quote-only login, decode)
//     is deliberately NOT definitive: no marker is written, so the very next
//     boot tries again naturally. Only a log warning surfaces it -- no
//     toast, since a quote-only login would otherwise nag on every boot.
//  4. Filter the results with moomoo.EligibleLiveUS and branch on the count:
//     exactly one -> seed it; zero or multiple -> mark attempted (this IS
//     definitive -- the current OpenD login's account list answered) and
//     notify so a human can act in Settings -> Venues.
func (s *Seeder) run(ctx context.Context) {
	attempted, venueExists, err := s.admin.MoomooSeedState()
	if err != nil {
		slog.Warn("venueseed: read seed state", "err", err)
		return
	}
	if attempted {
		return // removal (or an earlier definitive attempt) sticks: never re-seed.
	}
	if venueExists {
		if err := s.admin.MarkMoomooSeedAttempted(); err != nil {
			slog.Warn("venueseed: mark attempted for pre-existing moomoo venue", "err", err)
			return
		}
		slog.Info("venueseed: moomoo venue already configured; marked attempted so its removal will stick")
		return
	}

	dctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()
	accs, err := s.discover(dctx)
	if err != nil {
		// Not definitive: no marker, no write. Retry happens naturally on the
		// NEXT process boot; this process's CAS in OnFeedUp stays consumed, so
		// an OpenD reconnect later in THIS session will not retry it.
		slog.Warn("venueseed: discover accounts failed; will retry next boot", "err", err)
		return
	}

	var eligible []*trdcommon.TrdAcc
	for _, a := range accs {
		if moomoo.EligibleLiveUS(a) {
			eligible = append(eligible, a)
		}
	}

	switch len(eligible) {
	case 1:
		s.seedOne(eligible[0])
	case 0:
		s.declineSeed("moomoo: no live US-authorized account on this OpenD login.", "info")
	default:
		s.declineSeed(fmt.Sprintf("moomoo: %d live accounts found — pick one in Settings → Venues.", len(eligible)), "warn")
	}
}

// seedOne handles the exactly-one-eligible-account branch: create the venue
// via Admin.SeedMoomooVenue, which atomically writes the venue config entry
// and the marker together. created=false means SeedMoomooVenue itself found
// nothing left to do (a manual save raced this probe, or the marker was
// already set) -- not an error, just nothing further to report. A validation
// error (e.g. a non-moomoo venue already holds id "moomoo") is logged and
// left retryable: SeedMoomooVenue writes neither the venue nor the marker on
// that path, by design (see venueadmin.SeedMoomooVenue's doc comment).
func (s *Seeder) seedOne(a *trdcommon.TrdAcc) {
	accID := a.GetAccID()
	created, err := s.admin.SeedMoomooVenue(accID)
	switch {
	case err != nil:
		slog.Error("venueseed: seed moomoo venue", "accID", accID, "err", err)
	case created:
		s.notify("venue.seeded",
			fmt.Sprintf("moomoo venue configured from OpenD (account %d) — restart to activate.", accID),
			"warn")
	default:
		slog.Info("venueseed: moomoo venue not created (raced a manual save or already marked)", "accID", accID)
	}
}

// declineSeed handles the zero- and multiple-eligible-account branches: both
// are DEFINITIVE answers from the current OpenD login (unlike a discover
// error), so both mark attempted before notifying.
func (s *Seeder) declineSeed(detail, level string) {
	if err := s.admin.MarkMoomooSeedAttempted(); err != nil {
		slog.Warn("venueseed: mark attempted after seed decline", "err", err)
		return
	}
	s.notify("venue.seed_declined", detail, level)
}

// notify is Config.Notify's nil-safe call site -- the only place this package
// invokes it.
func (s *Seeder) notify(kind, detail, level string) {
	if s.notifyFn == nil {
		return
	}
	s.notifyFn(kind, detail, level)
}
