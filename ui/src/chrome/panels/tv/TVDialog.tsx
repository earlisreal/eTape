import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import { modalTracker } from "../../modalTracker";
import { IconClose } from "./tvIcons";

export interface TVDialogProps {
  title: string;
  chrome: TvChrome;
  onClose: () => void;
  children: React.ReactNode;
  tabs?: string[];
  activeTab?: string;
  onTab?: (t: string) => void;
  footer?: { onDefaults?: () => void; onOk?: () => void; okLabel?: string };
  width?: number;
}

export function TVDialog({ title, chrome, onClose, children, tabs, activeTab, onTab, footer, width = 360 }: TVDialogProps): JSX.Element {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const drag = useRef<{ dx: number; dy: number } | null>(null);

  useEffect(() => {
    modalTracker.setOpen(true);
    return () => modalTracker.setOpen(false);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    const move = (e: PointerEvent) => {
      if (!drag.current) return;
      setPos({ x: e.clientX - drag.current.dx, y: e.clientY - drag.current.dy });
    };
    const up = () => { drag.current = null; };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); };
  }, []);

  const boxStyle: CSSProperties = pos
    ? { position: "fixed", left: pos.x, top: pos.y }
    : { position: "fixed", left: "50%", top: "50%", transform: "translate(-50%,-50%)" };

  const btn = (label: string, onClick: () => void, primary = false): JSX.Element => (
    <button role="button" aria-label={label} onClick={onClick}
      style={{ font: `600 ${TV_GEOM.uiFont}px ${TV_FONT}`, padding: "6px 14px", borderRadius: TV_GEOM.radius, cursor: "pointer",
        border: `1px solid ${primary ? chrome.accent : chrome.border}`, background: primary ? chrome.accent : "transparent",
        color: primary ? "#fff" : chrome.text }}>
      {label}
    </button>
  );

  return (
    <div data-testid="tv-dialog-scrim" onClick={onClose}
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.4)", zIndex: 10000, fontFamily: TV_FONT }}>
      <div data-testid="tv-dialog-box" onClick={(e) => e.stopPropagation()} style={{ ...boxStyle, width, background: chrome.surface,
        border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 8px 28px rgba(0,0,0,.28)",
        color: chrome.text, fontSize: TV_GEOM.uiFont, display: "flex", flexDirection: "column", maxHeight: "82vh" }}>
        <header
          onPointerDown={(e) => { drag.current = { dx: e.clientX - (pos?.x ?? 0), dy: e.clientY - (pos?.y ?? 0) };
            if (!pos) { const r = (e.currentTarget.parentElement as HTMLElement).getBoundingClientRect(); setPos({ x: r.left, y: r.top }); drag.current = { dx: e.clientX - r.left, dy: e.clientY - r.top }; } }}
          style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "10px 12px",
            borderBottom: `1px solid ${chrome.border}`, cursor: "move", fontWeight: 600 }}>
          <span>{title}</span>
          <button aria-label="close dialog" onClick={onClose}
            style={{ display: "flex", background: "transparent", border: "none", color: chrome.muted, cursor: "pointer" }}>
            <IconClose size={16} />
          </button>
        </header>

        {tabs && tabs.length > 0 && (
          <div role="tablist" style={{ display: "flex", gap: 4, padding: "8px 12px 0", borderBottom: `1px solid ${chrome.border}` }}>
            {tabs.map((t) => (
              <button key={t} role="tab" aria-selected={t === activeTab} aria-label={t} onClick={() => onTab?.(t)}
                style={{ font: `${TV_GEOM.uiFont}px ${TV_FONT}`, padding: "6px 10px", cursor: "pointer", background: "transparent",
                  border: "none", borderBottom: `2px solid ${t === activeTab ? chrome.accent : "transparent"}`,
                  color: t === activeTab ? chrome.text : chrome.muted }}>
                {t}
              </button>
            ))}
          </div>
        )}

        <div style={{ padding: 14, overflow: "auto" }}>{children}</div>

        {footer && (
          <footer style={{ display: "flex", justifyContent: "space-between", gap: 8, padding: "10px 12px", borderTop: `1px solid ${chrome.border}` }}>
            <div>{footer.onDefaults && btn("Defaults", footer.onDefaults)}</div>
            <div style={{ display: "flex", gap: 8 }}>
              {btn("Cancel", onClose)}
              {footer.onOk && btn(footer.okLabel ?? "Ok", footer.onOk, true)}
            </div>
          </footer>
        )}
      </div>
    </div>
  );
}
