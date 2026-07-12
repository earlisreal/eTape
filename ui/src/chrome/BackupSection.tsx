// Settings UI for backup.ts (Task 1): export the panel layout and/or
// order-template hotkeys to a JSON file, and import either (or both) back.
// Thin shell around backup.ts's pure functions — every rule about envelope
// shape, id regeneration, and activeVenue scrubbing lives there; this file
// only wires checkboxes, the file dialog, and the confirm/toast flow.
import { useRef, useState } from "react";
import { useTheme } from "./ThemeProvider";
import { Button } from "./controls/Button";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";
import type { ActionTemplate, OrderConfig } from "./exec/actionTemplate";
import {
  buildExport, parseImport, prepareImportedWorkspace, prepareImportedOrderConfig,
  detectHotkeyConflicts, isPresentLayout, type SettingsExport,
} from "./backup";

export interface BackupSectionProps {
  getWorkspace: () => Workspace;
  onImportWorkspace: (ws: Workspace) => void;
  orderConfig: OrderConfig;
  onImportOrderConfig: (next: OrderConfig) => void;
  toast: ToastApi;
}

// parseImport (Task 1) only validates the top-level envelope (kind/app) —
// it does NOT check that `layout`/`hotkeys.templates` have the right inner
// shape. A hand-edited or partially-truncated file can carry a `layout` that
// isn't an object, or a `hotkeys.templates` that isn't an array; either must
// be treated as "that section isn't present" rather than crashing this
// component's `.map`/spread calls (or crashing further downstream in
// prepareImportedOrderConfig, which does call `.map` on `templates`).
// isPresentLayout itself now lives in backup.ts (shared with the
// empty-workspace import entry point); isPresentHotkeys stays local since
// only this Settings-based path imports hotkeys.
function isPresentHotkeys(hotkeys: SettingsExport["hotkeys"]): hotkeys is { templates: ActionTemplate[] } {
  return Array.isArray(hotkeys?.templates);
}

function localDateStamp(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function BackupSection(
  { getWorkspace, onImportWorkspace, orderConfig, onImportOrderConfig, toast }: BackupSectionProps,
): JSX.Element {
  const { palette } = useTheme();
  const [exportLayout, setExportLayout] = useState(true);
  const [exportHotkeys, setExportHotkeys] = useState(true);

  const [importData, setImportData] = useState<SettingsExport | null>(null);
  const [importLayoutChecked, setImportLayoutChecked] = useState(true);
  const [importHotkeysChecked, setImportHotkeysChecked] = useState(true);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const layoutPresent = importData ? isPresentLayout(importData.layout) : false;
  const hotkeysPresent = importData ? isPresentHotkeys(importData.hotkeys) : false;
  const doLayout = importLayoutChecked && layoutPresent;
  const doHotkeys = importHotkeysChecked && hotkeysPresent;

  const download = (): void => {
    const data = buildExport({ layout: exportLayout, hotkeys: exportHotkeys }, { workspace: getWorkspace(), orderConfig });
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `etape-settings-${localDateStamp(new Date())}.json`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const onFileSelected = (e: React.ChangeEvent<HTMLInputElement>): void => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      const result = parseImport(text);
      if (!result.ok) {
        toast.push({ level: "danger", text: result.error });
        setImportData(null);
        return;
      }
      setImportData(result.data);
      setImportLayoutChecked(true);
      setImportHotkeysChecked(true);
    };
    reader.readAsText(file);
    // Allow re-selecting the same file later (browsers don't fire onChange
    // again for an unchanged file path unless the input's value is cleared).
    e.target.value = "";
  };

  const applyImport = (): void => {
    if (!importData || (!doLayout && !doHotkeys)) return;
    const parts: string[] = [];
    if (doLayout) parts.push("layout");
    if (doHotkeys) parts.push("hotkeys");
    const noun = parts.length > 1 ? "ones" : "one";
    const confirmed = window.confirm(`Replace your current ${parts.join(" and ")} with the imported ${noun}?`);
    if (!confirmed) return;

    let importedTemplates: ActionTemplate[] | null = null;
    if (doLayout) {
      onImportWorkspace(prepareImportedWorkspace(importData.layout as Workspace, getWorkspace().name));
    }
    if (doHotkeys) {
      const nextOrderConfig = prepareImportedOrderConfig(importData.hotkeys as { templates: ActionTemplate[] }, orderConfig);
      onImportOrderConfig(nextOrderConfig);
      importedTemplates = nextOrderConfig.templates;
    }
    if (importedTemplates) {
      const conflicts = detectHotkeyConflicts(importedTemplates);
      if (conflicts.length > 0) {
        toast.push({ level: "warn", text: `Imported hotkeys have conflicting bindings: ${conflicts.join(", ")}` });
      }
    }
    toast.push({ level: "info", text: `Imported ${parts.join(" and ")}.` });

    setImportData(null);
    setImportLayoutChecked(true);
    setImportHotkeysChecked(true);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const headStyle = { marginBottom: 8 };
  const rowStyle = { display: "block", marginBottom: 6 };
  const noteStyle = { fontSize: 12, color: palette.textMuted, marginBottom: 8 };

  return (
    <div style={{ color: palette.text }}>
      <div style={{ ...noteStyle, marginBottom: 14 }} data-testid="scope-note">
        Layout export/import applies only to this window. Hotkeys are shared across all
        windows, but an already-open window won&apos;t see an import until it reloads.
      </div>
      <div className="col-head serif" style={headStyle}>Export</div>
      <label style={rowStyle}>
        <input type="checkbox" aria-label="Layout" data-testid="export-layout" checked={exportLayout} onChange={(e) => setExportLayout(e.target.checked)} /> Layout
      </label>
      <label style={{ ...rowStyle, marginBottom: 10 }}>
        <input type="checkbox" aria-label="Hotkeys" data-testid="export-hotkeys" checked={exportHotkeys} onChange={(e) => setExportHotkeys(e.target.checked)} /> Hotkeys
      </label>
      <Button data-testid="download-json" disabled={!exportLayout && !exportHotkeys} onClick={download}>
        Download JSON
      </Button>

      <div className="col-head serif" style={{ ...headStyle, marginTop: 22 }}>Import</div>
      <input
        type="file" accept="application/json" aria-label="Import settings file"
        data-testid="import-file" ref={fileInputRef} onChange={onFileSelected}
        style={{ display: "block", marginBottom: 10, fontSize: 12, color: palette.text }}
      />

      {importData && (
        <div>
          {!layoutPresent && !hotkeysPresent && (
            <div style={noteStyle}>This file has no layout or hotkeys to import.</div>
          )}
          {layoutPresent && (
            <label style={rowStyle}>
              <input type="checkbox" aria-label="Import layout" data-testid="import-layout" checked={importLayoutChecked} onChange={(e) => setImportLayoutChecked(e.target.checked)} /> Layout
            </label>
          )}
          {hotkeysPresent && (
            <label style={{ ...rowStyle, marginBottom: 10 }}>
              <input type="checkbox" aria-label="Import hotkeys" data-testid="import-hotkeys" checked={importHotkeysChecked} onChange={(e) => setImportHotkeysChecked(e.target.checked)} /> Hotkeys
            </label>
          )}
          <Button data-testid="apply-import" disabled={!doLayout && !doHotkeys} onClick={applyImport}>
            Apply import
          </Button>
        </div>
      )}
    </div>
  );
}
