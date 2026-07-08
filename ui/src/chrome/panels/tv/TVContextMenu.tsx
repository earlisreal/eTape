import { useEffect, useRef } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";

export type MenuEntry = { label: string; icon?: JSX.Element; onClick: () => void; danger?: boolean } | "separator";
export interface TVContextMenuProps { chrome: TvChrome; x: number; y: number; items: MenuEntry[]; onClose: () => void }

export function TVContextMenu({ chrome, x, y, items, onClose }: TVContextMenuProps): JSX.Element {
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const onDown = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) onClose(); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); window.removeEventListener("keydown", onKey); };
  }, [onClose]);

  return (
    <div ref={ref} role="menu" style={{ position: "fixed", left: x, top: y, zIndex: 10001, minWidth: 200,
      background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius,
      boxShadow: "0 6px 22px rgba(0,0,0,.24)", padding: 4, font: `${TV_GEOM.uiFont}px ${TV_FONT}`, color: chrome.text }}>
      {items.map((it, i) =>
        it === "separator" ? (
          <div key={`sep-${i}`} style={{ height: 1, background: chrome.border, margin: "4px 0" }} />
        ) : (
          <button key={it.label} role="button" aria-label={it.label} onClick={() => { it.onClick(); onClose(); }}
            style={{ display: "flex", alignItems: "center", gap: 8, width: "100%", textAlign: "left", padding: "6px 10px",
              background: "transparent", border: "none", borderRadius: 4, cursor: "pointer",
              color: it.danger ? chrome.down : chrome.text }}
            onMouseEnter={(e) => (e.currentTarget.style.background = chrome.hover)}
            onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}>
            <span style={{ width: 16, display: "inline-flex" }}>{it.icon}</span>{it.label}
          </button>
        ),
      )}
    </div>
  );
}
