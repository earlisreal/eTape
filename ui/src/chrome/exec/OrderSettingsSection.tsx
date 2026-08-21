// Task 11 history: this was OrderSettingsModal (a standalone overlay). Since the
// settings unification it is a plain section body embedded in SettingsModal's
// "Orders & hotkeys" tab. This revision (production redesign) replaces the dense
// grid table with a card-per-template editor: every template parameter is still
// editable — including price offset (value + $/%, now with a ±0.05 stepper) and
// sizing amount (now with a mode-aware stepper, clamped to 100 for the three
// percent-based sizing modes) — and management templates can still be created.
//
// Cards render in `templates` array order (not grouped by kind) so that
// getAllByTitle("remove") stays in insertion order — addPlace/addManage always
// append to the end of the array, and a card-layout regrouped by kind would
// reorder a newly-added card ahead of the other kind's trailing cards, breaking
// the "last remove button = most recently added template" invariant the
// stale-raw-edit-on-reused-id regression test relies on.
import { useEffect, useState, type CSSProperties, type DragEvent } from "react";
import { useTheme } from "../ThemeProvider";
import { FONTS, type Palette } from "../../render/palette";
import { HoverButton } from "../controls/HoverButton";
import { Button } from "../controls/Button";
import type { Side, OrderType, TIF, OrderSession } from "../../wire/contract";
import type { ToastApi } from "../Toast";
import type { PriceSource, PriceOffsetUnit } from "./priceSource";
import type { SizingSpec, SizingMode } from "./sizing";
import {
  DEFAULT_TEMPLATES, normalizeOrderConfig, type ActionTemplate, type DeckColor, type ManagementAction,
  type HotkeyDeckConfig, type OrderConfig, type PlaceOrderTemplate,
} from "./actionTemplate";
import { normalizeCombo } from "./hotkeys";
import { Keycap } from "./Keycap";
import { StepField } from "./StepField";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SESSIONS: OrderSession[] = ["AUTO", "RTH", "EXTENDED", "OVERNIGHT"];
const SESSION_LABEL: Record<OrderSession, string> = { AUTO: "Auto", RTH: "Regular", EXTENDED: "Extended", OVERNIGHT: "Overnight" };
const SOURCES: PriceSource[] = ["Bid", "Ask", "Last", "Mid"];
const MODES: SizingMode[] = ["Dollar", "CashPct", "BuyingPowerPct", "Shares", "PositionFraction"];
const MODE_LABEL: Record<SizingMode, string> = { Dollar: "Dollar", CashPct: "Cash %", BuyingPowerPct: "BP %", Shares: "Shares", PositionFraction: "Pos %" };
const MANAGE_ACTIONS: ManagementAction[] = ["CancelLast", "CancelAllFocused", "CancelAllEverything", "KillSwitch"];

const OFFSET_STEP = 0.05;
const SIZE_STEP: Record<SizingMode, number> = { Dollar: 100, CashPct: 1, BuyingPowerPct: 1, Shares: 1, PositionFraction: 1 };
// Percent-based modes have a natural 100% ceiling; Dollar and Shares are
// unbounded above.
const SIZE_MAX: Partial<Record<SizingMode, number>> = { CashPct: 100, BuyingPowerPct: 100, PositionFraction: 100 };
const isPercentMode = (m: SizingMode): boolean => m === "CashPct" || m === "BuyingPowerPct" || m === "PositionFraction";

// Rounds away float drift from repeated ±0.05 additions (e.g. 0.05+0.05 can
// land on 0.09999999999999999 in double precision).
function round2(n: number): number {
  return Math.round(n * 100) / 100;
}
function clampNum(n: number, min: number, max?: number): number {
  const v = Math.max(min, n);
  return max === undefined ? v : Math.min(max, v);
}
// Leading "+" on a positive offset so its sign is unambiguous at a glance
// (negatives already render "-" via String(), 0 shows plain "0").
function fmtOffset(n: number): string {
  return n > 0 ? `+${n}` : String(n);
}

