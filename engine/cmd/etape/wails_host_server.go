//go:build wails && server

package main

import (
	"github.com/earlisreal/eTape/engine/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func configureWailsHost(_ *application.App, _ *desktop.Host, _ []byte) error { return nil }
