# Repository Scripts

Repository-level build, packaging, and asset helpers. `gen-icons.sh` derives icon artifacts from source artwork. `smoke-linux-release.sh` validates an extracted Linux release archive in demo mode. Root `run.sh`/`run.cmd` own normal launch flows.

Inputs: checked-in sources/toolchains; outputs: generated/build artifacts. Generated outputs must not become hand-maintained. Verify affected build or generation command after changes.

`bash scripts/gen-icons.sh` requires `rsvg-convert` and ImageMagick 7 (`magick`).
It renders every favicon and transparent manifest icon at its final size from
`ui/public/favicon.svg`, and generates the separate tiled Windows `.exe`/tray/taskbar
icon from `engine/cmd/etape/assets/etape-mark.svg`. Its mark fills 75% of the tile's
width. Native 16/20/24/32/40/48/64/96/128/256 px ICO entries are rendered separately
for Windows DPI scaling. The Apple touch icon stays tiled.

`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-windows-launcher.ps1`
checks the real Windows launch flow with a tiny fixture engine. Live, seeded
demo, and unseeded demo launches must rebuild and execute at the same persistent
path, retain the executable after exit for Explorer, and forward engine flags
including paths with spaces. UI building and icon-tool downloads are stubbed;
the engine compilation and launch use the installed Go toolchain. This check
also runs in the Windows CI job.

## Linux release smoke check

Run it from the repository root with a staged archive:

```bash
bash scripts/smoke-linux-release.sh eTape-<version>-linux-amd64.tar.gz
```

The check extracts only the archive into temporary state, runs the embedded-UI
binary with `-demo -no-open` outside the source tree, verifies startup/version,
HTTP `index.html`, its referenced JavaScript asset, and clean SIGTERM shutdown.
