import { useState } from "react";
import type { ExecStatus, Side, OrderType, TIF } from "../../wire/contract";
import type { ActionTemplate, OrderConfig, PlaceOrderTemplate } from "./actionTemplate";
import type { PriceSource } from "./priceSource";
import type { SizingMode } from "./sizing";
import { normalizeCombo } from "./hotkeys";
import { useTheme } from "../ThemeProvider";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SOURCES: PriceSource[] = ["Bid", "Ask", "Last", "Mid"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];
const cap = (n: number) => (n === 0 ? "off" : String(n));

// Task 11: this used to be OrderSettingsModal — a standalone overlay with its own
// title bar and Cancel/Save footer, opened from the order ticket's gear icon. It's
// now a plain section body (no overlay/title/close) embedded in the unified
// SettingsModal's "Orders & hotkeys" tab; the Sounds sub-section moved out to its
// own tab (SettingsModal renders <SoundsSection/> directly) rather than being
// nested here. The Save button and every data-testid are unchanged so
// OrderSettingsModal.test.tsx (repointed to render this section directly) keeps
// passing without modification to its assertions.
export function OrderSettingsSection(
  { config, status, onSave }: { config: OrderConfig; status: ExecStatus | null; onSave: (next: OrderConfig) => void },
): JSX.Element {
  const { palette } = useTheme();
  const [templates, setTemplates] = useState<ActionTemplate[]>(() => config.templates.map((t) => ({ ...t })));

  const patch = (id: string, over: Partial<ActionTemplate>) =>
    setTemplates((ts) => ts.map((t) => (t.id === id ? ({ ...t, ...over } as ActionTemplate) : t)));
  const addTemplate = () =>
    setTemplates((ts) => [...ts, { kind: "place", id: `tmpl-${ts.length + 1}-${ts.reduce((s) => s + 1, 0)}`, label: "New", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 100 } } as PlaceOrderTemplate]);
  const removeTemplate = (id: string) => setTemplates((ts) => ts.filter((t) => t.id !== id));

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;

  return (
    <div style={{ color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 8 }}>
        <strong>Order Settings — Action Templates & Hotkeys</strong>
      </div>

      {templates.map((t) => (
        <div key={t.id} style={{ display: "flex", gap: 4, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}`, flexWrap: "wrap" }}>
          <input data-testid={`tmpl-label-${t.id}`} value={t.label} onChange={(e) => patch(t.id, { label: e.target.value })} style={{ ...inp, width: 110 }} />
          <span style={{ color: palette.textMuted }}>{t.kind}</span>
          {t.kind === "place" && (
            <>
              <select value={t.side} onChange={(e) => patch(t.id, { side: e.target.value as Side })} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
              <select value={t.type} onChange={(e) => patch(t.id, { type: e.target.value as OrderType })} style={inp}>{TYPES.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.tif} onChange={(e) => patch(t.id, { tif: e.target.value as TIF })} style={inp}>{TIFS.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.priceSource} onChange={(e) => patch(t.id, { priceSource: e.target.value as PriceSource })} style={inp}>{SOURCES.map((x) => <option key={x}>{x}</option>)}</select>
              <select value={t.sizing.mode} onChange={(e) => patch(t.id, { sizing: { ...t.sizing, mode: e.target.value as SizingMode } })} style={inp}>{MODES.map((x) => <option key={x}>{x}</option>)}</select>
            </>
          )}
          <input data-testid={`tmpl-hotkey-${t.id}`} readOnly value={t.hotkey ?? ""} placeholder="press keys"
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
            }} style={{ ...inp, width: 110 }} />
          <button onClick={() => removeTemplate(t.id)} style={{ ...inp, color: palette.danger, cursor: "pointer" }}>remove</button>
        </div>
      ))}

      <button data-testid="add-template" onClick={addTemplate} style={{ ...inp, marginTop: 8, cursor: "pointer" }}>+ Add template</button>

      <div style={{ marginTop: 12, borderTop: `1px solid ${palette.border}`, paddingTop: 8 }}>
        <div style={{ color: palette.textMuted, marginBottom: 4 }}>Gate limits in effect (read-only; edited engine-side)</div>
        <div>Global: max day loss <b>{cap(status?.global.maxDayLoss ?? 0)}</b> · symbol value <b>{cap(status?.global.maxSymbolPositionValue ?? 0)}</b> · symbol shares <b>{cap(status?.global.maxSymbolPositionShares ?? 0)}</b></div>
        {(status?.venues ?? []).map((v) => (
          <div key={v.venue}>{v.venue}: max order value <b>{cap(v.gate.maxOrderValue)}</b> · max position value <b>{cap(v.gate.maxPositionValue)}</b> · max position shares <b>{cap(v.gate.maxPositionShares)}</b> · max open orders <b>{cap(v.gate.maxOpenOrders)}</b></div>
        ))}
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
        <button data-testid="save" onClick={() => onSave({ ...config, templates })} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, cursor: "pointer" }}>Save</button>
      </div>
    </div>
  );
}
