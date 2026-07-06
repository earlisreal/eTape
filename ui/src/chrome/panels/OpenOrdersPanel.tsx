import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { displayStatus, STATUS_LABEL, sideLabel, bareSymbol, abbrevType, isWorking } from "../exec/orderStatus";
import { formatPrice, formatSize } from "../../render/format";

export function OpenOrdersPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const views = stores.exec.orders();
  const reconciling = (stores.exec.status()?.venues ?? []).some((v) => v.reconcilePending);

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>Open Orders</span>
        <button data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")}
          style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.warn}`, background: "transparent", color: palette.warn, cursor: "pointer" }}>Cancel All</button>
        {reconciling && (
          <span data-testid="reconcile-badge" style={{ marginLeft: "auto", fontSize: 10, color: palette.bg, background: palette.warn, padding: "1px 6px", borderRadius: 3 }}>
            state reconciled — verify before acting
          </span>
        )}
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <tbody>
          {views.map(({ order, optimistic }) => {
            const ds = displayStatus(order, optimistic);
            const working = !optimistic && isWorking(order.status);
            const priceStr = order.type === "MARKET" ? "MKT" : formatPrice(order.type === "STOP" ? order.stopPrice : order.limitPrice, 2);
            return (
              <tr key={order.id} style={{ borderTop: `1px solid ${palette.border}` }}>
                <td style={{ padding: "2px 8px", color: order.side === "BUY" || order.side === "COVER" ? palette.up : palette.down }}>{sideLabel(order.side)}</td>
                <td>{formatSize(order.leavesQty > 0 ? order.leavesQty : order.qty)}</td>
                <td>{bareSymbol(order.symbol)}</td>
                <td style={{ textAlign: "right" }}>{priceStr} {abbrevType(order.type)}</td>
                <td style={{ color: palette.textMuted }}>{STATUS_LABEL[ds]}</td>
                <td style={{ color: palette.danger, fontSize: 10 }}>{order.rejectReason}</td>
                <td style={{ color: palette.textMuted, fontSize: 10 }}>{order.venue}</td>
                <td>{(working || optimistic) ? (
                  <button data-testid={`cancel-${order.id}`} onClick={() => void oc.cancel(order.venue, order.id)}
                    style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Cancel</button>
                ) : null}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
