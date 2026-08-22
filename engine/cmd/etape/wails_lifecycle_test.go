//go:build wails

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
)

func TestEngineRuntimeStopsAdmissionBeforeEngineDrain(t *testing.T) {
	runtime := wailsruntime.New()
	started := make(chan struct{})
	observedInFlight := make(chan int, 1)
	observedNoLegacyHTTP := make(chan bool, 1)
	storeClosed := atomic.Bool{}
	var storeWrites atomic.Int32
	storeWriteAfterClose := make(chan struct{}, 1)

	owner := newEngineRuntime(runtime)
	owner.run = func(ctx context.Context, options bootOptions) (int, bool, []string) {
		observedNoLegacyHTTP <- options.noLegacyHTTP
		close(started)
		if options.onReady != nil {
			options.onReady()
		}
		<-ctx.Done()
		observedInFlight <- runtime.Gate().InFlight()
		storeClosed.Store(true)
		return 0, false, nil
	}

	if err := owner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start asynchronously")
	}
	if !<-observedNoLegacyHTTP {
		t.Fatal("Wails engine started the legacy HTTP listener")
	}

	if state := owner.State(); state.Phase != enginePhaseReady {
		t.Fatalf("state phase = %q, want %q", state.Phase, enginePhaseReady)
	}

	workContext, bindingRelease, err := runtime.EnterContext(context.Background())
	if err != nil {
		t.Fatalf("EnterContext() error = %v", err)
	}
	streamContext, streamRelease, err := runtime.EnterContext(context.Background())
	if err != nil {
		t.Fatalf("stream EnterContext() error = %v", err)
	}
	workDone := make(chan struct{}, 2)
	for _, work := range []struct {
		ctx     context.Context
		release func()
	}{
		{ctx: workContext, release: bindingRelease},
		{ctx: streamContext, release: streamRelease},
	} {
		go func(workCtx context.Context, release func()) {
			<-workCtx.Done()
			if storeClosed.Load() {
				storeWriteAfterClose <- struct{}{}
			}
			storeWrites.Add(1)
			release()
			workDone <- struct{}{}
		}(work.ctx, work.release)
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- owner.Stop(context.Background()) }()

	deadline := time.After(time.Second)
	for !runtime.Gate().Stopping() {
		select {
		case <-deadline:
			t.Fatal("stop did not close the admission boundary")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := runtime.Enter(context.Background()); !errors.Is(err, wailsruntime.ErrStopping) {
		t.Fatalf("new admission error = %v, want %v", err, wailsruntime.ErrStopping)
	}
	for range 2 {
		select {
		case <-workDone:
		case <-time.After(time.Second):
			t.Fatal("admitted work was not canceled and released")
		}
	}

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not complete")
	}

	select {
	case inFlight := <-observedInFlight:
		if inFlight != 0 {
			t.Fatalf("engine drained with %d admitted calls, want 0", inFlight)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not finish")
	}
	select {
	case <-storeWriteAfterClose:
		t.Fatal("admitted work wrote after store close")
	default:
	}
	if got := storeWrites.Load(); got != 2 {
		t.Fatalf("admitted store writes = %d, want 2", got)
	}

	if err := owner.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestEngineRuntimePublishesFailure(t *testing.T) {
	runtime := wailsruntime.New()
	owner := newEngineRuntime(runtime)
	owner.run = func(context.Context, bootOptions) (int, bool, []string) {
		return 1, false, nil
	}

	if err := owner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	deadline := time.After(time.Second)
	for {
		state := owner.State()
		if state.Phase == enginePhaseFailure {
			if state.Error == "" {
				t.Fatal("failure state has no error")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("state = %#v, want failure", state)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestRuntimeServiceRestartReleasesAdmissionBeforeQuit(t *testing.T) {
	runtime := wailsruntime.New()
	owner := newEngineRuntime(runtime)
	owner.run = func(ctx context.Context, _ bootOptions) (int, bool, []string) {
		<-ctx.Done()
		return 0, false, nil
	}
	if err := owner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	quitCalled := make(chan struct{})
	var bindingReturned atomic.Bool
	var quitOnce atomic.Bool
	owner.setRequestQuit(func() {
		if !bindingReturned.Load() {
			t.Error("restart began before the binding returned")
		}
		if quitOnce.CompareAndSwap(false, true) {
			close(quitCalled)
		}
	})

	service := &RuntimeService{runtime: runtime, lifecycle: owner}
	result, err := service.RestartApplication(context.Background())
	if err != nil {
		t.Fatalf("RestartApplication() error = %v", err)
	}
	if result != "accepted" {
		t.Fatalf("RestartApplication() = %q, want accepted", result)
	}
	bindingReturned.Store(true)
	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("restart did not request an asynchronous quit")
	}

	if runtime.Gate().InFlight() != 0 {
		t.Fatalf("restart left %d calls admitted", runtime.Gate().InFlight())
	}
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestEngineRuntimeIsNotRestartableAndDrainsOnce(t *testing.T) {
	runtime := wailsruntime.New()
	started := make(chan struct{})
	var runs atomic.Int32
	owner := newEngineRuntime(runtime)
	owner.run = func(ctx context.Context, options bootOptions) (int, bool, []string) {
		runs.Add(1)
		close(started)
		if options.onReady != nil {
			options.onReady()
		}
		<-ctx.Done()
		return 0, false, nil
	}

	if err := owner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("engine runs = %d, want 1", got)
	}
	if err := owner.Start(); !errors.Is(err, errEngineStarted) {
		t.Fatalf("second Start() error = %v, want %v", err, errEngineStarted)
	}
}