function sizingValue(s: SizingSpec): string {
  switch (s.mode) {
    case "Dollar": return String(s.dollar ?? 0);
    case "Shares": return String(s.shares ?? 0);
    case "CashPct":
    case "BuyingPowerPct":
    case "PositionFraction": return String(s.pct ?? 0);
  }
}
// Every commit path — typed or nudged — runs through here, so the 100% cap on
// Cash % / Buying-power % / Position % sizing holds no matter how the value was entered.
function setSizingValue(s: SizingSpec, n: number): SizingSpec {
  const max = SIZE_MAX[s.mode];
  switch (s.mode) {
    case "Dollar": return { mode: "Dollar", dollar: clampNum(n, 0, max) };
    case "Shares": return { mode: "Shares", shares: Math.floor(clampNum(n, 0, max)) };
    case "CashPct": return { mode: "CashPct", pct: clampNum(n, 0, max) };
    case "BuyingPowerPct": return { mode: "BuyingPowerPct", pct: clampNum(n, 0, max) };
    case "PositionFraction": return { mode: "PositionFraction", pct: clampNum(n, 0, max) };
  }
}
function nudgeSizing(s: SizingSpec, dir: 1 | -1): SizingSpec {
  return setSizingValue(s, Number(sizingValue(s)) + dir * SIZE_STEP[s.mode]);
}
function modeToSpec(mode: SizingMode): SizingSpec {
  switch (mode) {
    case "Dollar": return { mode, dollar: 0 };
    case "Shares": return { mode, shares: 100 };
    case "CashPct": return { mode, pct: 25 };
    case "BuyingPowerPct": return { mode, pct: 25 };
    case "PositionFraction": return { mode, pct: 100 };
  }
}

function iconBtn(palette: Palette, color: string): CSSProperties {
  return {
    width: 22, height: 22, display: "inline-flex", alignItems: "center", justifyContent: "center",
    background: palette.bg, border: `1px solid ${palette.border}`, borderRadius: 4,
    cursor: "pointer", fontSize: 13, lineHeight: 1, color,
  };
}

// Deck color swatches (4b): tint a palette hex token into a translucent
// background so the swatch stays legible in both themes without inventing a
// new color system — every swatch's color still traces back to a real
// palette token (up/down/accent/textMuted/danger), never a hardcoded hex.
function tint(hex: string, alpha: number): string {
  const h = hex.replace("#", "");
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}

const DECK_COLORS: DeckColor[] = ["auto", "green", "red", "bronze", "neutral", "danger"];

function cloneHotkeyDeck(deck: HotkeyDeckConfig | undefined): HotkeyDeckConfig {
  return { rows: (deck?.rows ?? []).map((row) => [...row]), showHotkeyLabels: deck?.showHotkeyLabels === true };
}

function removeDeckPlacement(rows: string[][], id: string): string[][] {
  return rows.flatMap((row) => {
    const next = row.filter((item) => item !== id);
    return row.length > 0 && next.length === 0 ? [] : [next];
  });
}

function moveDeckPlacement(rows: string[][], id: string, targetRow: number, targetIndex: number): string[][] {
  const next = rows.map((row) => [...row]);
  const sourceRow = next.findIndex((row) => row.includes(id));
  if (sourceRow < 0 || targetRow < 0 || targetRow >= next.length) return next;
  const sourceIndex = next[sourceRow].indexOf(id);
  next[sourceRow].splice(sourceIndex, 1);
  const insertionIndex = sourceRow === targetRow && sourceIndex < targetIndex ? targetIndex - 1 : targetIndex;
  next[targetRow].splice(Math.max(0, Math.min(insertionIndex, next[targetRow].length)), 0, id);
  return next;
}

// The selected swatch gets an outer ring (boxShadow) — a concrete, testable
// visual signal distinct from the swatch's own border, so selection state
// can be asserted independent of color.
function swatchStyle(palette: Palette, color: DeckColor, selected: boolean): CSSProperties {
  const base: CSSProperties = {
    width: 16, height: 16, borderRadius: 4, cursor: "pointer", padding: 0,
    boxShadow: selected ? `0 0 0 2px ${palette.text}` : "none",
  };
  switch (color) {
    case "auto": return { ...base, background: "transparent", border: `1px dashed ${palette.textMuted}` };
    case "green": return { ...base, background: tint(palette.up, 0.28), border: `1px solid ${palette.up}` };
    case "red": return { ...base, background: tint(palette.down, 0.28), border: `1px solid ${palette.down}` };
    case "bronze": return { ...base, background: tint(palette.accent, 0.28), border: `1px solid ${palette.accent}` };
    case "neutral": return { ...base, background: tint(palette.textMuted, 0.28), border: `1px solid ${palette.textMuted}` };
    case "danger": return { ...base, background: tint(palette.danger, 0.28), border: `1px solid ${palette.danger}` };
  }
}

