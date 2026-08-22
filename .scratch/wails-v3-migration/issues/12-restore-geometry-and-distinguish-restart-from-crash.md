# 12 — Restore geometry and distinguish restart from crash

**What to build:** Restore valid Workspace windows after an intentional whole-application restart, but recover conservatively after an unclean exit so corrupt or unreachable windows cannot create a crash loop.

**Blocked by:** 11 — Save Workspace durably before closing.

**Status:** ready-for-agent

- [ ] Persist the open Workspace identities, display identity, normal bounds, and maximised state after their durable Workspace state is flushed; minimized state is never restored.
- [ ] Restore clamps missing-display, off-screen, negative-coordinate, corrupt, and taskbar-obscured geometry into a usable current work area, including monitor reorder and mixed-DPI changes.
- [ ] Each launch is marked unclean before creating Workspace windows, while clean Quit and intentional restart are marked only after all required state commits.
- [ ] An intentional restart returns its initiating request before asynchronous shutdown, releases process and data locks before relaunch, and restores every valid previously open Workspace exactly once.
- [ ] An unclean launch opens Main only and offers explicit restore or decline; declining keeps every saved Workspace intact without opening it automatically.
- [ ] Restore validates Workspace documents independently and quarantines a corrupt document without blocking Main or other valid Workspaces.
- [ ] Policy and native tests cover normal Quit, intentional restart, forced crash, both crash-recovery choices, maximised versus normal bounds, invalid geometry, missing monitors, and duplicate-window prevention.
- [ ] Restart/crash-recovery and geometry behavior, including the non-restoration of minimized state, is documented in the affected desktop and operations guides.
