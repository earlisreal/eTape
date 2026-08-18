// Command etape is the eTape engine: the full boot sequence wiring the market-
// data plane (OpenD -> feed -> md.Core), the execution subsystem (exec.Core +
// broker venues), and the uihub WebSocket server the UI connects to.
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/backfill"
	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
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
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/singleinstance"
	"github.com/earlisreal/eTape/engine/internal/ssr"
	"github.com/earlisreal/eTape/engine/internal/stockinfo"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/synth"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueadmin"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
	"github.com/earlisreal/eTape/engine/internal/venueseed"
	"github.com/earlisreal/eTape/engine/internal/watchlist"
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
func boot(ctx context.Context, onListening func(addr string)) (code int, restart bool, nextArgs []string) {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	dist := flag.String("dist", "", "serve built UI from this dir (overrides [uihub].dist_dir)")
	demo := flag.Bool("demo", false, "run the built-in synthetic demo market (no OpenD/broker needed)")
	demoSeed := flag.Int64("demo-seed", 0, "PRNG seed for -demo; 0 = random per launch")
	noOpen := flag.Bool("no-open", false, "do not auto-open the default browser to the UI")
	logPath := flag.String("log", "", "also write logs to this file")
	logLevel := flag.String("log-level", os.Getenv("SLOG_LEVEL"), "log level: debug, info, warn, error (default SLOG_LEVEL env)")
	flag.Parse()

	// ETAPE_NO_OPEN suppresses auto-open, same as -no-open, so agent/CI boots
	// stay headless without every launch path remembering the flag.
	if v := os.Getenv("ETAPE_NO_OPEN"); v != "" && v != "0" && v != "false" {
		*noOpen = true
	}

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
				return 1, false, nil
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

	var handlerLevel slog.Level
	if *logLevel == "" {
		handlerLevel = slog.LevelInfo
	} else if err := handlerLevel.UnmarshalText([]byte(*logLevel)); err != nil {
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		log.Error("bad -log-level", "level", *logLevel, "err", err)
		return 1, false, nil
	}
	handlerOpts := &slog.HandlerOptions{Level: handlerLevel}
	log := slog.New(slog.NewTextHandler(out, handlerOpts))
	slog.SetDefault(log)
	log.Info("etape starting", "version", buildinfo.Version)

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
			return 1, false, nil
		}
		cfg.Store.DBPath = filepath.Join(demoDir, "demo.db")
	} else {
		// First run of a live boot with no config.toml: seed one so a fresh
		// install comes up with a ready-to-use paper sim practice venue
		// instead of zero configured venues. Gated to live only
		// (*replayDay == "") -- -demo (above) has its own injected sim venue
		// and its own temp config, and an explicit -replay forces every venue
		// to sim regardless, so neither needs (or should trigger) a write to
		// the real ~/.eTape/config.toml.
		if true {
			if seeded, serr := config.SeedDefaultIfMissing(*cfgPath); serr != nil {
				log.Warn("seed first-run config (continuing with empty venues)", "path", *cfgPath, "err", serr)
			} else if seeded {
				log.Info("first run: seeded config with a paper sim practice venue", "path", *cfgPath)
			}
		}
		var err error
		cfg, err = config.Load(*cfgPath)
		if err != nil {
			log.Error("load config", "err", err)
			return 1, false, nil
		}
	}
	if *dist != "" {
		cfg.UIHub.DistDir = *dist
	}
	// ETAPE_UIHUB_PORT isolates an automated boot onto its own port so it
	// never collides with a user's instance on the default port.
	if v := os.Getenv("ETAPE_UIHUB_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.UIHub.Port = p
		}
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		return 1, false, nil
	}
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(home, ".eTape", "etape.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Error("make db dir", "err", err)
		return 1, false, nil
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
			_ = openbrowser.Open(browserURL(cfg.UIHub.Addr(), handlerLevel == slog.LevelDebug))
		}
		return 0, false, nil
	}
	if err != nil {
		log.Error("single-instance lock", "err", err)
		return 1, false, nil
	}
	defer func() { _ = releaseLock() }()
	log.Debug("single-instance lock acquired", "lock", dbPath+".lock")

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	// restartRequested/requestRestart back every self-relaunch path -- the
	// plain "RestartEngine" WS command via restartInPlace below, and the
	// mode-switch closures (startReplay/goLive/startDemo) directly: calling
	// requestRestart flags the restart and cancels ctx via stop -- reusing the
	// exact ordered shutdown drain below. boot's named `restart` return value
	// picks up the flag after the drain completes, so the caller
	// (run_default.go/run_tray.go) only relaunches once every deferred
	// cleanup (releaseLock, st.Close, etc.) has actually run.
	var restartRequested atomic.Bool
	requestRestart := func() { restartRequested.Store(true); stop() }

	// nextArgs carries a relaunch's flag list from the restartInPlace/startDemo closures (built below, passed into
	// uihub.New) to boot's final return. atomic.Pointer because it's written
	// from the command-dispatch goroutine (via time.AfterFunc, same as
	// requestRestart) and read here after <-ctx.Done() on the boot goroutine.
	// Every closure now sets this before restarting (nil is only the
	// zero-value/no-restart-requested case) -- see relaunch_unix.go /
	// relaunch_windows.go for how a non-nil argv is applied.
	var nextArgsPtr atomic.Pointer[[]string]

	live := !*demo
	uihubClk := clock.System{}
	var execClk clock.Clock = clock.System{}

	// --- store ---
	log.Debug("store opening", "db", dbPath)
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		return 1, false, nil
	}
	if cfg.Store.RetentionDays > 0 {
		cutoff := bars10sRetentionCutoff(time.Now(), cfg.Store.RetentionDays)
		rows, pruneErr := st.PruneBars10sBefore(cutoff)
		if pruneErr != nil {
			log.Warn("prune 10s bars", "err", pruneErr)
		} else {
			log.Info("pruned 10s bars", "rows", rows, "retentionDays", cfg.Store.RetentionDays)
			if rows > 0 {
				vacuumed, vacuumErr := st.VacuumIfNeeded()
				if vacuumErr != nil {
					log.Warn("vacuum after 10s bar prune", "err", vacuumErr)
				} else if vacuumed {
					log.Info("vacuumed store after 10s bar prune")
				}
			}
		}
	}
	// NOTE: st.Close() is deferred until AFTER every store-writer goroutine has
	// stopped (feed pipe + forwardMD + exec.Core) — see the shutdown block below.

	// relaunchAckFlushDelay mirrors uihub's own restartAckFlushDelay (package-
	// private, so not importable from here): give the "accepted" ack time to
	// reach the client before ctx cancellation starts tearing down the connection.
	const relaunchAckFlushDelay = 200 * time.Millisecond

	// base carries the launch flags a mode-switch relaunch must preserve
	// (see childArgs, Task 1) -- built once here so both closures share it.
	base := baseFlags{ConfigPath: *cfgPath, DistDir: *dist, LogPath: *logPath}

	// startDemo relaunches into -demo.
	startDemo := func() error {
		argv := childArgs(base, replayMode{Demo: true})
		time.AfterFunc(relaunchAckFlushDelay, func() {
			nextArgsPtr.Store(&argv)
			requestRestart()
		})
		return nil
	}
	// restartInPlace backs the plain "RestartEngine" WS command. Unlike the
	// mode-switch closures above it reuses the current flags verbatim (so the
	// restart reboots into the exact same mode, preserving flags childArgs
	// would drop such as -demo-seed), but prepends -no-open: the user clicked
	// "Restart now" from an already-open tab, so the relaunch must not pop a
	// second one (same reasoning as childArgs' own -no-open). No
	// time.AfterFunc here -- uihub/commands.go already schedules cd.restart
	// via restartAckFlushDelay before invoking it.
	restartInPlace := func() {
		argv := append([]string{"-no-open"}, os.Args[1:]...)
		nextArgsPtr.Store(&argv)
		requestRestart()
	}

	// --- md core ---
	archiveFinalizedBar := func(b md.Bar) {
		stored := feed.Bar{Symbol: b.Symbol, BucketMs: b.BucketMs,
			O: b.O, H: b.H, L: b.L, C: b.C, Volume: b.V}
		switch b.TF {
		case session.TF10s:
			st.ArchiveBar10s(stored)
		case session.TF1m:
			st.ArchiveBar1m(stored)
		case session.TFDay:
			st.ArchiveDaily(stored)
		}
	}
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs, FinalizedBar: archiveFinalizedBar})
	coreDone := make(chan struct{})
	go func() {
		defer close(coreDone)
		_ = core.Run(ctx)
	}()

	// --- exec subsystem (Recover -> Run) ---
	var credsFile creds.File
	if live {
		if credsFile, err = creds.Load(creds.DefaultPath()); err != nil {
			log.Warn("load creds (non-sim venues will fail)", "err", err)
			credsFile = creds.File{}
		}
	}
	vbs, err := buildBrokers(cfg, credsFile, execClk)
	if err != nil {
		log.Error("build brokers", "err", err)
		_ = st.Close()
		return 1, false, nil
	}
	activeConfig, activeConfigOK, activeConfigErr := st.GetConfig("orderConfig")
	if activeConfigErr != nil {
		log.Warn("read active venue config (using fallback)", "err", activeConfigErr)
	}
	if !activeConfigOK {
		activeConfig = ""
	}
	activeVenue := resolveActiveVenue(activeConfig, vbs)
	locateProviders := locateRegistry(vbs)
	brokers := map[exec.VenueID]exec.Broker{}
	venueIDs := make([]exec.VenueID, 0, len(vbs))
	gateConfig := buildGateConfig(cfg.Gate)
	gateConfig.DayLossPolicies = dayLossPolicies(vbs)
	alpacaAdapterMap := make(map[exec.VenueID]*alpaca.Adapter)
	var brokerWG sync.WaitGroup
	for _, vb := range vbs {
		brokers[vb.ID] = vb.Broker
		venueIDs = append(venueIDs, vb.ID)
		if a, ok := vb.Broker.(*alpaca.Adapter); ok {
			alpacaAdapterMap[vb.ID] = a
		}
		if vb.Run != nil {
			brokerWG.Add(1)
			go func(run func(context.Context)) { defer brokerWG.Done(); run(ctx) }(vb.Run)
		}
	}
	execCore := exec.NewCore(exec.CoreConfig{
		Venues: venueIDs, Gate: gateConfig, Store: st, ActiveVenue: activeVenue,
		Brokers: brokers, Clock: execClk, IDGen: exec.NewOrderIDGen(execClk, rand.Reader),
		SysLog:          st.AppendSysEvent,
		StartingBalance: startingBalances(cfg),
	})
	if err := execCore.Recover(ctx); err != nil {
		log.Warn("exec recover (continuing; reactive reconcile will catch up)", "err", err)
	}
	execDone := make(chan struct{})
	go func() { defer close(execDone); _ = execCore.Run(ctx) }()
	accountPoller := alpaca.NewAccountPoller(alpacaAdapterMap, activeVenue, execClk)
	go func() { _ = accountPoller.Run(ctx) }()

	// Asset metadata is supplemental. Start one-shot loads for every Alpaca
	// account after execution recovery so locate eligibility is venue-specific;
	// wait for them immediately before Stock Info starts below.
	type activeAssetsResult struct {
		venue exec.VenueID
		count int
		err   error
	}
	var activeAssetsDone <-chan []activeAssetsResult
	alpacaAdapters := make([]struct {
		venue   exec.VenueID
		adapter *alpaca.Adapter
	}, 0)
	for _, vb := range vbs {
		if a, ok := vb.Broker.(*alpaca.Adapter); ok {
			alpacaAdapters = append(alpacaAdapters, struct {
				venue   exec.VenueID
				adapter *alpaca.Adapter
			}{venue: vb.ID, adapter: a})
		}
	}
	if len(alpacaAdapters) > 0 {
		results := make(chan []activeAssetsResult, 1)
		activeAssetsDone = results
		go func() {
			out := make([]activeAssetsResult, 0, len(alpacaAdapters))
			for _, item := range alpacaAdapters {
				count, err := item.adapter.LoadActiveAssets(ctx)
				out = append(out, activeAssetsResult{venue: item.venue, count: count, err: err})
			}
			results <- out
		}()
	}

	// --- uihub (listening BEFORE OpenD is dialed) ---
	venueAdm := venueadmin.New(*cfgPath, creds.DefaultPath(), config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate})
	venueProbe := venueprobe.New(creds.DefaultPath(), cfg.OpenD.Addr(), uihubClk)
	hub, srv := uihub.New(uihubClk, uihub.Config{
		Venues: venueMetas(cfg), Global: uihub.GlobalLimits{
			MaxDayLoss: cfg.Gate.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Gate.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: cfg.Gate.Global.MaxSymbolPositionShares,
		},
		MD: hz(cfg.UIHub.MDRateHz), Account: hz(cfg.UIHub.AccountRateHz),
		Position: time.Duration(cfg.UIHub.PositionMs) * time.Millisecond,
		Buf:      4096, TapeCap: cfg.UIHub.TapeSnapshot, NewsCap: 500, FillsCap: 1000, EventsCap: 500, TradesCap: 1000,
		OutBuf: cfg.UIHub.OutboundQueue, DistDir: cfg.UIHub.DistDir,
		Demo: *demo,
		OnConfigSet: func(key, value string) {
			if key != "orderConfig" {
				return
			}
			venue := resolveActiveVenue(value, vbs)
			if ack := execCore.DoContext(ctx, exec.SetActiveVenue{Venue: venue}); !ack.Accepted {
				log.Warn("set active venue", "venue", venue, "err", ack.Reason)
				return
			}
			accountPoller.SetActiveVenue(venue)
		},
	}, execCore, st, core, venueAdm, venueProbe, restartInPlace, startDemo, locateProviders)
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
			if err := openbrowser.Open(browserURL(cfg.UIHub.Addr(), handlerLevel == slog.LevelDebug)); err != nil {
				log.Warn("open browser", "err", err)
			}
		}()
	}

	// --- moomoo auto-config (live boots only) ---
	// Gated on `live` (never -demo/-replay), the same gate config.
	// SeedDefaultIfMissing above uses -- a synthetic/replayed feed never
	// really connects to OpenD, so there is no real account list to probe,
	// and demo's OpenD-free session must never write to the real
	// ~/.eTape/config.toml. venueAdm is the same instance uihub's commands
	// already use, satisfying venueseed.Admin without a second config seam.
	var seeder *venueseed.Seeder
	if live {
		var seedEventSeq int64 // local to this closure; venueseed's Notify runs on its own one-shot goroutine, never concurrently
		notify := func(kind, detail, level string) {
			seedEventSeq++
			hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
				Seq: seedEventSeq, Ts: uihubClk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
				Kind: kind, Detail: detail, Level: level,
			})
		}
		seeder = venueseed.New(venueseed.Config{
			Admin: venueAdm, OpenDAddr: cfg.OpenD.Addr(), Clock: uihubClk, Notify: notify,
		})
	}

	// --- fan-in: md/exec Updates -> hub; mark bridge md -> exec ---
	var forwardWG sync.WaitGroup
	forwardWG.Add(1)
	go func() { defer forwardWG.Done(); forwardMD(ctx, core, hub, seeder) }()
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
	var sysEventSeq int64
	if live && liveMoomooDayLossGap(cfg) {
		detail := "MaxDayLoss does not cover moomoo (DayPnL unavailable); moomoo-originated losses are not gated by the day-loss circuit breaker"
		log.Warn(detail)
		st.AppendSysEvent("gate", detail)
		sysEventSeq++
		hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
			Seq: sysEventSeq, Ts: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			Kind: "gate", Detail: detail, Level: "warn",
		})
	}
	st.AppendSysEvent("boot", "engine up")
	hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "connecting"})
	dropWG.Add(1)
	go watchDroppedUpdates(ctx, &dropWG, core, execCore, st)

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

	wl, err := watchlist.NewList(st)
	if err != nil {
		log.Error("watchlist: load failed, starting empty", "err", err)
		wl = watchlist.NewEmpty(st)
	}

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
		wl.Seed(gen.Symbols())                         // synth universe; trusted, no probe; into throwaway demo.db
		sf := synth.NewFeed(gen, st, clock.System{})
		req := synth.NewRequester(gen)
		go func() { _ = sf.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, sf.Events(), core)
		// Joined via forwardWG, same as forwardMD below: it also calls
		// st.ArchiveDaily/core.SeedDaily, so it must stop before st.Close()
		// for the same reason forwardMD does (see the shutdown-order
		// comment above forwardWG.Wait()).
		forwardWG.Add(1)
		go func() { defer forwardWG.Done(); forwardDailyBars(ctx, gen, core, st, dailyBarPollInterval) }()
		feedForHub, pollReq, mmProbe = sf, req, req
		log.Info("engine up (demo synth feed)", "seed", seed, "symbols", gen.Symbols())
		hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "ready"})
	} else {
		client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
		fd := opend.NewOpenDFeed(client, opend.FeedOptions{
			Budget: cfg.Feed.QuotaSlots, Hysteresis: time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
			DisableExtendedTime: !cfg.Feed.ExtendedTime,
		})
		go func() { _ = client.Run(ctx) }()
		go func() { _ = fd.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, fd.Events(), core)
		hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "ready"})
		probe := &moomooProbe{c: client}
		hub.SetMarketClockSource(probe)
		feedForHub, pollReq, mmProbe, demand = fd, client, probe, fd

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
			if alpacaSrc != nil {
				dailyChain = append(dailyChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
				intradayChain = append(intradayChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
			}
			if cfg.Backfill.Yahoo.Enabled {
				dailyChain = append(dailyChain, backfill.Source{Name: "yahoo", HistFetcher: histyahoo.New("", clock.System{})})
			}
			tail = fd // TailFetcher: OpenDFeed.Tail1m (quota-free Qot_GetKL)
		}
	}
	hub.SetFeed(feedForHub) // enables on-demand EnsureSymbol/ReleaseSymbol + FocusGroup probe
	hub.SetKnownSymbol(st.HasArchivedSymbol)

	var prepareChart func(sym string, done func(ok bool))
	var warmArchive func(sym string, done func(ok bool))
	var refreshDaily func(sym string)
	var warmSeen sync.Map // symbol set retained for after-close daily repair
	if cfg.Backfill.Enabled || *demo {
		// demo: dailyChain/intradayChain/tail are all nil here, so this is
		// a chain-less orchestrator -- walkChain over a nil chain returns
		// cleanly (nil,"",nil) and o.tail nil-checks before use -- it
		// still serves warmStart's archive-first history against the data
		// the demo seed already wrote, with no special-casing.
		orch = backfill.New(
			dailyChain,
			intradayChain,
			tail,
			core,
			st,
			clock.System{},
			backfill.Config{
				TenSecondDays: cfg.Backfill.TenSecondDays,
				IntradayDays:  cfg.Backfill.IntradayDays,
				DailyYears:    cfg.Backfill.DailyYears,
				Concurrency:   cfg.Backfill.Concurrency,
			},
		)
		prepareChart = func(sym string, done func(ok bool)) {
			warmSeen.Store(sym, struct{}{})
			backfillWG.Add(1)
			go func() {
				defer backfillWG.Done()
				err := orch.PrepareChart(ctx, sym)
				if done != nil {
					done(err == nil)
				}
				if err == nil {
					err = orch.WarmArchive(ctx, sym)
				}
				if err != nil && ctx.Err() == nil {
					log.Warn("focused history warm failed", "symbol", sym, "err", err)
				}
			}()
		}
		warmArchive = func(sym string, done func(ok bool)) {
			warmSeen.Store(sym, struct{}{})
			backfillWG.Add(1)
			go func() {
				defer backfillWG.Done()
				err := orch.WarmArchive(ctx, sym)
				if done != nil {
					done(err == nil)
				}
			}()
		}
		refreshDaily = func(sym string) {
			backfillWG.Add(1)
			go func() {
				defer backfillWG.Done()
				if err := orch.RefreshDaily(ctx, sym); err != nil && ctx.Err() == nil {
					log.Warn("after-close daily refresh failed", "symbol", sym, "err", err)
				}
			}()
		}
	}
	var backfillOne func(string)
	if warmArchive != nil {
		backfillOne = func(sym string) { warmArchive(sym, nil) }
		scanWG.Add(1)
		go func() {
			defer scanWG.Done()
			runAfterCloseHistoryRefresh(ctx, &warmSeen, refreshDaily)
		}()
	}
	hub.SetHistoryWarm(prepareChart, warmArchive)
	if fd, ok := feedForHub.(*opend.OpenDFeed); ok && st != nil && !cfg.Backfill.Enabled {
		hub.SetCachedDaily(func(sym string) {
			backfillWG.Add(1)
			go func() {
				defer backfillWG.Done()
				bars, err := fd.CachedDaily(ctx, sym)
				if err != nil {
					log.Warn("cached daily seed failed", "symbol", sym, "err", err)
					return
				}
				if len(bars) > 0 && ctx.Err() == nil {
					for _, b := range bars {
						st.ArchiveDaily(b)
					}
					core.SeedDaily(sym, bars)
				}
			}()
		})
	}
	if activeAssetsDone != nil {
		for _, result := range <-activeAssetsDone {
			if result.err != nil {
				log.Warn("alpaca active assets load failed", "venue", result.venue, "err", result.err)
			} else {
				log.Info("alpaca active assets loaded", "venue", result.venue, "count", result.count)
			}
		}
	}
	startPollers(ctx, cfg, pollReq, demand, hub, uihubClk, st, wl, hasTZVenue(cfg), mmProbe, accountPoller, firstAlpacaAssetReader(vbs), backfillOne, !*demo, &scanWG)
	mode := "live"
	if *demo {
		mode = "demo"
	}
	log.Info("etape ready", "version", buildinfo.Version, "mode", mode,
		"uiAddr", cfg.UIHub.Addr(), "venues", len(cfg.Venues))

	<-ctx.Done()

	// --- ordered shutdown: stop accepting, drain all store writers, then Close ---
	// Every goroutine that can call a store-writing method (RecordEvent,
	// AppendExecEvent, finalized-bar archives, AppendSysEvent, SetConfig)
	// must be joined before st.Close() runs, since Close() closes the
	// s.writes channel and any send on it afterward panics. Sources: pipe()
	// (RecordEvent, joined via pipeWG), md.Core.Run (finalized-bar callback,
	// joined via coreDone after pipeWG quiesces its feed input),
	// forwardDailyBars() in demo mode (ArchiveDaily for a
	// synth-generator day closed by an ET-midnight rollover, also joined via
	// forwardWG), backfill's orch.Backfill
	// goroutines (ArchiveBar1m/
	// ArchiveDaily for freshly-fetched history, joined via backfillWG),
	// watchDroppedUpdates (AppendSysEvent, joined via dropWG — depends only
	// on ctx, so it can be waited anywhere after <-ctx.Done()),
	// runSealScheduler (RequestSeal, joined via sealSchedWG — also depends
	// only on ctx, same reasoning as dropWG), exec.Core.Run
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
	// backfillWG.Add(1) has two producers: the scan poller (pool
	// admission, joined via scanWG); the Hub goroutine via the hubBackfill
	// closure injected with SetBackfill -- called from both
	// Hub.handleEnsureDemand (chart-open demand) and Hub.rearmBackfill
	// (OpenD-reconnect re-arm, triggered from handleMD on an
	// md.ResyncedUpdate). srv.Wait() only proves every conn's dispatch loop
	// has returned, not that the Hub goroutine has finished servicing the
	// ensureDemandCh/mdCh sends already
	// made on their way out -- that Add(1) can still be in flight on the Hub
	// goroutine after srv.Wait() returns. <-hubDone closes that gap: Hub.Run
	// only returns via its own <-ctx.Done() branch, by which point any
	// ensureDemandCh/mdCh message it had already received has
	// finished its handler call (and therefore its Add, if any), so no
	// further Add(1) can occur once hubDone closes. Waiting on it here,
	// before scanWG.Wait()/backfillWG.Wait(), keeps all three Add(1)
	// producers quiesced before the counter is read -- otherwise a late Add
	// could land after backfillWG.Wait() already observed zero, spawning an
	// unwaited orch.Backfill goroutine that touches
	// the store during/after st.Close().
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(shutCtx)
	cancelShut()
	srv.Wait()        // every conn.run() returned: no more SetConfig via dispatch
	<-hubDone         // hub.Run returned: no more handleEnsureDemand, hence no more backfillWG.Add from chart-open demands
	scanWG.Wait()     // scan poller stopped: no more backfillWG.Add from pool admissions
	backfillWG.Wait() // boot backfill workers stopped: no more Seed* into the core
	pipeWG.Wait()     // feed->core pipe stopped: no more RecordEvent
	<-coreDone        // md core stopped: no more finalized-bar archive callbacks
	forwardWG.Wait()  // forwardMD + demo's forwardDailyBars stopped: no more ArchiveDaily
	dropWG.Wait()     // dropped-updates watcher stopped: no more AppendSysEvent from it
	<-execDone        // exec.Core.Run returned: no more AppendExecEvent
	brokerWG.Wait()
	if err := st.Close(); err != nil {
		log.Error("close store", "err", err)
	}
	mdDrops := core.DropStats()
	log.Info("shutdown complete", "mdInboxDrops", mdDrops.Inbox,
		"mdUpdateDrops", mdDrops.Updates, "execUpdateDrops", execCore.DroppedUpdates(),
		"droppedUpdates", mdDrops.Total())
	var na []string
	if p := nextArgsPtr.Load(); p != nil {
		na = *p
	}
	return 0, restartRequested.Load(), na
}

