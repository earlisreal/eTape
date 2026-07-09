// Task 11 history: this was OrderSettingsModal (a standalone overlay). Since the
// settings unification it is a plain section body embedded in SettingsModal's
// "Orders & hotkeys" tab. This revision makes every template parameter editable
// — including price offset (value + $/%) and sizing amount, which had no input
// before — and lets management templates be created.
import { useState } from "react";
import { useTheme } from "../ThemeProvider";
import { HoverButton } from "../controls/HoverButton";
import type { Side, OrderType, TIF } from "../../wire/contract";
import type { PriceSource, PriceOffsetUnit } from "./priceSource";
import type { SizingSpec, SizingMode } from "./sizing";
import {
  DEFAULT_TEMPLATES, normalizeOrderConfig, type ActionTemplate, type ManagementAction,
  type OrderConfig, type PlaceOrderTemplate,
} from "./actionTemplate";
import { normalizeCombo } from "./hotkeys";
import { Keycap } from "./Keycap";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SOURCES: PriceSource[] = ["Bid", "Ask", "Last", "Mid"];
const MODES: SizingMode[] = ["Dollar", "BuyingPowerPct", "Shares", "PositionFraction"];
const MODE_LABEL: Record<SizingMode, string> = { Dollar: "Dollar", BuyingPowerPct: "BP %", Shares: "Shares", PositionFraction: "Pos %" };
const MANAGE_ACTIONS: ManagementAction[] = ["CancelLast", "CancelAllFocused", "CancelAllEverything", "KillSwitch"];
const COLS = "110px 68px 78px 58px 62px 118px 150px 130px 26px";

function sizingValue(s: SizingSpec): string {
  switch (s.mode) {
    case "Dollar": return String(s.dollar ?? 0);
    case "Shares": return String(s.shares ?? 0);
    case "BuyingPowerPct":
    case "PositionFraction": return String(s.pct ?? 0);
  }
}
function setSizingValue(s: SizingSpec, n: number): SizingSpec {
  switch (s.mode) {
    case "Dollar": return { mode: "Dollar", dollar: n };
    case "Shares": return { mode: "Shares", shares: n };
    case "BuyingPowerPct": return { mode: "BuyingPowerPct", pct: n };
    case "PositionFraction": return { mode: "PositionFraction", pct: n };
  }
}
function modeToSpec(mode: SizingMode): SizingSpec {
  switch (mode) {
    case "Dollar": return { mode, dollar: 0 };
    case "Shares": return { mode, shares: 100 };
    case "BuyingPowerPct": return { mode, pct: 25 };
    case "PositionFraction": return { mode, pct: 100 };
  }
}

