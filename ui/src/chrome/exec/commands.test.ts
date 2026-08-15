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

function fakes(ack: Partial<AckMsg> | ((orderId: string) => Partial<AckMsg>) = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const cmd: CommandAdapter = { sendCommand: vi.fn(async (name, args) => {
    sent.push({ name, args });
    const extra = typeof ack === "function" ? ack((args as { orderId?: string }).orderId ?? "") : ack;
    return { kind: "ack", corrId: "c1", status: "accepted", ...extra } as AckMsg;
  }) };
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
  it("action Cancel Last acknowledges immediately and stays silent after an accepted ACK", async () => {
    const { cmd, exec, pushed, oc } = fakes();
    exec.apply(snap([order("ET1", { createdMs: 2 })]));
    let resolveAck!: (ack: AckMsg) => void;
    vi.mocked(cmd.sendCommand).mockImplementation(() => new Promise<AckMsg>((resolve) => { resolveAck = resolve; }));

    const pending = oc.cancelLast("US.AAPL", { feedback: "action" });
    expect(pushed).toEqual([{ level: "info", text: "Cancel Last requested — AAPL" }]);

    resolveAck({ kind: "ack", corrId: "c1", status: "accepted" });
    await pending;
    expect(pushed).toHaveLength(1);
  });
  it("action Cancel Last reports an empty working set without sending", async () => {
    const { sent, pushed, oc } = fakes();
    await oc.cancelLast("US.AAPL", { feedback: "action" });
    expect(sent).toHaveLength(0);
    expect(pushed).toEqual([{ level: "info", text: "Cancel Last — no working order" }]);
  });
  it("action Cancel Last reports blocked and ambiguous outcomes once", async () => {
    const blocked = fakes({ status: "blocked", reason: "master disarmed" });
    blocked.exec.apply(snap([order("ET1")]));
    await blocked.oc.cancelLast("US.AAPL", { feedback: "action" });
    expect(blocked.pushed).toEqual([
      { level: "info", text: "Cancel Last requested — AAPL" },
      { level: "danger", text: "Cancel failed (alpaca-paper): master disarmed" },
    ]);

    const ambiguous = fakes({ ambiguous: true });
    ambiguous.exec.apply(snap([order("ET2")]));
    await ambiguous.oc.cancelLast("US.AAPL", { feedback: "action" });
    expect(ambiguous.pushed).toEqual([
      { level: "info", text: "Cancel Last requested — AAPL" },
      { level: "warn", text: "Cancel outcome uncertain — AAPL" },
    ]);
  });
  it("action Cancel Last uses the newest order's display symbol when no symbol is supplied", async () => {
    const { sent, exec, pushed, oc } = fakes();
    exec.apply(snap([
      order("ET1", { symbol: "US.AAPL", createdMs: 10 }),
      order("ET2", { symbol: "US.TSLA", createdMs: 20 }),
    ]));
    await oc.cancelLast("", { feedback: "action" });
    expect(sent.at(-1)?.args).toEqual({ venue: "alpaca-paper", orderId: "ET2" });
    expect(pushed[0]).toEqual({ level: "info", text: "Cancel Last requested — TSLA" });
  });
});

