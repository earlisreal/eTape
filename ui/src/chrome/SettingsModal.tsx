import { AppearanceSection } from "./AppearanceSection";
import { OrderSettingsSection } from "./exec/OrderSettingsSection";
import { VenuesSection } from "./exec/VenuesSection";
import { SoundsSection } from "../sound/SoundsSection";
import { BackupSection } from "./BackupSection";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useTheme } from "./ThemeProvider";
import { HoverButton } from "./controls/HoverButton";
import type { AckMsg } from "../wire/contract";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";

export type SettingsSection = "appearance" | "orders" | "venues" | "sounds" | "backup";
const NAV: { id: SettingsSection; label: string }[] = [
  { id: "appearance", label: "Appearance" },
  { id: "orders", label: "Orders & hotkeys" },
  { id: "venues", label: "Venues & creds" },
  { id: "sounds", label: "Sounds" },
  { id: "backup", label: "Import & export" },
];

export function SettingsModal({ open, section, onSection, onClose, commands, getWorkspace, onImportWorkspace, toast }:
  {
    open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void;
    commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> };
    getWorkspace: () => Workspace; onImportWorkspace: (ws: Workspace) => void; toast: ToastApi;
  }): JSX.Element | null {
  const { palette } = useTheme();
  const oc = useOrderConfig();
  if (!open) return null;
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 920, height: "min(640px, 85vh)", display: "grid", gridTemplateColumns: "208px 1fr" }}>
        <nav style={{ borderRight: `1px solid ${palette.border}`, padding: "16px 12px", background: palette.surface }}>
          <div className="serif" style={{ fontSize: 16, fontWeight: 600, color: palette.text, paddingBottom: 10, marginBottom: 12, borderBottom: `3px double ${palette.borderStrong}` }}>
            Settings
          </div>
          {NAV.map((n) => (
            <HoverButton key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
              style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 2, background: section === n.id ? palette.bg : "transparent",
                borderColor: "transparent", borderRadius: 4, borderLeft: `4px solid ${section === n.id ? palette.accent : "transparent"}`,
                color: section === n.id ? palette.text : palette.textMuted, fontWeight: section === n.id ? 600 : 500,
                fontSize: 12, padding: "9px 10px" }}
              // className="btn" sets an inline background too, which permanently
              // defeats global.css's .btn:hover rules (see HoverButton's own doc
              // comment). Active rows already sit on palette.bg (bridging to the
              // content pane below, which is the same color); hovering an inactive
              // row previews that same bg so it reads as "about to become current."
              // The border-left accent stripe stays the true active/inactive
              // differentiator, so it never washes out.
              hoverStyle={{ background: palette.bg, color: palette.text }}>
              {n.label}
            </HoverButton>
          ))}
        </nav>
        <section style={{ padding: 16, overflow: "auto", minHeight: 0, background: palette.bg }}>
          {section === "appearance" && <AppearanceSection />}
          {section === "orders" && <OrderSettingsSection config={oc.config} onSave={oc.save} />}
          {section === "venues" && <VenuesSection commands={commands} />}
          {section === "sounds" && <SoundsSection />}
          {section === "backup" && <BackupSection getWorkspace={getWorkspace} onImportWorkspace={onImportWorkspace} orderConfig={oc.config} onImportOrderConfig={oc.save} toast={toast} />}
        </section>
      </div>
    </div>
  );
}
