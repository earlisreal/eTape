//go:build wails && !server

package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	app, err := newWailsApp()
	if err != nil {
		log.Fatal(err)
	}

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "eTape",
		URL:    "/",
		Width:  1600,
		Height: 1000,
	}).Show()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
