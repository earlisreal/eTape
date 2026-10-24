import { useEffect, useRef, useState, type CSSProperties } from "react";
import type { TvChrome } from "../../../render/chart/tvTheme";
import { Dialog } from "../../controls/Dialog";
import { Button } from "../../controls/Button";

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

export function TVDialog({ title, onClose, children, tabs, activeTab, onTab, footer, width = 360 }: TVDialogProps): JSX.Element {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const drag = useRef<{ dx: number; dy: number; width: number; height: number } | null>(null);
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = drag.current;
      if (!d) return;
      setPos({ x: Math.max(8, Math.min(e.clientX - d.dx, window.innerWidth - d.width - 8)),
        y: Math.max(8, Math.min(e.clientY - d.dy, window.innerHeight - d.height - 8)) });
    };
    const up = () => { drag.current = null; };
    const resize = () => setPos(null);
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("resize", resize);
    return () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); window.removeEventListener("resize", resize); };
  }, []);
  const boxStyle: CSSProperties = pos ? { position: "fixed", left: pos.x, top: pos.y, maxWidth: "calc(100vw - 20px)" } : {};
  return (
    <Dialog title={title} onClose={onClose} width={width} style={boxStyle} testId="tv-dialog-box" scrimTestId="tv-dialog-scrim" closeLabel="close dialog"
      headerProps={{ style: { cursor: "move", touchAction: "none" }, onPointerDown: (e) => {
        if (e.button !== 0) return;
        const r = e.currentTarget.parentElement!.getBoundingClientRect();
        drag.current = { dx: e.clientX - r.left, dy: e.clientY - r.top, width: r.width, height: r.height };
        setPos({ x: r.left, y: r.top });
        e.preventDefault();
      } }}
      footer={footer && <>
        <div>{footer.onDefaults && <Button onClick={footer.onDefaults}>Defaults</Button>}</div>
        <div className="dialog-actions"><Button onClick={onClose}>Cancel</Button>
          {footer.onOk && <Button variant="primary" onClick={footer.onOk}>{footer.okLabel ?? "Ok"}</Button>}
        </div>
      </>}>
      {tabs && tabs.length > 0 && <div role="tablist" aria-label={`${title} sections`} className="dialog-tabs">
        {tabs.map((t) => <Button key={t} role="tab" aria-selected={t === activeTab} aria-label={t} onClick={() => onTab?.(t)}>{t}</Button>)}
      </div>}
      {children}
    </Dialog>
  );
}
