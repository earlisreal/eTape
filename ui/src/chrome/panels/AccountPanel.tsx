import { useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { PositionRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useVenueSelection } from "../exec/venueSelection";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { formatPrice, formatSize } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";

// Task 19 merges the old AccountBarPanel (stats strip + master/per-venue arm
// chips) and PositionsPanel (sortable positions table, Flatten) into one
// Account panel. Connection-link status dots stay in the top bar (Task 9) —
// this panel keeps only the per-venue "connected" health dot inside its own
// arm chip (a legitimate ok/danger health signal, distinct from arm state).

const money = (n: number | null): string => (n === null ? "—" : (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2));

const DEFAULT_SORT: SortState = { col: "unrealizedPnl", dir: "desc" };

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.sort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

const COLUMNS: { col: string; label: string; align: "left" | "right"; sortable: boolean }[] = [
  { col: "symbol", label: "Symbol", align: "left", sortable: true },
  { col: "venue", label: "Venue", align: "right", sortable: true },
  { col: "qty", label: "Qty", align: "right", sortable: true },
  { col: "avgPrice", label: "Avg", align: "right", sortable: true },
  { col: "unrealizedPnl", label: "Unrl P&L", align: "right", sortable: true },
  { col: "flatten", label: "", align: "right", sortable: false },
];
const SORT_ACCESSORS: Record<string, (r: PositionRow) => number | string | null> = {
  symbol: (r) => bareSymbol(r.symbol),
  venue: (r) => r.venue ?? "NET",
  qty: (r) => r.qty,
  avgPrice: (r) => r.avgPrice,
  unrealizedPnl: (r) => r.unrealizedPnl,
};

// ---- Stats strip + arm chips (folded from AccountBarPanel) ----

function StatsStrip({
  stores, oc, palette, venue, venues, selectVenue,
}: {
  stores: PanelProps["stores"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  venue: string;
  venues: string[];
  selectVenue: (v: string) => void;
}): JSX.Element {
  const status = stores.exec.status();
  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const equity = account?.equity ?? null;
  const bp = account?.buyingPower ?? null;
  const dayPnl = account?.dayPnl ?? null;
  const realized = account?.realized ?? null;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} className="mono" style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );
  const dot = (ok: boolean, title: string) => (
    <span title={title} style={{ width: 8, height: 8, borderRadius: 8, background: ok ? palette.ok : palette.danger, display: "inline-block" }} />
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "4px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <select data-testid="acct-venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
        {venues.map((v) => <option key={v} value={v}>{v}</option>)}
      </select>
      {cell("Equity", "acct-equity", money(equity))}
      {cell("Buying Power", "acct-bp", money(bp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
      {/* Per-venue arm chips stay all-venue (TopBar's arm-chip owns master arm;
          the old duplicate master ARMED button is removed). */}
      <div style={{ display: "flex", gap: 6, alignItems: "center", padding: "0 8px" }}>
        {(status?.venues ?? []).map((v) => (
          <button key={v.venue} data-testid={`venue-arm-${v.venue}`} data-armed={v.venueArmed}
            title={`${v.venue}: ${v.connected ? "connected" : "disconnected"} — click to ${v.venueArmed ? "disarm" : "arm"}`}
            onClick={() => (v.venueArmed ? oc.disarm(v.venue) : oc.arm(v.venue))}
            style={{
              display: "flex", gap: 3, alignItems: "center", fontSize: 10, cursor: "pointer",
              background: "transparent", border: `1px solid ${v.venueArmed ? palette.accent : palette.borderStrong}`,
              borderRadius: 4, padding: "2px 6px", color: v.venueArmed ? palette.accent : palette.textMuted,
            }}>
            {dot(v.connected, `${v.venue}: ${v.connected ? "connected" : "disconnected"}`)}{v.venue}{v.venueArmed ? " ●" : " ○"}
          </button>
        ))}
      </div>
    </div>
  );
}

// ---- Positions table (folded from PositionsPanel, now sortable via T16) ----

function PositionsTable({
  stores, commands, oc, palette, config, onConfigChange, venue,
}: {
  stores: PanelProps["stores"];
  commands: PanelProps["commands"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
}): JSX.Element {
  const toast = useToasts();
  const rows0 = stores.exec.positions().filter((p) => p.venue === venue); // venue-scoped; NET (venue===null) rows drop out
  const status = stores.exec.status();
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const armedFor = (v: string | null) => !!status?.masterArmed && !!status?.venues.find((x) => x.venue === v)?.venueArmed;

  const rows = useMemo(() => sortRows(rows0, sort, SORT_ACCESSORS), [rows0, sort]);
  const openCount = rows0.length;

  const clickSort = (col: string, sortable: boolean) => {
    if (!sortable) return;
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ sort: next });
  };

  const flatten = (row: PositionRow) => {
    if (row.venue === null) return; // net rows have no single venue to route to (button is hidden anyway)
    const venue = row.venue;        // narrowed to VenueID
    const quote = stores.quote.get(row.symbol);
    if (!quote) { toast.push({ level: "danger", text: `No quote to price the close for ${bareSymbol(row.symbol)}.` }); return; }
    const long = row.qty > 0;
    const t: PlaceOrderTemplate = {
      kind: "place", id: "flatten", label: "Flatten", side: long ? "SELL" : "COVER",
      type: "MARKET", tif: "DAY", priceSource: long ? "Bid" : "Ask", priceOffset: 0,
      sizing: { mode: "PositionFraction", fraction: "all" },
    };
    const r = resolvePlaceTemplate(t, { venue, symbol: row.symbol, quote, buyingPower: 0, positionQty: row.qty, nowMs: Date.now() });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  void commands; // oc already wraps commands; kept in signature for parity/legibility

  return (
    <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ padding: "4px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
        {openCount} open position{openCount === 1 ? "" : "s"}
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ color: palette.textMuted, textAlign: "right" }}>
              {COLUMNS.map((c) => (
                <th key={c.col} style={{ ...th, textAlign: c.align, cursor: c.sortable ? "pointer" : "default" }}
                  onClick={() => clickSort(c.col, c.sortable)}>
                  {c.label} {c.sortable ? sortIndicator(sort, c.col) : ""}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => {
              const net = r.venue === null;
              return (
                <tr key={`${r.venue ?? "NET"}-${r.symbol}-${i}`} data-testid={net ? "pos-net" : undefined}
                  style={{ textAlign: "right", borderTop: `1px solid ${palette.border}`, fontWeight: net ? 700 : 400 }}>
                  <td style={{ textAlign: "left", padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                  <td style={{ color: palette.textMuted }}>{net ? "NET" : r.venue}</td>
                  <td style={{ color: r.qty >= 0 ? palette.up : palette.down }}>{formatSize(r.qty)}</td>
                  <td>{formatPrice(r.avgPrice, 2)}</td>
                  <td style={{ color: r.unrealizedPnl >= 0 ? palette.up : palette.down }}>{formatPrice(r.unrealizedPnl, 2)}</td>
                  <td>{net ? null : (
                    <button data-testid={`flatten-${r.venue}-${r.symbol}`} data-armed={armedFor(r.venue)}
                      title={armedFor(r.venue) ? "Flatten position" : "Venue disarmed — flatten still allowed (exposure-reducing)"}
                      onClick={() => flatten(r)}
                      style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Flatten</button>
                  )}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function AccountPanel({ config, stores, commands, onConfigChange, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const group = groupProp ?? config.group;
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontFamily: "inherit" }}>
      <StatsStrip stores={stores} oc={oc} palette={palette} venue={venue} venues={venues} selectVenue={selectVenue} />
      <PositionsTable stores={stores} commands={commands} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} />
    </div>
  );
}
