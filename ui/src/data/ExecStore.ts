import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, Order, PositionRow, AccountRow, ExecStatus, SubmitOrderArgs } from "../wire/contract";
import { isWorking } from "../wire/orderStatus";

export interface OptimisticOrder { args: SubmitOrderArgs; id: string; createdMs: number }
export interface OrderView { order: Order; optimistic: boolean }

interface ExecState {
  accounts: Map<string, AccountRow>;
  positions: PositionRow[];
  orders: Map<string, Order>;
  optimistic: Map<string, OptimisticOrder>;
  status: ExecStatus | null;
}

function synthOptimistic(o: OptimisticOrder): Order {
  const a = o.args;
  return {
    venue: a.venue, id: o.id, symbol: a.symbol, side: a.side, type: a.type, tif: a.tif,
    qty: a.qty, limitPrice: a.limitPrice, stopPrice: a.stopPrice,
    status: "SUBMITTED", executedQty: 0, leavesQty: a.qty, avgFillPrice: 0,
    rejectReason: "", replacesId: "", createdMs: o.createdMs, updatedMs: o.createdMs,
  };
}

export class ExecStore extends ReactStore<ExecState> {
  constructor() { super({ accounts: new Map(), positions: [], orders: new Map(), optimistic: new Map(), status: null }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    switch (m.topic) {
      case "exec.account": {
        const row = m.payload as AccountRow;
        const accounts = new Map(cur.accounts); accounts.set(row.venue, row);
        this.set({ ...cur, accounts });
        return;
      }
      case "exec.positions":
        this.set({ ...cur, positions: m.payload as PositionRow[] }); // full replace (snapshot & delta)
        return;
      case "exec.orders": {
        const orders = new Map(cur.orders);
        const optimistic = new Map(cur.optimistic);
        const list = m.kind === "snapshot" ? (m.payload as Order[]) : [m.payload as Order];
        if (m.kind === "snapshot") orders.clear();
        for (const o of list) { orders.set(o.id, o); optimistic.delete(o.id); } // real event reconciles the optimistic row
        this.set({ ...cur, orders, optimistic });
        return;
      }
      case "exec.status":
        this.set({ ...cur, status: m.payload as ExecStatus }); // full replace
        return;
      default:
        return; // exec.fills is routed to FillStore (Task 14)
    }
  }

  addOptimistic(o: OptimisticOrder): void {
    const cur = this.getSnapshot();
    if (cur.orders.has(o.id)) return; // real order already arrived — no phantom
    const optimistic = new Map(cur.optimistic); optimistic.set(o.id, o);
    this.set({ ...cur, optimistic });
  }

  accounts(): AccountRow[] { return [...this.getSnapshot().accounts.values()]; }
  positions(): PositionRow[] { return this.getSnapshot().positions; }
  status(): ExecStatus | null { return this.getSnapshot().status; }

  // Real orders + not-yet-confirmed optimistic rows, newest first.
  orders(): OrderView[] {
    const cur = this.getSnapshot();
    const views: OrderView[] = [...cur.orders.values()].map((order) => ({ order, optimistic: false }));
    for (const o of cur.optimistic.values()) if (!cur.orders.has(o.id)) views.push({ order: synthOptimistic(o), optimistic: true });
    return views.sort((a, b) => b.order.createdMs - a.order.createdMs);
  }

  workingOrdersFor(symbol?: string): Order[] {
    return [...this.getSnapshot().orders.values()]
      .filter((o) => isWorking(o.status) && (symbol === undefined || o.symbol === symbol));
  }
}
