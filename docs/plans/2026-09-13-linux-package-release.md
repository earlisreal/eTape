# Linux package release

Scope agreed on 2026-09-13; implementation has not started.
Source: [design interview](../../.scratch/linux-package-release/spec.md).

## Goal and scope

Publish `eTape-<version>-linux-amd64.tar.gz` alongside the existing Windows and
macOS release archives. It contains an executable `etape-linux-amd64` with the
production UI embedded. Users extract it, run `./etape-linux-amd64` in a terminal,
and stop it with Ctrl+C. The existing browser opener opens the local UI; users
can open the logged URL manually if browser launch fails.

Support Ubuntu 24.04 LTS and 26.04 LTS on amd64: the latest two LTS releases
as of this plan, verified against the [Ubuntu release cycle](https://ubuntu.com/about/release-cycle).
Advance this explicitly tested pair after validating each new LTS, rather than
automatically claiming support on its release date.

Keep runtime state under `~/.eTape/`. OpenD remains separately installed and
configured; the portable archive does not bundle it. Demo mode needs no OpenD.

Non-goals: arm64, deb/rpm packages, package repositories, AppImage, desktop/tray
integration, system services, auto-update, signing infrastructure, or changes to
trading behavior. Other Linux distributions are outside the tested support claim.

## Current-code evidence

- [engine/Makefile](../../engine/Makefile) already shares UI embedding between
  CGO-disabled Windows and macOS builds and stamps `buildinfo.Version`.
- [Release workflow](../../.github/workflows/release.yml) already builds on an
  Ubuntu runner, stages archives, uploads workflow artifacts, and publishes on
  `v*` tags. Manual dispatch only uploads artifacts.
- Release uses Node 20, while [ui/package.json](../../ui/package.json) requires
  Node >=24 and [CI](../../.github/workflows/ci.yml) uses Node 24.
- [Console entrypoint](../../engine/cmd/etape/run_default.go) handles interrupt
  and SIGTERM. Existing Unix adapters supply locking and process restart.
- [Browser launcher](../../engine/internal/openbrowser/README.md) uses
  `xdg-open` on Linux and logs non-fatal launch failures.
- Existing Linux CI tests engine code, but does not validate a packaged Linux
  executable on both supported Ubuntu versions.

## File-level implementation

1. **engine/Makefile:** add `release-linux` to `.PHONY` and release comments.
   Reuse `embed-ui`; build with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, the
   `embed_ui` tag, and existing stripped/version-stamped linker flags. Output
   `dist/etape-linux-amd64`. Keep the shared UI build once per make invocation.
2. **.github/workflows/release.yml:** align Node with CI at 24; include
   `release-linux` in the existing build invocation. Stage a tarball with the
   executable bit preserved and add it to upload/publication paths. Use a
   shell environment variable for the ref/version rather than interpolating
   GitHub ref text into shell code. Ensure manual-dispatch filenames also work
   for branch refs containing slashes.
3. **scripts/smoke-linux-release.sh:** add one small shell smoke check taking
   the archive path. Extract into a temporary directory, verify the expected
   executable, isolate HOME and working directory from the source checkout,
   launch with `-demo -no-open`, and wait with a bounded timeout. Check the
   expected version in startup logs, demo readiness, HTTP index, and a referenced
   embedded JS asset. Send SIGTERM and require clean exit. Always clean up the
   process and temporary state, preserving failure logs in CI output.
4. **.github/workflows/release.yml:** run that check against the same staged
   archive in Ubuntu 24.04 and 26.04 amd64 containers before upload/publication.
   Install only test utilities such as curl and CA certificates in containers;
   do not provide Go, Node, source files, or UI dist to the tested app. Mount
   the archive and test script read-only. Failure in either version blocks the
   release; no new release matrix/framework is needed for two serial checks.
5. **README.md, engine/README.md, scripts/README.md:** document the artifact,
   supported versions/architecture, extraction and terminal launch, default
   browser/manual URL fallback, Ctrl+C shutdown, separate OpenD prerequisite,
   and build/smoke commands. Document manual upgrades as stop, replace the
   executable, restart; keep `~/.eTape/` intact.

## Acceptance and validation

- Build all three release targets together; verify Windows and macOS artifacts
  still stage under their existing names and Linux contains its executable.
- Both Ubuntu container checks pass against the exact archive to be published.
  An extracted binary works without the source tree or Go/Node installations.
- The script fails for a missing/non-executable binary, failed startup, wrong
  version, missing embedded asset, readiness timeout, or unclean shutdown.
- Before first release, manually verify default-browser opening, demo UI, and
  Ctrl+C shutdown on Ubuntu Desktop 24.04 and 26.04. Container HTTP checks do
  not establish desktop browser behavior. No real orders are needed.
- Run the full [CI-equivalent Windows checklist](../../README.md#ci-equivalent-validation-on-windows)
  because this changes build/release configuration: engine full tests, short
  race tests, vet, pinned lint, generated-contract check; UI clean dependency
  install, lint, tests, and build/typecheck; `git diff --check`, Go LF check, and
  review for generated drift or sensitive runtime files. Also build the engine.
- Verify workflow YAML and shell syntax, then run a manual release workflow
  before the first version tag. Hosted CI must pass. Report every skipped
  required check and its reason; do not equate cross-compilation with runtime
  validation. UI E2E is additional if implementation touches app behavior.

## Rollout and rollback

Implement and validate, update the guides, then commit and push to main under
the repository's executed-plan rule. Manual dispatch verifies downloadable
artifacts without publication. The next explicitly authorized version tag adds
Linux to the normal release. This planning task does not create a tag.

If validation fails, fix it before tagging. If a published Linux archive is
defective, withdraw that asset and publish a corrected version; retain the
Windows/macOS assets. Reverting these packaging changes removes the Linux
target without a data migration. Binary downgrades are safe only when runtime
data remains compatible with the older version.

## Risks and constraints

- The local cross-build probe failed before compilation due to a missing Go
  stdlib package and build-cache permissions. Fix the local environment or
  validate in Linux CI; build success has not yet been demonstrated here.
- Container checks cover Ubuntu userspace on the runner's kernel, not full
  desktop installations. Desktop validation remains a separate check.
- OpenD compatibility and credentials are external setup concerns. Demo smoke
  checks establish package startup, not broker connectivity or live execution.
- One Linux smoke failure gates the existing combined release workflow. This
  intentionally keeps publication simple and prevents an incomplete release.
