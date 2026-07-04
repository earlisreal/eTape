#!/usr/bin/env bash
set -euo pipefail
ENGINE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="$ENGINE_DIR/internal/feed/opend/pb/proto"
MODULE="github.com/earlisreal/eTape/engine"

command -v protoc >/dev/null || { echo "protoc not found on PATH" >&2; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go not found on PATH (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest)" >&2; exit 1; }

cd "$ENGINE_DIR"
protoc \
  -I "$PROTO_DIR" \
  --go_out=. \
  --go_opt=module="$MODULE" \
  "$PROTO_DIR"/*.proto
echo "generated pb bindings under internal/feed/opend/pb/"
