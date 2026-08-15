#!/usr/bin/env bash
set -euo pipefail

exec node "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/serve.mjs" "${1:-${ETAPE_UIHUB_PORT:-8686}}"
