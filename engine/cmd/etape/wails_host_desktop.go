//go:build wails && !server

package main

import (
	"github.com/earlisreal/eTape/engine/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func configureWailsHost(app *application.App, host *desktop.Host, icon []byte) error {
	if err := host.Attach(app, icon); err != nil {
		return err
	}
	return host.Start()
}
