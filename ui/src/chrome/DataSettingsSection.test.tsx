// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { DataSettingsSection } from "./DataSettingsSection";
import { SessionStore } from "../data/SessionStore";
import { HealthStore } from "../data/HealthStore";
import type { AckMsg, DataSettings, SetDataSettingsArgs } from "../wire/contract";

const original: DataSettings = {
  openDHost: "127.0.0.1", openDPort: 11111, quotaSlots: 300,
  scannerPremarketMs: 2000, scannerRTHMs: 3000, yahooNews: true,
  tenSecondDays: 0, intradayDays: 2, dailyYears: 2, barRetentionDays: 30,
  recordingEnabled: true, recordingRetentionDays: 30,
  recordingMaxBytes: 10 * 1024 ** 3, recordingMinFreeBytes: 2 * 1024 ** 3,
};

const defaults: DataSettings = {
  ...original, scannerPremarketMs: 1000, scannerRTHMs: 1000, yahooNews: false,
  intradayDays: 70, dailyYears: 0,
};

function commands(blockSave = false) {
  return { sendCommand: vi.fn(async (...[name]: [string, unknown]): Promise<AckMsg> => {
    if (name === "GetDataSettings") return { kind: "ack", corrId: "c", status: "accepted", value: { file: original, running: original, defaults } };
    if (name === "SetDataSettings" && blockSave) return { kind: "ack", corrId: "c", status: "blocked", reason: "settings changed elsewhere; reload before saving" };
    return { kind: "ack", corrId: "c", status: "accepted" };
  }) };
}

describe("DataSettingsSection", () => {
  it("confirms reset, restores engine defaults in the draft, and only persists on Save", async () => {
    const ctl = commands();
    render(<DataSettingsSection commands={ctl} engineState="open" />);
    await screen.findByLabelText("Regular-session refresh (seconds)");
    fireEvent.change(screen.getByLabelText("Host"), { target: { value: "custom.example" } });
    fireEvent.change(screen.getByLabelText("Port"), { target: { value: "22222" } });
    fireEvent.change(screen.getByLabelText("Archive limit (GiB)"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect((screen.getByLabelText("Host") as HTMLInputElement).value).toBe("custom.example");
    fireEvent.click(screen.getByRole("button", { name: "Confirm reset" }));
    expect((screen.getByLabelText("Host") as HTMLInputElement).value).toBe(defaults.openDHost);
    expect((screen.getByLabelText("Port") as HTMLInputElement).value).toBe(String(defaults.openDPort));
    expect((screen.getByLabelText("Regular-session refresh (seconds)") as HTMLInputElement).value).toBe("1");
    expect((screen.getByLabelText("1-minute history (days)") as HTMLInputElement).value).toBe("70");
    expect((screen.getByLabelText("Use experimental Yahoo headline supplement") as HTMLInputElement).checked).toBe(false);
    expect(ctl.sendCommand).not.toHaveBeenCalledWith("SetDataSettings", expect.anything());
    expect(ctl.sendCommand).not.toHaveBeenCalledWith("RestartEngine", {});
    fireEvent.click(screen.getByRole("button", { name: "Save data settings" }));
    await waitFor(() => expect(ctl.sendCommand).toHaveBeenCalledWith("SetDataSettings", { expected: original, settings: defaults }));
  });

  it("saves converted values with the original snapshot and requires an explicit restart", async () => {
    const ctl = commands();
    render(<DataSettingsSection commands={ctl} engineState="open" />);
    await screen.findByLabelText("Regular-session refresh (seconds)");
    fireEvent.change(screen.getByLabelText("Regular-session refresh (seconds)"), { target: { value: "1.5" } });
    fireEvent.change(screen.getByLabelText("Archive limit (GiB)"), { target: { value: "5.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save data settings" }));
    await waitFor(() => expect(ctl.sendCommand).toHaveBeenCalledWith("SetDataSettings", expect.anything()));
    const call = ctl.sendCommand.mock.calls.find(([name]) => name === "SetDataSettings");
    const args = call?.[1] as SetDataSettingsArgs;
    expect(args.expected).toEqual(original);
    expect(args.settings).toEqual({ ...original, scannerRTHMs: 1500, recordingMaxBytes: 5.5 * 1024 ** 3 });
    expect(await screen.findByText("Restart the engine to apply saved data settings.")).toBeTruthy();
    expect(ctl.sendCommand).not.toHaveBeenCalledWith("RestartEngine", {});
    fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm restart" }));
    await waitFor(() => expect(ctl.sendCommand).toHaveBeenCalledWith("RestartEngine", {}));
  });

  it("keeps edits visible when another window changed settings", async () => {
    const ctl = commands(true);
    render(<DataSettingsSection commands={ctl} engineState="open" />);
    await screen.findByLabelText("1-minute history (days)");
    fireEvent.change(screen.getByLabelText("1-minute history (days)"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save data settings" }));
    expect((await screen.findByRole("alert")).textContent).toContain("changed elsewhere");
    expect((screen.getByLabelText("1-minute history (days)") as HTMLInputElement).value).toBe("5");
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect((screen.getByLabelText("1-minute history (days)") as HTMLInputElement).value).toBe("2"));
  });

  it("explains that demo edits apply to live mode without a restart prompt", async () => {
    const ctl = commands();
    const session = new SessionStore();
    session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode: "demo" } } as never);
    render(<DataSettingsSection commands={ctl} engineState="open" session={session} />);
    await screen.findByLabelText("Yahoo headline supplement", { exact: false });
    expect(screen.getByText(/Changes apply the next time live mode starts/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Restart now" })).toBeNull();
  });

  it("shows the account quota alongside the eTape budget", async () => {
    const health = new HealthStore();
    health.apply({ kind: "snapshot", topic: "sys.health", payload: { links: [], quota: {
      subUsed: 120, subRemain: 180, subOwn: 100, subForeign: 20,
      histUsed: 2, histRemain: 298, state: "ok", histState: "ok",
    } } } as never);
    render(<DataSettingsSection commands={commands()} health={health} />);
    await screen.findByLabelText("eTape subscription budget");
    expect(screen.getByText(/Account subscriptions: 120 \/ 300 used \(eTape 100, other clients 20\)/)).toBeTruthy();
  });
});
