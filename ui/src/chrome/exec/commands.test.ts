import { describe, it, expect, vi } from "vitest";
import { OrderCommands, type CommandAdapter } from "./commands";
import { ExecStore } from "../../data/ExecStore";
import type { AckMsg, Order, SubmitOrderArgs } from "../../wire/contract";

function fakes(ack: Partial<AckMsg> = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const cmd: CommandAdapter = { sendCommand: vi.fn(async (name, args) => { sent.push({ name, args }); return { kind: "ack", corrId: "c1", status: "accepted", ...ack } as AckMsg; }) };
  const exec = new ExecStore();
  const pushed: Array<{ level: string; text: string }> = [];
  const toast = { push: (t: { level: string; text: string }) => pushed.push(t), dismiss: () => {} };
  const oc = new OrderCommands({ cmd, exec, toast: toast as never, now: () => 100 });
  return { sent, cmd, exec, pushed, oc };
}
const args: SubmitOrderArgs = { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 };
const snap = (payload: Order[]) => ({ kind: "snapshot" as const, topic: "exec.orders" as never, payload });
const order = (id: string, over: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over });

describe("OrderCommands", () => {
  it("submit accepted → registers optimistic row + info flash", async () => {
    const { sent, exec, pushed, oc } = fakes({ orderId: "ET7" });
    await oc.submit(args, "BUY 10 AAPL @ 3.50 LMT");
    expect(sent[0]).toEqual({ name: "SubmitOrder", args });
    expect(exec.orders().find((v) => v.order.id === "ET7")?.optimistic).toBe(true);
    expect(pushed).toContainEqual({ level: "info", text: "BUY 10 AAPL @ 3.50 LMT" });
  });
  it("submit blocked → danger toast with verbatim reason, no optimistic row", async () => {
    const { exec, pushed, oc } = fakes({ status: "blocked", reason: "venue disarmed" });
    await oc.submit(args, "flash");
    expect(exec.orders()).toHaveLength(0);
    expect(pushed).toContainEqual({ level: "danger", text: "Blocked: venue disarmed" });
  });
  it("cancel / arm / disarm / kill send the right command + args", async () => {
    const { sent, oc } = fakes();
    await oc.cancel("alpaca-paper", "ET7");
    await oc.arm(); await oc.disarm("alpaca-paper"); await oc.kill();
    expect(sent.map((s) => s.name)).toEqual(["CancelOrder", "Arm", "Disarm", "KillSwitch"]);
    expect(sent[0].args).toEqual({ venue: "alpaca-paper", orderId: "ET7" });
    expect(sent[1].args).toEqual({});                       // Arm master (no venue)
    expect(sent[2].args).toEqual({ venue: "alpaca-paper" });
    expect(sent[3].args).toEqual({});                       // KillSwitch all
  });
  it("cancelLast cancels the newest working order; cancelAll(focused) cancels only that symbol's working orders", async () => {
    const { sent, exec, oc } = fakes();
    exec.apply(snap([order("ET1", { createdMs: 1 }), order("ET2", { createdMs: 2 }), order("ET3", { symbol: "US.NVDA", venue: "alpaca-paper", createdMs: 3 })]));
    await oc.cancelLast("US.AAPL");
    expect(sent.at(-1)?.args).toEqual({ venue: "alpaca-paper", orderId: "ET2" }); // newest AAPL working
    sent.length = 0;
    await oc.cancelAll("focused", "US.AAPL");
    expect(sent.map((s) => (s.args as { orderId: string }).orderId).sort()).toEqual(["ET1", "ET2"]);
  });
});
