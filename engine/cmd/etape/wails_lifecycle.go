//go:build wails

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uiapi"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uistate"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	enginePhaseLoading = "loading"
	enginePhaseReady   = "ready"
	enginePhaseFailure = "failure"
)

var (
	errEngineStarted  = errors.New("engine runtime is not restartable")
	errEngineStopping = errors.New("engine runtime is stopping")
	errEngineNotStart = errors.New("engine runtime has not started")
)

const restartBindingAckDelay = 200 * time.Millisecond

type engineBootState struct {
	Phase string
	Error string
}

type engineBootRunner func(context.Context, bootOptions) (int, bool, []string)

// engineRuntime is the one Wails-owned lifecycle owner. It starts the existing
// engine composition root once, then joins the Wails admission boundary before
// that root is canceled so its established drain can close storage safely.
type engineRuntime struct {
	runtime *wailsruntime.Runtime
	run     engineBootRunner

	serverMu    sync.Mutex
	server      *uihub.Server
	serverReady chan struct{}
	serverOnce  sync.Once

	mu                      sync.Mutex
	started                 bool
	stopping                bool
	phase                   string
	bootError               string
	engineContext           context.Context
	cancelEngine            context.CancelFunc
	engineDone              chan struct{}
	restart                 bool
	relaunchArgs            []string
	statePublisher          func(engineBootState)
	querySourcePublisher    func(uiapi.QuerySources)
	workspaceStorePublisher func(uistate.Persistence) error
	workspaceHubPublisher   func(*uihub.Server)
	mutationSourcePublisher func(uiapi.MutationSources)
	requestQuit             func()
	restartOnce             sync.Once
	restartScheduleOnce     sync.Once
	stopOnce                sync.Once
	stopDone                chan struct{}
	stopError               error
}

func newEngineRuntime(runtime *wailsruntime.Runtime) *engineRuntime {
	return &engineRuntime{
		runtime:     runtime,
		run:         bootWithOptions,
		phase:       enginePhaseLoading,
		stopDone:    make(chan struct{}),
		serverReady: make(chan struct{}),
	}
}

func (e *engineRuntime) Start() error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return errEngineStarted
	}
	if e.stopping {
		e.mu.Unlock()
		return errEngineStopping
	}
	e.started = true
	e.phase = enginePhaseLoading
	e.bootError = ""
	e.engineContext, e.cancelEngine = context.WithCancel(context.Background())
	e.engineDone = make(chan struct{})
	ctx := e.engineContext
	done := e.engineDone
	run := e.run
	e.mu.Unlock()

	e.publishState(engineBootState{Phase: enginePhaseLoading})
	go e.runEngine(ctx, done, run)
	return nil
}

func (e *engineRuntime) runEngine(ctx context.Context, done chan struct{}, run engineBootRunner) {
	code, restart, nextArgs := run(ctx, bootOptions{
		noLegacyHTTP:     true,
		onHub:            e.setHubServer,
		onQuerySource:    e.setQuerySources,
		onWorkspaceStore: e.setWorkspaceStore,
		onMutationSource: e.setMutationSources,
		onReady:          e.markReady,
	})

	e.mu.Lock()
	if restart {
		e.restart = true
	}
	if nextArgs != nil {
		e.relaunchArgs = append([]string(nil), nextArgs...)
	}
	state := engineBootState{Phase: e.phase, Error: e.bootError}
	if !e.stopping {
		switch {
		case code != 0:
			state = engineBootState{
				Phase: enginePhaseFailure,
				Error: fmt.Sprintf("engine boot failed (exit code %d)", code),
			}
		case e.phase != enginePhaseReady:
			state = engineBootState{
				Phase: enginePhaseFailure,
				Error: "engine stopped before becoming ready",
			}
		}
	}
	if state.Phase != e.phase || state.Error != e.bootError {
		e.phase = state.Phase
		e.bootError = state.Error
	}
	e.mu.Unlock()

	if state.Phase == enginePhaseFailure {
		e.publishState(state)
	}
	close(done)
	if restart {
		e.BeginRestart()
	}
}

func (e *engineRuntime) setHubServer(server *uihub.Server) {
	e.serverMu.Lock()
	e.server = server
	e.serverMu.Unlock()
	e.mu.Lock()
	publish := e.workspaceHubPublisher
	e.mu.Unlock()
	if publish != nil {
		publish(server)
	}
	e.serverOnce.Do(func() { close(e.serverReady) })
}

func (e *engineRuntime) setWorkspaceStore(persistence uistate.Persistence) error {
	e.mu.Lock()
	publish := e.workspaceStorePublisher
	e.mu.Unlock()
	if publish == nil {
		return nil
	}
	return publish(persistence)
}

