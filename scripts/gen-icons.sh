#!/usr/bin/env bash
set -euo pipefail
# Regenerate raster icon derivatives from the source SVGs.
#   Browser favicon (theme-aware, transparent): ui/public/favicon.svg
#   Windows mark (bright bars, transparent):     engine/cmd/etape/assets/etape-mark.svg
# Requires: rsvg-convert + magick (ImageMagick 7).
# Deliberately does NOT touch apple-touch-icon.png / icon-192.png / icon-512.png:
# those stay opaque squares (OS re-masks them on install).
repo="$(cd "$(dirname "$0")/.." && pwd)"
fav="$repo/ui/public/favicon.svg"
mark="$repo/engine/cmd/etape/assets/etape-mark.svg"
ico="$repo/engine/cmd/etape/assets/etape.ico"

# Browser favicon PNG fallbacks (transparent; rsvg renders the light default branch --
# the `@media (prefers-color-scheme: dark)` rule only applies in a browser context).
for s in 16 32 48; do
  rsvg-convert -w "$s" -h "$s" "$fav" -o "$repo/ui/public/favicon-$s.png"
done

# Windows tray + .exe mark: transparent, bright bars, 6-size .ico.
tmp="$(mktemp -d)"
rsvg-convert -w 1024 -h 1024 "$mark" -o "$tmp/mark-1024.png"
magick "$tmp/mark-1024.png" -define icon:auto-resize=256,128,64,48,32,16 "$ico"
rm -rf "$tmp"

echo "icons regenerated: favicon-{16,32,48}.png, etape.ico"
