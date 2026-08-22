//go:build wails

package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/earlisreal/eTape/engine/internal/desktop"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/earlisreal/eTape/engine/internal/webui"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed assets/etape.ico
var wailsTrayIcon []byte

func newWailsApp() (*application.App, error) {
	assets, embedded := webui.Dist()
	if !embedded && os.Getenv("FRONTEND_DEVSERVER_URL") == "" {
		return nil, fmt.Errorf("embedded UI is missing; run the pinned Wails build or start with `go tool wails3 dev`")
	}

	host := desktop.NewHost()
	instance, err := prepareWailsInstance(func() { _ = host.FocusMain() })
	if err != nil {
		return nil, err
	}
	runtime := wailsruntime.New()
	_ = runtime.RegisterWorkspace("main")
	app := application.New(application.Options{
		Name:        "eTape",
		Description: "Local-first US-stock trading platform",
		Icon:        wailsTrayIcon,
		Services: []application.Service{
			application.NewService(&RuntimeService{runtime: runtime}),
		},
		OnShutdown:     runtime.Gate().BeginStop,
		SingleInstance: instance.options,
		PostShutdown: func() {
			if instance.release != nil {
				_ = instance.release()
			}
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			UseVisualHosting:              false,
		},
	})
	if err := configureWailsHost(app, host, wailsTrayIcon); err != nil {
		if instance.release != nil {
			_ = instance.release()
		}
		return nil, err
	}
	app.HandleStream(runtimeStreamName, runtime.HandleStream)
	go dispatchRuntimeHints(app, runtime)

	return app, nil
}

func dispatchRuntimeHints(app *application.App, runtime *wailsruntime.Runtime) {
	for {
		select {
		case <-app.Context().Done():
			return
		case <-runtime.HintWake():
			for {
				if app.Context().Err() != nil {
					return
				}
				hint, ok := runtime.PopHint()
				if !ok {
					break
				}
				event, ok := hint.Data.(RuntimeEvent)
				if ok {
					_ = app.Event.Emit(runtimeHintEvent, event)
				}
			}
		}
	}
}
