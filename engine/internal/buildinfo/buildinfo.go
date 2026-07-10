// Package buildinfo exposes basic engine identity, and exists so the module
// has a real package to build, test, and lint from the first commit.
package buildinfo

// Name is the engine binary's canonical name.
const Name = "etape-engine"

// Version is the engine binary's version string, overridden at release-build
// time via `-ldflags "-X github.com/earlisreal/eTape/engine/internal/buildinfo.Version=..."`
// (a `var`, not `const`, since -ldflags -X can only override package-level
// string vars). The default build (no ldflags) reports "dev".
var Version = "dev"
