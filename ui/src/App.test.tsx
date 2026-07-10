import { describe, it, expect } from "vitest";
import { makeEngineLink } from "./App";

// Regression coverage for the "ui-engine" health-chip invariant: status must
// never be "down" while the app's WebSocket is state === "open". "down" is
// reserved for "no live connection at all" — a connected-but-slow-or-not-yet-
// measured socket must read "ok" or "degraded", never "down".
describe("makeEngineLink", () => {
  it("is never 'down' while state is 'open', even with rtt === null (no pong has arrived yet)", () => {
    // This is exactly the fresh-page-load window: the WS opens before the
    // first ping-interval tick completes a round trip, so rtt is still null.
    // The old code (`ms === null ? "down" : ...`) returned "down" here,
    // making the ENG chip flash red for ~4s after every page load despite the
    // connection being fully up.
    const link = makeEngineLink("open", null);
    expect(link.status).not.toBe("down");
    expect(link.status).toBe("degraded");
    expect(link.ms).toBeNull();
  });

  it("is 'ok' when open with a fast rtt", () => {
    expect(makeEngineLink("open", 42).status).toBe("ok");
  });

  it("is 'degraded' (never 'down') when open with a slow rtt, including very high rtt", () => {
    expect(makeEngineLink("open", 800).status).toBe("degraded");
    expect(makeEngineLink("open", 5000).status).toBe("degraded");
  });

  it("is 'down' only when the socket itself is not open, regardless of a stale rtt value", () => {
    expect(makeEngineLink("connecting", null).status).toBe("down");
    expect(makeEngineLink("reconnecting", 42).status).toBe("down");
  });
});
