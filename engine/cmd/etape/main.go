// Command etape is the eTape engine: the full boot sequence wiring the market-
// data plane (OpenD -> feed -> md.Core), the execution subsystem (exec.Core +
// broker venues), and the uihub WebSocket server the UI connects to. With
// --replay it reconstructs a recorded day against SimBroker over the identical
// hub/contract (the mode the UI Playwright E2E boots on).
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/backfill"
	"github.com/earlisreal/eTape/engine/internal/buildinfo"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/health"
	histalpaca "github.com/earlisreal/eTape/engine/internal/hist/alpaca"
	histyahoo "github.com/earlisreal/eTape/engine/internal/hist/yahoo"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/news"
	"github.com/earlisreal/eTape/engine/internal/openbrowser"
	"github.com/earlisreal/eTape/engine/internal/quota"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/singleinstance"
	"github.com/earlisreal/eTape/engine/internal/stockinfo"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/synth"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/venueadmin"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
	"google.golang.org/protobuf/proto"
)

// openLogFile opens path for appending, creating both the file and its
// parent directory if missing. Logging is set up before config load (and
// thus before the store's own db-dir MkdirAll further down in boot), so the
// default log path's ~/.eTape directory may not exist yet.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// boot runs the full engine boot sequence -- flags, config, store/md-core/
// exec-core/uihub construction, feed startup (live OpenD or replay), and the
// ordered shutdown once ctx is cancelled -- and returns the process exit
// code. It is a plain top-level function (not a closure or method) taking
// only a context, so a later entrypoint (e.g. a system-tray build) can call
// it directly with a signal-derived context of its own; main itself stays a
// thin wrapper so os.Exit (which must run from main, never from inside a
// deferred call) sees boot's return value.
//
// onListening, if non-nil, is called with the uihub listening address (e.g.
// "127.0.0.1:8686") right after the server starts accepting connections. The
// default (!tray) entrypoint has no use for it and passes nil; the tray
// entrypoint uses it to learn the address for its "Open eTape" menu action
// without duplicating any config-resolution logic.
func boot(ctx context.Context, onListening func(addr string)) (code int, restart bool) {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	replayDay := flag.String("replay", "", "replay a recorded day (YYYY-MM-DD) instead of live OpenD")
	speed := flag.Float64("speed", 0, "replay speed (>0: real-time x speed; <=0: as fast as possible)")
	dist := flag.String("dist", "", "serve built UI from this dir (overrides [uihub].dist_dir)")
	replayHold := flag.Bool("replay-hold", false, "in replay, keep serving after the journal is exhausted (E2E)")
	demo := flag.Bool("demo", false, "run the built-in synthetic demo market (no OpenD/broker needed)")
	demoSeed := flag.Int64("demo-seed", 0, "PRNG seed for -demo; 0 = random per launch")
	noOpen := flag.Bool("no-open", false, "do not auto-open the default browser to the UI")
	logPath := flag.String("log", "", "also write logs to this file")
	flag.Parse()

	// Destination policy: logToStderr and defaultLogPath are supplied by
	// logdest_tray.go / logdest_default.go (chosen by the "tray" build tag).
	// The tray (windowsgui) build has no usable stderr, so it falls back to
	// a file under ~/.eTape when -log isn't given; the console build has a
	// real stderr and stays opt-in, exactly as before this split existed.
	logDest := *logPath
	explicitLog := logDest != ""
	if logDest == "" {
		logDest = defaultLogPath()
	}

	var writers []io.Writer
	if logToStderr {
		writers = append(writers, os.Stderr)
	}
	var logFile *os.File
	if logDest != "" {
		f, err := openLogFile(logDest)
		if err != nil {
			errLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
			if explicitLog {
				// The user asked for this exact file; fail loudly.
				errLog.Error("open log file", "path", logDest, "err", err)
				return 1, false
			}
			// The default path is best-effort: a logging hiccup must not
			// stop the engine from booting.
			errLog.Warn("open default log file, continuing without it", "path", logDest, "err", err)
		} else {
			logFile = f
			writers = append(writers, f)
		}
	}
	if logFile != nil {
		defer logFile.Close()
	}

	var out io.Writer
	switch len(writers) {
	case 0:
		out = io.Discard
	case 1:
		out = writers[0]
	default:
		out = io.MultiWriter(writers...)
	}

	log := slog.New(slog.NewTextHandler(out, nil))
	slog.SetDefault(log)
	log.Info("etape starting", "version", buildinfo.Version)

	if *demo && *replayDay != "" {
		log.Error("parse flags", "err", errors.New("-demo and -replay are mutually exclusive"))
		return 1, false
	}

	var cfg config.Config
	if *demo {
		cfg = config.Default()
		cfg.Venues = append(cfg.Venues, config.Venue{ID: "sim-paper", Broker: "sim", Env: "paper"})
		cfg.Gate.Global = config.GateGlobal{
			MaxDayLoss: 100000, MaxSymbolPositionValue: 100000, MaxSymbolPositionShares: 100000,
		}
		cfg.Gate.Venue = map[string]config.GateVenue{
			"sim-paper": {MaxOrderValue: 100000, MaxPositionValue: 100000, MaxPositionShares: 100000, MaxOpenOrders: 50},
		}
		demoDir, err := os.MkdirTemp("", "etape-demo-*")
		if err != nil {
			log.Error("create demo temp dir", "err", err)
			return 1, false
		}
		cfg.Store.DBPath = filepath.Join(demoDir, "demo.db")
	} else {
		var err error
		cfg, err = config.Load(*cfgPath)
		if err != nil {
			log.Error("load config", "err", err)
			return 1, false
		}
	}
	if *dist != "" {
		cfg.UIHub.DistDir = *dist
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		return 1, false
	}
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(home, ".eTape", "etape.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Error("make db dir", "err", err)
		return 1, false
	}

	// --- single-instance guard ---
	// Keyed on dbPath so a second launch pointed at the same store is
	// blocked before it touches the shared DB (per-process journal seq
	// counters -> duplicate-PK inserts), the shared moomoo OpenD
	// subscription/history quota (each engine assumes it owns the whole
	// pool), or the uihub port. -demo gets a unique temp dbPath (above), so
	// it always acquires its own lock and never collides with a live
	// instance. The lock is OS-held: it releases automatically even on a
	// crash, so there is no stale-lock cleanup to do.
	releaseLock, err := singleinstance.Acquire(dbPath + ".lock")
	if errors.Is(err, singleinstance.ErrAlreadyRunning) {
		log.Info("eTape is already running; opening it instead", "addr", cfg.UIHub.Addr())
		if !*noOpen {
			// Best-effort: reaches the already-running instance's UI. If it
			// fails (no browser handler, etc.) there's nothing more useful
			// to do than exit -- the other instance is already up.
			_ = openbrowser.Open("http://" + cfg.UIHub.Addr())
		}
		return 0, false
	}
	if err != nil {
		log.Error("single-instance lock", "err", err)
		return 1, false
	}
	defer releaseLock()
	log.Info("single-instance lock acquired", "lock", dbPath+".lock")

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	// restartRequested/requestRestart back the "RestartEngine" WS command
	// (uihub/commands.go): a client triggers requestRestart, which flags the
	// restart and cancels ctx via stop -- reusing the exact ordered shutdown
	// drain below. boot's named `restart` return value picks up the flag
	// after the drain completes, so the caller (run_default.go/run_tray.go)
	// only relaunches once every deferred cleanup (releaseLock, st.Close,
	// etc.) has actually run.
	var restartRequested atomic.Bool
	requestRestart := func() { restartRequested.Store(true); stop() }

	live := *replayDay == "" && !*demo
	uihubClk := clock.System{}
	var execClk clock.Clock = clock.System{}

	// --- store ---
	log.Info("store opening", "db", dbPath)
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		return 1, false
	}
	// NOTE: st.Close() is deferred until AFTER every store-writer goroutine has
	// stopped (feed pipe + forwardMD + exec.Core) — see the shutdown block below.

	// --- md core ---
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})
	go func() { _ = core.Run(ctx) }()

	// --- replay clock (execClk) if replaying ---
	var replayRows []store.JournalRow
	if *replayDay != "" {
		replayRows, err = st.ReadJournalDay(*replayDay)
		if err != nil || len(replayRows) == 0 {
			log.Error("replay day unavailable", "day", *replayDay, "err", err, "rows", len(replayRows))
			_ = st.Close()
			return 1, false
		}
		execClk = replay.NewClock(time.UnixMilli(replayRows[0].TsExch))
	}

	// --- exec subsystem (Recover -> Run) ---
	var credsFile creds.File
	if live {
		if credsFile, err = creds.Load(creds.DefaultPath()); err != nil {
			log.Warn("load creds (non-sim venues will fail)", "err", err)
			credsFile = creds.File{}
		}
	}
	vbs, err := buildBrokers(cfg, credsFile, execClk, !live)
	if err != nil {
		log.Error("build brokers", "err", err)
		_ = st.Close()
		return 1, false
	}
	brokers := map[exec.VenueID]exec.Broker{}
	venueIDs := make([]exec.VenueID, 0, len(vbs))
	var brokerWG sync.WaitGroup
	for _, vb := range vbs {
		brokers[vb.ID] = vb.Broker
		venueIDs = append(venueIDs, vb.ID)
		if vb.Run != nil {
			brokerWG.Add(1)
			go func(run func(context.Context)) { defer brokerWG.Done(); run(ctx) }(vb.Run)
		}
	}
	execCore := exec.NewCore(exec.CoreConfig{
		Venues: venueIDs, Gate: buildGateConfig(cfg.Gate), Store: st,
		Brokers: brokers, Clock: execClk, IDGen: exec.NewOrderIDGen(execClk, rand.Reader),
		SysLog:          st.AppendSysEvent,
		StartingBalance: startingBalances(cfg),
	})
	if err := execCore.Recover(ctx); err != nil {
		log.Warn("exec recover (continuing; reactive reconcile will catch up)", "err", err)
	}
	execDone := make(chan struct{})
	go func() { defer close(execDone); _ = execCore.Run(ctx) }()

	// --- uihub (listening BEFORE OpenD is dialed) ---
	venueAdm := venueadmin.New(*cfgPath, creds.DefaultPath(), config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate})
	venueProbe := venueprobe.New(creds.DefaultPath(), uihubClk)
	hub, srv := uihub.New(uihubClk, uihub.Config{
		Venues: venueMetas(cfg), Global: uihub.GlobalLimits{
			MaxDayLoss: cfg.Gate.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Gate.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: cfg.Gate.Global.MaxSymbolPositionShares,
		},
		MD: hz(cfg.UIHub.MDRateHz), Account: hz(cfg.UIHub.AccountRateHz),
		Position: time.Duration(cfg.UIHub.PositionMs) * time.Millisecond,
		Buf:      4096, TapeCap: cfg.UIHub.TapeSnapshot, NewsCap: 500, FillsCap: 1000, EventsCap: 500, TradesCap: 1000,
		OutBuf: cfg.UIHub.OutboundQueue, DistDir: cfg.UIHub.DistDir,
	}, execCore, st, core, venueAdm, venueProbe, requestRestart)
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); _ = hub.Run(ctx) }()
	httpSrv := &http.Server{
		Addr: cfg.UIHub.Addr(), Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second,
		// BaseContext ties every accepted connection's r.Context() to the
		// top-level shutdown ctx, independently of Hub's own lifecycle. Without
		// this, a connection accepted (and its conn.run(r.Context()) started)
		// after Hub.Run has already returned would never be told to close: its
		// hub.Register call silently no-ops against the already-closed Hub (see
		// Hub.Register's <-h.closed race), so it never lands in h.clients, and
		// Hub.Run's <-ctx.Done() teardown loop (which calls c.close() on every
		// registered client) can never reach it either. That connection's
		// readLoop would then block forever in ws.Read(r.Context()) waiting on a
		// client that may never send or disconnect, so srv.Wait() (which has no
		// timeout) would hang the whole shutdown sequence. Deriving r.Context()
		// from ctx here unblocks that Read as soon as the top-level ctx is
		// cancelled, regardless of Hub's state.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("uihub listen", "err", err)
		}
	}()
	log.Info("uihub up", "addr", cfg.UIHub.Addr(), "dist", cfg.UIHub.DistDir)
	if onListening != nil {
		onListening(cfg.UIHub.Addr())
	}
	if !*noOpen {
		go func() {
			if err := openbrowser.Open("http://" + cfg.UIHub.Addr()); err != nil {
				log.Warn("open browser", "err", err)
			}
		}()
	}

	// --- fan-in: md/exec Updates -> hub; mark bridge md -> exec ---
	var forwardWG sync.WaitGroup
	forwardWG.Add(1)
	go func() { defer forwardWG.Done(); forwardMD(ctx, core, hub, live || *demo, st) }()
	go forwardExec(ctx, execCore, hub)

	// Forward marks + books into every sim broker so submitted orders fill: in
	// replay every venue is forced to SimBroker, and in live mode a venue
	// explicitly configured with Broker: "sim" (a practice venue) is one too.
	// Non-sim live venues (tradezero/alpaca/moomoo) are fed by their own
	// broker connection and don't implement simSink, so the type-assertion in
	// simSinksOf alone selects the right set in either mode.
	go markBridge(ctx, core, execCore, simSinksOf(vbs))

	// --- feed (live OpenD, synthetic demo, or replay) ---
	var pipeWG sync.WaitGroup
	var backfillWG sync.WaitGroup
	var orch *backfill.Orchestrator
	var scanWG sync.WaitGroup
	var dropWG sync.WaitGroup
	if live || *demo {
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err == nil && n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.AppendSysEvent("boot", "engine up")
		dropWG.Add(1)
		go watchDroppedUpdates(ctx, &dropWG, core, st)

		// feedForHub/pollReq/mmProbe/demand/tail are the mode-agnostic seams
		// startPollers and the backfill orchestrator are built from below:
		// the demo branch fills them from a *synth.Feed/*synth.Requester
		// (no network, no quota), the live branch from the real OpenD
		// client/feed exactly as before.
		var feedForHub uihub.Feed
		var pollReq pollerRequester
		var mmProbe rttProber
		var demand demandFeeder
		var tail backfill.TailFetcher
		var dailyChain, intradayChain []backfill.Source

		if *demo {
			// demoSeedValue draws a fresh random seed via crypto/rand each call
			// when *demoSeed==0 (the documented "random per launch" default) --
			// calling it twice would build the generator with one seed and log a
			// different one, silently breaking the spec's reproducibility
			// contract ("the same -demo-seed reproduces the identical universe
			// and day") on the most common (default random) path. Call once.
			seed := demoSeedValue(*demoSeed)
			gen := synth.New(seed, clock.System{})
			gen.Seed(st, clock.System{}.Now().UnixMilli()) // flushes internally
			sf := synth.NewFeed(gen, st, clock.System{})
			req := synth.NewRequester(gen)
			go func() { _ = sf.Run(ctx) }()
			pipeWG.Add(1)
			go pipe(ctx, &pipeWG, sf.Events(), core, st) // journaling ON into demo.db
			feedForHub, pollReq, mmProbe = sf, req, req
			log.Info("engine up (demo synth feed)", "seed", seed, "symbols", gen.Symbols())
		} else {
			client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
			fd := opend.NewOpenDFeed(client, opend.FeedOptions{
				Budget: cfg.Feed.QuotaSlots, Hysteresis: time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
				DisableExtendedTime: !cfg.Feed.ExtendedTime,
			})
			go func() { _ = client.Run(ctx) }()
			go func() { _ = fd.Run(ctx) }()
			pipeWG.Add(1)
			go pipe(ctx, &pipeWG, fd.Events(), core, st)
			feedForHub, pollReq, mmProbe, demand = fd, client, moomooProbe{c: client}, fd

			if cfg.Backfill.Enabled {
				var alpacaSrc *histalpaca.Client
				if cfg.Backfill.Alpaca.Enabled {
					if p, label, err := resolveBackfillAlpacaCreds(cfg, credsFile); err == nil {
						alpacaSrc = histalpaca.New("", p.KeyID, p.SecretKey, cfg.Backfill.Alpaca.Feed, clock.System{})
						log.Info("backfill: alpaca provider resolved", "from", label, "feed", cfg.Backfill.Alpaca.Feed)
					} else if errors.Is(err, errAlpacaLiveCreds) {
						log.Warn("backfill: refusing alpaca-live creds for read-only historical provider", "key", cfg.Backfill.Alpaca.CredsKey)
					} else {
						log.Warn("backfill: alpaca provider disabled (no creds)", "key", cfg.Backfill.Alpaca.CredsKey, "err", err)
					}
				}
				moomoo := backfill.MoomooFetcher(fd)
				if alpacaSrc != nil {
					dailyChain = append(dailyChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
					intradayChain = append(intradayChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
				}
				if cfg.Backfill.Yahoo.Enabled {
					dailyChain = append(dailyChain, backfill.Source{Name: "yahoo", HistFetcher: histyahoo.New("", clock.System{})})
				}
				// moomoo request_history_kline is the quota-guarded last resort in both chains.
				dailyChain = append(dailyChain, backfill.Source{Name: "moomoo", HistFetcher: moomoo})
				intradayChain = append(intradayChain, backfill.Source{Name: "moomoo", HistFetcher: moomoo})
				tail = fd // TailFetcher: OpenDFeed.Tail1m (quota-free Qot_GetKL)
			}
		}
		hub.SetFeed(feedForHub) // enables on-demand EnsureSymbol/ReleaseSymbol + FocusGroup probe

		var hubBackfill func(sym string, done func(ok bool))
		if cfg.Backfill.Enabled || *demo {
			// demo: dailyChain/intradayChain/tail are all nil here, so this is
			// a chain-less orchestrator -- walkChain over a nil chain returns
			// cleanly (nil,"",nil) and o.tail nil-checks before use -- it
			// still serves warmStart's archive-first LoadOlder/LoadOlderDaily
			// against the history Seed already wrote, with no special-casing.
			orch = backfill.New(
				dailyChain,
				intradayChain,
				tail,
				core,
				st,
				clock.System{},
				backfill.Config{
					IntradayDays: cfg.Backfill.IntradayDays,
					DailyYears:   cfg.Backfill.DailyYears,
					Concurrency:  cfg.Backfill.Concurrency,
					SeedChunk:    cfg.Backfill.SeedChunk,
				},
			)
			// hubBackfill spawns orch.Backfill and reports the daily-fetch
			// outcome back via done, so the hub knows whether to mark the
			// symbol backfilled or leave it retryable (see
			// Hub.handleBackfillDone / Hub.rearmBackfill). The scan poller
			// (backfillOne below) doesn't need the retry signal -- it has its
			// own independent, pool-day-scoped dedup -- so it reuses this
			// same closure with a nil done rather than spawning its own copy
			// of the Add/goroutine/Done boilerplate.
			hubBackfill = func(sym string, done func(ok bool)) {
				backfillWG.Add(1)
				go func() {
					defer backfillWG.Done()
					err := orch.Backfill(ctx, sym)
					if done != nil {
						done(err == nil)
					}
				}()
			}
		}
		var backfillOne func(string)
		if hubBackfill != nil {
			backfillOne = func(sym string) { hubBackfill(sym, nil) }
		}
		hub.SetBackfill(hubBackfill) // chart-open demands also deep-backfill (nil-safe if disabled)
		startPollers(ctx, cfg, pollReq, demand, hub, uihubClk, st, hasTZVenue(cfg), mmProbe, firstAlpacaProber(vbs), backfillOne, !*demo, &scanWG)
	} else {
		sim := execClk.(*replay.Clock)
		fd := replay.NewFeed(replay.FeedOptions{Rows: replayRows, Sim: sim, Pace: clock.System{}, Speed: *speed})
		go func() { _ = fd.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, fd.Events(), core, nil) // no journal re-recording in replay
		if *replayHold {
			log.Info("replay-hold: serving last state until interrupted")
		} else {
			go func() { pipeWG.Wait(); stop() }() // self-terminate when the journal is exhausted
		}
		log.Info("engine up (replay)", "day", *replayDay, "rows", len(replayRows), "speed", *speed)
	}

	if orch == nil && st != nil {
		// No live backfill chains were built (replay, or cfg.Backfill.Enabled ==
		// false) — a chain-less orchestrator still serves archive-first
		// LoadOlder/LoadOlderDaily and acks exhausted past the archive, per the
		// spec's "no special casing beyond a nil-chain check." walkChain over a
		// nil chain returns (nil,"",nil), so LoadOlder degrades cleanly.
		orch = backfill.New(nil, nil, nil, core, st, clock.System{}, backfill.Config{IntradayDays: cfg.Backfill.IntradayDays})
	}
	loadOlderFn := func(sym string, daily bool, done func(added int, exhausted bool, err error)) {
		if orch == nil { // st itself was nil — should not happen in practice
			done(0, true, nil)
			return
		}
		backfillWG.Add(1)
		go func() {
			defer backfillWG.Done()
			if daily {
				added, exhausted, err := orch.LoadOlderDaily(ctx, sym)
				done(added, exhausted, err)
				return
			}
			added, exhausted, err := orch.LoadOlder(ctx, sym)
			done(added, exhausted, err)
		}()
	}
	hub.SetLoadOlder(loadOlderFn)

	<-ctx.Done()

	// --- ordered shutdown: stop accepting, drain all store writers, then Close ---
	// Every goroutine that can call a store-writing method (RecordEvent,
	// AppendExecEvent, ArchiveBar1m/ArchiveDaily, AppendSysEvent, SetConfig)
	// must be joined before st.Close() runs, since Close() closes the
	// s.writes channel and any send on it afterward panics. Sources: pipe()
	// (RecordEvent, joined via pipeWG), forwardMD() (ArchiveBar1m/
	// ArchiveDaily, joined via forwardWG — it drains already-buffered
	// core.Updates() after ctx is cancelled, so it must be waited on even
	// though md.Core.Run stops producing new updates once pipeWG is
	// drained), backfill's orch.Backfill goroutines (ArchiveBar1m/
	// ArchiveDaily for freshly-fetched history, joined via backfillWG),
	// watchDroppedUpdates (AppendSysEvent, joined via dropWG — depends only
	// on ctx, so it can be waited anywhere after <-ctx.Done()), exec.Core.Run
	// (AppendExecEvent, joined via execDone), and every uihub connection's
	// dispatch loop (SetConfig via commandHandler.handle, joined via
	// srv.Wait()). brokerWG has no store writes but is joined here too since
	// broker goroutines feed exec.Core, not the store.
	//
	// srv.Wait() must run after httpSrv.Shutdown (which only stops accepting
	// new connections and returns once in-flight *plain* HTTP requests finish
	// -- it does NOT wait on hijacked WebSocket connections) and before
	// pipeWG.Wait(): by the time httpSrv.Shutdown returns, ctx has already
	// been cancelled (we're past <-ctx.Done()), so Hub.Run's own <-ctx.Done()
	// branch has told (or is telling) every registered connection to close;
	// srv.Wait() blocks until each connection's conn.run() goroutine has
	// actually returned, confirming its dispatch loop -- and therefore any
	// SetConfig call it could make -- is stopped before st.Close() runs.
	//
	// backfillWG.Add(1) now has three producers: the scan poller (pool
	// admission, joined via scanWG); the Hub goroutine via the hubBackfill
	// closure injected with SetBackfill -- called from both
	// Hub.handleEnsureDemand (chart-open demand) and Hub.rearmBackfill
	// (OpenD-reconnect re-arm, triggered from handleMD on an
	// md.ResyncedUpdate); and the Hub goroutine again via the loadOlderFn
	// closure injected with SetLoadOlder -- called from Hub.handleLoadOlder,
	// itself reachable only via loadOlderCh (the LoadOlderBars command,
	// routed through Hub.LoadOlder from a conn's dispatch goroutine
	// specifically so its Add(1) executes on the Hub goroutine, not the conn
	// goroutine -- see Hub.LoadOlder's doc comment). srv.Wait() only proves
	// every conn's dispatch loop has returned, not that the Hub goroutine has
	// finished servicing the ensureDemandCh/mdCh/loadOlderCh sends already
	// made on their way out -- that Add(1) can still be in flight on the Hub
	// goroutine after srv.Wait() returns. <-hubDone closes that gap: Hub.Run
	// only returns via its own <-ctx.Done() branch, by which point any
	// ensureDemandCh/mdCh/loadOlderCh message it had already received has
	// finished its handler call (and therefore its Add, if any), so no
	// further Add(1) can occur once hubDone closes. Waiting on it here,
	// before scanWG.Wait()/backfillWG.Wait(), keeps all three Add(1)
	// producers quiesced before the counter is read -- otherwise a late Add
	// could land after backfillWG.Wait() already observed zero, spawning an
	// unwaited orch.Backfill/LoadOlder/LoadOlderDaily goroutine that touches
	// the store during/after st.Close().
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(shutCtx)
	cancelShut()
	srv.Wait()        // every conn.run() returned: no more SetConfig via dispatch
	<-hubDone         // hub.Run returned: no more handleEnsureDemand/handleLoadOlder, hence no more backfillWG.Add from chart-open demands or LoadOlderBars
	scanWG.Wait()     // scan poller stopped: no more backfillWG.Add from pool admissions
	backfillWG.Wait() // boot backfill workers stopped: no more Seed* into the core
	pipeWG.Wait()     // feed->core pipe stopped: no more RecordEvent
	forwardWG.Wait()  // forwardMD drained: no more ArchiveBar1m/ArchiveDaily
	dropWG.Wait()     // dropped-updates watcher stopped: no more AppendSysEvent from it
	<-execDone        // exec.Core.Run returned: no more AppendExecEvent
	brokerWG.Wait()
	if err := st.Close(); err != nil {
		log.Error("close store", "err", err)
	}
	log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates(), "droppedJournal", st.DroppedJournalRows())
	return 0, restartRequested.Load()
}