describe("OrderCommands action Cancel All feedback", () => {
  it("reports empty, focused, everything, singular, and plural scopes", async () => {
    const empty = fakes();
    await empty.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(empty.sent).toHaveLength(0);
    expect(empty.pushed).toEqual([{ level: "info", text: "Cancel All — no working orders" }]);

    const plural = fakes();
    plural.exec.apply(snap([order("ET1", { createdMs: 1 }), order("ET2", { createdMs: 2 })]));
    await plural.oc.cancelAll("focused", "US.AAPL", { feedback: "action" });
    expect(plural.pushed[0]).toEqual({ level: "info", text: "Cancel All requested — AAPL (2 orders)" });
    plural.pushed.length = 0;
    await plural.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(plural.pushed[0]).toEqual({ level: "info", text: "Cancel All requested — 2 orders" });
    plural.pushed.length = 0;
    await plural.oc.cancelAll("focused", "", { feedback: "action" });
    expect(plural.pushed[0]).toEqual({ level: "info", text: "Cancel All requested — 2 orders" });

    const singular = fakes();
    singular.exec.apply(snap([order("ET3")]));
    await singular.oc.cancelAll("focused", "US.AAPL", { feedback: "action" });
    expect(singular.pushed[0]).toEqual({ level: "info", text: "Cancel All requested — AAPL (1 order)" });
  });
  it("freezes the target count before requests complete", async () => {
    const sent: Array<{ name: string; args: unknown }> = [];
    const resolvers: Array<(ack: AckMsg) => void> = [];
    const cmd: CommandAdapter = { sendCommand: vi.fn(async (name, args) => {
      sent.push({ name, args });
      return new Promise<AckMsg>((resolve) => { resolvers.push(resolve); });
    }) };
    const exec = new ExecStore();
    const pushed: Array<{ level: string; text: string }> = [];
    const oc = new OrderCommands({ cmd, exec, toast: { push: (t: { level: string; text: string }) => pushed.push(t), dismiss: () => {} } as never, now: () => 100 });
    exec.apply(snap([order("ET1", { createdMs: 1 }), order("ET2", { createdMs: 2 })]));

    const pending = oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(pushed).toEqual([{ level: "info", text: "Cancel All requested — 2 orders" }]);
    expect(sent.map((s) => (s.args as { orderId: string }).orderId).sort()).toEqual(["ET1", "ET2"]);
    exec.apply(snap([]));
    resolvers[0]({ kind: "ack", corrId: "c1", status: "blocked", reason: "master disarmed" });
    resolvers[1]({ kind: "ack", corrId: "c2", status: "accepted" });
    await pending;
    expect(pushed).toEqual([
      { level: "info", text: "Cancel All requested — 2 orders" },
      { level: "danger", text: "Cancel All incomplete — 1 of 2 failed: master disarmed" },
    ]);
  });
  it("aggregates accepted, ambiguous, and blocked outcomes without per-order toast spam", async () => {
    const ambiguous = fakes(() => ({ ambiguous: true }));
    ambiguous.exec.apply(snap([order("ET1"), order("ET2"), order("ET3")]));
    await ambiguous.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(ambiguous.pushed).toEqual([
      { level: "info", text: "Cancel All requested — 3 orders" },
      { level: "warn", text: "Cancel All outcome uncertain — 3 of 3 requests could not be confirmed" },
    ]);

    const mixed = fakes((id) => id === "ET1"
      ? { status: "blocked", reason: "master disarmed" }
      : id === "ET2" ? { ambiguous: true } : {});
    mixed.exec.apply(snap([order("ET1"), order("ET2"), order("ET3")]));
    await mixed.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(mixed.pushed).toEqual([
      { level: "info", text: "Cancel All requested — 3 orders" },
      { level: "danger", text: "Cancel All incomplete — 1 failed, 1 uncertain of 3: master disarmed" },
    ]);
  });
  it("includes a common failure reason but omits different reasons", async () => {
    const common = fakes((id) => id === "ET1" || id === "ET2" ? { status: "blocked", reason: "master disarmed" } : {});
    common.exec.apply(snap([order("ET1"), order("ET2"), order("ET3")]));
    await common.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(common.pushed.at(-1)).toEqual({ level: "danger", text: "Cancel All incomplete — 2 of 3 failed: master disarmed" });

    const different = fakes((id) => id === "ET1"
      ? { status: "blocked", reason: "master disarmed" }
      : id === "ET2" ? { status: "blocked", reason: "venue disconnected" } : {});
    different.exec.apply(snap([order("ET1"), order("ET2"), order("ET3")]));
    await different.oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(different.pushed.at(-1)).toEqual({ level: "danger", text: "Cancel All incomplete — 2 of 3 failed" });
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
  it("action Cancel All keeps per-request rejection sounds for blocked cancellations", async () => {
    const sound = soundSpy();
    const cmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked", reason: "master disarmed" }) as AckMsg) };
    const exec = new ExecStore();
    exec.apply(snap([order("ET1"), order("ET2")]));
    const oc = new OrderCommands({ cmd, exec, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc.cancelAll("everything", undefined, { feedback: "action" });
    expect(sound.rejected).toBe(2);
  });

  it("ambiguous execution ACKs warn without rejection or placement feedback", async () => {
    const sound = soundSpy();
    const push = vi.fn();
    const cmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked", reason: "websocket disconnected", ambiguous: true }) as AckMsg) };
    const oc = new OrderCommands({ cmd, exec: {} as never, toast: { push } as never, now: () => 0, sound });

    await oc.submit(args, "flash");
    await oc.cancel("alpaca-paper", "o1");
    await oc.replace({ venue: "alpaca-paper", orderId: "o1", qty: 1, limitPrice: 1, stopPrice: 0 });
    await oc.flatten("alpaca-paper");

    expect(sound.rejected).toBe(0);
    expect(sound.placed).toEqual([]);
    expect(push).toHaveBeenCalledTimes(4);
    expect(push).toHaveBeenCalledWith({
      level: "warn",
      text: "Outcome unknown (alpaca-paper) — connection was lost after the request was sent. Verify Open Orders / position before submitting again.",
    });
  });

  it("Kill Switch reports initiated, blocked, and ambiguous outcomes", async () => {
    const run = async (ack: Partial<AckMsg>) => {
      const push = vi.fn();
      const cmd: CommandAdapter = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", ...ack }) as AckMsg) };
      const oc = new OrderCommands({ cmd, exec: {} as never, toast: { push } as never, now: () => 0 });
      await oc.kill();
      return push;
    };

    expect(await run({})).toHaveBeenCalledWith({ level: "warn", text: "KILL initiated — trading locked; cancel-all requested. Verify Open Orders." });
    expect(await run({ status: "blocked", reason: "master disarmed" })).toHaveBeenCalledWith({ level: "danger", text: "Kill Switch failed: master disarmed" });
    expect(await run({ status: "blocked", ambiguous: true, reason: "websocket disconnected" })).toHaveBeenCalledWith({
      level: "warn",
      text: "KILL outcome unknown — connection was lost. Verify open orders and positions immediately.",
    });
  });
});
