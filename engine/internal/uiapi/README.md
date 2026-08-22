# UI API

`uiapi` owns the low-rate Wails query and non-execution mutation contract. Go
models in `models.go` are the source of truth; `go tool wails3 generate
bindings` writes the TypeScript service under `ui/src/gen/wails/.../uiapi`,
while `tygo` continues to generate the Workspace Stream contract under
`ui/src/gen/wsmsg.ts`.

`EngineService` is the registered singleton for chart-window, fills,
cycle-fills, locate eligibility/quotes/records, export queries, scanner
filters, watchlist membership, venue setup, credentials, and read-only
connection tests.
`WorkspaceService` is registered as the workspace-scoped singleton; Stream
subscriptions, demands, indicators, snapshots, and updates remain on
`uihub.Server.HandleWailsStream`.

Each generated EngineService method enters `wailsruntime.Runtime`'s shared
admission gate before reading the configured store, Hub, or locate provider.
Expected validation and provider/business outcomes are typed result values;
storage, CSV, unavailable-service, and bridge failures reject the binding.
Mutating results carry a source revision when shared state changes; scanner
and watchlist Stream payloads carry the same revision so the UI can ignore
stale cross-lane updates. Credential methods accept write-only secret material
and never return it; venue setup returns credential names only. The migrated
scanner, watchlist, venue, credential, and connection cases are not generic
Stream commands. Generic Stream configuration and execution operations remain
until their Workspace or safety prerequisites land.

Regenerate both contracts from `engine/`:

```text
go tool tygo generate
go tool wails3 generate bindings -ts -i -clean=true -d ../ui/src/gen/wails -f "-tags wails" ./cmd/etape
```

Never hand-edit files under `ui/src/gen`; run the generated-contract drift
checks after a clean regeneration and run `npm run typecheck`.