func (e *engineRuntime) setQuerySources(sources uiapi.QuerySources) {
	e.mu.Lock()
	publish := e.querySourcePublisher
	e.mu.Unlock()
	if publish != nil {
		publish(sources)
	}
}

func (e *engineRuntime) setQuerySourcePublisher(publish func(uiapi.QuerySources)) {
	e.mu.Lock()
	e.querySourcePublisher = publish
	e.mu.Unlock()
}

func (e *engineRuntime) setWorkspaceStorePublisher(publish func(uistate.Persistence) error) {
	e.mu.Lock()
	e.workspaceStorePublisher = publish
	e.mu.Unlock()
}

func (e *engineRuntime) setWorkspaceHubPublisher(publish func(*uihub.Server)) {
	e.mu.Lock()
	e.workspaceHubPublisher = publish
	e.mu.Unlock()
}

func (e *engineRuntime) setMutationSources(sources uiapi.MutationSources) {
	e.mu.Lock()
	publish := e.mutationSourcePublisher
	e.mu.Unlock()
	if publish != nil {
		publish(sources)
	}
}

func (e *engineRuntime) setMutationSourcePublisher(publish func(uiapi.MutationSources)) {
	e.mu.Lock()
	e.mutationSourcePublisher = publish
	e.mu.Unlock()
}

func (e *engineRuntime) HandleStream(c *application.StreamConn) {
	e.HandleWorkspaceStream(c, "")
}

func (e *engineRuntime) HandleWorkspaceStream(c *application.StreamConn, workspaceID string) {
	select {
	case <-e.serverReady:
	case <-c.Context().Done():
		return
	}
	e.serverMu.Lock()
	server := e.server
	e.serverMu.Unlock()
	if server != nil {
		server.HandleWailsStream(c, workspaceID)
	}
}

func (e *engineRuntime) markReady() {
	e.mu.Lock()
	if !e.started || e.stopping || e.phase == enginePhaseFailure {
		e.mu.Unlock()
		return
	}
	e.phase = enginePhaseReady
	e.bootError = ""
	e.mu.Unlock()
	e.publishState(engineBootState{Phase: enginePhaseReady})
}

func (e *engineRuntime) State() engineBootState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return engineBootState{Phase: e.phase, Error: e.bootError}
}

func (e *engineRuntime) setStatePublisher(publish func(engineBootState)) {
	e.mu.Lock()
	e.statePublisher = publish
	e.mu.Unlock()
}

func (e *engineRuntime) publishState(state engineBootState) {
	e.mu.Lock()
	publish := e.statePublisher
	e.mu.Unlock()
	if publish != nil {
		publish(state)
	}
}

func (e *engineRuntime) setRequestQuit(requestQuit func()) {
	e.mu.Lock()
	e.requestQuit = requestQuit
	e.mu.Unlock()
}

func (e *engineRuntime) BeginStop() {
	e.mu.Lock()
	e.stopping = true
	restarting := e.restart
	e.mu.Unlock()
	if restarting {
		e.runtime.BeginStopWithReason("restarting")
		return
	}
	e.runtime.BeginStop()
}

func (e *engineRuntime) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.stopOnce.Do(func() {
		e.BeginStop()
		go e.finishStop()
	})

	select {
	case <-e.stopDone:
		e.mu.Lock()
		err := e.stopError
		e.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *engineRuntime) finishStop() {
	err := e.runtime.Stop(context.Background())

	e.mu.Lock()
	cancelEngine := e.cancelEngine
	engineDone := e.engineDone
	e.mu.Unlock()
	if cancelEngine != nil {
		cancelEngine()
	}
	if engineDone != nil {
		<-engineDone
	}

	e.mu.Lock()
	e.stopError = err
	close(e.stopDone)
	e.mu.Unlock()
}

func (e *engineRuntime) RequestRestart() error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return errEngineNotStart
	}
	if e.stopping {
		e.mu.Unlock()
		return errEngineStopping
	}
	e.restart = true
	e.mu.Unlock()

	// Let the binding response leave Wails before asking its event loop to
	// begin shutdown. The engine still records the intent synchronously, so a
	// concurrent quit cannot lose it.
	e.restartScheduleOnce.Do(func() {
		time.AfterFunc(restartBindingAckDelay, e.BeginRestart)
	})
	return nil
}

func (e *engineRuntime) BeginRestart() {
	e.restartOnce.Do(func() {
		e.mu.Lock()
		if e.stopping {
			e.mu.Unlock()
			return
		}
		requestQuit := e.requestQuit
		e.mu.Unlock()
		if requestQuit != nil {
			requestQuit()
		}
	})
}

func (e *engineRuntime) RestartRequested() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.restart
}

func (e *engineRuntime) RelaunchArgs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.relaunchArgs...)
}
