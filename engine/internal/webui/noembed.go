//go:build !embed_ui

// Package webui embeds the built UI into the binary for release builds (see
// embed.go, selected by the embed_ui build tag). This file backs the default
// build: no UI is embedded, so the binary's size and behavior are unchanged
// from before this package existed.
package webui

import "io/fs"

// Dist reports no embedded UI in the default build.
func Dist() (fs.FS, bool) { return nil, false }
