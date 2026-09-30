import { describe, expect, it } from "vitest";
import { chartOrderMarkers, orderActionPending, snapOrderMarkerPrice } from "./orderMarkers";
import type { Order } from "../../wire/contract";

const base: Order = {
  venue:"sim", id:"o1", symbol:"US.AAPL", side:"BUY", type:"LIMIT", tif:"DAY", session:"AUTO",
  qty:10, limitPrice:100, stopPrice:0, status:"ACCEPTED", executedQty:0, leavesQty:10,
  avgFillPrice:0, rejectReason:"", replacesId:"", createdMs:1, updatedMs:1,
};

describe("chart order markers", () => {
  it("filters confirmed working limit and stop-limit orders to the grouped venue and symbol", () => {
    const held: Order = { ...base, id:"held", type:"STOP_LIMIT", stopPrice:101, held:{phase:"WAITING", deadlineMs:2} };
    const nativeBase = { ...held };
    delete nativeBase.held;
    const native: Order = { ...nativeBase, id:"native", venue:"other" };
    const closed = { ...base, id:"closed", status:"FILLED" as const };
    expect(chartOrderMarkers([base, held, native, closed], "sim", "US.AAPL", false).map((m) => [m.order.id, m.kind, m.price]))
      .toEqual([["o1", "limit", 100], ["held", "stop-limit", 101]]);
    expect(chartOrderMarkers([base], "sim", "US.AAPL", true)).toEqual([]);
  });

  it("waits for venue confirmation before showing a native marker, but shows accepted held parents", () => {
    const submitted: Order = { ...base, id:"unconfirmed", status:"SUBMITTED" };
    const held: Order = { ...submitted, id:"held", type:"STOP_LIMIT", stopPrice:101, held:{phase:"WAITING", deadlineMs:2} };
    expect(chartOrderMarkers([submitted, held], "sim", "US.AAPL", false).map((marker) => marker.order.id)).toEqual(["held"]);
  });

  it("switches a held stop marker to its limit marker only after activation", () => {
    const order = { ...base, type:"STOP_LIMIT" as const, stopPrice:101, held:{phase:"ACTIVATING", deadlineMs:2, childClientId:"o1"} };
    const [marker] = chartOrderMarkers([order], "sim", "US.AAPL", false);
    expect(marker.kind).toBe("limit");
    expect(marker.price).toBe(100);
    expect(marker.draggable).toBe(false);
  });

  it("snaps to cents above one dollar and ten-thousandths below", () => {
    expect(snapOrderMarkerPrice(2.567)).toBe(2.57);
    expect(snapOrderMarkerPrice(0.98676)).toBe(0.9868);
  });

  it("keeps previous and requested prices visible for an ambiguous venue replace", () => {
    const order: Order = { ...base, action:{kind:"REPLACE", phase:"UNKNOWN", previousLimitPrice:100, requestedLimitPrice:101, requestedQty:10} };
    const [marker] = chartOrderMarkers([order], "sim", "US.AAPL", false);
    expect(marker.draggable).toBe(false);
    expect(orderActionPending(marker)).toEqual({kind:"replace", price:101, stop:false, confirmedPrice:100, outcome:"unknown"});
  });

  it("marks cancellation requests as non-draggable until a broker result arrives", () => {
    const order: Order = { ...base, action:{kind:"CANCEL", phase:"REQUESTED", previousLimitPrice:100} };
    const [marker] = chartOrderMarkers([order], "sim", "US.AAPL", false);
    expect(marker.draggable).toBe(false);
    expect(orderActionPending(marker)).toEqual({kind:"cancel", stop:false, outcome:"requested"});
  });
});
