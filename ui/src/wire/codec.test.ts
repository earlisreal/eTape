import { describe, it, expect } from "vitest";
import { decodeServerMessage, encodeClientMessage } from "./codec";

describe("decodeServerMessage", () => {
  it("decodes a snapshot frame", () => {
    const raw = JSON.stringify({
      kind: "snapshot",
      topic: "md.quote",
      key: "US.AAPL",
      payload: { symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.5, ts: "t" },
    });
    const msg = decodeServerMessage(raw);
    expect(msg?.kind).toBe("snapshot");
    if (msg?.kind === "snapshot") {
      expect(msg.topic).toBe("md.quote");
      expect((msg.payload as { bid: number }).bid).toBe(3.49);
    }
  });

  it("tolerates unknown fields without throwing", () => {
    const raw = JSON.stringify({
      kind: "delta", topic: "md.quote", key: "US.AAPL",
      payload: { last: 3.6 }, futureField: 42,
    });
    expect(decodeServerMessage(raw)?.kind).toBe("delta");
  });

  it("returns null on malformed JSON", () => {
    expect(decodeServerMessage("{not json")).toBeNull();
  });

  it("returns null on a frame with no known kind", () => {
    expect(decodeServerMessage(JSON.stringify({ kind: "bogus" }))).toBeNull();
  });
});

describe("encodeClientMessage", () => {
  it("round-trips a subscribe", () => {
    const s = encodeClientMessage({ kind: "subscribe", topic: "md.book" });
    expect(JSON.parse(s)).toEqual({ kind: "subscribe", topic: "md.book" });
  });
});
