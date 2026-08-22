# UI API

`uiapi` owns the low-rate Wails query contract. Go models in `models.go` are
the source of truth; `go tool wails3 generate bindings` writes the read-only
TypeScript service under `ui/src/gen/wails/.../uiapi`, while `tygo` continues
to generate the Workspace Stream contract under `ui/src/gen/wsmsg.ts`.

`EngineService` is the registered singleton for chart-window, fills,
cycle-fills, locate eligibility/quotes/records, and export queries.
`WorkspaceService` is registered as the workspace-scoped singleton. It owns
typed catalog/document snapshots plus create, rename, delete, load, save,
open, focus, close, and durable-flush operations. Stream subscriptions,
demands, indicators, snapshots, and updates remain on
`uihub.Server.HandleWailsStream`.

Each generated EngineService method enters `wailsruntime.Runtime`'s shared
admission gate before reading the configured store, Hub, or locate provider.
Expected validation and provider/business outcomes are typed result values;
storage, CSV, unavailable-service, and bridge failures reject the binding.
The browser bridge may retain its legacy config adapter for non-Wails server
mode, but Wails boot makes Go's WorkspaceService the catalog/document/window
authority. Business conflicts are typed blocked results; only unavailable
storage or bridge failures reject the binding.

Regenerate both contracts from `engine/`:

```text
go tool tygo generate
go tool wails3 generate bindings -ts -i -clean=true -d ../ui/src/gen/wails -f "-tags wails" ./cmd/etape
```

Never hand-edit files under `ui/src/gen`; run the generated-contract drift
checks after a clean regeneration and run `npm run typecheck`.
