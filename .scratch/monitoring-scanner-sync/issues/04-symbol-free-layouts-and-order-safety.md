# 04 — Make Layouts Symbol-Free and Safe

**What to build:** Make new built-in layouts and General settings Layout downloads structural rather than symbol-bearing. Trading and Monitoring seeds must not introduce a selected ticker. All user-facing symbol-bearing panels must tolerate being unassigned instead of falling back to AAPL, and execution paths must refuse placement until a real symbol is present. Layout downloads must preserve useful arrangement and non-symbol configuration while removing portable symbol state.

**Blocked by:** 01 — Reserve the Monitoring Workspace; 02 — Follow Monitoring's Scanner.

**Status:** ready-for-agent

- [ ] Monitoring and Trading seeds contain no default selected symbols while retaining their intended non-symbol layout, panel settings, and Link Group membership.
- [ ] Chart, DOM Ladder, Time & Sales, Order Ticket, Stock Info, and Locates display an honest unassigned state rather than silently using a fallback ticker.
- [ ] Unassigned symbol-bearing panels make no arbitrary market-data demand until they receive a typed or linked symbol.
- [ ] Direct Order Ticket placement and place-order hotkeys are blocked when no effective symbol exists; existing symbol-independent safety actions retain their established behavior.
- [ ] General settings Layout downloads remove every panel selected symbol and every Link Group focused symbol while retaining arrangement, panel membership, focused venues, and non-symbol settings.
- [ ] A Monitoring Layout download retains enabled Scanner Sync intent but omits the non-portable Scanner Source; importing it leaves enabled Sync paused until the trader selects a source.
- [ ] The independent Orders and hotkeys export remains unchanged.
- [ ] Export/import tests prove the live saved workspace is not mutated, and no existing workspace or previously downloaded file is erased or rewritten.
- [ ] Panel, order-safety, preset, and Layout export tests cover the symbol-free behavior and relevant documentation is updated.
