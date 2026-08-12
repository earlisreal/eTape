#!/usr/bin/env bash
# Boots the deterministic sim-only engine for ticketless-hotkeys.spec.ts.
set -euo pipefail

UI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$UI_DIR/.." && pwd)"
ENGINE_DIR="$ROOT/engine"

echo "e2e: building UI bundle" >&2
(cd "$UI_DIR" && npm run build >&2)

echo "e2e: booting deterministic demo engine (sim-paper)" >&2
cd "$ENGINE_DIR"
exec go run ./cmd/etape -demo -demo-seed 1 -no-open -dist "$UI_DIR/dist"
