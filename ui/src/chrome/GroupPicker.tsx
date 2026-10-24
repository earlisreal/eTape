import { useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { LinkGroup } from "./linkGroups";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";
import { Button } from "./controls/Button";
import { useDismissibleLayer } from "./controls/useDismissibleLayer";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];
const WIDTH = 180;
const sw = (g: Exclude<LinkGroup, null>, p: Palette): string =>
  ({ red: p.linkRed, green: p.linkGreen, blue: p.linkBlue, yellow: p.linkYellow }[g]);

// Popover opened from a panel's ledger-header swatch button (PanelFrame). Picking
// a group re-links the panel to that group's shared symbol; "Pinned" detaches it
// to its own settings.symbol. Click outside or Escape dismisses without a pick.
export function GroupPicker({ group, onPick, onClose, anchor }: { group: LinkGroup; onPick: (g: LinkGroup) => void; onClose: () => void; anchor?: HTMLElement | null }): JSX.Element | null {
  const { palette } = useTheme();
  const ref = useRef<HTMLDivElement>(null);
  useDismissibleLayer(ref, onClose, { anchor: anchor ?? null });
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
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
  const row = (sel: boolean): React.CSSProperties => ({ display: "flex", alignItems: "center", gap: 8, padding: "7px 6px", borderRadius: 4, cursor: "pointer", fontSize: 11.5,
    width: "100%", border: 0, background: sel ? palette.surface : "transparent", fontWeight: sel ? 600 : 400 });
  const hover = (sel: boolean) => ({ background: sel ? palette.surface : "rgba(154,106,27,.06)" });
  const picker = (
    <div ref={ref} className="popover" role="dialog" aria-label="Link group" style={anchor ? { position: "fixed", top: position?.top ?? 0, left: position?.left ?? 0, width: WIDTH, zIndex: 10001 } : { top: 26, left: 6, width: WIDTH }}>
      <div className="col-head" style={{ marginBottom: 6 }}>Follows</div>
      {GROUPS.map((g) => (
        <Button key={g} aria-pressed={group === g} style={row(group === g)} hoverStyle={hover(group === g)} onClick={() => { onPick(g); onClose(); }}>
          <span style={{ width: 10, height: 10, borderRadius: 2, background: sw(g, palette) }} /> {g[0].toUpperCase() + g.slice(1)} group
        </Button>
      ))}
      <Button aria-pressed={group === null} style={row(group === null)} hoverStyle={hover(group === null)} onClick={() => { onPick(null); onClose(); }}>
        <span style={{ width: 10, height: 10, borderRadius: 2, border: `1.5px solid ${palette.textMuted}` }} /> Pinned — own symbol
      </Button>
      <div style={{ fontSize: 10, color: palette.textMuted, marginTop: 6, borderTop: `1px solid ${palette.border}`, paddingTop: 6, lineHeight: 1.4 }}>
        Panels in the same group load the same symbol together.
      </div>
    </div>
  );
  return anchor ? (position ? createPortal(picker, document.body) : null) : picker;
}
