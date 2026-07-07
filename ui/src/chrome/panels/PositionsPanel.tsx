import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { PositionRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { formatPrice, formatSize } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";

export function PositionsPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const rows = stores.exec.positions();
  const status = stores.exec.status();
  const armedFor = (v: string | null) => !!status?.masterArmed && !!status?.venues.find((x) => x.venue === v)?.venueArmed;

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

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead><tr style={{ color: palette.textMuted, textAlign: "right" }}>
          <th style={{ textAlign: "left", padding: "2px 8px" }}>Symbol</th><th>Venue</th><th>Qty</th><th>Avg</th><th>Unreal</th><th></th>
        </tr></thead>
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
  );
}
