import { useState } from "react";
import type { LinkGroup } from "./linkGroups";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];
const sw = (g: Exclude<LinkGroup, null>, p: Palette): string =>
  ({ red: p.linkRed, green: p.linkGreen, blue: p.linkBlue, yellow: p.linkYellow }[g]);

// Popover opened from a panel's ledger-header swatch button (PanelFrame). Picking
// a group re-links the panel to that group's shared symbol; "Pinned" detaches it
// to its own settings.symbol. onClose is called both on pick (see PanelFrame) and
// on mouse-leave, matching the other chrome popovers (Catalog, SettingsModal).
export function GroupPicker({ group, onPick, onClose }: { group: LinkGroup; onPick: (g: LinkGroup) => void; onClose: () => void }): JSX.Element {
  const { palette } = useTheme();
  // Hover key mirrors the row-identity sentinel already used for selection
  // (group value, with `null` meaning the Pinned row); `undefined` means "not
  // hovering any row", distinct from the Pinned row's `null` identity.
  const [hoveredGroup, setHoveredGroup] = useState<LinkGroup | undefined>(undefined);
  const row = (sel: boolean, hovered: boolean): React.CSSProperties => ({ display: "flex", alignItems: "center", gap: 8, padding: "4px 6px", borderRadius: 4, cursor: "pointer", fontSize: 11.5,
    background: sel ? palette.surface : hovered ? "rgba(154,106,27,.06)" : "transparent", fontWeight: sel ? 600 : 400, transition: "background 120ms ease" });
  return (
    <div className="popover" style={{ top: 26, left: 6, width: 180 }} onMouseLeave={onClose}>
      <div className="col-head" style={{ marginBottom: 6 }}>Follows</div>
      {GROUPS.map((g) => (
        <div key={g} role="button" style={row(group === g, hoveredGroup === g)} onClick={() => { onPick(g); onClose(); }}
          onMouseEnter={() => setHoveredGroup(g)} onMouseLeave={() => setHoveredGroup((h) => (h === g ? undefined : h))}>
          <span style={{ width: 10, height: 10, borderRadius: 2, background: sw(g, palette) }} /> {g[0].toUpperCase() + g.slice(1)} group
        </div>
      ))}
      <div role="button" style={row(group === null, hoveredGroup === null)} onClick={() => { onPick(null); onClose(); }}
        onMouseEnter={() => setHoveredGroup(null)} onMouseLeave={() => setHoveredGroup((h) => (h === null ? undefined : h))}>
        <span style={{ width: 10, height: 10, borderRadius: 2, border: `1.5px solid ${palette.textMuted}` }} /> Pinned — own symbol
      </div>
      <div style={{ fontSize: 10, color: palette.textMuted, marginTop: 6, borderTop: `1px solid ${palette.border}`, paddingTop: 6, lineHeight: 1.4 }}>
        Panels in the same group load the same symbol together.
      </div>
    </div>
  );
}
