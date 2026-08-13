# eTape Agent Guide

Local-first US-stock trading platform: Go engine plus TypeScript/React/Vite UI. moomoo OpenD supplies primary market data; sim, Alpaca, TradeZero, and moomoo adapters execute orders.

## Guides

- [Codebase and user guide](README.md)
- [Engine](engine/README.md), [UI](ui/README.md), [external APIs](docs/external-apis.md)
- [Documentation](docs/README.md), [plans](docs/plans/README.md), [prototypes](prototypes/README.md), [scripts](scripts/README.md)

## Agent skills

### Issue tracker

Issues are tracked as local Markdown files under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Issue status uses the five default canonical triage roles. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context domain layout. See `docs/agents/domain.md`.

## Invariants

- High-frequency data never flows through React state. Stores/controllers mutate imperatively; canvas/chart rendering coalesces work.
- Go types in `engine/internal/uihub/wsmsg` own WebSocket contract. `ui/src/gen/wsmsg.ts` is generated; never edit it.
- TICKER ticks build exchange-time 10-second bars. One-minute K-lines feed larger intraday bars. Daily is fetched; weekly/monthly derive from daily.
- OpenD uses raw TCP plus protobuf at configured address. Trade unlock occurs in OpenD GUI, not engine.
- Credentials/config/database stay under `~/.eTape/`; never commit sensitive runtime data.
- Every executed plan updates relevant READMEs when flow, interfaces, dependencies, invariants, or operations change.

## Commands

```bash
cd engine && go build ./cmd/etape
cd engine && go test ./...
cd engine && mingw32-make gen-ts-check
cd ui && npm test
cd ui && npm run typecheck
cd ui && npm run e2e
```

## Validation

After an approved plan, a substantial change, changes spanning engine and UI,
or changes to CI, build configuration, dependencies, or generated contracts,
run the CI-equivalent Windows checklist in [README.md](README.md#ci-equivalent-validation-on-windows).
The workflow at [.github/workflows/ci.yml](.github/workflows/ci.yml) is the
executable source of truth if the command lists drift. Small isolated changes
may use proportional subsystem checks. Every handoff must list checks run and
results, plus every skipped required check and its reason; hosted CI must still
complete successfully.

## Live-order safety

Never place, modify, or cancel real orders unless Earl explicitly authorizes current session and reconfirms live run. Read-only account checks allowed. Authorized live leg: RTH only, cheap liquid symbol, long only, one-share marketable limit, flatten immediately.

## Git

Keep commits scoped. Main hook escape only when task explicitly authorizes it. Preserve unrelated changes. Approved specs use `docs(specs):`; plans use `docs(plans):`.

After executing a plan or addressing review comments, automatically commit the resulting changes and push directly to main branch, but only on weekends or on weekdays after 3:00 PM Philippines time (Asia/Manila). Skip this auto-commit and auto-push rule for small, specific tasks unless explicitly requested. Whenever the rule applies, push immediately after the commit succeeds.
