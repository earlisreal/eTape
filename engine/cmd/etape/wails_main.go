//go:build wails && !server

package main

import (
	"log"
)

func main() {
	app, err := newWailsApp()
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
