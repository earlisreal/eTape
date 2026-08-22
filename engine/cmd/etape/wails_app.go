//go:build wails

package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/earlisreal/eTape/engine/internal/desktop"
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
	app := application.New(application.Options{
		Name:           "eTape",
		Description:    "Local-first US-stock trading platform",
		Icon:           wailsTrayIcon,
		Services:       []application.Service{application.NewService(host)},
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
	return app, nil
}
