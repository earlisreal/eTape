import { describe, it, expect, beforeEach, vi } from "vitest";
import { WsClient } from "./WsClient";
import { FakeSocket } from "../../test/fakes";
import { perf } from "../perf/PerfMonitor";
import { uiLog } from "../logging/logger";

function makeClient() {
  const timers: Array<() => void> = [];
  const setTimeoutLike = (fn: () => void) => { timers.push(fn); return timers.length; };
  const client = new WsClient({
    url: "ws://x/ws",
    socketFactory: (u) => new FakeSocket(u),
    now: () => 1000,
    setTimeout: setTimeoutLike as unknown as typeof setTimeout,
    backoff: () => 5,
  });
  return { client, flushTimers: () => { const t = timers.splice(0); t.forEach((f) => f()); } };
}

beforeEach(() => { vi.restoreAllMocks(); FakeSocket.reset(); });

describe("WsClient", () => {
  it("logs connection lifecycle with reconnect metadata", () => {
    const info = vi.spyOn(uiLog, "info").mockImplementation(() => {});
    const warn = vi.spyOn(uiLog, "warn").mockImplementation(() => {});
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    expect(info).toHaveBeenCalledWith("ws connected", expect.objectContaining({ reconnect: false }));

    FakeSocket.last().dropFromServer();
    expect(warn).toHaveBeenCalledWith("ws disconnected", expect.objectContaining({ reconnectAttempt: 1, retryMs: 5 }));
  });

  it("sends subscribe on first subscriber and unsubscribe on last", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();

    const off1 = client.subscribe("md.quote", () => {});
    const off2 = client.subscribe("md.quote", () => {});
    const subs = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(subs.filter((m) => m.kind === "subscribe" && m.topic === "md.quote")).toHaveLength(1);

    off1();
    expect(FakeSocket.last().sent.map((s) => JSON.parse(s))
      .some((m) => m.kind === "unsubscribe")).toBe(false);
    off2();
    expect(FakeSocket.last().sent.map((s) => JSON.parse(s))
      .some((m) => m.kind === "unsubscribe" && m.topic === "md.quote")).toBe(true);
  });

  it("dispatches snapshot then delta to the subscriber", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const got: string[] = [];
    client.subscribe("md.quote", (m) => got.push(m.kind));
    FakeSocket.last().emit(JSON.stringify({ kind: "snapshot", topic: "md.quote", payload: {} }));
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.quote", payload: {} }));
    // a message for another topic is ignored by this subscriber
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.book", payload: {} }));
    expect(got).toEqual(["snapshot", "delta"]);
  });

  it("re-subscribes all live topics after a reconnect", () => {
    const { client, flushTimers } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.subscribe("md.quote", () => {});
    client.subscribe("md.book", () => {});

    FakeSocket.last().dropFromServer();  // server drops
    flushTimers();                        // backoff fires → new socket
    FakeSocket.last().open();             // reconnected

    const resent = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(resent.filter((m) => m.kind === "subscribe").map((m) => m.topic).sort())
      .toEqual(["md.book", "md.quote"]);
  });

  it("reports state transitions", () => {
    const { client, flushTimers } = makeClient();
    const states: string[] = [];
    client.onState((s) => states.push(s));
    client.start();
    FakeSocket.last().open();
    FakeSocket.last().dropFromServer();
    flushTimers();
    FakeSocket.last().open();
    expect(states).toEqual(["connecting", "open", "reconnecting", "connecting", "open"]);
  });

  it("resolves sendCommand when the matching ack arrives", async () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendCommand("Subscribe", { topic: "x" });
    const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
    expect(sent.kind).toBe("command");
    FakeSocket.last().emit(JSON.stringify({ kind: "ack", corrId: sent.corrId, status: "accepted" }));
    await expect(p).resolves.toMatchObject({ status: "accepted" });
  });

  it("settles sent commands as ambiguous on disconnect without replaying them", async () => {
    const { client, flushTimers } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendCommand("RequestLocate", { idempotencyKey: "same-key" });
    FakeSocket.last().dropFromServer();
    await expect(p).resolves.toMatchObject({ status: "blocked", reason: "websocket disconnected", ambiguous: true });

    flushTimers();
    FakeSocket.last().open();
    expect(FakeSocket.last().sent).toHaveLength(0);
  });

  it("rejects sent queries on disconnect", async () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendQuery("QueryLocateQuotes", { symbols: ["US.AAPL"] });
    FakeSocket.last().dropFromServer();
    await expect(p).rejects.toThrow("websocket disconnected");
  });

  it("measures RTT from ping/pong", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.sendPing();
    const ping = JSON.parse(FakeSocket.last().sent.at(-1)!);
    FakeSocket.last().emit(JSON.stringify({ kind: "pong", t: ping.t }));
    expect(client.rttMs()).toBe(0); // now() is fixed at 1000 in the fake
  });

  it("counts and rate-limits malformed frames without logging their contents", () => {
    const warn = vi.spyOn(uiLog, "warn").mockImplementation(() => {});
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const raw = "malformed-secret-frame";
    FakeSocket.last().emit(raw);
    expect(warn).toHaveBeenCalledWith("malformed websocket frame dropped", { count: 1, length: raw.length });
    expect(JSON.stringify(warn.mock.calls)).not.toContain(raw);

    for (let i = 0; i < 99; i++) FakeSocket.last().emit("x");
    expect(warn).toHaveBeenCalledTimes(1);
    FakeSocket.last().emit("xx");
    expect(warn).toHaveBeenCalledTimes(2);
    expect(warn).toHaveBeenLastCalledWith("malformed websocket frame dropped", { count: 101, length: 2 });
  });

  it("logs rejected command ACKs without retaining or logging args", async () => {
    const warn = vi.spyOn(uiLog, "warn").mockImplementation(() => {});
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const args = { symbol: "US.SECRET", token: "do-not-log" };
    const p = client.sendCommand("EnsureSymbol", args);
    const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
    FakeSocket.last().emit(JSON.stringify({ kind: "ack", corrId: sent.corrId, status: "blocked", reason: "unknown symbol" }));
    await expect(p).resolves.toMatchObject({ status: "blocked" });
    expect(warn).toHaveBeenCalledWith(`command rejected command=EnsureSymbol corrId=${sent.corrId} status=blocked reason=unknown symbol`, {
      command: "EnsureSymbol", corrId: sent.corrId, status: "blocked", reason: "unknown symbol",
    });
    expect(JSON.stringify(warn.mock.calls)).not.toContain("do-not-log");
  });

  it("does not warn for successful command ACKs or normal wire traffic", async () => {
    const debug = vi.spyOn(uiLog, "debug").mockImplementation(() => {});
    const info = vi.spyOn(uiLog, "info").mockImplementation(() => {});
    const warn = vi.spyOn(uiLog, "warn").mockImplementation(() => {});
    const error = vi.spyOn(uiLog, "error").mockImplementation(() => {});
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendCommand("Subscribe", { topic: "md.quote" });
    const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
    FakeSocket.last().emit(JSON.stringify({ kind: "ack", corrId: sent.corrId, status: "accepted" }));
    await expect(p).resolves.toMatchObject({ status: "accepted" });
    vi.clearAllMocks();

    client.subscribe("md.quote", () => {});
    client.sendPing();
    FakeSocket.last().emit(JSON.stringify({ kind: "snapshot", topic: "md.quote", payload: {} }));
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.quote", payload: {} }));
    FakeSocket.last().emit(JSON.stringify({ kind: "pong", t: 1000 }));
    expect(debug).not.toHaveBeenCalled();
    expect(info).not.toHaveBeenCalled();
    expect(warn).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  it("sendQuery resolves with the correlated result payload", async () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendQuery("QueryFills", { symbol: "US.AAPL", fromMs: 0, toMs: 9 });
    const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
    expect(sent.kind).toBe("query");
    FakeSocket.last().emit(JSON.stringify({ kind: "result", corrId: sent.corrId,
      payload: [{ venue: "v", orderId: "ET1", symbol: "US.AAPL", side: "BUY", qty: 1, price: 3.5, tsMs: 5 }] }));
    await expect(p).resolves.toHaveLength(1);
  });

  it("buffers a command issued before open and flushes it on connect", () => {
    const { client } = makeClient();
    client.start();                 // connecting, socket not open yet
    const p = client.sendCommand("GetConfig", { key: "workspace.trading" });
    expect(FakeSocket.last().sent).toHaveLength(0); // nothing sent while connecting
    FakeSocket.last().open();       // onopen flushes the outbox
    const sent = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(sent.some((m) => m.kind === "command" && m.name === "GetConfig")).toBe(true);
    void p; // promise stays pending until an ack arrives — not awaited here
  });

  it("reports each snapshot/delta frame's topic to the shared perf singleton on the hot decode path", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.subscribe("md.tape", () => {});
    const spy = vi.spyOn(perf, "countMessage");
    FakeSocket.last().emit(JSON.stringify({ kind: "snapshot", topic: "md.tape", payload: [] }));
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.tape", payload: [] }));
    expect(spy).toHaveBeenNthCalledWith(1, "md.tape");
    expect(spy).toHaveBeenNthCalledWith(2, "md.tape");
    spy.mockRestore();
  });
});
