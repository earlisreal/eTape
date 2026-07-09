import { AppearanceSection } from "./AppearanceSection";
import { OrderSettingsSection } from "./exec/OrderSettingsSection";
import { VenuesSection } from "./exec/VenuesSection";
import { SoundsSection } from "../sound/SoundsSection";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useTheme } from "./ThemeProvider";
import type { AckMsg } from "../wire/contract";

export type SettingsSection = "appearance" | "orders" | "venues" | "sounds";
const NAV: { id: SettingsSection; label: string }[] = [
  { id: "appearance", label: "Appearance" },
  { id: "orders", label: "Orders & hotkeys" },
  { id: "venues", label: "Venues & creds" },
  { id: "sounds", label: "Sounds" },
];

export function SettingsModal({ open, section, onSection, onClose, commands }:
  { open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void; commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> } }): JSX.Element | null {
  const { palette } = useTheme();
  const oc = useOrderConfig();
  if (!open) return null;
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 920, maxHeight: "min(640px, 85vh)", overflow: "auto", display: "grid", gridTemplateColumns: "180px 1fr" }}>
        <nav style={{ borderRight: `1px solid ${palette.border}`, padding: 12 }}>
          <div className="serif" style={{ fontWeight: 600, marginBottom: 10 }}>Settings</div>
          {NAV.map((n) => (
            <button key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
              style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 4, background: "transparent",
                borderColor: "transparent", borderLeft: `3px solid ${section === n.id ? palette.accent : "transparent"}`,
                color: section === n.id ? palette.text : palette.textMuted, paddingLeft: 8 }}>
              {n.label}
            </button>
          ))}
        </nav>
        <section style={{ padding: 16 }}>
          {section === "appearance" && <AppearanceSection />}
          {section === "orders" && <OrderSettingsSection config={oc.config} onSave={oc.save} />}
          {section === "venues" && <VenuesSection commands={commands} />}
          {section === "sounds" && <SoundsSection />}
        </section>
      </div>
    </div>
  );
}
