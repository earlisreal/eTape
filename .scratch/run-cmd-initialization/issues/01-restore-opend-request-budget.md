# Restore OpenD request budget for run.cmd

Type: task
Status: resolved

`run.cmd` builds the UI, then fails to compile the engine with
`client.go:156:102: undefined: hasRollingRequestLimit`.

## Comments

- `go build ./cmd/etape` reproduced the exact compiler error.
- An uncommitted edit to `request_budget.go` removed the helper still used by
  `Client.Request`, combined independent quota/metadata gates, and reverted
  the pacer's subscription cooldown exemption.
- Restored the existing committed implementation, including its shared helper
  and both pacing callers. Existing OpenD regression tests cover subscription
  startup, independent quota gates, and persisted rolling-request cooldowns.
  No launcher changes or new helper implementation are needed.
- Preserved unrelated local files. The original request-budget diff is backed
  up in the system temporary directory. Since the correct code was already in
  `main`, only this recovery record introduces a new tracked change.

## Validation

Passed on Windows:

- `go build ./cmd/etape`
- `run.cmd live -h`: UI typecheck/build and engine compile/entrypoint succeed;
  exits zero without connecting to live providers.
- `go test ./internal/feed/opend -count=1`
- `go test -race -short ./internal/feed/opend -count=1` with the existing
  MinGW UCRT64 compiler
- `go test ./...`
- `go vet ./...`
- `golangci-lint run` (2.12.2, zero issues)
- `git diff --check`; tracked Go files retain LF line endings; the generated
  WebSocket contract has no diff.

The first sandboxed test runs failed on blocked atomic temporary-file renames
and timing-sensitive tests under concurrent build load. OpenD tests and the
full engine suite passed with normal temporary-file access; the OpenD run
bypassed the test cache.

Proportional validation applies to this isolated restoration. Full-engine race
tests, contract regeneration, and UI install/lint/unit/E2E checks were not
required locally because no new engine implementation, contract, dependency,
or UI change remains. Live engine/provider initialization was not exercised;
the launcher smoke check intentionally stops at engine flag handling.
Hosted CI must pass after the recovery record is merged and pushed.
