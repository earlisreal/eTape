//go:build wails && !server

package main

import (
	"context"
	"os"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestWailsDesktopCallerIdentityUsesNativeWindow(t *testing.T) {
	t.Setenv("FRONTEND_DEVSERVER_URL", "http://127.0.0.1:1")
	previousArgs := os.Args
	os.Args = []string{"etape", "-profile", "test", "-data-root", t.TempDir()}
	defer func() { os.Args = previousArgs }()
	app, err := newWailsApp()
	if err != nil {
		t.Fatalf("new Wails app: %v", err)
	}
	defer func() {
		app.Quit()
		if callback := app.Config().PostShutdown; callback != nil {
			callback()
		}
	}()
	if wailsruntime.ServerMode {
		t.Fatal("desktop build selected server mode")
	}

	var service *RuntimeService
	for _, candidate := range app.Config().Services {
		if runtimeService, ok := candidate.Instance().(*RuntimeService); ok {
			service = runtimeService
			break
		}
	}
	if service == nil {
		t.Fatal("RuntimeService was not registered")
	}
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "capability-test"})
	ctx := context.WithValue(context.Background(), application.WindowKey, application.Window(window))

	capabilities, err := service.Capabilities(ctx)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if !capabilities.BindingHasWindow {
		t.Fatal("binding context did not retain the native window")
	}

	token, err := service.OpenStreamSession(ctx, "alpha")
	if err != nil {
		t.Fatalf("open stream session: %v", err)
	}
	if err := service.runtime.ValidateSession(wailsruntime.StreamHello{
		Protocol:    wailsruntime.StreamProtocol,
		WorkspaceID: "alpha",
		Session:     token,
	}, uint64(window.ID())); err != nil {
		t.Fatalf("validate desktop stream owner: %v", err)
	}
}
