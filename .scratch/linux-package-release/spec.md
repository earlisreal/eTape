# Linux package release

Draft: design interview in progress; implementation is not approved.

## Settled decisions

- Support the latest two Ubuntu LTS releases. As of 2026-09-13 these are
  Ubuntu 26.04 LTS and Ubuntu 24.04 LTS, per the
  [Ubuntu release cycle](https://ubuntu.com/about/release-cycle).
- Ship a portable `.tar.gz` archive containing the executable with its UI
  embedded, following the existing macOS release approach.

## Current-code evidence

- [Release workflow](../../.github/workflows/release.yml) builds Windows amd64
  and macOS arm64, uploads archives, and publishes them for `v*` tags. Manual
  dispatch uploads artifacts without publishing a release.
- [Makefile](../../engine/Makefile) shares the UI embedding step and disables
  CGO for release builds; there is no Linux release target yet.
- [CI](../../.github/workflows/ci.yml) already runs engine tests on Linux.
- Linux uses the console entrypoint, Unix single-instance locking and restart,
  and `xdg-open` for the browser. Runtime state stays under `~/.eTape/`.
- OpenD is a separate market-data dependency; demo mode requires no broker.

## Open decisions

- CPU architectures: amd64 only or amd64 plus arm64.
- Launch experience: existing terminal process and default browser, or added
  desktop integration.
- Whether OpenD remains separately installed/configured by the user.
- Publication and validation requirements, including tests on both LTS versions.
- How the supported versions advance when a future Ubuntu LTS is released.

## Validation so far

- Read-only repository inspection; no implementation changes or runtime tests.
- Investigator attempted a Linux amd64 cross-build but the local Go environment
  failed before compilation (missing `internal/runtime/cgroup` and build-cache
  access denied). Linux build success remains unverified.

## Comments

- 2026-09-13: User selected the last two Ubuntu LTS releases and accepted the
  recommendation to ship a portable archive.
