import { AppearanceSection } from "./AppearanceSection";
import { OrderSettingsSection } from "./exec/OrderSettingsSection";
import { VenuesSection } from "./exec/VenuesSection";
import { SoundsSection } from "../sound/SoundsSection";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useTheme } from "./ThemeProvider";
import { HoverButton } from "./controls/HoverButton";
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
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 920, height: "min(640px, 85vh)", display: "grid", gridTemplateColumns: "180px 1fr" }}>
        <nav style={{ borderRight: `1px solid ${palette.border}`, padding: 12 }}>
          <div className="serif" style={{ fontWeight: 600, marginBottom: 10 }}>Settings</div>
          {NAV.map((n) => (
            <HoverButton key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
              style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 4, background: "transparent",
                borderColor: "transparent", borderLeft: `3px solid ${section === n.id ? palette.accent : "transparent"}`,
                color: section === n.id ? palette.text : palette.textMuted, paddingLeft: 8 }}
              // className="btn" sets an inline background too, which permanently
              // defeats global.css's .btn:hover rules (see HoverButton's own doc
              // comment). The active tab's own resting color already IS the
              // "highlighted" reading, so hover just promotes the muted/inactive
              // tab to the same text color — same technique as
              // ChartHeaderControls' timeframe hover (active keeps its own
              // resting color, inactive brightens toward it). The border-left
              // accent stripe (untouched here, preserved from base style) stays
              // the true active/inactive differentiator, so it never washes out.
              hoverStyle={{ background: palette.surface, color: palette.text }}>
              {n.label}
            </HoverButton>
          ))}
        </nav>
        <section style={{ padding: 16, overflow: "auto", minHeight: 0 }}>
          {section === "appearance" && <AppearanceSection />}
          {section === "orders" && <OrderSettingsSection config={oc.config} onSave={oc.save} />}
          {section === "venues" && <VenuesSection commands={commands} />}
          {section === "sounds" && <SoundsSection />}
        </section>
      </div>
    </div>
  );
}
