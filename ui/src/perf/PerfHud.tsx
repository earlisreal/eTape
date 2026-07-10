// Diagnostic overlay for Task 0 of the UI perf plan. Only ever mounted by
// App.tsx when perf.enabled — polls perf.snapshot() on a fixed ~250ms
// interval (well below rAF rate) rather than every frame, so the HUD itself
// never becomes part of the hot path it's measuring. No persistence, no
// export, no charting — just a text readout for a manual before/after
// comparison against Tasks 1-4's per-symbol scoping fix.
import { useEffect, useState } from "react";
import { perf, type PerfSnapshot } from "./PerfMonitor";

const POLL_MS = 250;

export function PerfHud(): JSX.Element {
  const [snap, setSnap] = useState<PerfSnapshot>(() => perf.snapshot());

  useEffect(() => {
    const id = window.setInterval(() => setSnap(perf.snapshot()), POLL_MS);
    return () => window.clearInterval(id);
  }, []);

  return (
    <div
      data-testid="perf-hud"
      style={{
        position: "fixed", top: 8, right: 8, zIndex: 999999,
        background: "rgba(0,0,0,0.78)", color: "#7CFC7C",
        fontFamily: "ui-monospace, Menlo, Consolas, monospace",
        fontSize: 11, lineHeight: 1.5, padding: "6px 9px", borderRadius: 4,
        whiteSpace: "pre", pointerEvents: "none",
      }}
    >
      {formatSnapshot(snap)}
    </div>
  );
}

function formatSnapshot(s: PerfSnapshot): string {
  if (!s.enabled) return "perf HUD (disabled)";
  const lines: string[] = [];
  lines.push("perf HUD");
  lines.push(`ws ${s.wsMsgsPerSec}/s  ticks ${s.ticksPerSec}/s`);
  lines.push(
    `frame ${s.frame.intervalMs !== null ? s.frame.intervalMs.toFixed(1) : "-"}ms  dropped ${s.frame.droppedFrames}`,
  );
  const ids = [...new Set([...Object.keys(s.paint), ...Object.keys(s.scan)])].sort();
  for (const id of ids) {
    const p = s.paint[id];
    const sc = s.scan[id];
    const parts: string[] = [];
    if (p) parts.push(`paint ${p.last.toFixed(1)}/${p.max.toFixed(1)}/${p.ewma.toFixed(1)}ms (last/max/ewma)`);
    if (sc) parts.push(`scan ${sc.last}/${sc.max} (last/max)`);
    lines.push(`${id}: ${parts.join("  ")}`);
  }
  return lines.join("\n");
}
