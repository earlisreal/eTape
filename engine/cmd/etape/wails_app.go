//go:build wails

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
	"github.com/earlisreal/eTape/engine/internal/webui"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func newWailsApp() (*application.App, error) {
	assets, embedded := webui.Dist()
	if !embedded && os.Getenv("FRONTEND_DEVSERVER_URL") == "" {
		return nil, fmt.Errorf("embedded UI is missing; run the pinned Wails build or start with `go tool wails3 dev`")
	}

	runtime := wailsruntime.New()
	app := application.New(application.Options{
		Name:        "eTape",
		Description: "Local-first US-stock trading platform",
		Services: []application.Service{
			application.NewService(&RuntimeService{runtime: runtime}),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})
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
