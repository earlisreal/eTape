# 26 — Ship the per-user NSIS installer

**What to build:** Produce the first supported eTape native distribution as one reproducible unsigned Windows 11 x64 NSIS installer that needs no administrator rights, handles WebView2, and preserves user-scoped configuration and databases through install, upgrade, and uninstall.

**Blocked by:** 25 — Remove the legacy product host and make CI Wails-native.

**Status:** ready-for-agent

- [ ] The installer places the application under the current user's LocalAppData programs directory without requesting elevation.
- [ ] Configuration, credentials, and databases remain under `%USERPROFILE%\.eTape`; install location never relocates, exposes, or resets that profile.
- [ ] The package contains version metadata, Start menu integration, predictable upgrade behavior, and uninstall behavior that preserves `%USERPROFILE%\.eTape`.
- [ ] WebView2-present and WebView2-missing-with-network paths launch successfully through the supported bootstrap behavior.
- [ ] A missing-WebView2 offline installation fails with an actionable message and leaves no partial application installation.
- [ ] Clean standard-user Windows 11 x64 virtual-machine checks cover fresh install, launch, upgrade, uninstall, reinstall, and preservation of user data.
- [ ] Packaging and release automation uses pinned Go, Node 24, Wails, and NSIS tooling and produces a reproducible versioned installer.
- [ ] The release publishes only the NSIS installer: no portable ZIP, raw developer executable, test-only server build, macOS archive, or other unsupported artifact.
- [ ] The installed product opens no console, external browser product window, or localhost product listener.
- [ ] Installation, WebView2, unsigned SmartScreen, upgrade, uninstall, backup, and rollback expectations are documented for users and maintainers.
