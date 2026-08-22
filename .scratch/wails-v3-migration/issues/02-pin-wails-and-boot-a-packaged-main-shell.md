# 02 — Pin Wails and boot a packaged Main shell

**What to build:** Produce the smallest reproducible Wails desktop shell that embeds and displays the current React application in Main, using one reviewed beta toolchain and deterministic development, build, generation, server-test, Windows, and package entrypoints.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] The Wails Go module, repository-owned CLI invocation, frontend runtime, generated runtime, and build assets are pinned to mutually compatible v3.0.0-beta.11 versions; no build or generator resolves from `latest` or a developer-global Wails installation.
- [ ] A production Windows build embeds and opens the current React application in Main while development serves the same frontend through Wails' supported Vite integration.
- [ ] The Wails composition root owns the application event loop and does not start the legacy product HTTP or WebSocket listener as a compatibility layer.
- [ ] Repository entrypoints deterministically cover frontend generation/build, development, binding generation, server testing, Windows 11 x64 build, and NSIS packaging, and fail clearly when a pinned prerequisite is missing.
- [ ] The existing embedded-distribution contract is reused for production assets without introducing another asset abstraction or moving Wails into engine-domain packages.
- [ ] A minimal unsigned per-user NSIS smoke package targets the accepted LocalAppData application location and proves the exact pinned toolchain can package successfully; full installer behavior remains deferred.
- [ ] Clean build and package checks pass on Windows, and the developer/build documentation records the authoritative pinned commands and beta upgrade rule.
