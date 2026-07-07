import { AppearanceSection } from "./AppearanceSection";
import { OrderSettingsSection } from "./exec/OrderSettingsModal";
import { SoundsSection } from "../sound/SoundsSection";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useTheme } from "./ThemeProvider";
import type { ExecStatus } from "../wire/contract";

export type SettingsSection = "appearance" | "orders" | "sounds";
const NAV: { id: SettingsSection; label: string }[] = [
  { id: "appearance", label: "Appearance" }, { id: "orders", label: "Orders & hotkeys" }, { id: "sounds", label: "Sounds" },
];

export function SettingsModal({ open, section, onSection, onClose, status }:
  { open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void; status?: ExecStatus | null }): JSX.Element | null {
  const { palette } = useTheme();
  const oc = useOrderConfig();
  if (!open) return null;
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 680, maxHeight: "82vh", overflow: "auto", display: "grid", gridTemplateColumns: "160px 1fr" }}>
        <nav style={{ borderRight: `1px solid ${palette.border}`, padding: 12 }}>
          <div className="serif" style={{ fontWeight: 600, marginBottom: 10 }}>Settings</div>
          {NAV.map((n) => (
            <button key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
              style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 4, background: section === n.id ? palette.bg : "transparent", borderColor: section === n.id ? palette.accent : "transparent" }}>
              {n.label}
            </button>
          ))}
        </nav>
        <section style={{ padding: 16 }}>
          {section === "appearance" && <AppearanceSection />}
          {section === "orders" && <OrderSettingsSection config={oc.config} status={status ?? null} onSave={oc.save} />}
          {section === "sounds" && <SoundsSection />}
        </section>
      </div>
    </div>
  );
}