func browserURL(addr string, debug bool) string {
	url := "http://" + addr
	if debug {
		url += "?debug=1"
	}
	return url
}

func bars10sRetentionCutoff(now time.Time, days int) int64 {
	return now.AddDate(0, 0, -days).UnixMilli()
}

func runAfterCloseHistoryRefresh(ctx context.Context, seen *sync.Map, refresh func(string)) {
	for {
		now := time.Now().In(session.Loc())
		next := nextHistoryRefresh(now)
		t := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return
		case <-t.C:
			if session.IsTradingDay(next) {
				seen.Range(func(key, _ any) bool {
					refresh(key.(string))
					return true
				})
			}
		}
	}
}

func nextHistoryRefresh(now time.Time) time.Time {
	s := session.Schedule(now)
	if s.TradingDay {
		ready := s.DataClose.Add(5 * time.Minute)
		if ready.After(now) {
			return ready
		}
	}
	return session.Schedule(session.NextTradingDay(now)).DataClose.Add(5 * time.Minute)
}

// dropWatchInterval controls how often watchDroppedUpdates samples
// core.DropStats() for a live sys.events trail: a drop should surface
// during the session it happens in, not only in the shutdown log line.
const (
	dropWatchInterval = 5 * time.Second
	dropWarnCooldown  = time.Minute
)

