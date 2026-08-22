//go:build wails

package desktop

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type closeWindowFake struct {
	mu      sync.Mutex
	events  int
	closes  int
	request workspaceCloseRequest
}

func (w *closeWindowFake) EmitEvent(_ string, data ...any) bool {
	w.mu.Lock()
	w.events++
	if len(data) == 1 {
		w.request = data[0].(workspaceCloseRequest)
	}
	w.mu.Unlock()
	return false
}

func (w *closeWindowFake) Close() {
	w.mu.Lock()
	w.closes++
	w.mu.Unlock()
}

func (w *closeWindowFake) snapshot() (events, closes int, request workspaceCloseRequest) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.events, w.closes, w.request
}

func TestCloseHandshakeCancelsDuplicateRequestsAndEmitsOnce(t *testing.T) {
	h := newCloseHandshake(time.Hour)
	window := &closeWindowFake{}
	event := application.NewWindowEvent()
	h.intercept("desk", window, func(func(), func()) {}, event)

	if !event.IsCancelled() {
		t.Fatal("first close request was not held")
	}
	event = application.NewWindowEvent()
	h.intercept("desk", window, func(func(), func()) {}, event)
	if !event.IsCancelled() {
		t.Fatal("duplicate close request was not held")
	}
	events, closes, request := window.snapshot()
	if events != 1 || closes != 0 || request.WorkspaceID != "desk" || request.RequestID == "" {
		t.Fatalf("events=%d closes=%d request=%+v, want one pending request", events, closes, request)
	}
	h.finished("desk")
}

func TestCloseHandshakeCompletesExactlyOnce(t *testing.T) {
	h := newCloseHandshake(time.Hour)
	window := &closeWindowFake{}
	h.intercept("desk", window, func(func(), func()) {}, application.NewWindowEvent())
	_, _, request := window.snapshot()

	if err := h.complete("desk", request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := h.complete("desk", request.RequestID); err != nil {
		t.Fatalf("duplicate completion: %v", err)
	}
	_, closes, _ := window.snapshot()
	if closes != 1 {
		t.Fatalf("close calls=%d, want 1", closes)
	}

	allowed := application.NewWindowEvent()
	h.intercept("desk", window, func(func(), func()) {}, allowed)
	if allowed.IsCancelled() {
		t.Fatal("the completion close was cancelled")
	}
	duplicate := application.NewWindowEvent()
	h.intercept("desk", window, func(func(), func()) {}, duplicate)
	if duplicate.IsCancelled() {
		t.Fatal("a duplicate completion close was cancelled")
	}
	h.finished("desk")

	blocked := application.NewWindowEvent()
	h.intercept("desk", window, func(func(), func()) {}, blocked)
	if !blocked.IsCancelled() {
		t.Fatal("a new native close was not held")
	}
	h.finished("desk")
}

func TestCloseHandshakeTimesOutAndOffersForceOrKeepOpen(t *testing.T) {
	h := newCloseHandshake(5 * time.Millisecond)
	window := &closeWindowFake{}
	prompted := make(chan struct{}, 1)
	var force, keep func()
	h.intercept("desk", window, func(onForce, onKeep func()) {
		force, keep = onForce, onKeep
		prompted <- struct{}{}
	}, application.NewWindowEvent())
	_, _, request := window.snapshot()

	select {
	case <-prompted:
	case <-time.After(time.Second):
		t.Fatal("close timeout did not present a choice")
	}
	if err := h.complete("desk", request.RequestID); !errors.Is(err, errCloseTimedOut) {
		t.Fatalf("late completion error=%v, want timeout", err)
	}
	if force == nil || keep == nil {
		t.Fatal("timeout did not provide both choices")
	}
	force()
	_, closes, _ := window.snapshot()
	if closes != 1 {
		t.Fatalf("force-close calls=%d, want 1", closes)
	}
	h.finished("desk")

	var keepChoice func()
	h.intercept("desk", window, func(_, onKeep func()) { keepChoice = onKeep }, application.NewWindowEvent())
	_, _, request = window.snapshot()
	if err := h.expire("desk", request.RequestID); err != nil {
		t.Fatal(err)
	}
	keepChoice()
	if err := h.complete("desk", request.RequestID); !errors.Is(err, errCloseRequestMissing) {
		t.Fatalf("kept-close completion error=%v, want missing request", err)
	}
	h.finished("desk")
}
