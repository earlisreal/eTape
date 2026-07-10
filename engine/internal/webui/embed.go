//go:build embed_ui

// Package webui embeds the built React/Vite UI (ui/dist) into the etape
// binary for release builds. This file is selected only when the embed_ui
// build tag is passed; the release Makefile target (Task 5) populates
// dist/ by copying ui/dist/ in before compiling with that tag. The default
// build compiles noembed.go instead, whose Dist always reports absent.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded UI filesystem rooted at dist/, and true if it is
// present. The bool return lets callers fall back to an external -dist
// override or, in the default build, to no static serving at all.
func Dist() (fs.FS, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// "dist" is a fixed, valid fs.Sub path, so this should be
		// unreachable; treat it as "no embedded UI" rather than panicking.
		return nil, false
	}
	return sub, true
}
