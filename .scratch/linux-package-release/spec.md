# Linux package release

Implementation completed on 2026-09-13; first-release manual checks remain.

## Settled decisions

- Support the latest two Ubuntu LTS releases. As of 2026-09-13 these are
  Ubuntu 26.04 LTS and Ubuntu 24.04 LTS, per the
  [Ubuntu release cycle](https://ubuntu.com/about/release-cycle).
- Ship a portable `.tar.gz` archive containing the executable with its UI
  embedded, following the existing macOS release approach.
- Target amd64 only.
- Run from a terminal, open the default browser, and stop with Ctrl+C.
- Keep OpenD separately installed and configured; demo needs no OpenD.
- Add Linux to existing version-tag releases, gated by smoke tests on both
  supported Ubuntu versions. Advance the two-LTS window only after validating
  the new LTS.

## Current-code evidence

- [Release workflow](../../.github/workflows/release.yml) builds Windows amd64,
  macOS arm64, and Linux amd64, uploads archives, and publishes them for `v*`
  tags. Manual dispatch uploads artifacts without publishing a release.
- [Makefile](../../engine/Makefile) shares the UI embedding step and disables
  CGO for release builds, including the Linux release target.
- [CI](../../.github/workflows/ci.yml) already runs engine tests on Linux.
- Linux uses the console entrypoint, Unix single-instance locking and restart,
  and `xdg-open` for the browser. Runtime state stays under `~/.eTape/`.
- OpenD is a separate market-data dependency; demo mode requires no broker.

## Implementation plan

See [Linux package release](../../docs/plans/2026-09-13-linux-package-release.md)
for file-level steps, acceptance checks, rollout, and risks.

## Validation so far

- Added the Linux release target, version-safe release workflow staging, Ubuntu
  LTS smoke checks, and packaging documentation.
- Engine full tests, short race tests, vet, pinned lint, generated-contract
  drift check, UI clean install/lint/tests/build, and the engine build pass.
- The cross-compiled Linux amd64 archive passed the smoke check in Ubuntu 24.04
  WSL: executable mode, isolated demo boot, version/readiness logs, embedded
  index and JavaScript asset, and clean SIGTERM shutdown.
- The manual [GitHub release workflow run](https://github.com/earlisreal/eTape/actions/runs/34742374398)
  passed build, staging, artifact upload, and Linux smoke checks on Ubuntu
  24.04 and 26.04. Ubuntu Desktop browser/Ctrl+C checks remain pre-release
  manual checks.

## Comments

- 2026-09-13: User selected the last two Ubuntu LTS releases and accepted the
  recommendation to ship a portable archive.
- 2026-09-13: User accepted amd64, terminal/default-browser launch, separate
  OpenD, existing version-tag publication, and validation on both LTS releases.
