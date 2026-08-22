# Workspace state

`uistate.Store` is the canonical low-rate authority for the persistent
Workspace catalog, bounded opaque Dockview documents, document/catalog
revisions, open Workspace identities, and the one-Native-Window registry.
Mutations are serialized under one mutex and reuse the existing config-store
`GetConfig`/`SetConfig`/`DeleteConfig` surface. Saves update the in-memory
document and revision immediately; `Flush` is the explicit durable barrier
when the persistence adapter supports it.

Go validates Workspace identity, display-name rules, document identity, JSON
size, layout size, and expected revisions. It deliberately does not interpret
Dockview layout, panel groups, or frontend settings. Main and Monitoring are
reserved; closing removes only the runtime open identity, while deleting an
open Workspace is rejected.

The store emits catalog/document invalidations for `uihub`'s owning-stream
lane. Durable close acknowledgement, window geometry, and crash restoration
remain later lifecycle tickets.