interface DeckLayoutEditorProps {
  palette: Palette;
  rows: string[][];
  templates: ActionTemplate[];
  showHotkeyLabels: boolean;
  onShowHotkeyLabels: (show: boolean) => void;
  onAddRow: () => void;
  onMove: (id: string, targetRow: number, targetIndex: number) => void;
  onRemove: (id: string) => void;
}

// Module-scope and passive: this editor only moves template ids. It never
// renders or invokes an order action, so drag/drop cannot become a second
// execution path.
function DeckLayoutEditor({ palette, rows, templates, showHotkeyLabels, onShowHotkeyLabels, onAddRow, onMove, onRemove }: DeckLayoutEditorProps): JSX.Element {
  const templateById = new Map(templates.map((t) => [t.id, t]));
  const startDrag = (e: DragEvent<HTMLDivElement>, id: string) => {
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", id);
  };
  const allowDrop = (e: DragEvent<HTMLElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  };
  const drop = (e: DragEvent<HTMLElement>, rowIndex: number, index: number) => {
    e.preventDefault();
    e.stopPropagation();
    const id = e.dataTransfer.getData("text/plain");
    if (id) onMove(id, rowIndex, index);
  };
  const controlStyle: CSSProperties = {
    border: `1px solid ${palette.border}`, background: palette.bg, color: palette.textMuted,
    borderRadius: 3, cursor: "pointer", minWidth: 22, height: 22, lineHeight: 1,
  };

  return (
    <div data-testid="deck-layout-editor" style={{ border: `1px solid ${palette.border}`, borderRadius: 5, padding: "8px 10px", marginBottom: 10 }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8, marginBottom: 8 }}>
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11 }}>
          <input
            type="checkbox" data-testid="show-hotkey-labels" aria-label="Show hotkey labels"
            checked={showHotkeyLabels} onChange={(e) => onShowHotkeyLabels(e.target.checked)}
          />
          Show hotkey labels
        </label>
        <Button type="button" data-testid="add-deck-row" onClick={onAddRow}>+ Add row</Button>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
        {rows.map((row, rowIndex) => (
          <div
            key={`deck-row-${rowIndex}`} data-testid={`deck-row-${rowIndex}`} data-row-index={rowIndex}
            onDragOver={allowDrop} onDrop={(e) => drop(e, rowIndex, row.length)}
            style={{ display: "flex", alignItems: "center", gap: 4, minHeight: 34, padding: 4, overflowX: "auto", border: `1px dashed ${palette.border}`, borderRadius: 4 }}
          >
            <span style={{ color: palette.textMuted, fontSize: 10, width: 42, flex: "0 0 auto" }}>Row {rowIndex + 1}</span>
            {row.map((id, index) => {
              const template = templateById.get(id);
              if (!template) return null;
              return (
                <div
                  key={id} data-testid={`deck-placement-${id}`} data-deck-placement-id={id}
                  draggable onDragStart={(e) => startDrag(e, id)} onDragOver={allowDrop} onDrop={(e) => drop(e, rowIndex, index)}
                  style={{ display: "inline-flex", alignItems: "center", gap: 3, flex: "0 0 auto", padding: "3px 4px", border: `1px solid ${palette.border}`, borderRadius: 4, background: palette.surface }}
                >
                  <span>{template.label}</span>
                  <button type="button" data-testid={`deck-left-${id}`} aria-label={`Move ${template.label} left`} disabled={index === 0}
                    onClick={() => onMove(id, rowIndex, index - 1)} style={{ ...controlStyle, opacity: index === 0 ? 0.35 : 1 }}>←</button>
                  <button type="button" data-testid={`deck-right-${id}`} aria-label={`Move ${template.label} right`} disabled={index === row.length - 1}
                    onClick={() => onMove(id, rowIndex, index + 2)} style={{ ...controlStyle, opacity: index === row.length - 1 ? 0.35 : 1 }}>→</button>
                  <button type="button" data-testid={`deck-up-${id}`} aria-label={`Move ${template.label} to previous row`} disabled={rowIndex === 0}
                    onClick={() => onMove(id, rowIndex - 1, index)} style={{ ...controlStyle, opacity: rowIndex === 0 ? 0.35 : 1 }}>↑</button>
                  <button type="button" data-testid={`deck-down-${id}`} aria-label={`Move ${template.label} to next row`} disabled={rowIndex === rows.length - 1}
                    onClick={() => onMove(id, rowIndex + 1, index)} style={{ ...controlStyle, opacity: rowIndex === rows.length - 1 ? 0.35 : 1 }}>↓</button>
                  <button type="button" data-testid={`deck-remove-${id}`} aria-label={`Remove ${template.label} from deck`} onClick={() => onRemove(id)} style={{ ...controlStyle, color: palette.danger }}>×</button>
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

interface TemplateCardProps {
  t: ActionTemplate;
  palette: Palette;
  dup: boolean;
  isFirst: boolean;
  isLast: boolean;
  rawEdits: Record<string, string>;
  setRawEdit: (key: string, v: string) => void;
  clearRawEdit: (key: string) => void;
  patch: (id: string, over: Partial<ActionTemplate>) => void;
  isPlaced: boolean;
  onToggleDeck: (id: string, placed: boolean) => void;
  onRemove: (id: string) => void;
  onMove: (id: string, dir: -1 | 1) => void;
}

// Module-scope (not nested in OrderSettingsSection) so its identity is stable
// across renders — a component defined inside another component's body gets a
// fresh type every render, forcing React to unmount+remount every card (and
// drop input focus) on each keystroke.
function TemplateCard({ t, palette, dup, isFirst, isLast, rawEdits, setRawEdit, clearRawEdit, patch, isPlaced, onToggleDeck, onRemove, onMove }: TemplateCardProps): JSX.Element {
  const card: CSSProperties = { border: `1px solid ${palette.border}`, borderRadius: 6, background: palette.surface, padding: "8px 10px 10px", marginBottom: 8 };
  const eyebrow: CSSProperties = { fontSize: 9.5, letterSpacing: "0.08em", textTransform: "uppercase", color: palette.textMuted, marginBottom: 4 };
  const headerRow: CSSProperties = { display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8 };
  const labelInput: CSSProperties = { fontFamily: FONTS.serif, fontSize: 13, fontWeight: 600, minWidth: 140, background: "transparent" };
  const fieldRow: CSSProperties = { display: "flex", gap: 10, marginTop: 8, flexWrap: "wrap" };
  const fieldGroup: CSSProperties = { display: "flex", flexDirection: "column", gap: 2 };
  const fieldLabel: CSSProperties = { fontSize: 9.5, letterSpacing: "0.06em", textTransform: "uppercase", color: palette.textMuted };
  const hotkeyBox: CSSProperties = {
    fontFamily: FONTS.mono, fontSize: 12, width: 108, textAlign: "center", padding: "3px 6px",
    borderRadius: 4, border: `1px solid ${dup ? palette.danger : palette.border}`, background: palette.bg, color: palette.text,
  };

  return (
    <div style={card} data-testid={`tmpl-card-${t.id}`}>
      <div style={eyebrow}>{t.kind === "place" ? "Place order" : "Management"}</div>
      <div style={headerRow}>
        <input
          className="field" data-testid={`tmpl-label-${t.id}`} value={t.label}
          onChange={(e) => patch(t.id, { label: e.target.value })} style={labelInput}
        />
        <div style={{ display: "flex", gap: 5, alignItems: "center" }}>
          <input
            data-testid={`tmpl-hotkey-${t.id}`} readOnly value={t.hotkey ?? ""} placeholder="press keys"
            onKeyDown={(e) => {
              // Must stop propagation, not just preventDefault: the real hotkey
              // engine (useHotkeys, mounted globally in AppShell) listens on
              // `window` in the bubble phase. Without this, a candidate combo
              // typed here while capturing a binding can also be a *live* combo
              // (e.g. default Ctrl+Shift+K = KillSwitch, Ctrl+1..4 = place
              // templates) and fire the real action — this settings screen must
              // stay inert with zero order-safety authority.
              e.preventDefault();
              e.stopPropagation();
              const c = normalizeCombo(e);
              if (c) patch(t.id, { hotkey: c });
            }}
            style={hotkeyBox}
          />
          {t.hotkey ? (
            <HoverButton data-testid={`tmpl-unbind-${t.id}`} title="unbind" aria-label={`Unbind hotkey for ${t.label}`}
              onClick={() => patch(t.id, { hotkey: "" })} style={iconBtn(palette, palette.textMuted)}>×</HoverButton>
          ) : null}
          {dup ? <span style={{ color: palette.danger, fontSize: 10 }}>dup</span> : null}
          <HoverButton
            data-testid={`tmpl-move-up-${t.id}`} title="move up" aria-label={`Move ${t.label} up`}
            disabled={isFirst} onClick={() => onMove(t.id, -1)}
            style={{ ...iconBtn(palette, palette.textMuted), opacity: isFirst ? 0.35 : 1, cursor: isFirst ? "not-allowed" : "pointer" }}
          >▲</HoverButton>
          <HoverButton
            data-testid={`tmpl-move-down-${t.id}`} title="move down" aria-label={`Move ${t.label} down`}
            disabled={isLast} onClick={() => onMove(t.id, 1)}
            style={{ ...iconBtn(palette, palette.textMuted), opacity: isLast ? 0.35 : 1, cursor: isLast ? "not-allowed" : "pointer" }}
          >▼</HoverButton>
          <HoverButton title="remove" aria-label={`Remove ${t.label}`} onClick={() => onRemove(t.id)} style={iconBtn(palette, palette.danger)}>×</HoverButton>
        </div>
      </div>

      {t.kind === "place" ? (
        <>
          <div style={fieldRow}>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Side</span>
              <select aria-label={`side-${t.id}`} className="field" value={t.side} onChange={(e) => patch(t.id, { side: e.target.value as Side })} style={{ width: 92 }}>
                {SIDES.map((s) => <option key={s}>{s}</option>)}
              </select>
            </div>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Type</span>
              <select aria-label={`type-${t.id}`} className="field" value={t.type} onChange={(e) => patch(t.id, { type: e.target.value as OrderType })} style={{ width: 108 }}>
                {TYPES.map((x) => <option key={x}>{x}</option>)}
              </select>
            </div>
            <div style={fieldGroup}>
              <span style={fieldLabel}>TIF</span>
              <select aria-label={`tif-${t.id}`} className="field" value={t.tif} onChange={(e) => patch(t.id, { tif: e.target.value as TIF })} style={{ width: 80 }}>
                {TIFS.map((x) => <option key={x}>{x}</option>)}
              </select>
            </div>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Session</span>
              <select aria-label={`session-${t.id}`} className="field" value={t.session ?? "AUTO"} onChange={(e) => patch(t.id, { session: e.target.value as OrderSession })} style={{ width: 92 }}>
                {SESSIONS.map((s) => <option key={s} value={s}>{SESSION_LABEL[s]}</option>)}
              </select>
            </div>
          </div>

          <div style={fieldRow}>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Price</span>
              <select aria-label={`price-source-${t.id}`} className="field" value={t.priceSource} onChange={(e) => patch(t.id, { priceSource: e.target.value as PriceSource })} style={{ width: 84 }}>
                {SOURCES.map((x) => <option key={x}>{x}</option>)}
              </select>
            </div>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Offset</span>
              <div style={{ display: "flex", gap: 4, alignItems: "center" }}>
                <StepField
                  ariaLabel={`offset-${t.id}`}
                  testid={`offset-${t.id}`}
                  value={rawEdits[`${t.id}:offset`] ?? fmtOffset(t.priceOffset)}
                  onType={(v) => {
                    setRawEdit(`${t.id}:offset`, v);
                    const n = Number(v);
                    if (!Number.isNaN(n)) patch(t.id, { priceOffset: n });
                  }}
                  onStep={(dir) => {
                    patch(t.id, { priceOffset: round2(t.priceOffset + dir * OFFSET_STEP) });
                    clearRawEdit(`${t.id}:offset`);
                  }}
                  onBlur={() => clearRawEdit(`${t.id}:offset`)}
                  style={{ width: 92 }}
                />
                <select aria-label={`offset-unit-${t.id}`} className="field" value={t.priceOffsetUnit ?? "$"} onChange={(e) => patch(t.id, { priceOffsetUnit: e.target.value as PriceOffsetUnit })} style={{ width: 44 }}>
                  <option value="$">$</option><option value="%">%</option>
                </select>
              </div>
            </div>
          </div>

          <div style={fieldRow}>
            <div style={fieldGroup}>
              <span style={fieldLabel}>Size</span>
              <div style={{ display: "flex", gap: 4, alignItems: "center" }}>
                <select aria-label={`size-mode-${t.id}`} className="field" value={t.sizing.mode} onChange={(e) => patch(t.id, { sizing: modeToSpec(e.target.value as SizingMode) })} style={{ width: 96 }}>
                  {MODES.map((m) => <option key={m} value={m}>{MODE_LABEL[m]}</option>)}
                </select>
                <StepField
                  ariaLabel={`size-value-${t.id}`}
                  testid={`size-value-${t.id}`}
                  value={rawEdits[`${t.id}:size`] ?? sizingValue(t.sizing)}
                  onType={(v) => {
                    setRawEdit(`${t.id}:size`, v);
                    const n = Number(v);
                    if (!Number.isNaN(n)) patch(t.id, { sizing: setSizingValue(t.sizing, n) });
                  }}
                  onStep={(dir) => {
                    patch(t.id, { sizing: nudgeSizing(t.sizing, dir) });
                    clearRawEdit(`${t.id}:size`);
                  }}
                  onBlur={() => clearRawEdit(`${t.id}:size`)}
                  style={{ width: 84 }}
                />
                {isPercentMode(t.sizing.mode) ? <span style={{ fontSize: 10, color: palette.textMuted }}>max 100</span> : null}
              </div>
            </div>
          </div>
        </>
      ) : (
        <div style={fieldRow}>
          <div style={fieldGroup}>
            <span style={fieldLabel}>Action</span>
            <select aria-label={`action-${t.id}`} className="field" value={t.action} onChange={(e) => patch(t.id, { action: e.target.value as ManagementAction })} style={{ width: 200 }}>
              {MANAGE_ACTIONS.map((a) => <option key={a}>{a}</option>)}
            </select>
          </div>
        </div>
      )}

      <div style={fieldRow}>
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: palette.text }}>
          <input
            type="checkbox" data-testid={`tmpl-deck-toggle-${t.id}`} aria-label={`deck-${t.id}`}
            checked={isPlaced} onChange={(e) => onToggleDeck(t.id, e.target.checked)}
          />
          Show as button
        </label>
        {isPlaced ? (
          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            {DECK_COLORS.map((c) => (
              <button
                key={c} type="button" data-testid={`tmpl-deck-color-${t.id}-${c}`}
                title={c} aria-label={`deck-color-${t.id}-${c}`}
                onClick={() => patch(t.id, { deckColor: c })}
                style={swatchStyle(palette, c, (t.deckColor ?? "auto") === c)}
              />
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function OrderSettingsSection({ config, onSave, toast, onClose }: {
  config: OrderConfig; onSave: (next: OrderConfig) => void; toast?: ToastApi; onClose?: () => void;
}): JSX.Element {
  const { palette } = useTheme();
  const [templates, setTemplates] = useState<ActionTemplate[]>(() => normalizeOrderConfig(config).templates.map((t) => ({ ...t })));
  const [deck, setDeck] = useState<HotkeyDeckConfig>(() => cloneHotkeyDeck(normalizeOrderConfig(config).hotkeyDeck));
  const [addOpen, setAddOpen] = useState(false);
  // Offset and size-value are fully-controlled numeric fields whose display
  // is re-derived from the numeric model every render. Without this, typing
  // "0" then "." commits Number("0.") -> 0 back into the model and the next
  // render clobbers the trailing "." before the next digit can be typed, so
  // fractional values (e.g. 0.05) can never be entered keystroke-by-keystroke.
  // Track the in-progress raw text per cell (keyed by `${templateId}:field`)
  // and only fall back to the derived numeric string once editing ends.
  const [rawEdits, setRawEdits] = useState<Record<string, string>>({});
  // Task 6 co-mounted this section with the hotkeys BackupPanel in the same
  // "orders" pane (both share the OrderConfig context), removing the nav-
  // switch unmount/remount that used to re-run the `templates` useState
  // initializer above whenever `config` changed underneath this component.
  // Without this effect, importing hotkeys updates the shared config but this
  // component's local `templates` (what the cheat sheet and cards render
  // from) silently keeps showing the pre-import list.
  //
  // The BackupPanel import is not the only thing that can change `config`
  // while this component is mounted, though. `OrderConfigProvider`
  // (useOrderConfig.tsx) is a single app-wide context, and its
  // `setActiveVenue` is reachable from venue-selection UI in the
  // AccountPanel/OrderTicketPanel dockview panels, which sit underneath the
  // Settings modal and are never unmounted by it — the modal is an overlay,
  // not a remount boundary for them. So `config` genuinely can change here
  // for reasons that have nothing to do with templates.
  //
  // That is exactly why this effect is keyed on `[config.templates,
  // config.hotkeyDeck]` and not `[config]`: `setActiveVenue` builds its next config as
  // `{ ...c, activeVenue: v }` — a shallow spread that reuses the exact same
  // `templates` array reference — so a venue-only change does not fire this
  // effect and does not clobber an in-progress local edit (e.g. an
  // added-but-unsaved template). This only fires on a genuinely new
  // `config.templates`/`config.hotkeyDeck` references: a hotkey import, or this
  // component's own Save round-trip (re-`.map()`ing content that already
  // matches what's displayed there, a harmless no-op render).
  //
  // This safety is an invariant of `setActiveVenue`'s implementation, not of
  // the effect itself: if `setActiveVenue` (or any future `OrderConfig`
  // writer) ever starts minting a new `templates` array for a change
  // unrelated to templates, this effect will wrongly fire and silently
  // clobber in-progress local edits — the exact bug it exists to prevent.
  // The regression test "keeps an unsaved added template across a
  // venue-only config change" below pins this invariant.
  useEffect(() => {
    const normalized = normalizeOrderConfig(config);
    setTemplates(normalized.templates.map((t) => ({ ...t })));
    setDeck(cloneHotkeyDeck(normalized.hotkeyDeck));
    setRawEdits({});
  }, [config.templates, config.hotkeyDeck]);
  const setRawEdit = (key: string, v: string) => setRawEdits((r) => ({ ...r, [key]: v }));
  const clearRawEdit = (key: string) => setRawEdits((r) => {
    if (!(key in r)) return r;
    const next = { ...r };
    delete next[key];
    return next;
  });

  const patch = (id: string, over: Partial<ActionTemplate>) =>
    setTemplates((ts) => ts.map((t) => (t.id === id ? ({ ...t, ...over } as ActionTemplate) : t)));
  // Removing a row must also drop its rawEdits entries. uid() below is
  // deterministic in templates.length alone, so an add-then-remove that
  // returns the array to a prior length reuses the exact same id on the
  // next add. Without this cleanup, a still-in-progress (unblurred) edit on
  // the removed row would leak onto whichever new row is later assigned the
  // reused id — the input would show stale typed text while the saved model
  // holds the real, correct value.
  const removeTemplate = (id: string) => {
    setTemplates((ts) => ts.filter((t) => t.id !== id));
    setDeck((d) => ({ ...d, rows: removeDeckPlacement(d.rows, id) }));
    clearRawEdit(`${id}:offset`);
    clearRawEdit(`${id}:size`);
  };
  // Reorder (4a): swap two adjacent templates by id. Bounds-checked so the
  // move buttons are simple no-ops (never throw) if somehow clicked past the
  // array edge; the header row disables them there anyway.
  const moveTemplate = (id: string, dir: -1 | 1) =>
    setTemplates((ts) => {
      const i = ts.findIndex((t) => t.id === id);
      const j = i + dir;
      if (i < 0 || j < 0 || j >= ts.length) return ts;
      const next = ts.slice();
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  const uid = (p: string) => `${p}-${templates.length + 1}-${Math.max(0, ...templates.map((_, i) => i)) + 1}`;
  const addPlace = () => setTemplates((ts) => [...ts, { kind: "place", id: uid("tmpl"), label: "New", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", priceSource: "Ask", priceOffset: 0, priceOffsetUnit: "$", sizing: { mode: "Shares", shares: 100 } } as PlaceOrderTemplate]);
  const addManage = () => setTemplates((ts) => [...ts, { kind: "manage", id: uid("mng"), label: "New action", action: "CancelLast" }]);
  // Reset replaces every template wholesale, so any live rawEdits entry —
  // even for an id that still exists after reset (default ids are fixed
  // strings, not uid()-generated) — must not survive it; otherwise the
  // display would keep showing pre-reset in-progress typed text instead of
  // snapping to the restored default value.
  const doReset = () => {
    const reset = normalizeOrderConfig({
      ...config,
      templates: DEFAULT_TEMPLATES.map((t) => ({ ...t })),
      hotkeyDeck: { rows: [], showHotkeyLabels: false },
    });
    setTemplates(reset.templates);
    setDeck(cloneHotkeyDeck(reset.hotkeyDeck));
    setRawEdits({});
  };
  const toggleDeck = (id: string, placed: boolean) => setDeck((d) => {
    if (!placed) return { ...d, rows: removeDeckPlacement(d.rows, id) };
    if (d.rows.some((row) => row.includes(id))) return d;
    const rows = d.rows.map((row) => [...row]);
    if (rows.length === 0) rows.push([]);
    rows[rows.length - 1].push(id);
    return { ...d, rows };
  });
  const addDeckRow = () => setDeck((d) => ({ ...d, rows: [...d.rows.map((row) => [...row]), []] }));
  const moveDeck = (id: string, targetRow: number, targetIndex: number) => setDeck((d) => ({ ...d, rows: moveDeckPlacement(d.rows, id, targetRow, targetIndex) }));
  const removeDeck = (id: string) => setDeck((d) => ({ ...d, rows: removeDeckPlacement(d.rows, id) }));
  const isPlaced = (id: string) => deck.rows.some((row) => row.includes(id));
  const places = templates.filter((t): t is PlaceOrderTemplate => t.kind === "place");

  const combos = templates.map((t) => t.hotkey ?? "").filter((c) => c !== "");
  const dupes = new Set(combos.filter((c, i) => combos.indexOf(c) !== i));
  const isDup = (t: ActionTemplate) => !!t.hotkey && dupes.has(t.hotkey);
  const hasConflict = dupes.size > 0;
  const manages = templates.filter((t) => t.kind === "manage");

  const sectionLabel: CSSProperties = { color: palette.textMuted, fontSize: 10, letterSpacing: 0.4, margin: "2px 0 6px" };
  const actionBtn: CSSProperties = { fontFamily: FONTS.sans };

  return (
    <div style={{ color: palette.text }}>
      <div data-testid="cheat-sheet" style={{ border: `1px solid ${palette.border}`, borderRadius: 4, padding: "6px 8px", marginBottom: 10 }}>
        <div style={{ color: palette.textMuted, fontSize: 10, letterSpacing: 0.4, marginBottom: 4 }}>CHEAT SHEET</div>
        {[{ label: "Place", rows: places }, { label: "Manage", rows: manages }].map((grp) => (
          <div key={grp.label} style={{ display: "flex", flexWrap: "wrap", gap: 12, alignItems: "center", marginBottom: 2 }}>
            <span style={{ width: 52, color: palette.textMuted }}>{grp.label}</span>
            {grp.rows.filter((t) => t.hotkey).map((t) => (
              <span key={t.id} style={{ display: "inline-flex", gap: 5, alignItems: "center" }}>
                <Keycap combo={t.hotkey as string} danger={isDup(t) || (t.kind === "manage" && t.action === "KillSwitch")} />
                <span style={{ color: isDup(t) ? palette.danger : palette.text }}>{t.label}</span>
              </span>
            ))}
          </div>
        ))}
      </div>

      <DeckLayoutEditor
        palette={palette} rows={deck.rows} templates={templates} showHotkeyLabels={deck.showHotkeyLabels}
        onShowHotkeyLabels={(show) => setDeck((d) => ({ ...d, showHotkeyLabels: show }))}
        onAddRow={addDeckRow} onMove={moveDeck} onRemove={removeDeck}
      />

      <div style={sectionLabel}>TEMPLATES</div>
      {templates.map((t, i) => (
        <TemplateCard
          key={t.id} t={t} palette={palette} dup={isDup(t)}
          isFirst={i === 0} isLast={i === templates.length - 1}
          rawEdits={rawEdits} setRawEdit={setRawEdit} clearRawEdit={clearRawEdit}
          patch={patch} isPlaced={isPlaced(t.id)} onToggleDeck={toggleDeck}
          onRemove={removeTemplate} onMove={moveTemplate}
        />
      ))}

      <div style={{ display: "flex", gap: 6, marginTop: 10, alignItems: "center", position: "relative" }}>
        <Button data-testid="add-template" onClick={() => setAddOpen((v) => !v)} style={actionBtn}>+ Add ▾</Button>
        {addOpen && (
          <>
            <Button data-testid="add-place" onClick={() => { addPlace(); setAddOpen(false); }} style={actionBtn}>Order template</Button>
            <Button data-testid="add-manage" onClick={() => { addManage(); setAddOpen(false); }} style={actionBtn}>Management action</Button>
          </>
        )}
        <Button variant="danger" confirm confirmLabel="Confirm reset" data-testid="reset-defaults" onClick={doReset} style={actionBtn}>Reset to defaults</Button>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
        <Button
          variant="primary" size="md" data-testid="save" disabled={hasConflict}
          onClick={() => {
            onSave(normalizeOrderConfig({ ...config, templates, hotkeyDeck: deck }));
            toast?.push({ level: "success", text: "Order templates & hotkeys saved." });
            onClose?.();
          }}
          style={actionBtn}
        >
          Save
        </Button>
      </div>
    </div>
  );
}