// dropWatchInterval controls how often watchDroppedUpdates samples
// core.DroppedUpdates() for a live sys.events trail: a drop should surface
// during the session it happens in, not only in the shutdown log line.
const dropWatchInterval = 5 * time.Second

// watchDroppedUpdates polls core.DroppedUpdates() and appends a "md-drop"
// sys.events row whenever it increases, so an md.Core updates-channel
// overflow (see Core.emit) is visible on the sys.events topic live instead
// of only in the "shutdown complete" log line. It is a store-writing
// goroutine (AppendSysEvent) and must be joined via wg before st.Close() --
// see the shutdown-ordering comment in main().
func watchDroppedUpdates(ctx context.Context, wg *sync.WaitGroup, core *md.Core, st *store.Store) {
	defer wg.Done()
	t := time.NewTicker(dropWatchInterval)
	defer t.Stop()
	var last uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if cur := core.DroppedUpdates(); cur > last {
				st.AppendSysEvent("md-drop", fmt.Sprintf("dropped %d md update(s) since last check (total %d)", cur-last, cur))
				last = cur
			}
		}
	}
}

func hz(rate float64) time.Duration {
	if rate <= 0 {
		return 33 * time.Millisecond
	}
	return time.Duration(float64(time.Second) / rate)
}

// forwardMD drains md.Core.Updates(): publishes each to the hub and (live only)
// archives finalized 1m/daily bars — merging the old drainUpdates archiving with
// the new hub fan-in.
func forwardMD(ctx context.Context, core *md.Core, hub *uihub.Hub, live bool, archive *store.Store) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-core.Updates():
			hub.PublishMD(u)
			if !live {
				continue
			}
			if bu, ok := u.(md.BarUpdate); ok && !bu.Bar.InProgress {
				b := feed.Bar{Symbol: bu.Bar.Symbol, BucketMs: bu.Bar.BucketMs,
					O: bu.Bar.O, H: bu.Bar.H, L: bu.Bar.L, C: bu.Bar.C, Volume: bu.Bar.V}
				switch bu.Bar.TF {
				case session.TF1m:
					archive.ArchiveBar1m(b)
				case session.TFDay:
					archive.ArchiveDaily(b)
				}
			}
		}
	}
}

