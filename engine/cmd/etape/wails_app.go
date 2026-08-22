//go:build wails

package main

import (
	"fmt"
	"os"

	"github.com/earlisreal/eTape/engine/internal/webui"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func newWailsApp() (*application.App, error) {
	assets, embedded := webui.Dist()
	if !embedded && os.Getenv("FRONTEND_DEVSERVER_URL") == "" {
		return nil, fmt.Errorf("embedded UI is missing; run the pinned Wails build or start with `go tool wails3 dev`")
	}

	return application.New(application.Options{
		Name:        "eTape",
		Description: "Local-first US-stock trading platform",
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	}), nil
}
