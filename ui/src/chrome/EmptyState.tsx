import { Catalog } from "./Catalog";
import { useTheme } from "./ThemeProvider";

// The blank-workspace hero: wraps Catalog with heading + lede copy. Shown by
// AppShell whenever the current workspace has zero panels — first run, or after
// the last panel is removed.
export function EmptyState({ onAddPanel, onApplyPreset }: { onAddPanel: (id: string) => void; onApplyPreset: (id: string) => void }): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", padding: "36px 40px", background: palette.bg }}>
      <div style={{ maxWidth: 720, width: "100%" }}>
        <h4 className="serif" style={{ fontWeight: 600, fontSize: 17, margin: "0 0 4px" }}>Empty workspace</h4>
        <p style={{ color: palette.textMuted, margin: "0 0 22px" }}>Load a preset and rearrange it, or build from the panel list. Everything is saved as you go.</p>
        <Catalog onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} />
      </div>
    </div>
  );
}