func dropWarningDue(now, last time.Time) bool {
	return last.IsZero() || now.Sub(last) >= dropWarnCooldown
}

func formatMDDropDetail(delta, total md.DropStats) string {
	return fmt.Sprintf("dropped %d md message(s) since last check (inbox=%d updates=%d total=%d; cumulative inbox=%d updates=%d total=%d)",
		delta.Total(), delta.Inbox, delta.Updates, delta.Total(), total.Inbox, total.Updates, total.Total())
}

func formatExecDropDetail(delta, total uint64) string {
	return fmt.Sprintf("dropped %d execution update(s) since last check (total %d)", delta, total)
}

type dropWatchState struct {
	lastMD                   md.DropStats
	lastExec                 uint64
	lastMDWarn, lastExecWarn time.Time
}

func reportDroppedUpdates(now time.Time, mdTotal md.DropStats, execTotal uint64, appendSysEvent func(string, string), state *dropWatchState) {
	mdDelta := md.DropStats{Inbox: mdTotal.Inbox - state.lastMD.Inbox, Updates: mdTotal.Updates - state.lastMD.Updates}
	if mdDelta.Total() > 0 {
		appendSysEvent("md-drop", formatMDDropDetail(mdDelta, mdTotal))
		if dropWarningDue(now, state.lastMDWarn) {
			slog.Warn("md backpressure detected", "inboxDelta", mdDelta.Inbox,
				"updatesDelta", mdDelta.Updates, "inboxTotal", mdTotal.Inbox,
				"updatesTotal", mdTotal.Updates, "total", mdTotal.Total())
			state.lastMDWarn = now
		}
	}
	state.lastMD = mdTotal

	if execTotal > state.lastExec {
		delta := execTotal - state.lastExec
		appendSysEvent("exec-drop", formatExecDropDetail(delta, execTotal))
		if dropWarningDue(now, state.lastExecWarn) {
			slog.Warn("execution update drops detected", "delta", delta, "total", execTotal)
			state.lastExecWarn = now
		}
		state.lastExec = execTotal
	}
}

