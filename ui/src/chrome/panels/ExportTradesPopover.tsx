// ui/src/chrome/panels/ExportTradesPopover.tsx
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Palette } from "../../render/palette";
import type { ExportFillsResult } from "../../wire/contract";
import type { ToastApi } from "../Toast";
import { HoverButton } from "../controls/HoverButton";

type Preset = "today" | "week" | "month" | "all" | "custom";

const PRESETS: Array<{ value: Preset; label: string }> = [
  { value: "today", label: "Today" },
  { value: "week", label: "This week" },
  { value: "month", label: "This month" },
  { value: "all", label: "All time" },
  { value: "custom", label: "Custom" },
];

export interface ExportTradesPopoverProps {
  palette: Palette;
  anchor: HTMLElement | null;
  venue: string;
  commands: { sendQuery(name: string, args: unknown): Promise<unknown> };
  toast: ToastApi;
  onClose: () => void;
}

const WIDTH = 220;

// Anchored dropdown for the Account panel's Export action. Same portal +
// fixed-position pattern as IndicatorPickerPopover (see that file's
// comment): the trigger sits inside PanelFrame's overflow:hidden header
// slot, so an absolutely-positioned child would be clipped.
export function ExportTradesPopover(
  { palette, anchor, venue, commands, toast, onClose }: ExportTradesPopoverProps,
): JSX.Element | null {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const [preset, setPreset] = useState<Preset>("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const rangeInvalid = preset === "custom" && !!from && !!to && from > to;

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

  const download = () => {
    void commands.sendQuery("ExportFills", {
      venue, preset, from: preset === "custom" ? from : "", to: preset === "custom" ? to : "",
    }).then((payload) => {
      const { csv, count } = payload as ExportFillsResult;
      if (!count) { toast.push({ level: "info", text: `No fills to export for ${venue}` }); return; }
      const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      const label = preset === "custom" ? `${from}_${to}` : preset;
      a.download = `etape-${venue}-${label}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      onClose();
    }).catch((err: unknown) => {
      toast.push({ level: "danger", text: `Export failed: ${err instanceof Error ? err.message : String(err)}` });
    });
  };

  const labelStyle = { fontSize: 11, color: palette.textMuted };
  const inputStyle = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, borderRadius: 4, padding: "3px 6px", fontSize: 12, width: "100%" };

  return createPortal(
    <div ref={ref} className="popover" role="menu" style={{
      position: "fixed", top: pos?.top ?? 0, left: pos?.left ?? 0, width: WIDTH, zIndex: 10001,
      background: palette.bg, color: palette.text, fontFamily: '"IBM Plex Sans", system-ui, sans-serif',
      fontVariantNumeric: "tabular-nums",
    }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "6px 10px" }}>
        <span style={labelStyle}>Export — {venue}</span>
        <select data-testid="export-preset" value={preset} onChange={(e) => setPreset(e.target.value as Preset)} style={inputStyle}>
          {PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
        </select>
        {preset === "custom" && (
          <>
            <label style={labelStyle}>From
              <input data-testid="export-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} style={{ ...inputStyle, marginTop: 2 }} />
            </label>
            <label style={labelStyle}>To
              <input data-testid="export-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} style={{ ...inputStyle, marginTop: 2 }} />
            </label>
            {rangeInvalid && <span style={{ fontSize: 11, color: palette.danger }}>From must be on or before To</span>}
          </>
        )}
        <HoverButton data-testid="export-download" onClick={download}
          disabled={preset === "custom" && (!from || !to || rangeInvalid)}
          style={{ marginTop: 4, padding: "5px 8px", border: `1px solid ${palette.border}`, borderRadius: 4, background: "transparent", color: palette.text, cursor: "pointer", fontSize: 12 }}
          hoverStyle={{ background: palette.surface }}>
          Download CSV
        </HoverButton>
      </div>
    </div>,
    document.body,
  );
}
