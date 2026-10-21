#!/usr/bin/env bash
set -euo pipefail
# Regenerate raster icon derivatives from the source SVGs.
#   Browser/window mark (flat, transparent):    ui/public/favicon.svg
#   Windows mark (light squircle, deep bars):    engine/cmd/etape/assets/etape-mark.svg
# Requires: rsvg-convert + magick (ImageMagick 7).
# apple-touch-icon.png keeps its opaque tile for iOS.
repo="$(cd "$(dirname "$0")/.." && pwd)"
fav="$repo/ui/public/favicon.svg"
mark="$repo/engine/cmd/etape/assets/etape-mark.svg"
ico="$repo/engine/cmd/etape/assets/etape.ico"

# Browser favicon PNG fallbacks (transparent, fixed Windows mark palette).
# Render every candidate directly from the SVG at its final size; do not resize one
# raster through the whole set.
for s in 16 20 24 32 40 48 64; do
  rsvg-convert -w "$s" -h "$s" "$fav" -o "$repo/ui/public/favicon-$s.png"
done

# Manifest icons can also supply the Chrome app's taskbar icon.
for s in 192 512; do
  rsvg-convert -w "$s" -h "$s" "$fav" -o "$repo/ui/public/icon-$s.png"
done

# Browser app-mode icon: preserve the individually rendered PNGs as ICO entries.
browser_ico="$repo/ui/public/favicon.ico"
magick \
  "$repo/ui/public/favicon-16.png" \
  "$repo/ui/public/favicon-20.png" \
  "$repo/ui/public/favicon-24.png" \
  "$repo/ui/public/favicon-32.png" \
  "$repo/ui/public/favicon-40.png" \
  "$repo/ui/public/favicon-48.png" \
  "$repo/ui/public/favicon-64.png" \
  "$browser_ico"

# Windows tray + .exe mark: light squircle tile, deep bars, 6-size .ico.
tmp="$(mktemp -d)"
rsvg-convert -w 1024 -h 1024 "$mark" -o "$tmp/mark-1024.png"
magick "$tmp/mark-1024.png" -define icon:auto-resize=256,128,64,48,32,16 "$ico"
rm -rf "$tmp"

echo "icons regenerated: favicon-{16,20,24,32,40,48,64}.png, favicon.ico, icon-{192,512}.png, etape.ico"
