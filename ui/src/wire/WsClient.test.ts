import { describe, it, expect, beforeEach } from "vitest";
import { WsClient } from "./WsClient";
import { FakeSocket } from "../../test/fakes";

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

beforeEach(() => FakeSocket.reset());

describe("WsClient", () => {
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

  it("measures RTT from ping/pong", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.sendPing();
    const ping = JSON.parse(FakeSocket.last().sent.at(-1)!);
    FakeSocket.last().emit(JSON.stringify({ kind: "pong", t: ping.t }));
    expect(client.rttMs()).toBe(0); // now() is fixed at 1000 in the fake
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
});
