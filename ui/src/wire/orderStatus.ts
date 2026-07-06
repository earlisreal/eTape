// Pure predicates over the wire's OrderStatus/Side. Lives in wire/ so the data and
// render layers can import it (they must not import chrome/). Display concerns live
// in chrome/exec/orderStatus.ts.
import type { OrderStatus, Side } from "./contract";

const WORKING: ReadonlySet<OrderStatus> = new Set(["SUBMITTED", "ACCEPTED", "PARTIALLY_FILLED"]);
export function isWorking(status: OrderStatus): boolean { return WORKING.has(status); }
export function isTerminal(status: OrderStatus): boolean { return !WORKING.has(status); }
export function sideIsSell(side: Side): boolean { return side === "SELL" || side === "SHORT"; }
