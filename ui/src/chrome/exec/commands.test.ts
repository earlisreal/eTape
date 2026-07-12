import { describe, it, expect, vi } from "vitest";
import { OrderCommands, type CommandAdapter } from "./commands";
import { ExecStore } from "../../data/ExecStore";
import type { AckMsg, Order, SubmitOrderArgs } from "../../wire/contract";
import type { SoundApi } from "../../sound/SoundEngine";

function soundSpy(): SoundApi & { placed: string[]; rejected: number } {
  const s = {
    placed: [] as string[], rejected: 0,
    orderPlaced: (side: string) => { s.placed.push(side); },
    orderRejected: () => { s.rejected += 1; },
  };
  return s as SoundApi & { placed: string[]; rejected: number };
}

function fakes(ack: Partial<AckMsg> = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const cmd: CommandAdapter = { sendCommand: vi.fn(async (name, args) => { sent.push({ name, args }); return { kind: "ack", corrId: "c1", status: "accepted", ...ack } as AckMsg; }) };
  const exec = new ExecStore();
  const pushed: Array<{ level: string; text: string }> = [];
  const toast = { push: (t: { level: string; text: string }) => pushed.push(t), dismiss: () => {} };
  const oc = new OrderCommands({ cmd, exec, toast: toast as never, now: () => 100 });
  return { sent, cmd, exec, pushed, oc };
}
const args: SubmitOrderArgs = { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0 };
const snap = (payload: Order[]) => ({ kind: "snapshot" as const, topic: "exec.orders" as never, payload });
const order = (id: string, over: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over });

describe("OrderCommands", () => {
  it("submit accepted → registers optimistic row + info flash", async () => {
    const { sent, exec, pushed, oc } = fakes({ orderId: "ET7" });
    await oc.submit(args, "BUY 10 AAPL @ 3.50 LMT");
    expect(sent[0]).toEqual({ name: "SubmitOrder", args });
    expect(exec.orders().find((v) => v.order.id === "ET7")?.optimistic).toBe(true);
    expect(pushed).toContainEqual({ level: "info", text: "BUY 10 AAPL @ 3.50 LMT" });
  });
  it("submit blocked → danger toast names the venue, verbatim reason when unmapped, no optimistic row", async () => {
    const { exec, pushed, oc } = fakes({ status: "blocked", reason: "venue disarmed" });
    await oc.submit(args, "flash");
    expect(exec.orders()).toHaveLength(0);
    expect(pushed).toContainEqual({ level: "danger", text: "Blocked (alpaca-paper): venue disarmed" });
  });
  it("submit blocked with 'no gate config for venue' → toast names the venue and humanizes the reason", async () => {
    const { pushed, oc } = fakes({ status: "blocked", reason: "no gate config for venue" });
    await oc.submit(args, "flash");
    expect(pushed).toContainEqual({
      level: "danger",
      text: "Blocked (alpaca-paper): no risk limits configured — set them in Settings › Venues",
    });
  });
  it("submit blocked with 'master disarmed' → toast names the venue and humanizes the reason", async () => {
    const { pushed, oc } = fakes({ status: "blocked", reason: "master disarmed" });
    await oc.submit(args, "flash");
    expect(pushed).toContainEqual({
      level: "danger",
      text: "Blocked (alpaca-paper): trading is locked — unlock it in the top bar",
    });
  });
  it("cancel / arm / disarm / kill send the right command + args", async () => {
    const { sent, oc } = fakes();
    await oc.cancel("alpaca-paper", "ET7");
    await oc.arm(); await oc.disarm(); await oc.kill();
    expect(sent.map((s) => s.name)).toEqual(["CancelOrder", "Arm", "Disarm", "KillSwitch"]);
    expect(sent[0].args).toEqual({ venue: "alpaca-paper", orderId: "ET7" });
    expect(sent[1].args).toEqual({});                       // Arm master-only
    expect(sent[2].args).toEqual({});                       // Disarm master-only
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

describe("OrderCommands sound triggers", () => {
  it("submit accepted -> orderPlaced(side); blocked -> orderRejected", async () => {
    const sound = soundSpy();
    const okCmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", orderId: "x" }) as AckMsg) };
    const oc = new OrderCommands({ cmd: okCmd, exec: { addOptimistic: vi.fn() } as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc.submit({ venue: "alpaca-paper", symbol: "US.AAPL", side: "SELL", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 1, limitPrice: 1, stopPrice: 0 }, "flash");
    expect(sound.placed).toEqual(["SELL"]);

    const blockCmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked", reason: "disarmed" }) as AckMsg) };
    const oc2 = new OrderCommands({ cmd: blockCmd, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc2.submit({ venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 1, limitPrice: 1, stopPrice: 0 }, "flash");
    expect(sound.rejected).toBe(1);
  });

  it("flatten accepted -> orderPlaced('SELL'); cancel/replace blocked -> orderRejected", async () => {
    const sound = soundSpy();
    const okCmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted" }) as AckMsg) };
    const oc = new OrderCommands({ cmd: okCmd, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc.flatten("alpaca-paper");
    expect(sound.placed).toEqual(["SELL"]);

    const blockCmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked" }) as AckMsg) };
    const oc2 = new OrderCommands({ cmd: blockCmd, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc2.cancel("alpaca-paper", "o1");
    await oc2.replace({ venue: "alpaca-paper", orderId: "o1", qty: 1, limitPrice: 1, stopPrice: 0 });
    expect(sound.rejected).toBe(2);
  });
});