// watchDroppedUpdates polls MD and execution drop counters and appends
// "md-drop"/"exec-drop" sys.events rows whenever they increase, so an
// md.Core updates-channel or exec.Core updates-channel
// overflow (see Core.emit) is visible on the sys.events topic live instead
// of only in the "shutdown complete" log line. It is a store-writing
// goroutine (AppendSysEvent) and must be joined via wg before st.Close() --
// see the shutdown-ordering comment in main().
func watchDroppedUpdates(ctx context.Context, wg *sync.WaitGroup, core *md.Core, execCore *exec.Core, st *store.Store) {
	defer wg.Done()
	t := time.NewTicker(dropWatchInterval)
	defer t.Stop()
	state := dropWatchState{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reportDroppedUpdates(time.Now(), core.DropStats(), execCore.DroppedUpdates(), st.AppendSysEvent, &state)
		}
	}
}

func hz(rate float64) time.Duration {
	if rate <= 0 {
		return 33 * time.Millisecond
	}
	return time.Duration(float64(time.Second) / rate)
}

// forwardMD drains md.Core.Updates(), publishes each to the hub, and, on
// every feed-up transition, kicks the
// moomoo auto-config probe. seeder is nil outside a real live boot (replay,
// -demo, or the auto-config already run this process) — see boot's own
// venueseed.New call site for the exact gate; forwardMD only ever guards
// against nil, it doesn't decide when a Seeder exists.
func forwardMD(ctx context.Context, core *md.Core, hub *uihub.Hub, seeder *venueseed.Seeder) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-core.Updates():
			hub.PublishMD(u)
			if cu, ok := u.(md.ConnUpdate); ok && cu.Up && seeder != nil {
				seeder.OnFeedUp(ctx)
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
func startPollers(ctx context.Context, cfg config.Config, r pollerRequester, demand demandFeeder, hub *uihub.Hub, clk clock.Clock, st *store.Store, wl *watchlist.List, hasTZ bool, mmProbe rttProber, accountHealth health.AccountHealthSource, assetReader stockInfoAssetReader, backfillOne func(string), startQuota bool, scanWG *sync.WaitGroup) {
	ssrResolver := ssr.New(st)
	scanPoller := scan.New(cfg.Scan, r, hub, clk, demand, backfillOne, ssrResolver)
	if raw, ok, err := st.GetConfig("scanner.filters.v1"); err == nil && ok {
		var saved wsmsg.ScannerFilters
		if json.Unmarshal([]byte(raw), &saved) == nil && scan.ValidateFilters(saved) == nil {
			_ = scanPoller.SetFilters(saved)
		}
	}
	hub.SetScanner(scanPoller)
	newsPlan := func() news.SymbolPlan { return newsSymbolPlan(scanPoller.PoolSymbols(), hub.ActiveDemandSymbols()) }
	symbols := func() []string { return newsPlan().All() }
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, r, hub, clk, newsPlan).Run(ctx) }()
	go func() {
		_ = stockinfo.New(cfg.StockInfo, r, hub, clk, symbols, st, assetReader, ssrResolver).Run(ctx)
	}()
	if cfg.Watchlist.Enabled {
		interval := time.Duration(cfg.Watchlist.PollMs) * time.Millisecond
		wp := watchlist.New(wl, r, hub, clk, interval)
		hub.SetWatchlist(watchlistAdapter{l: wl, p: wp})
		go func() { _ = wp.Run(ctx) }()
	}
	// health: mmProbe is the moomoo probe (real OpenD RTT in live/replay, a
	// constant synthetic RTT in -demo); app-ping RTT source is nil in v1
	// (ui-engine shows down until ping tracking is wired). accountHealth is the
	// active Alpaca account poller's cached result, so health never performs a
	// second Alpaca REST request.
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
		_ = health.New(cfg.Health, hub, clk, mmProbe, nil, hasTZ, accountHealth, qsrc).Run(ctx)
	}()
}

