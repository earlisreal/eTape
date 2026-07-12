import { useRef } from "react";
import { Catalog } from "./Catalog";
import { useTheme } from "./ThemeProvider";

// The blank-workspace hero: wraps Catalog with heading + lede copy. Shown by
// AppShell whenever the current workspace has zero panels — first run, or after
// the last panel is removed.
export function EmptyState({ onAddPanel, onApplyPreset, showTryDemo, onTryDemo, onImportLayoutFile }: {
  onAddPanel: (id: string) => void;
  onApplyPreset: (id: string) => void;
  // Task 6 (U4): AppShell computes this off sessionMode.mode — hidden while
  // already inside a demo/replay session (see AppShell's showTryDemo).
  showTryDemo: boolean;
  onTryDemo: () => void;
  // A direct entry point into the same import machinery BackupSection uses
  // (Settings -> Import & export), so a blank workspace can load a saved
  // layout without first adding a throwaway panel just to reach Settings.
  // Layout-only (ignores hotkeys even if present in the file) and skips the
  // checkbox/confirm ceremony BackupSection uses — there's nothing to lose
  // on a blank workspace. AppShell owns the FileReader/parse/apply logic;
  // this component only forwards the picked File.
  onImportLayoutFile: (file: File) => void;
}): JSX.Element {
  const { palette } = useTheme();
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const onFileSelected = (e: React.ChangeEvent<HTMLInputElement>): void => {
    const file = e.target.files?.[0];
    if (file) onImportLayoutFile(file);
    // Allow re-selecting the same file later (browsers don't fire onChange
    // again for an unchanged file path unless the input's value is cleared)
    // — same reset trick as BackupSection.onFileSelected.
    e.target.value = "";
  };
  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", padding: "36px 40px", background: palette.bg }}>
      <div style={{ maxWidth: 720, width: "100%" }}>
        <h4 className="serif" style={{ fontWeight: 600, fontSize: 17, margin: "0 0 4px" }}>Empty workspace</h4>
        <p style={{ color: palette.textMuted, margin: "0 0 22px" }}>Load a preset and rearrange it, or build from the panel list. Everything is saved as you go.</p>
        {showTryDemo && (
          // Left-accent notice band, not a card — echoes the newspaper/ledger
          // idiom (a boxed notice, same family as Catalog's ruled column
          // heads) rather than introducing a new visual pattern. palette.demo
          // is the SAME magenta/plum already used everywhere else in the app
          // to mean "this leads to the synthetic demo market" (DemoBanner,
          // PracticeLauncherModal's demo section stripe) — reusing it here
          // means the color itself previews what clicking the button does,
          // before the user has seen either of those surfaces.
          <div style={{
            display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 14,
            borderLeft: `3px solid ${palette.demo}`, background: palette.surface, padding: "10px 14px", marginBottom: 22,
          }}>
            <p style={{ margin: 0, fontSize: 12, color: palette.text }}>
              New here? Explore a live-feeling synthetic market — no broker or setup required.
            </p>
            <button className="btn" onClick={onTryDemo}
              style={{ background: palette.demo, color: "#fff", border: `1px solid ${palette.demo}`, flexShrink: 0 }}>
              Try demo
            </button>
          </div>
        )}
        <Catalog onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} />
        <div style={{
          display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 14,
          borderTop: `1px solid ${palette.borderStrong}`, marginTop: 22, paddingTop: 14,
        }}>
          <p className="serif" style={{ margin: 0, fontSize: 12, color: palette.textMuted }}>Have a saved layout?</p>
          <button className="btn" aria-label="Import layout" onClick={() => importInputRef.current?.click()}>
            ⤓ Import layout…
          </button>
          <input
            ref={importInputRef} type="file" accept="application/json"
            data-testid="empty-import-file" onChange={onFileSelected}
            style={{ display: "none" }}
          />
        </div>
      </div>
    </div>
  );
}
