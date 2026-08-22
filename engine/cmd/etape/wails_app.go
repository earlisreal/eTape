//go:build wails

package main

import (
	"context"
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
	app := application.New(application.Options{
		Name:        "eTape",
		Description: "Local-first US-stock trading platform",
		Icon:        wailsTrayIcon,
		Services: []application.Service{
			application.NewService(host),
			application.NewService(&RuntimeService{runtime: runtime}),
		},
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

	// App.Context is the earliest public shutdown signal. Stop the application
	// gate independently of Wails cleanup; ServiceShutdown remains the final
	// wait so the next lifecycle phase can drain after this gate.
	go func() {
		<-app.Context().Done()
		_ = runtime.Stop(context.Background())
	}()
	app.OnShutdown(func() { _ = runtime.Stop(context.Background()) })

	return app, nil
}
