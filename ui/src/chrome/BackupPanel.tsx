// Settings UI for backup.ts (Task 1), single-part variant: unlike
// BackupSection.tsx (the combined two-checkbox shell), this component backs
// up exactly one part — `layout` or `hotkeys` — chosen by the `part` prop, so
// it can be mounted next to the feature it backs up (General settings for
// layout, Orders & hotkeys for hotkeys). Every rule about envelope shape, id
// regeneration, and activeVenue scrubbing lives in backup.ts; this file only
// wires the button, the file dialog, and the confirm/toast flow for its part.
import { useRef, useState } from "react";
import { useTheme } from "./ThemeProvider";
import { HoverButton } from "./controls/HoverButton";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";
import type { ActionTemplate, OrderConfig } from "./exec/actionTemplate";
import {
  buildExport, parseImport, prepareImportedWorkspace, prepareImportedOrderConfig,
  detectHotkeyConflicts, type SettingsExport,
} from "./backup";

export type BackupPanelProps =
  | { part: "layout"; getWorkspace: () => Workspace; onImportWorkspace: (ws: Workspace) => void; toast: ToastApi }
  | { part: "hotkeys"; orderConfig: OrderConfig; onImportOrderConfig: (next: OrderConfig) => void; toast: ToastApi };

// Same shape guards as BackupSection.tsx (not exported there, so ported
// rather than imported): parseImport only validates the top-level envelope,
// not `layout`/`hotkeys.templates`'s inner shape, so a hand-edited or
// partially-truncated file must be treated as "that section isn't present"
// rather than crashing this component or prepareImportedOrderConfig's `.map`.
function isPresentLayout(layout: SettingsExport["layout"]): layout is Workspace {
  return typeof layout === "object" && layout !== null && !Array.isArray(layout);
}
function isPresentHotkeys(hotkeys: SettingsExport["hotkeys"]): hotkeys is { templates: ActionTemplate[] } {
  return Array.isArray(hotkeys?.templates);
}

function localDateStamp(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function BackupPanel(props: BackupPanelProps): JSX.Element {
  const { palette } = useTheme();
  const [importData, setImportData] = useState<SettingsExport | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // Placeholders for whichever field buildExport's `sel` tells it not to
  // read (buildExport only reads `src.workspace` when `sel.layout` is true,
  // and only reads `src.orderConfig.templates` when `sel.hotkeys` is true) —
  // never actually inspected, so a cheap stand-in avoids needing a real
  // Workspace/OrderConfig for the part this panel instance doesn't own.
  const placeholderWorkspace: Workspace = { name: "", panels: [], layout: {} };
  const placeholderOrderConfig: OrderConfig = { templates: [], activeVenue: "" };

  const partPresent = importData
    ? props.part === "layout" ? isPresentLayout(importData.layout) : isPresentHotkeys(importData.hotkeys)
    : false;

  const download = (): void => {
    const src = props.part === "layout"
      ? { workspace: props.getWorkspace(), orderConfig: placeholderOrderConfig }
      : { workspace: placeholderWorkspace, orderConfig: props.orderConfig };
    const data = buildExport({ layout: props.part === "layout", hotkeys: props.part === "hotkeys" }, src);
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `etape-${props.part}-${localDateStamp(new Date())}.json`;
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
        props.toast.push({ level: "danger", text: result.error });
        setImportData(null);
        return;
      }
      setImportData(result.data);
    };
    reader.readAsText(file);
    // Allow re-selecting the same file later (browsers don't fire onChange
    // again for an unchanged file path unless the input's value is cleared).
    e.target.value = "";
  };

  const applyImport = (): void => {
    if (!importData || !partPresent) return;
    if (props.part === "layout") {
      if (!window.confirm("Replace your current layout with the imported one?")) return;
      props.onImportWorkspace(prepareImportedWorkspace(importData.layout as Workspace, props.getWorkspace().name));
      props.toast.push({ level: "info", text: "Imported layout." });
    } else {
      if (!window.confirm("Replace your current hotkeys with the imported ones?")) return;
      const next = prepareImportedOrderConfig(importData.hotkeys as { templates: ActionTemplate[] }, props.orderConfig);
      props.onImportOrderConfig(next);
      const conflicts = detectHotkeyConflicts(next.templates);
      if (conflicts.length > 0) {
        props.toast.push({ level: "warn", text: `Imported hotkeys have conflicting bindings: ${conflicts.join(", ")}` });
      }
      props.toast.push({ level: "info", text: "Imported hotkeys." });
    }

    setImportData(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const headStyle = { marginBottom: 8 };
  const noteStyle = { fontSize: 12, color: palette.textMuted, marginBottom: 8 };
  const scopeNote = props.part === "layout"
    ? "Layout export/import applies only to this window."
    : "Hotkeys are shared across all windows, but an already-open window won't see an import until it reloads.";
  const missingText = props.part === "layout"
    ? "This file has no layout to import."
    : "This file has no hotkeys to import.";

  return (
    <div style={{ color: palette.text }}>
      <div style={{ ...noteStyle, marginBottom: 14 }} data-testid="scope-note">{scopeNote}</div>

      <div className="col-head serif" style={headStyle}>Export</div>
      <HoverButton
        className="btn" data-testid="download-json" onClick={download}
        hoverStyle={{ background: palette.surface }}
      >
        Download JSON
      </HoverButton>

      <div className="col-head serif" style={{ ...headStyle, marginTop: 22 }}>Import</div>
      <input
        type="file" accept="application/json" aria-label="Import settings file"
        data-testid="import-file" ref={fileInputRef} onChange={onFileSelected}
        style={{ display: "block", marginBottom: 10, fontSize: 12, color: palette.text }}
      />

      {importData && (
        partPresent ? (
          <HoverButton
            className="btn" data-testid="apply-import" onClick={applyImport}
            hoverStyle={{ background: palette.surface }}
          >
            Apply import
          </HoverButton>
        ) : (
          <div style={noteStyle}>{missingText}</div>
        )
      )}
    </div>
  );
}
