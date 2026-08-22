import { describe, expect, it, vi } from "vitest";
import { makeMutationClient } from "./mutations";

const engine = vi.hoisted(() => ({
  GetScannerFilters: vi.fn(),
  SetScannerFilters: vi.fn(),
  WatchlistAdd: vi.fn(),
  WatchlistRemove: vi.fn(),
  GetVenueSetup: vi.fn(),
  SetVenueSetup: vi.fn(),
  PutCredential: vi.fn(),
  DeleteCredential: vi.fn(),
  TestConnection: vi.fn(),
}));

vi.mock("../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/index.js", () => ({ EngineService: engine }));

describe("typed mutation client", () => {
  it("normalizes generated nullable values and preserves revisions", async () => {
    engine.GetScannerFilters.mockResolvedValue({
      filters: { mode: "most_active", minChangePct: 2, maxFloatShares: null, minVolume: 10, minVolumeRatio: 1.5, floatUnit: "M", volumeUnit: "K" },
      revision: 4,
    });
    engine.WatchlistAdd.mockResolvedValue({ status: "accepted", reason: "", symbols: null, revision: 5 });
    const client = makeMutationClient(true, vi.fn());

    await expect(client.GetScannerFilters()).resolves.toMatchObject({ revision: 4, filters: { mode: "most_active" } });
    await expect(client.WatchlistAdd({ symbol: "US.AAPL" })).resolves.toMatchObject({ status: "accepted", symbols: [], revision: 5 });
  });

  it("fails closed outside the native host instead of sending migrated commands", async () => {
    const sendCommand = vi.fn();
    const client = makeMutationClient(false, sendCommand);

    await expect(client.PutCredential({ name: "cred", keyId: "key", secretKey: "secret" })).rejects.toThrow("native Wails host");
    expect(sendCommand).not.toHaveBeenCalled();
  });
});
