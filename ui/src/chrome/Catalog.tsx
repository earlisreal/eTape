import { CATALOG, isDevPanel, PANELS } from "./panels/registry";
import { PRESETS } from "./presets";
import { useTheme } from "./ThemeProvider";

function Thumb({ kind }: { kind: "monitoring" | "trading" }): JSX.Element {
  // Simple grid thumbnail; green cells mark chart slots. Purely decorative.
  const cells = kind === "monitoring"
    ? { cols: "1fr 1fr 1fr", rows: "1fr 1fr", green: [0, 1, 2] }
    : { cols: "2fr 1fr 1fr", rows: "2fr 1fr", green: [0] };
  const { palette } = useTheme();
  return (
    <div style={{ width: 84, height: 56, border: `1px solid ${palette.borderStrong}`, borderRadius: 3,
      display: "grid", gap: 1, background: palette.border, gridTemplateColumns: cells.cols, gridTemplateRows: cells.rows }}>
      {Array.from({ length: 6 }, (_, i) => (
        <i key={i} style={{ background: cells.green.includes(i) ? "rgba(23,122,88,.18)" : palette.surface }} />
      ))}
    </div>
  );
}

// Presets-first catalog: "Start from a preset" hero beside "Or add panels one by
// one". Reused by the blank-workspace EmptyState and the TopBar's "+ Add panel"
// popover. Dev-only panels (smoke-painter) only ever appear in the one-by-one list
// when running under `import.meta.env.DEV` — never in a production build.
export function Catalog({ onAddPanel, onApplyPreset }: { onAddPanel: (id: string) => void; onApplyPreset: (id: string) => void }): JSX.Element {
  const { palette } = useTheme();
  const panels = CATALOG.concat(import.meta.env.DEV && !CATALOG.some((c) => isDevPanel(c.panelId))
    ? [{ panelId: "smoke-painter", title: PANELS["smoke-painter"].title, glyph: PANELS["smoke-painter"].glyph, description: PANELS["smoke-painter"].description }]
    : []);
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1.1fr 1fr", gap: 28, alignItems: "start" }}>
      <div>
        <div className="col-head serif" style={{ borderBottom: `3px double ${palette.borderStrong}`, paddingBottom: 5, marginBottom: 12 }}>Start from a preset</div>
        {PRESETS.map((p) => (
          <button key={p.id} className="btn" onClick={() => onApplyPreset(p.id)}
            style={{ display: "flex", gap: 12, width: "100%", textAlign: "left", padding: 12, marginBottom: 12, alignItems: "center" }}>
            <Thumb kind={p.thumb} />
            <span><span className="serif" style={{ fontWeight: 600, display: "block" }}>{p.name}</span>
              <span style={{ color: palette.textMuted, fontSize: 10.5 }}>{p.description}</span></span>
          </button>
        ))}
      </div>
      <div>
        <div className="col-head serif" style={{ borderBottom: `3px double ${palette.borderStrong}`, paddingBottom: 5, marginBottom: 12 }}>Or add panels one by one</div>
        {panels.map((c) => (
          <div key={c.panelId} role="button" tabIndex={0} onClick={() => onAddPanel(c.panelId)}
            style={{ display: "flex", alignItems: "baseline", gap: 10, padding: "7px 2px", borderBottom: `1px solid ${palette.border}`, cursor: "pointer" }}>
            <span className="mono" style={{ color: palette.up, width: 24 }}>{c.glyph}</span>
            <span style={{ fontWeight: 600, width: 110 }}>{c.title}</span>
            <span style={{ color: palette.textMuted, fontSize: 10.5 }}>{c.description}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