func forwardExec(ctx context.Context, execCore *exec.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-execCore.Updates():
			hub.PublishExec(u)
		}
	}
}

// simSink receives last-trade marks and L2 book snapshots. Implemented by
// *sim.Broker (SetMark/SetBook) — every replay venue, plus any live venue
// explicitly configured as Broker: "sim" — so a submitted order fills
// against the fed marks and (from Task 2 onward) prices against the fed
// book. Named simSink rather than markSink now that it carries both.
type simSink interface {
	SetMark(symbol string, price float64)
	SetBook(symbol string, book feed.Book)
}

// simSinksOf returns every configured broker that is a simSink. No live/
// replay branch is needed: buildBrokers forces every venue to sim.Broker in
// replay, and only venues configured with Broker: "sim" are sim.Broker in
// live mode, so the type-assertion alone selects the correct set either way.
func simSinksOf(vbs []venueBroker) []simSink {
	var sinks []simSink
	for _, vb := range vbs {
		if s, ok := vb.Broker.(simSink); ok {
			sinks = append(sinks, s)
		}
	}
	return sinks
}

// markBridge copies md.Core.Marks() -> exec.Core.FeedMark (the single md<->exec
// seam) and -> every sim broker's SetMark (sinks) so a submitted order fills
// against the fed marks; it also copies md.Core.Books() -> every sim broker's
// SetBook so those brokers track the latest L2 snapshot per symbol (stored
// only as of Task 1 — Task 2 makes fills price off it). Non-sim live venues
// get marks/books from their own broker feed instead and are excluded from
// sinks by simSinksOf.
func markBridge(ctx context.Context, core *md.Core, execCore *exec.Core, sinks []simSink) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-core.Marks():
			execCore.FeedMark(exec.Mark{Symbol: m.Symbol, Price: m.Price, TsMs: m.TsMs})
			for _, s := range sinks {
				s.SetMark(m.Symbol, m.Price)
			}
		case bk := <-core.Books():
			for _, s := range sinks {
				s.SetBook(bk.Symbol, bk)
			}
		}
	}
}

