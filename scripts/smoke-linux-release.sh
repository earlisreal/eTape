#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 ARCHIVE" >&2
  exit 2
fi

archive=$1
if [[ ! -f "$archive" ]]; then
  echo "smoke: archive not found: $archive" >&2
  exit 1
fi

archive_name=${archive##*/}
if [[ "$archive_name" != eTape-*-linux-amd64.tar.gz ]]; then
  echo "smoke: expected eTape-<version>-linux-amd64.tar.gz, got $archive_name" >&2
  exit 1
fi
version=${archive_name#eTape-}
version=${version%-linux-amd64.tar.gz}
if [[ -z "$version" || "$version" == "$archive_name" ]]; then
  echo "smoke: expected eTape-<version>-linux-amd64.tar.gz, got $archive_name" >&2
  exit 1
fi

tmp=$(mktemp -d)
extract="$tmp/extract"
home="$tmp/home"
work="$tmp/work"
log="$tmp/etape.log"
index="$tmp/index.html"
asset_file="$tmp/app.js"
pid=""
port=18686

stop_process() {
  local deadline=$((SECONDS + 15))
  kill -TERM "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    state=$(ps -o stat= -p "$pid" 2>/dev/null || true)
    [[ "$state" == Z* ]] && break
    if (( SECONDS >= deadline )); then
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 1
  done
  wait "$pid"
}

cleanup() {
  status=$?
  set +e
  if [[ -n "$pid" ]]; then
    stop_process 2>/dev/null || true
  fi
  if (( status != 0 )); then
    echo "--- eTape smoke log ---" >&2
    if [[ -f "$log" ]]; then
      cat "$log" >&2
    else
      echo "(no log)" >&2
    fi
    echo "--- end eTape smoke log ---" >&2
  fi
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

mkdir -p "$extract" "$home" "$work" "$tmp/tmp"
tar -xzf "$archive" -C "$extract"
binary="$extract/etape-linux-amd64"
if [[ ! -f "$binary" || ! -x "$binary" ]]; then
  echo "smoke: extracted etape-linux-amd64 is missing or not executable" >&2
  exit 1
fi

(
  cd "$work"
  exec env HOME="$home" TMPDIR="$tmp/tmp" ETAPE_UIHUB_PORT="$port" \
    "$binary" -demo -no-open
) >"$log" 2>&1 &
pid=$!
base="http://127.0.0.1:$port"
deadline=$((SECONDS + 60))

while :; do
  if grep -Fq "etape ready" "$log" && \
     grep -Fq "version=$version" "$log" && \
     grep -Fq "mode=demo" "$log" && \
     curl --fail --silent --show-error --max-time 2 "$base/" >"$index"; then
    break
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "smoke: eTape exited before readiness" >&2
    exit 1
  fi
  if (( SECONDS >= deadline )); then
    echo "smoke: readiness timed out after 60 seconds" >&2
    exit 1
  fi
  sleep 1
done

grep -Fq '<title>eTape</title>' "$index"
asset=$(sed -n 's/.*src="\(\/assets\/[^" ]*\.js\)".*/\1/p' "$index" | head -n 1)
if [[ -z "$asset" ]]; then
  echo "smoke: index.html has no embedded JS asset reference" >&2
  exit 1
fi
curl --fail --silent --show-error --max-time 5 "$base$asset" -o "$asset_file"
if [[ ! -s "$asset_file" ]]; then
  echo "smoke: referenced JS asset is empty: $asset" >&2
  exit 1
fi

if ! stop_process; then
  echo "smoke: eTape did not exit cleanly after SIGTERM" >&2
  exit 1
fi
pid=""
