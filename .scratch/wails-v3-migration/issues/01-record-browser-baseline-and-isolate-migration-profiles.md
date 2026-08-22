# 01 — Record browser baseline and isolate migration profiles

**What to build:** Establish a reproducible browser-host performance baseline and safe runtime-profile defaults so the final Wails result can be compared on the same Windows machine without development, testing, replay, prototypes, or server mode ever touching the trader's real profile by default.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] A deterministic fixture records the Windows 11 x64 hardware, display setup, demo seed or replay, symbol set, and representative twelve-Panel mix used in each of four browser Workspaces.
- [ ] The documented protocol performs a five-minute warm-up followed by three fifteen-minute measurement runs and can be repeated without hidden manual state.
- [ ] Results record startup, bridge-to-store and order-intent latency, process-tree CPU and private memory, frame intervals, queue high-water marks, coalesces, overflows, disconnects, drops, and open/close recovery.
- [ ] Raw measurements, method, hardware, fixture identity, and the later Wails comparison procedure are recorded in the performance documentation.
- [ ] Development, automated tests, prototypes, replay, server mode, and migration fixtures default to isolated data roots; accessing the real user profile requires an explicit migration opt-in.
- [ ] An automated check proves the default profiles cannot resolve to the real user data directory, and fixtures or logs contain no credentials, account data, or captured private runtime data.
- [ ] Any order-intent measurement uses deterministic demo, replay, or simulated execution and never places, modifies, or cancels a real order.
- [ ] Relevant performance, development, and profile-isolation documentation describes the repeatable commands and safety boundary.