// demoSeedValue returns the seed to use: the flag if non-zero, else a random
// per-launch seed. Kept off the hot path; determinism in tests comes from
// passing a fixed -demo-seed.
func demoSeedValue(flagSeed int64) int64 {
	if flagSeed != 0 {
		return flagSeed
	}
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand, imported unaliased as `rand` above
	return int64(binary.LittleEndian.Uint64(b[:]))
}

// pollerRequester is the request/response seam scan/news/stockinfo/quota's
// own local `requester` interfaces already require (identical method set on
// all four): satisfied by *opend.Client in live/replay and *synth.Requester
// in -demo, so startPollers doesn't need to know which one it was handed.
type pollerRequester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// demandFeeder is the subscription-control surface the scan pool drives:
// satisfied by *opend.OpenDFeed in live/replay. In -demo it is left nil
// (*synth.Feed's Ensure/Release are no-ops -- the synthetic universe
// simulates every symbol unconditionally), which cleanly disables the pool
// via scan.go's own `if p.feed == nil { return }` guard -- the same
// mechanism tests/replay already rely on.
type demandFeeder interface {
	Ensure(d feed.Demand)
	Release(id string)
}

// startQuota gates the quota poller: false in -demo, since the synthetic
// requester answers Qot_GetSubInfo with the generic "no data" response
// rather than a real subscription budget, so tracking it would be noise.
func startPollers(ctx context.Context, cfg config.Config, r pollerRequester, demand demandFeeder, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, mmProbe rttProber, alpacaProbe rttProber, backfillOne func(string), startQuota bool, scanWG *sync.WaitGroup) {
	scanPoller := scan.New(cfg.Scan, r, hub, clk, demand, backfillOne)
	symbols := func() []string {
		return newsSymbols(scanPoller.PoolSymbols(), hub.ActiveDemandSymbols())
	}
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, r, hub, clk, symbols).Run(ctx) }()
	go func() { _ = stockinfo.New(cfg.StockInfo, r, hub, clk, symbols, st).Run(ctx) }()
	// health: mmProbe is the moomoo probe (real OpenD RTT in live/replay, a
	// constant synthetic RTT in -demo); app-ping RTT source is nil in v1
	// (ui-engine shows down until ping tracking is wired). alpacaProbe is the
	// first configured Alpaca adapter (nil if none -- which is always the
	// case in -demo/replay, since buildBrokers forces every venue to sim
	// there and sim.Broker doesn't implement rttProber), giving the
	// engine-alpaca link the same reachability-RTT treatment as moomoo. The
	// health poller's sys.events are also persisted by main via a store hook
	// if desired.
	var qsrc health.QuotaSource
	if startQuota {
		quotaPoller := quota.New(quota.Config{
			SubWarnHeadroom: cfg.Feed.QuotaWarnHeadroom,
			HistWarnRemain:  cfg.Feed.HistQuotaWarnRemain,
		}, r, hub, clk)
		go func() { _ = quotaPoller.Run(ctx) }()
		qsrc = quotaPoller
	}
	go func() {
		_ = health.New(cfg.Health, hub, clk, mmProbe, nil, hasTZ, alpacaProbe, qsrc).Run(ctx)
	}()
}

func hasTZVenue(cfg config.Config) bool {
	for _, v := range cfg.Venues {
		if v.Broker == "tradezero" {
			return true
		}
	}
	return false
}

// pipe forwards feed events into the core, journaling each first when journal != nil.
func pipe(ctx context.Context, wg *sync.WaitGroup, in <-chan feed.Event, core *md.Core, journal *store.Store) {
	defer wg.Done()
	sys := clock.System{}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			if journal != nil {
				journal.RecordEvent(ev, sys.Now().UnixMilli())
			}
			core.Feed(ev)
		}
	}
}
