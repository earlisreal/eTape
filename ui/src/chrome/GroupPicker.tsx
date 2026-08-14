import { useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { LinkGroup } from "./linkGroups";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];
const WIDTH = 180;
const sw = (g: Exclude<LinkGroup, null>, p: Palette): string =>
  ({ red: p.linkRed, green: p.linkGreen, blue: p.linkBlue, yellow: p.linkYellow }[g]);

// Popover opened from a panel's ledger-header swatch button (PanelFrame). Picking
// a group re-links the panel to that group's shared symbol; "Pinned" detaches it
// to its own settings.symbol. onClose is called both on pick (see PanelFrame) and
// on mouse-leave, matching the other chrome popovers (Catalog, SettingsModal).
export function GroupPicker({ group, onPick, onClose, anchor }: { group: LinkGroup; onPick: (g: LinkGroup) => void; onClose: () => void; anchor?: HTMLElement | null }): JSX.Element | null {
  const { palette } = useTheme();
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  // Hover key mirrors the row-identity sentinel already used for selection
  // (group value, with `null` meaning the Pinned row); `undefined` means "not
  // hovering any row", distinct from the Pinned row's `null` identity.
  const [hoveredGroup, setHoveredGroup] = useState<LinkGroup | undefined>(undefined);
  useLayoutEffect(() => {
    if (!anchor) return;
    const place = () => {
      const rect = anchor.getBoundingClientRect();
      setPosition({ top: rect.bottom + 2, left: Math.min(Math.max(rect.left - 4, 8), window.innerWidth - WIDTH - 8) });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [anchor]);
  const row = (sel: boolean, hovered: boolean): React.CSSProperties => ({ display: "flex", alignItems: "center", gap: 8, padding: "4px 6px", borderRadius: 4, cursor: "pointer", fontSize: 11.5,
    background: sel ? palette.surface : hovered ? "rgba(154,106,27,.06)" : "transparent", fontWeight: sel ? 600 : 400, transition: "background 120ms ease" });
  const picker = (
    <div className="popover" style={anchor ? { position: "fixed", top: position?.top ?? 0, left: position?.left ?? 0, width: WIDTH, zIndex: 10001 } : { top: 26, left: 6, width: WIDTH }} onMouseLeave={onClose}>
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
  return anchor ? (position ? createPortal(picker, document.body) : null) : picker;
}
