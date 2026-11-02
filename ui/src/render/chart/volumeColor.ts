// Resolve CSS through the browser only when volume style/settings change.
export function overlapVolumeColor(color: string): string {
  const probe = document.createElement("span");
  probe.style.color = color;
  document.body.append(probe);
  const normalized = getComputedStyle(probe).color;
  probe.remove();
  const channels = normalized.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/);
  if (!channels) return color;
  return `rgba(${channels[1]}, ${channels[2]}, ${channels[3]}, ${Math.min(Number(channels[4] ?? 1), 0.5)})`;
}
