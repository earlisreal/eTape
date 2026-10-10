import { afterEach, expect, test, vi } from "vitest";
import type { QueryVolumeProfileArgs, QueryVolumeProfileResult } from "../../gen/wsmsg";
import { VolumeProfileController } from "./volumeProfileController";

afterEach(() => vi.useRealTimers());

test("new selections hide old results and ignore obsolete responses while offline sends are skipped", async () => {
  vi.useFakeTimers();
  let online = true;
  const requests: { args: QueryVolumeProfileArgs; resolve: (value: QueryVolumeProfileResult) => void }[] = [];
  const model = new VolumeProfileController((args) => new Promise((resolve) => requests.push({ args, resolve })), () => online, () => {});
  const first = { symbol: "US.TEST", timeframe: "1m", fromMs: 1000, toMs: 2000, rows: 100, valueArea: 70 };
  model.setSelection(first);
  await vi.advanceTimersByTimeAsync(60);
  const second = { ...first, fromMs: 3000, toMs: 4000 };
  model.setSelection(second);
  expect(model.snapshot().status).toBe("loading");
  requests[0].resolve({ selection: first, status: "ready", source: "captured", partial: true, reasons: ["coverage_unproven"], rows: [], capturedVolume: 50, asOfMs: 1000 });
  await vi.advanceTimersByTimeAsync(60);
  expect(model.snapshot().result).toBeNull();
  expect(requests[1].args).toEqual(second);
  requests[1].resolve({ selection: second, status: "ready", source: "captured", partial: true, reasons: ["coverage_unproven"], rows: [{ lower: 10, upper: 10, volume: 75 }], capturedVolume: 75, poc: 10, vah: 10, val: 10, asOfMs: 3000 });
  await vi.advanceTimersByTimeAsync(1);
  expect(model.snapshot().result?.capturedVolume).toBe(75);
  online = false;
  model.setSelection({ ...second, symbol: "US.OTHER" });
  await vi.advanceTimersByTimeAsync(2000);
  expect(requests.map((r) => r.args.symbol)).toEqual(["US.TEST", "US.TEST"]);
  expect(model.snapshot().result).toBeNull();
  model.dispose();
});

test("same-selection failures retain stale data and malformed results never reach the renderer", async () => {
  const selection = { symbol: "US.TEST", timeframe: "1m", fromMs: 1000, toMs: 2000, rows: 100, valueArea: 70 };
  const result: QueryVolumeProfileResult = { selection, status: "ready", source: "captured", partial: true, reasons: [], rows: [{ lower: 1, upper: 1, volume: 10 }], capturedVolume: 10, poc: 1, vah: 1, val: 1, asOfMs: 1500 };
  let response = result;
  const model = new VolumeProfileController(async () => response, () => true, () => {});
  model.setSelection(selection);
  await model.refresh();
  response = { ...result, rows: [{ lower: NaN, upper: 1, volume: 10 }] };
  await model.refresh();
  expect(model.snapshot().status).toBe("stale");
  expect(model.snapshot().result).toBe(result);
  model.dispose();
});
