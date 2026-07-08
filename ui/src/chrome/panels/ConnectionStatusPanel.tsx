import { useSyncExternalStore } from "react";
import type { HealthStore } from "../../data/HealthStore";
import { useTheme } from "../ThemeProvider";
import type { Palette } from "../../render/palette";

const dotColor = (status: string, palette: Palette) =>
  status === "ok" ? palette.ok : status === "degraded" ? palette.warn : palette.danger;

export function ConnectionStatusPanel({ health }: { health: HealthStore }): JSX.Element {
  const { palette } = useTheme();
  const state = useSyncExternalStore((cb) => health.subscribe(cb), () => health.getSnapshot());
  return (
    <div style={{ padding: 10, fontSize: 12, color: palette.textMuted, height: "100%", overflow: "auto" }}>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr>
            <th className="col-head" style={{ textAlign: "left", padding: "2px 8px" }}>Link</th>
            <th className="col-head" style={{ textAlign: "right", padding: "2px 8px" }}>Latency</th>
            <th className="col-head" style={{ textAlign: "right", padding: "2px 8px" }}>Min/Avg/Max</th>
          </tr>
        </thead>
        <tbody>
          {state.links.map((l) => (
            <tr key={l.link} className="data-row">
              <td style={{ padding: "2px 8px" }}><span style={{ color: dotColor(l.status, palette) }}>●</span> {l.link}</td>
              <td style={{ textAlign: "right", padding: "2px 8px" }}>{l.ms === null ? "—" : `${l.ms} ms`}</td>
              <td style={{ textAlign: "right", padding: "2px 8px", opacity: 0.6 }}>
                {l.min === null ? "" : `${l.min}/${l.avg}/${l.max}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div style={{ marginTop: 10, borderTop: `1px solid ${palette.border}`, paddingTop: 6 }}>
        {state.events.slice(-50).reverse().map((e, i) => (
          // seq is per-source (Hub-owned sysEventSeq vs health.Poller's own
          // counter), so two events from different sources can share a seq;
          // fold in ts/kind/index so React never sees a duplicate key.
          <div key={`${e.ts}-${e.kind}-${e.seq}-${i}`} className="mono" style={{ display: "flex", gap: 8 }}>
            <span style={{ opacity: 0.5 }}>{e.ts}</span>
            <span style={{ opacity: 0.7 }}>{e.kind}</span>
            <span>{e.detail}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
