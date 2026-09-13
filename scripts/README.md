# Repository Scripts

Repository-level build, packaging, and asset helpers. `gen-icons.sh` derives icon artifacts from source artwork. `smoke-linux-release.sh` validates an extracted Linux release archive in demo mode. Root `run.sh`/`run.cmd` own normal launch flows.

Inputs: checked-in sources/toolchains; outputs: generated/build artifacts. Generated outputs must not become hand-maintained. Verify affected build or generation command after changes.

## Linux release smoke check

Run it from the repository root with a staged archive:

```bash
bash scripts/smoke-linux-release.sh eTape-<version>-linux-amd64.tar.gz
```

The check extracts only the archive into temporary state, runs the embedded-UI
binary with `-demo -no-open` outside the source tree, verifies startup/version,
HTTP `index.html`, its referenced JavaScript asset, and clean SIGTERM shutdown.
