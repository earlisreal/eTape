// ui/src/chrome/panels/tv/TVLegend.tsx
import { useEffect, useRef, useState } from "react";
import { TV_FONT, type TvChrome } from "../../../render/chart/tvTheme";
import { INDICATOR_CATALOG, type IndicatorInstance } from "../../../render/chart/indicatorSeries";
import type { LegendView } from "./legendView";
import { IconEye, IconEyeOff, IconGear, IconClose } from "./tvIcons";

export interface TVLegendHandle { update(view: LegendView): void }
export interface TVLegendProps {
  chrome: TvChrome; symbol: string; timeframe: string; instances: IndicatorInstance[]; paneOffsets: number[];
  onToggleHidden: (id: string) => void; onEditIndicator: (id: string) => void; onRemoveIndicator: (id: string) => void;
  legendRef: React.MutableRefObject<TVLegendHandle | null>;
}

const fmtPrice = (n: number | null): string => (n === null ? "—" : n.toFixed(2));
const fmtVol = (n: number | null): string => {
  if (n === null) return "—";
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return `${n}`;
};

export function TVLegend({ chrome, symbol, timeframe, instances, paneOffsets, onToggleHidden, onEditIndicator, onRemoveIndicator, legendRef }: TVLegendProps): JSX.Element {
  const cells = useRef(new Map<string, HTMLElement>());
  const [hovered, setHovered] = useState<string | null>(null);
  const bare = symbol.replace(/^US\./, "");

  const setCell = (key: string) => (el: HTMLElement | null) => { if (el) cells.current.set(key, el); };
  const write = (key: string, text: string, color?: string) => {
    const el = cells.current.get(key);
    if (!el) return;
    el.textContent = text;
    if (color) el.style.color = color;
  };

  useEffect(() => {
    legendRef.current = {
      update(v: LegendView) {
        const tint = v.up ? chrome.up : chrome.down;
        write("o", fmtPrice(v.o), tint); write("h", fmtPrice(v.h), tint);
        write("l", fmtPrice(v.l), tint); write("c", fmtPrice(v.c), tint);
        write("chg", v.changePct === null ? "" : `${v.changePct >= 0 ? "+" : ""}${v.changePct.toFixed(2)}%`, tint);
        write("vol", fmtVol(v.volume), chrome.muted);
        for (const row of v.indicators) {
          row.values.forEach((val, idx) => write(`ind-${row.instanceId}-${idx}`, fmtPrice(val), row.colors[idx]));
        }
      },
    };
    return () => { legendRef.current = null; };
  }, [legendRef, chrome]);

  const overlayInstances = instances.filter((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex === 0);
  const paneInstances = (pane: number) => instances.filter((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex === pane);
  const panes = Array.from(new Set(instances.map((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex))).filter((p) => p > 0);

  const val = (key: string, extraColor?: string): JSX.Element => <span data-testid={`legend-${key}`} ref={setCell(key)} style={{ color: extraColor ?? chrome.text }} />;

  const indicatorRow = (inst: IndicatorInstance): JSX.Element => {
    const descs = INDICATOR_CATALOG[inst.type].slots;
    return (
      <div key={inst.instanceId} data-testid={`legend-row-${inst.instanceId}`}
        onMouseEnter={() => setHovered(inst.instanceId)} onMouseLeave={() => setHovered((h) => (h === inst.instanceId ? null : h))}
        style={{ display: "flex", alignItems: "center", gap: 6, pointerEvents: "auto" }}>
        <span style={{ color: chrome.muted }}>{legendLabel(inst)}</span>
        {descs.map((s, idx) => <span key={s.slot} data-testid={`legend-ind-${inst.instanceId}-${idx}`} ref={setCell(`ind-${inst.instanceId}-${idx}`)} />)}
        {hovered === inst.instanceId && (
          <span style={{ display: "inline-flex", gap: 2 }}>
            <button aria-label={`hide ${inst.instanceId}`} onClick={() => onToggleHidden(inst.instanceId)} style={ctrlBtn(chrome)}>
              {inst.hidden ? <IconEyeOff size={13} /> : <IconEye size={13} />}
            </button>
            <button aria-label={`settings ${inst.instanceId}`} onClick={() => onEditIndicator(inst.instanceId)} style={ctrlBtn(chrome)}><IconGear size={13} /></button>
            <button aria-label={`remove ${inst.instanceId}`} onClick={() => onRemoveIndicator(inst.instanceId)} style={ctrlBtn(chrome)}><IconClose size={13} /></button>
          </span>
        )}
      </div>
    );
  };

  return (
    <>
      <div style={legendBox(paneOffsets[0] ?? 0)}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 600 }}>
          <span>{bare} · {timeframe} · eTape</span>
          <span style={{ color: chrome.muted }}>O</span>{val("o")}
          <span style={{ color: chrome.muted }}>H</span>{val("h")}
          <span style={{ color: chrome.muted }}>L</span>{val("l")}
          <span style={{ color: chrome.muted }}>C</span>{val("c")}
          {val("chg")}
        </div>
        <div style={{ display: "flex", gap: 6 }}><span style={{ color: chrome.muted }}>Vol</span>{val("vol", chrome.muted)}</div>
        {overlayInstances.map(indicatorRow)}
      </div>
      {panes.map((pane) => (
        <div key={pane} style={legendBox(paneOffsets[pane] ?? 0)}>
          {paneInstances(pane).map(indicatorRow)}
        </div>
      ))}
    </>
  );

  function legendBox(top: number): React.CSSProperties {
    return { position: "absolute", top: top + 6, left: 8, zIndex: 5, pointerEvents: "none", font: `12px ${TV_FONT}`,
      color: chrome.text, display: "flex", flexDirection: "column", gap: 2 };
  }
}

function legendLabel(inst: IndicatorInstance): string {
  const entry = INDICATOR_CATALOG[inst.type];
  const nums = entry.params.map((p) => inst.params[p.key] ?? p.default).join(" ");
  const src = inst.type === "EMA" || inst.type === "SMA" ? " close" : "";
  return nums ? `${entry.label} ${nums}${src}` : entry.label;
}

function ctrlBtn(chrome: TvChrome): React.CSSProperties {
  return { display: "grid", placeItems: "center", width: 18, height: 18, background: "transparent", border: "none",
    color: chrome.muted, cursor: "pointer", pointerEvents: "auto" };
}
