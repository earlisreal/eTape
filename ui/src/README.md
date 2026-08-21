# UI Source

`main.tsx` boots shell. Wire data routes into imperative stores; chrome composes panels; renderers paint chart/ladder/tape; sound handles fill cues.

The feed status banner follows `feed-up`/`feed-down` events and non-empty live
`md.tape` deltas; periodic `sys.health` RTT failures are diagnostic only.

Children: [chrome](chrome/README.md), [stores](data/README.md), [renderers](render/README.md), [sound](sound/README.md), [wire](wire/README.md), [performance](perf/README.md). `gen/` is generated. Test: `npm test`; typecheck: `npm run typecheck`.
