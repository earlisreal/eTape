import { describe, expect, it } from "vitest";
import { mapCrosshairBar } from "./crosshairSync";

const bar = (bucketStart: string, dataGap = false) => ({ bucketStart, dataGap });

describe("mapCrosshairBar", () => {
  it("maps a finer candle to the destination candle containing its start", () => {
    const bars = [bar("2026-10-01T13:30:00.000Z"), bar("2026-10-01T13:35:00.000Z")];

    expect(mapCrosshairBar(Date.parse("2026-10-01T13:32:00.000Z"), "1m", bars, "5m")).toBe(0);
  });

  it("maps a Daily candle to the first loaded intraday candle on that ET day", () => {
    const bars = [bar("2026-10-01T13:30:00.000Z"), bar("2026-10-02T13:30:00.000Z")];

    expect(mapCrosshairBar(Date.parse("2026-10-01T04:00:00.000Z"), "D", bars, "1m")).toBe(0);
  });

  it("skips a Data Gap when looking for the first finer candle", () => {
    const bars = [bar("2026-10-01T13:30:00.000Z", true), bar("2026-10-01T13:31:00.000Z")];

    expect(mapCrosshairBar(Date.parse("2026-10-01T04:00:00.000Z"), "D", bars, "1m")).toBe(1);
  });

  it("does not borrow a nearby bar from the next exchange-calendar period", () => {
    const bars = [bar("2026-10-02T13:30:00.000Z")];

    expect(mapCrosshairBar(Date.parse("2026-10-01T04:00:00.000Z"), "D", bars, "1m")).toBeNull();
  });

  it("maps weekly and monthly bars using exchange-calendar bucket starts", () => {
    const weeklyBars = [bar("2026-10-05T04:00:00.000Z"), bar("2026-10-12T04:00:00.000Z")];
    const monthlyBars = [bar("2026-10-01T04:00:00.000Z"), bar("2026-11-01T04:00:00.000Z")];

    expect(mapCrosshairBar(Date.parse("2026-10-01T04:00:00.000Z"), "M", weeklyBars, "W")).toBe(0);
    expect(mapCrosshairBar(Date.parse("2026-10-07T04:00:00.000Z"), "W", monthlyBars, "M")).toBe(0);
  });

  it("does not match a Data Gap destination candle", () => {
    const bars = [bar("2026-10-01T13:30:00.000Z", true)];

    expect(mapCrosshairBar(Date.parse("2026-10-01T13:32:00.000Z"), "1m", bars, "5m")).toBeNull();
  });
});
