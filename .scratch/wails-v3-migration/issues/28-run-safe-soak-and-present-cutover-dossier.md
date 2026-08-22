# 28 — Run safe soak and present the cutover dossier

**What to build:** Complete the safe production-shaped soak as independently recorded, resumable runs and assemble the installer, migration, rollback, validation, performance, hosted-CI, and Wails-beta evidence needed for an explicit all-or-nothing cutover decision.

**Blocked by:** 27 — Run native lifecycle and four-by-twelve acceptance.

**Status:** ready-for-agent

- [ ] An eight-hour deterministic demo soak runs four Workspaces with twelve representative Panels each, scripted focus/layout activity, and complete simulated order lifecycles.
- [ ] One full regular-trading-hours live-data/read-only plus simulated-execution session and one full regular-trading-hours paper-broker session complete with deliberately bounded paper orders.
- [ ] Each long-running session records its fixture, build, start and end time, interruptions, counters, logs, and result durably enough for a fresh agent to audit or continue the campaign without repeating already valid evidence.
- [ ] Soak coverage includes OpenD or network disconnect/reconnect, sleep/wake, tray close/reopen, two clean restarts, and one forced crash with the approved recovery choices.
- [ ] Post-soak logs and counters show no panic, unrecoverable blank window, lossless gap, duplicate execution, unexplained Stream overflow, leaked demand, restoration loop, corruption, or sustained memory growth.
- [ ] Verified migration backup and prior-installer rollback instructions are exercised against a copied redacted profile without inspecting, copying, logging, or committing live credentials or account data.
- [ ] A source and diff audit finds no hand-edited generated output, credentials, account identifiers, balances, keys, captured runtime data, stale product-runtime code, or undocumented changed flow, interface, dependency, invariant, or operation.
- [ ] The complete hosted CI run is green, every required local/native/installer/soak check is listed with its result, and every skip or explicitly accepted performance exception has a reason and approver.
- [ ] Any Wails release newer than the pinned beta is reviewed without upgrading inside the cutover change; a required upgrade is isolated and followed by rerunning the capability and final validation gates.
- [ ] The cutover dossier presents the migration branch, versioned installer, verified backup and rollback procedure, measurements, native and installer evidence, soak results, hosted CI, and known beta limitations together.
- [ ] No migration work is partially merged or pushed as the product cutover before the user explicitly approves the dossier.
- [ ] No real order is placed, modified, or cancelled during acceptance or soak; any future live leg still requires separate current-session authorization and reconfirmation.
