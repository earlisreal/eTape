//go:build wails

package desktop

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const MainWorkspaceID = "main"

// Host owns Wails-specific native lifecycle. Workspace persistence remains a
// separate concern: closing a window only removes its runtime identity.
type Host struct {
	app      *application.App
	registry *WorkspaceRegistry
	tray     *application.SystemTray
	icon     []byte
}

func NewHost() *Host {
	host := &Host{}
	host.registry = NewWorkspaceRegistry(func() {
		if host.tray != nil {
			host.tray.Show()
		}
	})
	return host
}

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

	_, err := h.registry.Open(id, func() NativeWindow {
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
		window.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			h.registry.Close(id)
		})
		window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
			window.Show().Focus()
		})
		return &wailsWindow{window: window}
	})
	return err
}

// CloseWorkspace is intentionally idempotent; a native close event may have
// already removed the registry entry before a UI action reaches this method.
func (h *Host) CloseWorkspace(id string) error {
	window, ok := h.registry.Get(id)
	if !ok {
		return nil
	}
	window.Close()
	h.registry.Close(id)
	return nil
}

func (h *Host) FocusMain() error { return h.OpenWorkspace(MainWorkspaceID) }

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
