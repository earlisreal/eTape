// Command etape is the eTape engine: the full boot sequence wiring the market-
// data plane (OpenD -> feed -> md.Core), the execution subsystem (exec.Core +
// broker venues), and the uihub WebSocket server the UI connects to. With
// --replay it reconstructs a recorded day against SimBroker over the identical
// hub/contract (the mode the UI Playwright E2E boots on).
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/backfill"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/health"
	histalpaca "github.com/earlisreal/eTape/engine/internal/hist/alpaca"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/news"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/venueadmin"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	replayDay := flag.String("replay", "", "replay a recorded day (YYYY-MM-DD) instead of live OpenD")
	speed := flag.Float64("speed", 0, "replay speed (>0: real-time x speed; <=0: as fast as possible)")
	dist := flag.String("dist", "", "serve built UI from this dir (overrides [uihub].dist_dir)")
	replayHold := flag.Bool("replay-hold", false, "in replay, keep serving after the journal is exhausted (E2E)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	if *dist != "" {
		cfg.UIHub.DistDir = *dist
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		os.Exit(1)
	}
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(home, ".eTape", "etape.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Error("make db dir", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	live := *replayDay == ""
	uihubClk := clock.System{}
	var execClk clock.Clock = clock.System{}

	// --- store ---
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	// NOTE: st.Close() is deferred until AFTER every store-writer goroutine has
	// stopped (feed pipe + forwardMD + exec.Core) — see the shutdown block below.

	// --- md core ---
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})
	go func() { _ = core.Run(ctx) }()

	// --- replay clock (execClk) if replaying ---
	var replayRows []store.JournalRow
	if !live {
		replayRows, err = st.ReadJournalDay(*replayDay)
		if err != nil || len(replayRows) == 0 {
			log.Error("replay day unavailable", "day", *replayDay, "err", err, "rows", len(replayRows))
			_ = st.Close()
			os.Exit(1)
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
		os.Exit(1)
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
		SysLog:  st.AppendSysEvent,
		AutoArm: autoArmVenues(cfg),
	})
	if err := execCore.Recover(ctx); err != nil {
		log.Warn("exec recover (continuing; reactive reconcile will catch up)", "err", err)
	}
	execDone := make(chan struct{})
	go func() { defer close(execDone); _ = execCore.Run(ctx) }()

	// --- uihub (listening BEFORE OpenD is dialed) ---
	venueAdm := venueadmin.New(*cfgPath, creds.DefaultPath(), config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate})
	hub, srv := uihub.New(uihubClk, uihub.Config{
		Venues: venueMetas(cfg), Global: uihub.GlobalLimits{
			MaxDayLoss: cfg.Gate.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Gate.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: cfg.Gate.Global.MaxSymbolPositionShares,
		},
		MD: hz(cfg.UIHub.MDRateHz), Account: hz(cfg.UIHub.AccountRateHz),
		Position: time.Duration(cfg.UIHub.PositionMs) * time.Millisecond,
		Buf:      4096, TapeCap: cfg.UIHub.TapeSnapshot, NewsCap: 500, FillsCap: 1000, EventsCap: 500, TradesCap: 1000,
		OutBuf: cfg.UIHub.OutboundQueue, DistDir: cfg.UIHub.DistDir,
	}, execCore, st, core, venueAdm)
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

	// --- fan-in: md/exec Updates -> hub; mark bridge md -> exec ---
	var forwardWG sync.WaitGroup
	forwardWG.Add(1)
	go func() { defer forwardWG.Done(); forwardMD(ctx, core, hub, live, st) }()
	go forwardExec(ctx, execCore, hub)

	// In replay every venue is a SimBroker; forward replayed marks into them so
	// submitted orders fill. Live venues are fed by their own broker connection.
	var simSinks []markSink
	if !live {
		for _, vb := range vbs {
			if s, ok := vb.Broker.(markSink); ok {
				simSinks = append(simSinks, s)
			}
		}
	}
	go markBridge(ctx, core, execCore, simSinks)

	// --- feed (live OpenD or replay) ---
	var pipeWG sync.WaitGroup
	var backfillWG sync.WaitGroup
	var scanWG sync.WaitGroup
	var dropWG sync.WaitGroup
	var client *opend.Client
	if live {
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err == nil && n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.AppendSysEvent("boot", "engine up")
		dropWG.Add(1)
		go watchDroppedUpdates(ctx, &dropWG, core, st)
		client = opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
		fd := opend.NewOpenDFeed(client, opend.FeedOptions{
			Budget: cfg.Feed.QuotaSlots, Hysteresis: time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
			DisableExtendedTime: !cfg.Feed.ExtendedTime,
		})
		hub.SetFeed(fd) // enables on-demand EnsureSymbol/ReleaseSymbol + FocusGroup probe
		go func() { _ = client.Run(ctx) }()
		go func() { _ = fd.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, fd.Events(), core, st)
		var backfillOne func(string)
		if cfg.Backfill.Enabled {
			var fallback backfill.HistFetcher
			if cfg.Backfill.Alpaca.Enabled {
				if cfg.Backfill.Alpaca.CredsKey == "alpaca-live" {
					log.Warn("backfill: refusing alpaca-live creds for read-only historical fallback", "key", cfg.Backfill.Alpaca.CredsKey)
				} else if p, err := credsFile.Get(cfg.Backfill.Alpaca.CredsKey); err == nil {
					fallback = histalpaca.New("", p.KeyID, p.SecretKey, cfg.Backfill.Alpaca.Feed, clock.System{})
				} else {
					log.Warn("backfill: alpaca fallback disabled (no creds)", "key", cfg.Backfill.Alpaca.CredsKey, "err", err)
				}
			}
			orch := backfill.New(
				backfill.MoomooFetcher(fd),
				fallback,
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
			backfillOne = func(sym string) {
				backfillWG.Add(1)
				go func() {
					defer backfillWG.Done()
					orch.Backfill(ctx, sym)
				}()
			}
		}
		hub.SetBackfill(backfillOne) // chart-open demands also deep-backfill (nil-safe if disabled)
		startPollers(ctx, cfg, client, fd, hub, uihubClk, st, hasTZVenue(cfg), backfillOne, &scanWG)
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
	// backfillWG.Add(1) now has two producers: the scan poller (pool admission,
	// joined via scanWG) and Hub.handleEnsureDemand (chart-open demand, via the
	// backfillOne closure injected with SetBackfill). srv.Wait() only proves
	// every conn's dispatch loop has returned, not that the Hub goroutine has
	// finished servicing the ensureDemandCh sends those dispatch loops made on
	// their way out -- that Add(1) can still be in flight on the Hub goroutine
	// after srv.Wait() returns. <-hubDone closes that gap: Hub.Run only returns
	// via its own <-ctx.Done() branch, by which point any ensureDemandCh message
	// it had already received has finished its handleEnsureDemand call (and
	// therefore its Add, if any), so no further Add(1) can occur once hubDone
	// closes. Waiting on it here, before scanWG.Wait()/backfillWG.Wait(), keeps
	// both Add(1) producers quiesced before the counter is read -- otherwise a
	// late Add could land after backfillWG.Wait() already observed zero,
	// spawning an unwaited orch.Backfill that touches the store during/after
	// st.Close().
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(shutCtx)
	cancelShut()
	srv.Wait()        // every conn.run() returned: no more SetConfig via dispatch
	<-hubDone         // hub.Run returned: no more handleEnsureDemand, hence no more backfillWG.Add from chart-open demands
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

// markSink receives last-trade marks. Implemented by *sim.Broker (SetMark);
// used only in replay so submitted orders fill against the replayed tape.
type markSink interface {
	SetMark(symbol string, price float64)
}

// markBridge copies md.Core.Marks() -> exec.Core.FeedMark (the single md<->exec
// seam) and, in replay, -> every sim broker's SetMark so a submitted order fills
// against the replayed marks (live venues get marks from their own broker feed).
func markBridge(ctx context.Context, core *md.Core, execCore *exec.Core, sinks []markSink) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-core.Marks():
			execCore.FeedMark(exec.Mark{Symbol: m.Symbol, Price: m.Price, TsMs: m.TsMs})
			for _, s := range sinks {
				s.SetMark(m.Symbol, m.Price)
			}
		}
	}
}

func startPollers(ctx context.Context, cfg config.Config, client *opend.Client, fd *opend.OpenDFeed, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, backfillOne func(string), scanWG *sync.WaitGroup) {
	scanPoller := scan.New(cfg.Scan, client, hub, clk, fd, backfillOne)
	symbols := func() []string {
		return newsSymbols(scanPoller.PoolSymbols(), hub.ActiveDemandSymbols())
	}
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, client, hub, clk, symbols).Run(ctx) }()
	// health: moomoo probe via the OpenD client; app-ping RTT source is nil in v1
	// (ui-engine shows down until ping tracking is wired). The health poller's
	// sys.events are also persisted by main via a store hook if desired.
	go func() { _ = health.New(cfg.Health, hub, clk, moomooProbe{c: client}, nil, hasTZ).Run(ctx) }()
	_ = st // reserved: wire health.Event -> st.AppendSysEvent in a later pass
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
