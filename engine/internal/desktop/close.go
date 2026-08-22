//go:build wails

package desktop

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const workspaceCloseRequestEvent = "etape:workspace-close-requested"

var (
	errCloseTimedOut       = errors.New("desktop: workspace close timed out")
	errCloseRequestMissing = errors.New("desktop: workspace close request is no longer pending")
)

type workspaceCloseRequest struct {
	WorkspaceID string `json:"workspaceId"`
	RequestID   string `json:"requestId"`
	TimeoutMS   int    `json:"timeoutMs"`
}

type closeWindow interface {
	EmitEvent(string, ...any) bool
	Close()
}

type closePending struct {
	window    closeWindow
	requestID string
	prompt    func(func(), func())
	timer     *time.Timer
	timedOut  bool
	allowNext bool
	closed    bool
}

type closeHandshake struct {
	mu      sync.Mutex
	timeout time.Duration
	next    uint64
	pending map[string]*closePending
}

func newCloseHandshake(timeout time.Duration) *closeHandshake {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &closeHandshake{timeout: timeout, pending: make(map[string]*closePending)}
}

func (h *closeHandshake) intercept(id string, window closeWindow, prompt func(func(), func()), event *application.WindowEvent) {
	h.mu.Lock()
	if pending := h.pending[id]; pending != nil && (pending.allowNext || pending.closed) {
		pending.allowNext = false
		h.mu.Unlock()
		return
	}
	if h.pending[id] != nil {
		h.mu.Unlock()
		event.Cancel()
		return
	}
	h.next++
	requestID := strconv.FormatUint(h.next, 10)
	pending := &closePending{window: window, requestID: requestID, prompt: prompt}
	pending.timer = time.AfterFunc(h.timeout, func() { _ = h.expire(id, requestID) })
	h.pending[id] = pending
	h.mu.Unlock()

	event.Cancel()
	window.EmitEvent(workspaceCloseRequestEvent, workspaceCloseRequest{
		WorkspaceID: id,
		RequestID:   requestID,
		TimeoutMS:   int(h.timeout / time.Millisecond),
	})
}

func (h *closeHandshake) complete(id, requestID string) error {
	h.mu.Lock()
	pending := h.pending[id]
	if pending == nil || pending.requestID != requestID {
		h.mu.Unlock()
		return errCloseRequestMissing
	}
	if pending.timedOut {
		h.mu.Unlock()
		return errCloseTimedOut
	}
	if pending.closed {
		h.mu.Unlock()
		return nil
	}
	pending.closed = true
	pending.allowNext = true
	if pending.timer != nil {
		pending.timer.Stop()
	}
	window := pending.window
	h.mu.Unlock()
	window.Close()
	return nil
}

func (h *closeHandshake) expire(id, requestID string) error {
	h.mu.Lock()
	pending := h.pending[id]
	if pending == nil || pending.requestID != requestID {
		h.mu.Unlock()
		return errCloseRequestMissing
	}
	if pending.timedOut || pending.closed {
		h.mu.Unlock()
		return nil
	}
	pending.timedOut = true
	prompt := pending.prompt
	h.mu.Unlock()
	if prompt != nil {
		prompt(
			func() { h.force(id, requestID) },
			func() { h.keepOpen(id, requestID) },
		)
	}
	return nil
}

func (h *closeHandshake) force(id, requestID string) {
	h.mu.Lock()
	pending := h.pending[id]
	if pending == nil || pending.requestID != requestID || !pending.timedOut || pending.closed {
		h.mu.Unlock()
		return
	}
	pending.closed = true
	pending.allowNext = true
	window := pending.window
	h.mu.Unlock()
	window.Close()
}

func (h *closeHandshake) keepOpen(id, requestID string) {
	h.mu.Lock()
	pending := h.pending[id]
	if pending != nil && pending.requestID == requestID && pending.timedOut && !pending.closed {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

func (h *closeHandshake) finished(id string) {
	h.mu.Lock()
	if pending := h.pending[id]; pending != nil {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

func closeRequestTimeoutMessage(timeout time.Duration) string {
	return fmt.Sprintf("The workspace renderer did not confirm a durable save within %s.", timeout.Round(time.Second))
}
