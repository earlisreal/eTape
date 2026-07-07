#!/usr/bin/env bash
# Boots the real engine for the Playwright E2E: builds the UI, generates a
# synthetic replay day, writes a config pointing at it + ui/dist, then execs
# the engine in replay-hold mode. exec so Playwright's teardown reaches it.
set -euo pipefail

UI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$UI_DIR/.." && pwd)"
ENGINE_DIR="$ROOT/engine"
DAY="2026-01-02"
WORK="$(mktemp -d)"
DB="$WORK/e2e.db"
CFG="$WORK/e2e.toml"

echo "e2e: building UI bundle" >&2
(cd "$UI_DIR" && npm run build >&2)

echo "e2e: generating synthetic journal ($DB)" >&2
(cd "$ENGINE_DIR" && go run ./cmd/genjournal -db "$DB" -day "$DAY" >&2)

cat > "$CFG" <<EOF
[store]
db_path = "$DB"
[uihub]
host = "127.0.0.1"
port = 8686
[[venue]]
id = "sim-paper"
broker = "sim"
env = "paper"
[gate.global]
max_day_loss = 100000
max_symbol_position_value = 100000
max_symbol_position_shares = 100000
[gate.venue.sim-paper]
max_order_value = 100000
max_position_value = 100000
max_position_shares = 100000
max_open_orders = 50
EOF

echo "e2e: booting engine (replay $DAY, hold)" >&2
cd "$ENGINE_DIR"
exec go run ./cmd/etape -config "$CFG" -replay "$DAY" -speed 0 -replay-hold -dist "$UI_DIR/dist"
