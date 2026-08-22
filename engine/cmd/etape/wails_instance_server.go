//go:build wails && server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

type wailsInstance struct {
	options *application.SingleInstanceOptions
	release func() error
}

func prepareWailsInstance(func()) (wailsInstance, error) { return wailsInstance{}, nil }
