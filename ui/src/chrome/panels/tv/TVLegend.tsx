// ui/src/chrome/panels/tv/TVLegend.tsx
import { useEffect, useRef, useState } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import { INDICATOR_CATALOG, type IndicatorInstance } from "../../../render/chart/indicatorSeries";
import type { LegendView } from "./legendView";
import { IconEye, IconEyeOff, IconGear, IconClose, IconChevronDown } from "./tvIcons";
import { HoverButton } from "../../controls/HoverButton";
import { formatCompactShares } from "../../format";

export interface TVLegendHandle { update(view: LegendView): void }
export interface TVLegendProps {
  chrome: TvChrome; symbol: string; timeframe: string; instances: IndicatorInstance[]; floatShares: number | null; borrowStatus: string | null; paneOffsets: number[];
  rightAxisWidth: number;
  onToggleHidden: (id: string) => void; onEditIndicator: (id: string) => void; onRemoveIndicator: (id: string) => void;
  onClosePane: (paneIndex: number) => void; onToggleCollapsePane: (paneIndex: number) => void;
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

const formatBorrowStatus = (value: string | null): string => {
  switch (value?.trim().toLowerCase()) {
    case "easy_to_borrow": return "ETB";
    case "hard_to_borrow": return "HTB";
    default: return "-";
  }
};

export function TVLegend({ chrome, symbol, timeframe, instances, floatShares, borrowStatus, paneOffsets, rightAxisWidth, onToggleHidden, onEditIndicator, onRemoveIndicator, onClosePane, onToggleCollapsePane, legendRef }: TVLegendProps): JSX.Element {
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
        write("vol", v.volumeHidden ? "" : fmtVol(v.volume), tint);
        for (const row of v.indicators) {
          if (row.profile) {
            const { status, result, detail } = row.profile;
            const state = status === "ready" ? result?.partial ? "Partial" : "" : status === "empty" ? "No captured prints" : status === "idle" ? "No visible bars" : status.replaceAll("_", " ");
            const levels = result?.poc !== undefined ? ` · POC ${result.poc.toFixed(4)} · VAH ${result.vah?.toFixed(4)} · VAL ${result.val?.toFixed(4)}` : "";
            write(`profile-${row.instanceId}`, row.hidden ? "" : `${state}${result ? ` · Captured ${fmtVol(result.capturedVolume)}${levels}` : ""}`);
            const cell = cells.current.get(`profile-${row.instanceId}`);
            if (cell) cell.title = [detail, result && `Selected [${new Date(result.selection.fromMs).toISOString()}, ${new Date(result.selection.toMs).toISOString()})`, result && `Committed through ${new Date(result.asOfMs).toISOString()}`, result?.firstPrintMs && result.lastPrintMs && `First/last captured print: ${new Date(result.firstPrintMs).toISOString()} / ${new Date(result.lastPrintMs).toISOString()}; continuous coverage is unproven`].filter(Boolean).join("\n");
            continue;
          }
          row.values.forEach((val, idx) => write(
            `ind-${row.instanceId}-${idx}`,
            row.hidden || row.slotHidden?.[idx] ? "" : fmtPrice(val),
            row.colors[idx],
          ));
          // Always write (even blank) so a stale POSITIVE/NEGATIVE doesn't linger once the
          // signal goes back to null (e.g. scrubbed to a bar with a missing value).
          write(`sig-${row.instanceId}`, row.hidden ? "" : row.signal === "open" ? "POSITIVE" : row.signal === "close" ? "NEGATIVE" : "",
            row.signal === "open" ? chrome.up : row.signal === "close" ? chrome.down : undefined);
        }
      },
    };
    return () => { legendRef.current = null; };
  }, [legendRef, chrome]);

  const volumeInstance = instances.find((i) => i.type === "VOLUME");
  const overlayInstances = instances.filter((i) => i.type !== "VOLUME" && INDICATOR_CATALOG[i.type].slots[0].paneIndex === 0);
  const paneInstances = (pane: number) => instances.filter((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex === pane);
  const panes = Array.from(new Set(instances.map((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex))).filter((p) => p > 0);

  const val = (key: string, extraColor?: string): JSX.Element => <span data-testid={`legend-${key}`} ref={setCell(key)} style={{ color: extraColor ?? chrome.text }} />;

  const indicatorRow = (inst: IndicatorInstance, compactLabel?: string): JSX.Element => {
    const descs = INDICATOR_CATALOG[inst.type].slots;
    const hidden = (inst.hidden ?? false) || descs.every((slot) => inst.styles?.[slot.slot]?.hidden ?? false);
    const volume = inst.type === "VOLUME";
    return (
      <div key={inst.instanceId} data-testid={`legend-row-${inst.instanceId}`}
        onMouseEnter={() => setHovered(inst.instanceId)} onMouseLeave={() => setHovered((h) => (h === inst.instanceId ? null : h))}
        style={{ display: "flex", alignItems: "center", alignSelf: "flex-start", gap: 6, pointerEvents: "auto" }}>
        <span style={{ color: hidden ? chrome.muted : chrome.text }}>{compactLabel ?? legendLabel(inst)}</span>
        {inst.type === "VOLUME_PROFILE" ? val(`profile-${inst.instanceId}`) : descs.map((s, idx) => <span key={s.slot} data-testid={volume ? "legend-vol" : `legend-ind-${inst.instanceId}-${idx}`}
          ref={setCell(volume ? "vol" : `ind-${inst.instanceId}-${idx}`)} />)}
        {inst.type === "MACD" && (
          <span data-testid={`legend-sig-${inst.instanceId}`} ref={setCell(`sig-${inst.instanceId}`)} style={{ fontWeight: 600 }} />
        )}
        {hovered === inst.instanceId && (
          <span style={{ display: "inline-flex", gap: 2 }}>
            <HoverButton aria-label={`hide ${inst.instanceId}`} title={hidden ? "Show" : "Hide"} onClick={() => onToggleHidden(inst.instanceId)}
              style={ctrlBtn(chrome)} hoverStyle={{ background: chrome.hover, color: chrome.text }}>
              {hidden ? <IconEyeOff size={13} /> : <IconEye size={13} />}
            </HoverButton>
            <HoverButton aria-label={`settings ${inst.instanceId}`} title="Settings" onClick={() => onEditIndicator(inst.instanceId)}
              style={ctrlBtn(chrome)} hoverStyle={{ background: chrome.hover, color: chrome.text }}><IconGear size={13} /></HoverButton>
            <HoverButton aria-label={`remove ${inst.instanceId}`} title="Remove" onClick={() => onRemoveIndicator(inst.instanceId)}
              style={ctrlBtn(chrome)} hoverStyle={{ background: chrome.hover, color: chrome.text }}><IconClose size={13} /></HoverButton>
          </span>
        )}
      </div>
    );
  };

  return (
    <>
      <div style={legendBox(paneOffsets[0] ?? 0)}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 600 }}>
          <span>{bare} · {timeframe} · {formatBorrowStatus(borrowStatus)}</span>
          <span style={{ color: chrome.text }}>O</span>{val("o")}
          <span style={{ color: chrome.text }}>H</span>{val("h")}
          <span style={{ color: chrome.text }}>L</span>{val("l")}
          <span style={{ color: chrome.text }}>C</span>{val("c")}
          {val("chg")}
        </div>
        <div style={{ display: "flex", gap: 6 }}><span style={{ color: chrome.text }}>Float</span><span data-testid="legend-float" style={{ color: chrome.text }}>{formatCompactShares(floatShares)}</span></div>
        {volumeInstance && indicatorRow(volumeInstance, "Vol")}
        {overlayInstances.map((inst) => indicatorRow(inst))}
      </div>
      {panes.map((pane) => {
        const collapsed = paneInstances(pane).some((i) => i.collapsed);
        return (
          <div key={pane}>
            <div style={legendBox(paneOffsets[pane] ?? 0)}>
              {paneInstances(pane).map((inst) => indicatorRow(inst))}
            </div>
            <div style={paneControlBox(paneOffsets[pane] ?? 0)}>
              <HoverButton aria-label={collapsed ? `expand pane ${pane}` : `collapse pane ${pane}`} title={collapsed ? "Expand pane" : "Collapse pane"}
                onClick={() => onToggleCollapsePane(pane)} style={ctrlBtn(chrome)}
                hoverStyle={{ background: chrome.hover, color: chrome.text }}>
                <span style={{ display: "inline-flex", transform: collapsed ? "rotate(180deg)" : undefined }}>
                  <IconChevronDown size={13} />
                </span>
              </HoverButton>
              <HoverButton aria-label={`close pane ${pane}`} title="Close pane" onClick={() => onClosePane(pane)} style={ctrlBtn(chrome)}
                hoverStyle={{ background: chrome.hover, color: chrome.text }}>
                <IconClose size={13} />
              </HoverButton>
            </div>
          </div>
        );
      })}
    </>
  );

  function legendBox(top: number): React.CSSProperties {
    return { position: "absolute", top: top + 6, left: 8, zIndex: 5, pointerEvents: "none", font: `${TV_GEOM.uiFont}px ${TV_FONT}`,
      color: chrome.text, display: "flex", flexDirection: "column", gap: 2 };
  }

  function paneControlBox(top: number): React.CSSProperties {
    return { position: "absolute", top: top + 6, right: rightAxisWidth + 6, zIndex: 6, display: "flex", gap: 2, pointerEvents: "auto" };
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
