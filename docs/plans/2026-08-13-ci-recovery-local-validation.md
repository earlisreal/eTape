# CI recovery and local validation

Status: Approved on 2026-08-13.

## Goal

Restore every required GitHub Actions check to green, including failures that are currently hidden behind earlier failing steps, and make the same checks practical for agents to run on the Windows development machine before handing off substantial work.

Success means:

- the current UI and engine lint failures are fixed without weakening lint rules;
- the locally reproducible store-shutdown panic is fixed at its lifecycle root cause;
- all locally applicable checks from [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) pass on Windows;
- any check that cannot be run locally is named with its reason in the agent handoff; and
- one complete hosted CI run passes after the fixes are pushed.

## Non-goals

- Do not add a pre-push hook or make `git push` slower.
- Do not add a root validation wrapper or duplicate the workflow as another executable source of truth.
- Do not restructure CI to aggregate failures or change its step ordering in this effort.
- Do not redesign `store.Store`, change backfill behavior, or suppress formatter/linter findings.
- Do not require every small, isolated edit to run the full matrix; use proportional subsystem checks for those changes.
- Do not add a glossary entry or ADR. These are generic engineering practices and reversible implementation choices, not eTape domain language or durable architectural decisions.

## Current-code evidence

- GitHub Actions run [31684524139](https://github.com/earlisreal/eTape/actions/runs/31684524139) fails on three deterministic lint findings:
  - [`ui/src/render/chart/ChartController.ts`](../../ui/src/render/chart/ChartController.ts) binds an unused `_gap` while removing the optional `gap` field at the two synthetic-bar construction sites.
  - [`engine/internal/broker/alpaca/rest.go`](../../engine/internal/broker/alpaca/rest.go) retains an unused `doHTTP` wrapper after callers moved to `doHTTPWithHeaders`.
- The workflow runs engine full tests, short race tests, vet, golangci-lint 2.12.2, and generated TypeScript contract drift checks; it separately runs Windows engine tests and the Windows UI lint/test/build sequence. Because steps within each job are sequential, the current lint failures mask later checks.
- Local UI lint reproduces the two `_gap` failures. UI tests and build pass. Engine full tests and vet pass, and `mingw32-make -C engine gen-ts-check` passes.
- `golangci-lint` 2.12.2 is installed at `C:\Users\ching\go\bin\golangci-lint.exe`, and that directory is already on `PATH`. A pinned no-install fallback also works:

  ```powershell
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
  ```

- All tracked Go blobs are LF in Git, but this Windows worktree contains 327 CRLF Go files. [`.gitattributes`](../../.gitattributes) does not currently define Go line endings, so local golangci-lint reports checkout-induced `gofmt` noise that Linux CI does not.
- The following isolated race command fails deterministically with `send on closed channel` in `store.ArchiveDaily`:

  ```powershell
  Set-Location engine
  go test -race -short -count=1 -run '^TestSynthDemoBoot_EnsureSymbolWarmHistoryAndScannerConsistent$' ./internal/uihubtest
  ```

  It failed twice in two attempts, taking about 40 seconds per run. [`synth_demo_test.go`](../../engine/internal/uihubtest/synth_demo_test.go) starts `orch.Backfill` asynchronously, then lets the test return once warm history is visible. [`openStore`](../../engine/internal/uihubtest/e2e_test.go) closes the Store during cleanup without waiting for that worker. The worker subsequently sends to the closed `s.writes` channel.
- Production already encodes the required ownership order in [`engine/cmd/etape/main.go`](../../engine/cmd/etape/main.go): cancel, wait for `hub.Run` to stop producing backfill work, wait for the backfill workers, and only then close the Store.

## Design decisions

### Workflow authority

`.github/workflows/ci.yml` remains the executable source of truth. `AGENTS.md` and `README.md` provide a readable Windows command map, but explicitly defer to the workflow if the lists drift.

Agents must run every locally applicable workflow check after:

- executing an approved plan;
- implementing a feature or other substantial change;
- changing both engine and UI;
- changing CI, build configuration, dependencies, or generated contracts.

Small, isolated changes receive proportional checks. Every handoff lists the checks run and their results, and identifies every skipped required check with a reason. Hosted CI remains authoritative for the exact Ubuntu environment.

### Local lint tooling

Document a one-time pinned install of golangci-lint 2.12.2 for fast repeated use. Agents use the installed binary when `golangci-lint version` reports 2.12.2 and otherwise use the pinned `go run` fallback. Do not add golangci-lint and its large tool dependency graph to `engine/go.mod`, and do not require a global installer script.

### Go line endings

Declare `*.go text eol=lf`. Normalize only the worktree representation; the indexed Go content is already LF. Do not relax `gofmt` or golangci-lint to accommodate CRLF checkout noise.

### Store shutdown race

Repair the integration test's worker lifecycle rather than hardening Store writes after closure. Store's established contract requires owners to quiesce writers before `Close`, and production already follows it. Cancellation alone is insufficient because it can race the next channel send; the test must join the worker.

## File-level implementation

### 1. Establish LF Go worktree parity

- In [`.gitattributes`](../../.gitattributes), add `*.go text eol=lf` while retaining the existing shell-script rule.
- Before making Go source changes, verify the worktree has no unrelated Go edits. If it does, stop and isolate/preserve them before normalization.
- Rewrite tracked Go files through `gofmt` so the current Windows worktree becomes LF, then verify no Go-content diff was introduced. `git ls-files --eol '*.go'` should report LF for both index and worktree.
- Treat any substantive diff from normalization as unexpected and investigate it rather than staging it wholesale.

### 2. Repair deterministic lint failures

- In [`ChartController.ts`](../../ui/src/render/chart/ChartController.ts), replace both unused destructuring bindings with the smallest lint-clean way to copy the bar and delete its optional `gap` field. Preserve the semantic requirement that generated No-Trade Bars and Data Gaps do not inherit a prior bar's `gap` marker. Do not alter the global unused-variable rule.
- Reuse or add a focused assertion in [`ChartController.test.ts`](../../ui/src/render/chart/ChartController.test.ts) only if the existing cases do not prove that inherited `gap` is removed.
- In [`engine/internal/broker/alpaca/rest.go`](../../engine/internal/broker/alpaca/rest.go), delete the orphaned `doHTTP` wrapper. Keep `do`, `doWithHeaders`, and the live `doHTTPWithHeaders` request path unchanged.

### 3. Repair the integration-test lifecycle

- In [`engine/internal/uihubtest/synth_demo_test.go`](../../engine/internal/uihubtest/synth_demo_test.go):
  - add a local `sync.WaitGroup` for backfill workers;
  - call `Add(1)` synchronously in the injected backfill callback before starting its goroutine, and `defer Done()` inside the worker;
  - track `hub.Run` with a `hubDone` channel closed when the Hub exits;
  - register lifecycle cleanup after `openStore` so Go's LIFO cleanup order cancels the context, waits for `hubDone`, and waits for the backfill group before `openStore` closes the Store.
- Preserve the existing integration seam and assertions. Do not add a shallow test that expects Store writes after `Close` to be safe; that would encode the wrong ownership contract.
- Do not change [`engine/internal/store/store.go`](../../engine/internal/store/store.go), [`engine/internal/store/bars.go`](../../engine/internal/store/bars.go), or backfill archiving to hide the lifecycle error.

### 4. Document reproducible Windows validation

- Update the development section in [`README.md`](../../README.md) with:
  - the one-time pinned install and verification commands:

    ```powershell
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
    Get-Command golangci-lint
    golangci-lint version
    ```

  - a statement that the reported version must be 2.12.2;
  - the installed `golangci-lint run` command from `engine` and the pinned `go run ...@v2.12.2 run` fallback;
  - Windows equivalents for every locally applicable CI check, using `mingw32-make -C engine gen-ts-check` or its existing underlying Go command rather than requiring a command named `make`;
  - the existing MSYS2 UCRT64 prerequisite for native Windows race tests; and
  - a clear distinction between the CI-equivalent suite and additional proportional checks such as UI E2E.
- Update [`AGENTS.md`](../../AGENTS.md) with the substantial-change trigger, the CI-equivalent checklist, workflow authority, proportional-check exception, and mandatory disclosure of skipped checks. Keep the guidance concise and link to the README for setup details.

## Validation

Run the narrow checks first, then the complete local CI-equivalent suite so one failure does not hide another.

### Focused regression checks

From `engine`:

```powershell
go test -race -short -count=3 -run '^TestSynthDemoBoot_EnsureSymbolWarmHistoryAndScannerConsistent$' ./internal/uihubtest
golangci-lint run
```

From `ui`:

```powershell
npm run lint
npx vitest run --project chart-core src/render/chart/ChartController.test.ts
```

### Full Windows CI-equivalent checks

From the repository root:

```powershell
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run
Set-Location ..
mingw32-make -C engine gen-ts-check
Set-Location ui
npm ci
npm run lint
npm test
npm run build
Set-Location ..
git diff --check
```

Also verify:

- `golangci-lint version` reports 2.12.2;
- `git ls-files --eol '*.go'` reports LF worktree files;
- `ui/src/gen/wsmsg.ts` has no generated drift;
- no credentials, runtime database files, or unrelated worktree changes were introduced; and
- the complete GitHub Actions run is green after push. If hosted CI exposes an additional masked failure, reproduce it locally where possible, fix it, and repeat the full validation before declaring recovery.

## Rollout and rollback

- Land the lint fixes, lifecycle correction, line-ending rule, and documentation together so the documented validation path is true at merge time.
- No runtime feature flag or data migration is involved.
- If the lifecycle change causes a test shutdown timeout, revert only that test change and re-open diagnosis around worker ownership; do not weaken Store closure or skip the race suite.
- If `.gitattributes` creates unexpected content changes, restore the affected worktree files from their known LF index state while preserving unrelated edits, then investigate the pathspec/checkout behavior before retrying.

## Risks

- Race timing can vary. Repeating the focused test three times provides stronger evidence than one green run, but the full race suite and hosted Ubuntu run remain required.
- Installed linter versions can drift. The version check and pinned fallback prevent a newer local binary from silently becoming the validation standard.
- Documentation can drift from CI. Explicit workflow authority limits that risk; future CI changes must update the human and agent command maps when local execution changes.
- Bulk line-ending normalization can obscure unrelated edits. Perform it only from a verified clean Go worktree and inspect the diff immediately.
