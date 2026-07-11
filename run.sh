#!/usr/bin/env bash
# Convenience launcher for eTape. Three modes:
#   live  - real engine against ~/.eTape/config.toml (live OpenD feed + venues)
#   demo  - real engine against a live built-in synthetic market (no OpenD/broker needed)
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
                     Requires OpenD already running and logged in. Extra
                     args are passed through to the engine, e.g.:
                       ./run.sh live -no-open -log /tmp/etape.log

  demo [SEED]        Build the UI, then run the engine against its
                     built-in, self-seeding synthetic market -- a live
                     feed over a fictional universe, no OpenD or broker
                     required, no journal to pre-generate. SEED is an
                     optional PRNG seed (omit for a random seed per
                     launch). Extra args are passed through to the
                     engine, e.g.:
                       ./run.sh demo -no-open

  dev [FIXTURE]      Run the mock WS engine + Vite dev server with hot
                     reload, for UI iteration. FIXTURE selects a
                     ui/fixtures/<name>.json (default: session-basic).

Examples:
  ./run.sh live
  ./run.sh demo
  ./run.sh demo 42
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
    # Optional leading positional SEED (any non-flag first arg); everything
    # else (e.g. -no-open, -log ...) passes through to the engine untouched.
    seed=""
    if [ $# -gt 0 ] && [[ "$1" != -* ]]; then
      seed="$1"
      shift
    fi

    echo "run.sh: building UI bundle" >&2
    (cd "$UI_DIR" && npm run build)

    echo "run.sh: booting engine (demo synthetic market) -- open http://127.0.0.1:8686" >&2
    cd "$ENGINE_DIR"
    if [ -n "$seed" ]; then
      exec go run ./cmd/etape -dist "$UI_DIR/dist" -demo -demo-seed "$seed" "$@"
    else
      exec go run ./cmd/etape -dist "$UI_DIR/dist" -demo "$@"
    fi
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
