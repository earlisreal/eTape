//go:build wails

package desktop

import (
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uistate"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const MainWorkspaceID = "main"

const workspaceCloseTimeout = 3 * time.Second

// Host owns Wails-specific native lifecycle. Workspace persistence remains a
// separate concern: the native close hook holds disposal until the renderer
// confirms a durable save or the user explicitly chooses force close.
type Host struct {
	app      *application.App
	state    *uistate.Store
	registry *uistate.WindowRegistry
	tray     *application.SystemTray
	icon     []byte
	close    *closeHandshake
	cleanup  func(string)
}

func NewHost(states ...*uistate.Store) *Host {
	state := uistate.NewRuntimeStore()
	if len(states) > 0 && states[0] != nil {
		state = states[0]
	}
	host := &Host{state: state, registry: state.Windows(), close: newCloseHandshake(workspaceCloseTimeout)}
	host.registry.SetOnEmpty(func() {
		if host.tray != nil {
			host.tray.Show()
		}
	})
	return host
}

// SetWorkspaceCleanup installs the runtime cleanup that follows native window
// disposal. The persistent Workspace identity is intentionally retained.
func (h *Host) SetWorkspaceCleanup(cleanup func(string)) { h.cleanup = cleanup }

func (h *Host) Attach(app *application.App, icon []byte) error {
	if app == nil {
		return errors.New("desktop: nil Wails app")
	}
	if h.app != nil && h.app != app {
		return errors.New("desktop: host already attached")
	}
	h.app, h.icon = app, icon
	if h.tray != nil {
		return nil
	}

	menu := application.NewMenu()
	menu.Add("Open Main").OnClick(func(*application.Context) { _ = h.OpenWorkspace(MainWorkspaceID) })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { h.Quit() })

	h.tray = app.SystemTray.New()
	h.tray.SetIcon(h.icon)
	h.tray.SetTooltip("eTape")
	h.tray.SetMenu(menu)
	h.tray.OnClick(func() { _ = h.OpenWorkspace(MainWorkspaceID) })
	return nil
}

func (h *Host) Start() error {
	if h.app == nil {
		return errors.New("desktop: host is not attached")
	}
	return h.OpenWorkspace(MainWorkspaceID)
}

// OpenWorkspace is idempotent. A repeated request activates the existing
// Native Window instead of creating a second WebView for the same identity.
func (h *Host) OpenWorkspace(id string) error {
	if err := ValidateWorkspaceID(id); err != nil {
		return err
	}
	if h.app == nil {
		return errors.New("desktop: host is not attached")
	}

	_, err := h.state.OpenWorkspace(id, func() NativeWindow {
		window := h.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:      WindowName(id),
			Title:     fmt.Sprintf("eTape — %s", id),
			URL:       "/?workspace=" + url.QueryEscape(id),
			Width:     1600,
			Height:    1000,
			MinWidth:  720,
			MinHeight: 480,
			Hidden:    true,
			Frameless: true,
			Windows: application.WindowsWindow{
				NonClientRegionSupport:     true,
				WebView2CompositionHosting: false,
			},
		})
		var closeOnce sync.Once
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			// App shutdown closes native windows after cancelling the app context;
			// that path must not be trapped by the workspace handshake.
			if h.app.Context().Err() != nil {
				return
			}
			h.close.intercept(id, window, func(force, keep func()) {
				h.showCloseTimeoutDialog(window, force, keep)
			}, event)
		})
		window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			h.close.finished(id)
			closeOnce.Do(func() {
				h.state.CloseWorkspace(id)
				if h.cleanup != nil {
					h.cleanup(id)
				}
			})
		})
		window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
			window.Show().Focus()
		})
		return &wailsWindow{window: window}
	})
	return err
}

// CloseWorkspace starts the same guarded native close path used by Alt+F4.
// Persistence is finalized by the WindowClosing hook after the UI ack.
func (h *Host) CloseWorkspace(id string) error {
	window, ok := h.registry.Get(id)
	if !ok {
		return nil
	}
	window.Close()
	return nil
}

func (h *Host) CompleteWorkspaceClose(id, requestID string) error {
	return h.close.complete(id, requestID)
}

func (h *Host) showCloseTimeoutDialog(window *application.WebviewWindow, force, keep func()) {
	if h.app == nil {
		keep()
		return
	}
	dialog := h.app.Dialog.Question().
		SetTitle("Workspace close is waiting").
		SetMessage(closeRequestTimeoutMessage(h.close.timeout))
	dialog.AddButton("Force close").OnClick(force)
	keepButton := dialog.AddButton("Keep open").OnClick(keep)
	dialog.SetDefaultButton(keepButton).SetCancelButton(keepButton).AttachToWindow(window).Show()
}

func (h *Host) FocusMain() error { return h.OpenWorkspace(MainWorkspaceID) }

func (h *Host) FocusWorkspace(id string) error {
	if h.app == nil {
		return errors.New("desktop: host is not attached")
	}
	return h.state.FocusWorkspace(id)
}

func (h *Host) Quit() {
	if h.app != nil {
		h.app.Quit()
	}
}

func (h *Host) ServiceName() string { return "desktop.Host" }

type wailsWindow struct{ window *application.WebviewWindow }

func (w *wailsWindow) Show()             { w.window.Show() }
func (w *wailsWindow) Focus()            { w.window.Focus() }
func (w *wailsWindow) Restore()          { w.window.Restore() }
func (w *wailsWindow) IsMinimised() bool { return w.window.IsMinimised() }
func (w *wailsWindow) Close()            { w.window.Close() }