export function OrderSettingsSection({ config, onSave }: { config: OrderConfig; onSave: (next: OrderConfig) => void }): JSX.Element {
  const { palette } = useTheme();
  const [templates, setTemplates] = useState<ActionTemplate[]>(() => config.templates.map((t) => ({ ...t })));
  const [addOpen, setAddOpen] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);
  // Offset and size-value are fully-controlled numeric fields whose display
  // is re-derived from the numeric model every render. Without this, typing
  // "0" then "." commits Number("0.") -> 0 back into the model and the next
  // render clobbers the trailing "." before the next digit can be typed, so
  // fractional values (e.g. 0.05) can never be entered keystroke-by-keystroke.
  // Track the in-progress raw text per cell (keyed by `${templateId}:field`)
  // and only fall back to the derived numeric string once editing ends.
  const [rawEdits, setRawEdits] = useState<Record<string, string>>({});
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
    clearRawEdit(`${id}:offset`);
    clearRawEdit(`${id}:size`);
  };
  const uid = (p: string) => `${p}-${templates.length + 1}-${Math.max(0, ...templates.map((_, i) => i)) + 1}`;
  const addPlace = () => setTemplates((ts) => [...ts, { kind: "place", id: uid("tmpl"), label: "New", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, priceOffsetUnit: "$", sizing: { mode: "Shares", shares: 100 } } as PlaceOrderTemplate]);
  const addManage = () => setTemplates((ts) => [...ts, { kind: "manage", id: uid("mng"), label: "New action", action: "CancelLast" }]);
  // Reset replaces every template wholesale, so any live rawEdits entry —
  // even for an id that still exists after reset (default ids are fixed
  // strings, not uid()-generated) — must not survive it; otherwise the
  // display would keep showing pre-reset in-progress typed text instead of
  // snapping to the restored default value.
  const doReset = () => { setTemplates(normalizeOrderConfig({ ...config, templates: DEFAULT_TEMPLATES.map((t) => ({ ...t })) }).templates); setRawEdits({}); setConfirmReset(false); };
  const places = templates.filter((t): t is PlaceOrderTemplate => t.kind === "place");

  const combos = templates.map((t) => t.hotkey ?? "").filter((c) => c !== "");
  const dupes = new Set(combos.filter((c, i) => combos.indexOf(c) !== i));
  const isDup = (t: ActionTemplate) => !!t.hotkey && dupes.has(t.hotkey);
  const hasConflict = dupes.size > 0;
  const manages = templates.filter((t) => t.kind === "manage");

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px", width: "100%", boxSizing: "border-box" } as const;
  const cell = { display: "grid", gridTemplateColumns: COLS, gap: 4, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}` } as const;
  const head = { ...cell, color: palette.textMuted, fontSize: 10, letterSpacing: 0.4 };

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

      <div style={head}>
        <span>LABEL</span><span>SIDE</span><span>TYPE</span><span>TIF</span><span>PRICE</span><span>OFFSET</span><span>SIZE</span><span>KEY</span><span />
      </div>

      {templates.map((t) => (
        <div key={t.id} style={cell}>
          <input data-testid={`tmpl-label-${t.id}`} value={t.label} onChange={(e) => patch(t.id, { label: e.target.value })} style={inp} />
          {t.kind === "place" ? (
            <>
              <select value={t.side} onChange={(e) => patch(t.id, { side: e.target.value as Side })} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
              <select value={t.type} onChange={(e) => patch(t.id, { type: e.target.value as OrderType })} style={inp}>{TYPES.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.tif} onChange={(e) => patch(t.id, { tif: e.target.value as TIF })} style={inp}>{TIFS.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.priceSource} onChange={(e) => patch(t.id, { priceSource: e.target.value as PriceSource })} style={inp}>{SOURCES.map((x) => <option key={x}>{x}</option>)}</select>
              <span style={{ display: "flex", gap: 3 }}>
                <input
                  aria-label={`offset-${t.id}`}
                  value={rawEdits[`${t.id}:offset`] ?? String(t.priceOffset)}
                  onChange={(e) => {
                    const v = e.target.value;
                    setRawEdit(`${t.id}:offset`, v);
                    const n = Number(v);
                    if (!Number.isNaN(n)) patch(t.id, { priceOffset: n });
                  }}
                  onBlur={() => clearRawEdit(`${t.id}:offset`)}
                  style={{ ...inp, width: 62 }}
                />
                <select aria-label={`offset-unit-${t.id}`} value={t.priceOffsetUnit ?? "$"} onChange={(e) => patch(t.id, { priceOffsetUnit: e.target.value as PriceOffsetUnit })} style={{ ...inp, width: 46 }}>
                  <option value="$">$</option><option value="%">%</option>
                </select>
              </span>
              <span style={{ display: "flex", gap: 3 }}>
                <select aria-label={`size-mode-${t.id}`} value={t.sizing.mode} onChange={(e) => patch(t.id, { sizing: modeToSpec(e.target.value as SizingMode) })} style={{ ...inp, width: 84 }}>
                  {MODES.map((m) => <option key={m} value={m}>{MODE_LABEL[m]}</option>)}
                </select>
                <input
                  aria-label={`size-value-${t.id}`}
                  value={rawEdits[`${t.id}:size`] ?? sizingValue(t.sizing)}
                  onChange={(e) => {
                    const v = e.target.value;
                    setRawEdit(`${t.id}:size`, v);
                    const n = Number(v);
                    if (!Number.isNaN(n)) patch(t.id, { sizing: setSizingValue(t.sizing, n) });
                  }}
                  onBlur={() => clearRawEdit(`${t.id}:size`)}
                  style={{ ...inp, width: 60 }}
                />
              </span>
            </>
          ) : (
            <select aria-label={`action-${t.id}`} value={t.action} onChange={(e) => patch(t.id, { action: e.target.value as ManagementAction })} style={{ ...inp, gridColumn: "2 / 8" }}>
              {MANAGE_ACTIONS.map((a) => <option key={a}>{a}</option>)}
            </select>
          )}
          <span style={{ display: "flex", gap: 3, alignItems: "center" }}>
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
              style={{ ...inp, width: 96, borderColor: isDup(t) ? palette.danger : palette.border }}
            />
            {t.hotkey ? <HoverButton data-testid={`tmpl-unbind-${t.id}`} title="unbind" onClick={() => patch(t.id, { hotkey: "" })} style={{ ...inp, width: 22, cursor: "pointer", color: palette.textMuted }}>×</HoverButton> : null}
            {isDup(t) ? <span style={{ color: palette.danger, fontSize: 10 }}>dup</span> : null}
          </span>
          <HoverButton title="remove" onClick={() => removeTemplate(t.id)} style={{ ...inp, width: 22, color: palette.danger, cursor: "pointer" }}>×</HoverButton>
        </div>
      ))}

      <div style={{ display: "flex", gap: 6, marginTop: 10, alignItems: "center", position: "relative" }}>
        <HoverButton data-testid="add-template" onClick={() => setAddOpen((v) => !v)} style={{ ...inp, width: "auto", cursor: "pointer" }}>+ Add ▾</HoverButton>
        {addOpen && (
          <>
            <HoverButton data-testid="add-place" onClick={() => { addPlace(); setAddOpen(false); }} style={{ ...inp, width: "auto", cursor: "pointer" }}>Order template</HoverButton>
            <HoverButton data-testid="add-manage" onClick={() => { addManage(); setAddOpen(false); }} style={{ ...inp, width: "auto", cursor: "pointer" }}>Management action</HoverButton>
          </>
        )}
        {confirmReset
          ? <HoverButton data-testid="reset-confirm" onClick={doReset} style={{ ...inp, width: "auto", color: palette.danger, cursor: "pointer" }}>Confirm reset</HoverButton>
          : <HoverButton data-testid="reset-defaults" onClick={() => setConfirmReset(true)} style={{ ...inp, width: "auto", cursor: "pointer" }}>Reset to defaults</HoverButton>}
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
        <HoverButton data-testid="save" disabled={hasConflict} onClick={() => onSave({ ...config, templates })} style={{ ...inp, width: "auto", background: hasConflict ? palette.border : palette.accent, color: palette.bg, fontWeight: 700, cursor: hasConflict ? "not-allowed" : "pointer" }}>Save</HoverButton>
      </div>
    </div>
  );
}
