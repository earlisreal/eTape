// ui/src/chrome/panels/tv/IndicatorPickerPopover.tsx
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { INDICATOR_CATALOG, type IndicatorType } from "../../../render/chart/indicatorSeries";
import type { Palette } from "../../../render/palette";

export interface IndicatorPickerPopoverProps {
  palette: Palette; anchor: HTMLElement | null; onClose: () => void; onAdd: (type: IndicatorType) => void;
}

const WIDTH = 200;

// Header-anchored dropdown for adding an indicator (replaces the old centered
// TVDialog modal — picking one of 5 catalog entries doesn't warrant a full-screen
// scrim over the chart). Portalled to document.body with `position: fixed`
// computed from the anchor button's rect: the button sits inside two
// `overflow: hidden` containers (PanelFrame's header slot, ChartHeaderControls'
// root), so an absolute-positioned child would be clipped to the header row —
// same reason TVContextMenu portals/fixed-positions instead of nesting.
export function IndicatorPickerPopover({ palette, anchor, onClose, onAdd }: IndicatorPickerPopoverProps): JSX.Element | null {
  const ref = useRef<HTMLDivElement | null>(null);
  const [q, setQ] = useState("");
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const entries = useMemo(() => Object.values(INDICATOR_CATALOG), []);
  const filtered = entries.filter((e) => e.label.toLowerCase().includes(q.trim().toLowerCase()));

  useLayoutEffect(() => {
    if (!anchor) { setPos(null); return; }
    const place = () => {
      const rect = anchor.getBoundingClientRect();
      const left = Math.min(Math.max(rect.left, 8), window.innerWidth - WIDTH - 8);
      setPos({ top: rect.bottom + 4, left });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [anchor]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (ref.current && !ref.current.contains(t) && !(anchor && anchor.contains(t))) onClose();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); window.removeEventListener("keydown", onKey); };
  }, [anchor, onClose]);

  if (!pos && anchor) return null; // first-tick guard: position not measured yet

  return createPortal(
    <div ref={ref} className="popover" role="menu" style={{
      position: "fixed", top: pos?.top ?? 0, left: pos?.left ?? 0, width: WIDTH, zIndex: 10001,
      background: palette.bg, color: palette.text, fontFamily: '"IBM Plex Sans", system-ui, sans-serif',
      fontVariantNumeric: "tabular-nums",
    }}>
      <input placeholder="Search" value={q} onChange={(e) => setQ(e.target.value)} autoFocus
        style={{ width: "100%", boxSizing: "border-box", padding: "6px 8px", marginBottom: 8, background: palette.bg,
          border: `1px solid ${palette.border}`, borderRadius: 4, color: palette.text }} />
      <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {filtered.map((e) => (
          <button key={e.type} aria-label={`add ${e.label}`} onClick={() => { onAdd(e.type); onClose(); }}
            style={{ textAlign: "left", padding: "8px 10px", background: "transparent", border: "none", borderRadius: 4,
              color: palette.text, cursor: "pointer" }}
            onMouseEnter={(ev) => (ev.currentTarget.style.background = palette.surface)}
            onMouseLeave={(ev) => (ev.currentTarget.style.background = "transparent")}>
            {e.label}
          </button>
        ))}
        {filtered.length === 0 && <div style={{ color: palette.textMuted, padding: "8px 10px" }}>No matches</div>}
      </div>
    </div>,
    document.body,
  );
}