// watchlistAdapter satisfies uihub's watchlistCtl: Add/Remove on the List,
// Poke on the Poller.
type watchlistAdapter struct {
	l *watchlist.List
	p *watchlist.Poller
}

func (a watchlistAdapter) Add(s string) (bool, error) { return a.l.Add(s) }
func (a watchlistAdapter) Remove(s string) bool       { return a.l.Remove(s) }
func (a watchlistAdapter) Poke()                      { a.p.Poke() }

func hasTZVenue(cfg config.Config) bool {
	for _, v := range cfg.Venues {
		if v.Broker == "tradezero" {
			return true
		}
	}
	return false
}

// pipe forwards feed events into the core.
func pipe(ctx context.Context, wg *sync.WaitGroup, in <-chan feed.Event, core *md.Core) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			core.FeedContext(ctx, ev)
		}
	}
}

// dailyBarPollInterval is how often forwardDailyBars checks the demo
// generator for a newly-closed day. Daily bars only appear once per symbol
// per ET-midnight rollover, so this has no latency requirement -- a coarse
// poll is deliberate, not a shortcut.
const dailyBarPollInterval = time.Minute

// dailyBarSource is satisfied by *synth.Generator. A live feed has no
// equivalent: OpenD's official daily bars arrive via a separate periodic
// K_DAY re-fetch through the backfill orchestrator, not through this poll.
type dailyBarSource interface {
	DrainDailyBars() []feed.Bar
}

