import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { formatPrice } from "../../render/format";

const money = (n: number | null): string => (n === null ? "—" : (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2));

export function AccountBarPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const accounts = stores.exec.accounts();
  const status = stores.exec.status();
  const sum = (pick: (a: (typeof accounts)[number]) => number) => (accounts.length ? accounts.reduce((s, a) => s + pick(a), 0) : null);
  const equity = sum((a) => a.equity);
  const bp = sum((a) => a.buyingPower);
  const dayPnl = sum((a) => a.dayPnl);
  const realized = sum((a) => a.realized);
  const armed = status?.masterArmed ?? false;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );
  const dot = (ok: boolean, title: string) => (
    <span title={title} style={{ width: 8, height: 8, borderRadius: 8, background: ok ? palette.ok : palette.danger, display: "inline-block" }} />
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, height: "100%", padding: "0 8px", background: palette.surface, color: palette.text, fontFamily: "inherit" }}>
      {cell("Equity", "acct-equity", money(equity))}
      {cell("Buying Power", "acct-bp", money(bp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
      <div style={{ display: "flex", gap: 6, alignItems: "center", padding: "0 8px" }}>
        {(status?.venues ?? []).map((v) => (
          <span key={v.venue} style={{ display: "flex", gap: 3, alignItems: "center", fontSize: 10, color: palette.textMuted }}>
            {dot(v.connected, `${v.venue}: ${v.connected ? "connected" : "disconnected"}`)}{v.venue}{v.venueArmed ? " ●" : " ○"}
          </span>
        ))}
      </div>
      <button data-testid="arm-toggle" onClick={() => (armed ? oc.disarm() : oc.arm())}
        style={{ fontWeight: 700, padding: "4px 12px", borderRadius: 4, border: `1px solid ${armed ? palette.up : palette.warn}`,
          background: armed ? palette.up : "transparent", color: armed ? palette.bg : palette.warn, cursor: "pointer" }}>
        {armed ? "ARMED" : "DISARMED"}
      </button>
    </div>
  );
}
