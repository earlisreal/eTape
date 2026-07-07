#!/usr/bin/env bash
# Convenience launcher for eTape. Three modes:
#   live  - real engine against ~/.eTape/config.toml (live OpenD feed + venues)
#   demo  - real engine against a synthetic replay day (no OpenD/broker needed)
#   dev   - mock WS engine + Vite dev server, hot reload for UI work
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$ROOT/engine"
UI_DIR="$ROOT/ui"

usage() {
  cat <<'EOF'
Usage: ./run.sh <mode> [options]

Modes:
  live               Build the UI, then run the real engine against
                     ~/.eTape/config.toml (live OpenD feed + real venues).
                     Requires OpenD already running and unlocked. Extra
                     args are passed through to the engine, e.g.:
                       ./run.sh live -watch=AAPL,TSLA -focus=AAPL

  demo [DAY] [SPEED] Build the UI, generate a synthetic replay day, and run
                     the engine against it. No OpenD or broker required.
                     DAY defaults to 2026-01-02. SPEED defaults to 1
                     (real-time); use 0 to replay as fast as possible.

  dev [FIXTURE]      Run the mock WS engine + Vite dev server with hot
                     reload, for UI iteration. FIXTURE selects a
                     ui/fixtures/<name>.json (default: session-basic).

Examples:
  ./run.sh live
  ./run.sh live -watch=AAPL,TSLA
  ./run.sh demo
  ./run.sh demo 2026-01-02 0
  ./run.sh dev ladder-tape
EOF
}

mode="${1:-}"
[ $# -gt 0 ] && shift

case "$mode" in
  live)
    echo "run.sh: building UI bundle" >&2
    (cd "$UI_DIR" && npm run build)
    echo "run.sh: booting engine (live) -- open http://127.0.0.1:8686" >&2
    cd "$ENGINE_DIR"
    exec go run ./cmd/etape -dist "$UI_DIR/dist" "$@"
    ;;

  demo)
    day="${1:-2026-01-02}"
    speed="${2:-1}"
    work="$(mktemp -d)"
    db="$work/demo.db"
    cfg="$work/demo.toml"

    echo "run.sh: building UI bundle" >&2
    (cd "$UI_DIR" && npm run build)

    echo "run.sh: generating synthetic journal ($db)" >&2
    (cd "$ENGINE_DIR" && go run ./cmd/genjournal -db "$db" -day "$day")

    cat > "$cfg" <<EOF
[store]
db_path = "$db"
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

    echo "run.sh: booting engine (replay $day, speed $speed) -- open http://127.0.0.1:8686" >&2
    cd "$ENGINE_DIR"
    exec go run ./cmd/etape -config "$cfg" -replay "$day" -speed "$speed" -replay-hold -dist "$UI_DIR/dist"
    ;;

  dev)
    fixture="${1:-session-basic}"
    cd "$UI_DIR"
    echo "run.sh: starting mock engine (fixture: $fixture)" >&2
    npm run mock-engine -- "$fixture" &
    mock_pid=$!
    trap 'kill "$mock_pid" 2>/dev/null || true' EXIT
    echo "run.sh: starting Vite dev server" >&2
    npm run dev
    ;;

  ""|-h|--help|help)
    usage
    exit 0
    ;;

  *)
    echo "run.sh: unknown mode '$mode'" >&2
    usage
    exit 1
    ;;
esac
