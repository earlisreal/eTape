import { useState } from "react";
import type { Tool } from "../../render/chart/drawings/interaction";

export interface DrawingRailProps {
  activeTool: Tool;
  magnet: boolean;
  symbol: string;
  onSelectTool(t: Tool): void;
  onToggleMagnet(): void;
  hasSelection(): boolean;
  onDeleteSelection(): void;
  onClearAll(): void;
}

const TOOLS: { tool: Tool; label: string; glyph: string }[] = [
  { tool: "select", label: "select", glyph: "⌖" },
  { tool: "hline", label: "horizontal line", glyph: "─" },
  { tool: "hray", label: "horizontal ray", glyph: "╌►" },
  { tool: "trendline", label: "trendline", glyph: "╱" },
  { tool: "ray", label: "ray", glyph: "╱►" },
  { tool: "rect", label: "rectangle", glyph: "▭" },
  { tool: "measure", label: "measure", glyph: "↥" },
];

const railBtn = (active: boolean): React.CSSProperties => ({
  width: 24, height: 24, display: "flex", alignItems: "center", justifyContent: "center",
  fontSize: 13, lineHeight: 1, cursor: "pointer", borderRadius: 3,
  border: `1px solid ${active ? "var(--accent)" : "var(--border-strong)"}`,
  background: active ? "rgba(154,106,27,.08)" : "var(--bg)",
  color: active ? "var(--accent)" : "var(--text)",
});

export function DrawingRail(props: DrawingRailProps): JSX.Element {
  const { activeTool, magnet, symbol, onSelectTool, onToggleMagnet, hasSelection, onDeleteSelection, onClearAll } = props;
  const [confirmClear, setConfirmClear] = useState(false);

  const onTrash = () => {
    if (hasSelection()) { onDeleteSelection(); return; }
    setConfirmClear(true);
  };

  return (
    <div
      onPointerDown={(e) => e.stopPropagation()} // rail clicks must not reach the drawing pointer handlers
      style={{
        position: "absolute", left: 4, top: 4, zIndex: 5,
        display: "flex", flexDirection: "column", gap: 2, padding: 2,
        background: "var(--surface)", border: "1px solid var(--border-strong)", borderRadius: 4,
      }}
    >
      {TOOLS.map(({ tool, label, glyph }) => (
        <button key={tool} aria-label={label} aria-pressed={activeTool === tool}
          style={railBtn(activeTool === tool)} onClick={() => onSelectTool(tool)}>{glyph}</button>
      ))}
      <button aria-label="magnet" aria-pressed={magnet} style={railBtn(magnet)} onClick={onToggleMagnet}>🧲</button>
      <button aria-label="delete" aria-pressed={false} style={railBtn(false)} onClick={onTrash}>🗑</button>

      {confirmClear && (
        <div className="popover" role="dialog"
          style={{ position: "absolute", left: 30, bottom: 0, width: 200, padding: 8, fontSize: 12, zIndex: 6 }}>
          <div style={{ marginBottom: 8, color: "var(--text)" }}>Clear all drawings for {symbol}?</div>
          <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
            <button className="btn" onClick={() => setConfirmClear(false)}>Cancel</button>
            <button className="btn" style={{ borderColor: "var(--danger)", color: "var(--danger)" }}
              onClick={() => { onClearAll(); setConfirmClear(false); }}>Clear</button>
          </div>
        </div>
      )}
    </div>
  );
}