// dailyBarSeeder is satisfied by *md.Core.
type dailyBarSeeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
}

// dailyBarArchiver is satisfied by *store.Store.
type dailyBarArchiver interface {
	ArchiveDaily(b feed.Bar)
}

// forwardDailyBars persists demo-only daily bars that the synth generator
// closes out at each ET-midnight rollover. Without this, a demo session
// left running past midnight would keep the just-completed day only in the
// generator's own in-memory state: md.Core's own daily aggregation from 1m
// bars never finalizes on its own (deriveDaily's locally-aggregated bar
// stays InProgress forever absent an "official" bar to replace it, per
// internal/md/bars.go), and there is no backfill chain in demo mode to
// supply one the way a live K_DAY re-fetch would. Polling gen and pushing
// each newly-closed day through the same two calls a live boot's daily
// backfill uses (core.SeedDaily to finalize md.Core's own aggregate,
// archive.ArchiveDaily to persist it) closes that gap with no change to the
// live (non-demo) boot path.
func forwardDailyBars(ctx context.Context, gen dailyBarSource, core dailyBarSeeder, archive dailyBarArchiver, pollEvery time.Duration) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, b := range gen.DrainDailyBars() {
				archive.ArchiveDaily(b)
				core.SeedDaily(b.Symbol, []feed.Bar{b})
			}
		}
	}
}
