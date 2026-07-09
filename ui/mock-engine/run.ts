import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startMockEngine, type Fixture } from "./server";

const here = dirname(fileURLToPath(import.meta.url));

// Fixture selection: defaults to "session-basic" (Plan 1's quote/health/events
// dev flow — unchanged so its docs/tests keep working). Select another fixture
// file from ui/fixtures/<name>.json via a CLI arg or the FIXTURE env var:
//   npm run mock-engine -- chart-session      (candles + VWAP dev-app fixture, Plan 2)
//   npm run mock-engine -- ladder-tape        (L2 book + tape + working orders, Plan 3)
//   npm run mock-engine -- monitoring         (scanner rank/hit + news, Plan 4)
//   npm run mock-engine -- exec-session       (execution surfaces dev flow, Plan 5)
//   npm run mock-engine -- session-e2e       (combined candles + book + tape, captured from replay)
//   FIXTURE=chart-session npm run mock-engine
const name = process.argv[2] ?? process.env.FIXTURE ?? "session-basic";
const fixture = JSON.parse(
  readFileSync(join(here, "..", "fixtures", `${name}.json`), "utf8"),
) as Fixture;

const port = 8686;
const isExec = name === "exec-session";
let seq = 0;
// Tracks master-arm state across commands so the account bar's ARMED toggle and
// a disarmed SubmitOrder's gate-block are both observable in the dev app (the
// exec-session fixture seeds masterArmed: true — see fixtures/exec-session.json).
let armed = true;

const execStatus = () => ({
  kind: "delta",
  topic: "exec.status",
  payload: {
    masterArmed: armed,
    global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }],
  },
});

// The two responders are always attached (never `undefined`) so their inline
// arrow functions pick up contextual typing from `startMockEngine`'s param
// type under this repo's strict tsconfig (exactOptionalPropertyTypes rejects
// assigning `undefined` to an optional property). Non-exec fixtures fall
// through to the same behavior the mock engine's own defaults would give
// (blanket ack / empty result), gated by the `isExec` check inside each.
startMockEngine({
  port,
  fixture,
  onCommand: (msg, send) => {
    if (!isExec) { send({ kind: "ack", corrId: msg.corrId, status: "accepted" }); return; }
    if (msg.name === "Arm") { armed = true; send({ kind: "ack", corrId: msg.corrId, status: "accepted" }); send(execStatus()); return; }
    if (msg.name === "Disarm") { armed = false; send({ kind: "ack", corrId: msg.corrId, status: "accepted" }); send(execStatus()); return; }
    if (msg.name === "SubmitOrder") {
      if (!armed) { send({ kind: "ack", corrId: msg.corrId, status: "blocked", reason: "Master arm is OFF." }); return; }
      const id = `ET-mock-${++seq}`;
      const a = msg.args as { venue: string; symbol: string; side: string; type: string; tif: string; qty: number; limitPrice: number; stopPrice: number };
      send({ kind: "ack", corrId: msg.corrId, status: "accepted", orderId: id });
      const mk = (status: string, over: Record<string, unknown> = {}) => ({
        kind: "delta",
        topic: "exec.orders",
        key: id,
        payload: { venue: a.venue, id, symbol: a.symbol, side: a.side, type: a.type, tif: a.tif, qty: a.qty, limitPrice: a.limitPrice, stopPrice: a.stopPrice, status, executedQty: 0, leavesQty: a.qty, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 0, updatedMs: 0, ...over },
      });
      send(mk("SUBMITTED"), 150);
      send(mk("ACCEPTED"), 400);
      send(mk("FILLED", { executedQty: a.qty, leavesQty: 0, avgFillPrice: a.limitPrice }), 900);
      send({ kind: "delta", topic: "exec.fills", payload: { venue: a.venue, orderId: id, symbol: a.symbol, side: a.side, qty: a.qty, price: a.limitPrice, tsMs: 900 } }, 900);
      return;
    }
    send({ kind: "ack", corrId: msg.corrId, status: "accepted" }); // CancelOrder/KillSwitch/etc. — fire regardless of armed
  },
  onQuery: (msg, send) => {
    if (!isExec) { send({ kind: "result", corrId: msg.corrId, payload: [] }); return; }
    if (msg.name === "QueryFills") {
      const a = msg.args as { symbol: string };
      send({ kind: "result", corrId: msg.corrId, payload: [{ venue: "alpaca-paper", orderId: "ET-seed-0", symbol: a.symbol, side: "BUY", qty: 300, price: 3.40, tsMs: 0 }] });
      return;
    }
    send({ kind: "result", corrId: msg.corrId, payload: [] });
  },
});
console.log(`mock engine listening on ws://127.0.0.1:${port}/ws (fixture: ${name})`);
