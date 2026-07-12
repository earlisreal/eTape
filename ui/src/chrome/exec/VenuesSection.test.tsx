// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { VenuesSection } from "./VenuesSection";
import type { AckMsg, Gate, VenueConfig, VenueSetup, TestConnectionResult } from "../../wire/contract";

function okResult(overrides: Partial<TestConnectionResult> = {}): TestConnectionResult {
  return { ok: true, env: "paper", accountId: "PA999", accountType: "cash", message: "ok", accounts: [], ...overrides };
}
function testAck(value: TestConnectionResult): AckMsg {
  return { kind: "ack", corrId: "c", status: "accepted", value };
}

const runningConfig: VenueConfig = {
  venues: [
    { id: "alpaca-paper", broker: "alpaca", env: "paper", credentials: "alpaca", accountId: "PA123", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 },
    { id: "tradezero-live", broker: "tradezero", env: "live", credentials: "tradeZero", accountId: "TZ456", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 },
  ],
  gate: {
    global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venue: {},
  },
};

function baseSetup(overrides: Partial<VenueSetup> = {}): VenueSetup {
  return {
    file: runningConfig,
    running: runningConfig,
    credKeys: ["alpaca", "tradeZero"],
    ...overrides,
  };
}

function makeCommands(setupSequence: VenueSetup[], acks: Partial<Record<string, AckMsg>> = {}) {
  let getCalls = 0;
  const sent: Array<{ name: string; args: unknown }> = [];
  const sendCommand = vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
    sent.push({ name, args });
    if (name === "GetVenueSetup") {
      const s = setupSequence[Math.min(getCalls, setupSequence.length - 1)];
      getCalls++;
      return { kind: "ack", corrId: "c", status: "accepted", value: s };
    }
    if (acks[name]) return acks[name] as AckMsg;
    return { kind: "ack", corrId: "c", status: "accepted" };
  });
  return { sendCommand, sent };
}

function wrap(commands: { sendCommand: (name: string, args: unknown) => Promise<AckMsg> }, engineState?: "connecting" | "open" | "reconnecting") {
  return render(
    <ThemeProvider>
      <ToastProvider>
        <VenuesSection commands={commands} engineState={engineState} />
      </ToastProvider>
    </ThemeProvider>,
  );
}

describe("VenuesSection", () => {
  it("shows a LIVE badge on a live venue", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-1")).toBeTruthy());

    const liveHeader = screen.getByTestId("venue-remove-1").parentElement!;
    expect(liveHeader.textContent).toContain("LIVE");
  });

  it("switching an existing live venue's broker to sim clears the LIVE state (setBroker forces env: paper)", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-1")).toBeTruthy());

    const liveHeader = screen.getByTestId("venue-remove-1").parentElement!;
    expect(liveHeader.textContent).toContain("LIVE");

    fireEvent.change(screen.getByTestId("venue-broker-1"), { target: { value: "sim" } });

    const header = screen.getByTestId("venue-remove-1").parentElement!;
    expect(header.textContent).not.toContain("LIVE");
    expect(header.className ?? "").not.toContain("venue-card-header-live");
  });

  it("normalizes a legacy sim venue loaded with env: \"live\" (saved by an older build's manual dropdown) to paper on load, so it never shows LIVE", async () => {
    const legacySimLive: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-legacy", broker: "sim", env: "live", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([legacySimLive]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());

    const header = screen.getByTestId("venue-remove-2").parentElement!;
    expect(header.textContent).not.toContain("LIVE");
    expect(header.className ?? "").not.toContain("venue-card-header-live");
  });

  it("hides the CREDENTIALS group for a sim venue but shows it for tradezero/alpaca/moomoo", async () => {
    const withSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([withSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());

    expect(screen.queryByTestId("venue-cred-keyid-0")).toBeTruthy(); // alpaca
    expect(screen.queryByTestId("venue-cred-keyid-1")).toBeTruthy(); // tradezero
    expect(screen.queryByTestId("venue-cred-keyid-2")).toBeNull();   // sim
  });

  it("mints a real credential name when an existing sim venue (credentials: \"\") switches broker to alpaca, so PutCredential is never saved under an empty name", async () => {
    const withSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([withSim, withSim], { TestConnection: testAck(okResult()) });
    wrap(commands);
    const i = 2;
    await waitFor(() => expect(screen.getByTestId(`venue-id-${i}`)).toBeTruthy());
    expect(screen.queryByTestId(`venue-cred-keyid-${i}`)).toBeNull(); // sim: no CREDENTIALS group yet

    fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "alpaca" } });
    await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());

    fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "new-id" } });
    fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "new-secret" } });
    // A freshly-typed key on a testable broker (alpaca) still requires a
    // passing Test connection before Save is enabled.
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByTestId(`venue-test-${i}`));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const put = commands.sent.find((s) => s.name === "PutCredential" && (s.args as { keyId?: string }).keyId === "new-id");
    expect(put).toBeTruthy();
    expect((put!.args as { name: string }).name).not.toBe("");
  });

  it("hides the restart banner when file == running, and shows it after a save whose re-fetch reports drift", async () => {
    const drifted: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([baseSetup(), drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    expect(screen.queryByTestId("restart-banner")).toBeNull();

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());
  });

  it("restart button requires a second click to confirm, and Cancel backs out without sending RestartEngine", async () => {
    const drifted: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    expect(restartBtn().textContent).toBe("Restart now");

    fireEvent.click(restartBtn());
    expect(restartBtn().textContent).toBe("Confirm restart");
    expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(false);

    fireEvent.click(screen.getByText("Cancel"));
    expect(restartBtn().textContent).toBe("Restart now");
    expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(false);
  });

  it("sends RestartEngine only after confirming, and disables the button while restarting", async () => {
    const drifted: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    fireEvent.click(restartBtn()); // -> "Confirm restart"
    fireEvent.click(restartBtn()); // -> sends RestartEngine

    await waitFor(() => expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(true));
    await waitFor(() => expect(restartBtn().disabled).toBe(true));
    expect(restartBtn().textContent).toBe("Restarting…");
  });

  it("reloads the page once the engine drops and reconnects, without any user action", async () => {
    const drifted: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([drifted]);
    const reload = vi.fn();
    const originalLocation = window.location;
    // jsdom's Location.reload isn't configurable enough for vi.spyOn, so
    // swap the whole window.location property instead; restored below.
    Object.defineProperty(window, "location", { value: { ...originalLocation, reload }, writable: true, configurable: true });
    const { rerender } = wrap(commands, "open");
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    fireEvent.click(restartBtn());
    fireEvent.click(restartBtn());
    await waitFor(() => expect(restartBtn().disabled).toBe(true));

    // Simulate WsClient's own auto-reconnect cycle: the engine restarts
    // (socket drops, then comes back) without any user action here.
    rerender(
      <ThemeProvider><ToastProvider><VenuesSection commands={commands} engineState="reconnecting" /></ToastProvider></ThemeProvider>,
    );
    expect(reload).not.toHaveBeenCalled(); // must wait for the actual drop, not reload while still "open"
    rerender(
      <ThemeProvider><ToastProvider><VenuesSection commands={commands} engineState="open" /></ToastProvider></ThemeProvider>,
    );

    try {
      await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
    } finally {
      Object.defineProperty(window, "location", { value: originalLocation, writable: true, configurable: true });
    }
  });

  it("save order: typing a venue's key+secret fires PutCredential (named after that venue's credentials key), then SetVenueSetup", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()], { TestConnection: testAck(okResult()) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "AKIA-new-id" } });
    fireEvent.change(screen.getByTestId("venue-cred-secret-0"), { target: { value: "super-secret-value" } });
    fireEvent.click(screen.getByTestId("venue-test-0"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const names = commands.sent.map((s) => s.name);
    const putIdx = names.indexOf("PutCredential");
    const setIdx = names.indexOf("SetVenueSetup");
    expect(putIdx).toBeGreaterThanOrEqual(0);
    expect(setIdx).toBeGreaterThan(putIdx);
    expect(commands.sent[putIdx].args).toEqual({ name: "alpaca", keyId: "AKIA-new-id", secretKey: "super-secret-value" });
  });

  it("bails before SetVenueSetup and surfaces the reason when PutCredential is rejected", async () => {
    const commands = makeCommands([baseSetup()], {
      PutCredential: { kind: "ack", corrId: "c", status: "blocked", reason: "bad key format" },
      TestConnection: testAck(okResult()),
    });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "x" } });
    fireEvent.change(screen.getByTestId("venue-cred-secret-0"), { target: { value: "y" } });
    fireEvent.click(screen.getByTestId("venue-test-0"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByTestId("save-venues"));

    await waitFor(() => expect(screen.getByTestId("venues-error").textContent).toBe("bad key format"));
    expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(false);
  });

  it("cleans up (best-effort) the credential of a venue removed from the draft, after a successful save", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-remove-1")).toBeTruthy());

    // two-click confirm removes the tradezero-live venue (credentials: "tradeZero")
    fireEvent.click(screen.getByTestId("venue-remove-1"));
    fireEvent.click(screen.getByTestId("venue-remove-1"));
    expect(screen.queryByTestId("venue-id-1")).toBeNull();

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "DeleteCredential")).toBe(true));
    expect(commands.sent.find((s) => s.name === "DeleteCredential")?.args).toEqual({ name: "tradeZero" });
  });

  it("a two-click remove needs a second click on the same button; one click alone doesn't remove", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-remove-0")).toBeTruthy());

    fireEvent.click(screen.getByTestId("venue-remove-0"));
    // still both venues after one click — the second venue (index 1) is present
    expect(screen.getByTestId("venue-id-1")).toBeTruthy();

    fireEvent.click(screen.getByTestId("venue-remove-0"));
    // index 0 (alpaca-paper) is gone; the former index-1 venue shifts down to
    // index 0, so only one venue card remains.
    expect(screen.queryByTestId("venue-id-1")).toBeNull();
    expect((screen.getByTestId("venue-id-0") as HTMLInputElement).value).toBe("tradezero-live");
  });

  it("clears the masked credential inputs after a save and never renders them pre-filled from a refresh", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()], { TestConnection: testAck(okResult()) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    const keyId = screen.getByTestId("venue-cred-keyid-0") as HTMLInputElement;
    const secret = screen.getByTestId("venue-cred-secret-0") as HTMLInputElement;
    fireEvent.change(keyId, { target: { value: "AKIA-secret-id" } });
    fireEvent.change(secret, { target: { value: "super-secret-value" } });
    expect(keyId.value).toBe("AKIA-secret-id");
    expect(secret.value).toBe("super-secret-value");

    fireEvent.click(screen.getByTestId("venue-test-0"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "PutCredential")).toBe(true));

    await waitFor(() => expect((screen.getByTestId("venue-cred-keyid-0") as HTMLInputElement).value).toBe(""));
    expect((screen.getByTestId("venue-cred-secret-0") as HTMLInputElement).value).toBe("");
  });

  it("renders a blocked SetVenueSetup ack's reason inline", async () => {
    const reason = 'venue "tradezero-live": account id is required for TradeZero';
    const commands = makeCommands([baseSetup()], { SetVenueSetup: { kind: "ack", corrId: "c", status: "blocked", reason } });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(screen.getByTestId("venues-error")).toBeTruthy());
    expect(screen.getByTestId("venues-error").textContent).toBe(reason);
  });

  it("reconciles gate.venue on save: a venue added and saved without touching risk limits gets an all-zero gate entry", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("add-venue")).toBeTruthy());

    fireEvent.click(screen.getByTestId("add-venue"));
    const i = 2;
    fireEvent.change(screen.getByTestId(`venue-id-${i}`), { target: { value: "sim-2" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const gate = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { gate: Gate }).gate;
    expect(gate.venue["sim-2"]).toEqual({ maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 });
  });

  it("reconciles gate.venue on save: renaming a venue with non-zero caps carries them to the new id, dropping the old-id entry", async () => {
    const withCaps: VenueSetup = baseSetup({
      file: {
        ...runningConfig,
        gate: {
          ...runningConfig.gate,
          venue: { "alpaca-paper": { maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 } },
        },
      },
    });
    const commands = makeCommands([withCaps, withCaps]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    fireEvent.change(screen.getByTestId("venue-id-0"), { target: { value: "alpaca-paper-2" } });
    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const gate = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { gate: Gate }).gate;
    expect(gate.venue["alpaca-paper-2"]).toEqual({ maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 });
    expect(gate.venue["alpaca-paper"]).toBeUndefined();
  });

  it("does not clobber another venue's gate.venue caps when a rename collides with that venue's current id", async () => {
    const withCaps: VenueSetup = baseSetup({
      file: {
        ...runningConfig,
        gate: {
          ...runningConfig.gate,
          venue: {
            "alpaca-paper": { maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 },
            "tradezero-live": { maxOrderValue: 1000, maxPositionValue: 4000, maxPositionShares: 50, maxOpenOrders: 1 },
          },
        },
      },
    });
    const commands = makeCommands([withCaps, withCaps]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    // Rename venue 0 (alpaca-paper, caps 5000/20000/100/3) to collide with
    // venue 1's current id (tradezero-live, caps 1000/4000/50/1) — the exact
    // transient state the reviewer flagged, reachable before the "id must be
    // unique" validation blocks Save.
    fireEvent.change(screen.getByTestId("venue-id-0"), { target: { value: "tradezero-live" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true); // dup id blocks Save

    // Risk limits are collapsed by default — expand both rows so the cap
    // inputs are actually in the DOM to query.
    fireEvent.click(screen.getByTestId("venue-limits-toggle-0"));
    fireEvent.click(screen.getByTestId("venue-limits-toggle-1"));

    // Caps are tracked per-row (a stable synthetic key), not by the id shown
    // in the field, so both cards keep displaying their own row's original
    // caps throughout the collision — there is no shared key for a clobber
    // to happen through in the first place.
    const maxOrderInputs = screen.getAllByLabelText("maxOrderValue") as HTMLInputElement[];
    expect(maxOrderInputs[0].value).toBe("5000");
    expect(maxOrderInputs[1].value).toBe("1000");

    // Resolve the collision the "normal" way — give venue 1 a fresh, genuinely
    // free id (its own uncontested rename, not a second edit to venue 0).
    fireEvent.change(screen.getByTestId("venue-id-1"), { target: { value: "tradezero-live-2" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const gate = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { gate: Gate }).gate;
    // Venue 1's caps made it through intact, under its own new id.
    expect(gate.venue["tradezero-live-2"]).toEqual({ maxOrderValue: 1000, maxPositionValue: 4000, maxPositionShares: 50, maxOpenOrders: 1 });
    // Venue 0's caps made it through intact too, under its own (renamed) id —
    // renaming never touched capsByRow, so nothing was lost or swapped.
    expect(gate.venue["tradezero-live"]).toEqual({ maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 });
  });

  it("does not lose or swap caps when the id collision is resolved by continuing to edit the SAME row a second time", async () => {
    // This is the exact second-order sequence that broke round 2's id-keyed
    // migration scheme: a guarded (skipped) collision on the first keystroke,
    // then a second keystroke on the SAME row reading a since-corrupted
    // "old id", which could migrate-and-delete the OTHER venue's real entry.
    const withCaps: VenueSetup = baseSetup({
      file: {
        ...runningConfig,
        gate: {
          ...runningConfig.gate,
          venue: {
            "alpaca-paper": { maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 },
            "tradezero-live": { maxOrderValue: 1000, maxPositionValue: 4000, maxPositionShares: 50, maxOpenOrders: 1 },
          },
        },
      },
    });
    const commands = makeCommands([withCaps, withCaps]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    // Keystroke 1: rename venue 0 (A) to collide with venue 1's (B) current id.
    fireEvent.change(screen.getByTestId("venue-id-0"), { target: { value: "tradezero-live" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

    // Keystroke 2: continue editing venue 0 (A) again — not venue 1 (B) — to a
    // fresh, non-colliding id. Under the old id-keyed migration scheme this
    // second edit on the same row was the trigger for misattributing B's real
    // caps entry to A. Under stable row keys, this edit never touches
    // capsByRow at all.
    fireEvent.change(screen.getByTestId("venue-id-0"), { target: { value: "alpaca-paper-2" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const gate = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { gate: Gate }).gate;
    // B (tradezero-live) never moved and kept its own original caps intact.
    expect(gate.venue["tradezero-live"]).toEqual({ maxOrderValue: 1000, maxPositionValue: 4000, maxPositionShares: 50, maxOpenOrders: 1 });
    // A's original caps followed it to its final id, intact — not lost, not
    // swapped with B's.
    expect(gate.venue["alpaca-paper-2"]).toEqual({ maxOrderValue: 5000, maxPositionValue: 20000, maxPositionShares: 100, maxOpenOrders: 3 });
  });

  it("collapses per-venue risk limits by default; clicking the toggle reveals the cap inputs, and the toggle reports how many caps are set", async () => {
    const withCaps: VenueSetup = baseSetup({
      file: {
        ...runningConfig,
        gate: {
          ...runningConfig.gate,
          venue: {
            "alpaca-paper": { maxOrderValue: 5000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
          },
        },
      },
    });
    const commands = makeCommands([withCaps]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    // Collapsed by default: no cap input in the DOM, but the toggle signals
    // that venue 0 already has a limit set (1) and venue 1 has none.
    expect(screen.queryByLabelText("maxOrderValue")).toBeNull();
    expect(screen.getByTestId("venue-limits-toggle-0").textContent).toContain("Risk limits · 1 set");
    expect(screen.getByTestId("venue-limits-toggle-1").textContent).toContain("Configure risk limits");

    fireEvent.click(screen.getByTestId("venue-limits-toggle-0"));
    const maxOrderInputs = screen.getAllByLabelText("maxOrderValue") as HTMLInputElement[];
    expect(maxOrderInputs).toHaveLength(1); // only venue 0's group expanded
    expect(maxOrderInputs[0].value).toBe("5000");

    // Clicking again collapses it back.
    fireEvent.click(screen.getByTestId("venue-limits-toggle-0"));
    expect(screen.queryByLabelText("maxOrderValue")).toBeNull();
  });

  it("does not crash adding a venue on a fresh install where the engine reports credKeys: null", async () => {
    const emptyConfig: VenueConfig = { venues: [], gate: { global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} } };
    const freshInstall = { file: emptyConfig, running: emptyConfig, credKeys: null } as unknown as VenueSetup;
    const commands = makeCommands([freshInstall]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("add-venue")).toBeTruthy());

    fireEvent.click(screen.getByTestId("add-venue"));

    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());
    expect(screen.getByTestId("venue-broker-0")).toBeTruthy();
  });

  it("shows the starting-balance field only for sim venues, prefilled from the wire value; addVenue() defaults new sim rows to 100000", async () => {
    const withSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 25000, slippageBps: 0, fillLatencyMs: 0 }] },
    });
    const commands = makeCommands([withSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());

    expect(screen.queryByTestId("venue-startingbalance-0")).toBeNull(); // alpaca
    expect(screen.queryByTestId("venue-startingbalance-1")).toBeNull(); // tradezero
    expect((screen.getByTestId("venue-startingbalance-2") as HTMLInputElement).value).toBe("25000");

    fireEvent.click(screen.getByTestId("add-venue"));
    const i = 3;
    await waitFor(() => expect(screen.getByTestId(`venue-id-${i}`)).toBeTruthy());
    expect((screen.getByTestId(`venue-startingbalance-${i}`) as HTMLInputElement).value).toBe("100000");
  });

  it("shows Reset balance for a sim venue that's actually running, but not for alpaca/tradezero", async () => {
    const simVenue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const withRunningSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      running: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
    });
    const commands = makeCommands([withRunningSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());
    expect(screen.getByTestId("venue-reset-2")).toBeTruthy();
    expect(screen.queryByTestId("venue-reset-0")).toBeNull(); // alpaca
    expect(screen.queryByTestId("venue-reset-1")).toBeNull(); // tradezero
  });

  it("hides Reset balance for a sim venue that only exists in the draft, not yet running", async () => {
    const simVenue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const draftOnlySim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      // running unchanged: sim-1 isn't booted yet, just drafted
    });
    const commands = makeCommands([draftOnlySim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());
    expect(screen.queryByTestId("venue-reset-2")).toBeNull();
  });

  it("two-click confirm sends ResetBalance for the right venue and toasts success", async () => {
    const simVenue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const withRunningSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      running: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
    });
    const commands = makeCommands([withRunningSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-reset-2")).toBeTruthy());

    fireEvent.click(screen.getByTestId("venue-reset-2"));
    expect(commands.sent.some((s) => s.name === "ResetBalance")).toBe(false); // first click only arms confirm

    fireEvent.click(screen.getByTestId("venue-reset-2"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "ResetBalance")).toBe(true));
    expect(commands.sent.find((s) => s.name === "ResetBalance")?.args).toEqual({ venue: "sim-1" });
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("sim-1"));
  });

  it("toasts the rejection reason when ResetBalance is blocked", async () => {
    const simVenue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const withRunningSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      running: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
    });
    const commands = makeCommands([withRunningSim], {
      ResetBalance: { kind: "ack", corrId: "c", status: "blocked", reason: "reset balance unsupported on venue" },
    });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-reset-2")).toBeTruthy());

    fireEvent.click(screen.getByTestId("venue-reset-2"));
    fireEvent.click(screen.getByTestId("venue-reset-2"));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("reset balance unsupported"));
  });

  describe("client-side validation disables Save", () => {
    it("blocks on a duplicate venue id", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);

      fireEvent.change(screen.getByTestId("venue-id-1"), { target: { value: "alpaca-paper" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("blocks on an invalid (uppercase) venue id", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

      fireEvent.change(screen.getByTestId("venue-id-0"), { target: { value: "Alpaca Paper" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("blocks a new alpaca venue with no credential key set or typed, and still blocks after typing keys until a Test connection succeeds", async () => {
      const commands = makeCommands([baseSetup()], { TestConnection: testAck(okResult()) });
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("add-venue")).toBeTruthy());

      fireEvent.click(screen.getByTestId("add-venue"));
      const i = 2;
      fireEvent.change(screen.getByTestId(`venue-id-${i}`), { target: { value: "alpaca-2" } });
      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "alpaca" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

      // Typing both key id and secret satisfies the "a credential key exists"
      // rule, but alpaca is a TESTABLE_BROKERS entry — a freshly-typed key
      // still requires a passing Test connection before Save unblocks.
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "id" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "secret" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
    });

    it("blocks on a partially-entered key (key id typed, secret blank)", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

      fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "only-id-typed" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("blocks a tradezero venue with an empty account id, even after a successful Test whose result omits one", async () => {
      // tradezero's account id is now auto-detected (no manual input to blank
      // out directly) — the equivalent real-world case is a fresh tradezero
      // venue whose probe result comes back ok but without an account id;
      // the "account id is required for TradeZero" rule must still block.
      const commands = makeCommands([baseSetup()], {
        TestConnection: testAck({ ok: true, env: "live", accountId: "", accountType: "", message: "connected", accounts: [] }),
      });
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("add-venue")).toBeTruthy());

      fireEvent.click(screen.getByTestId("add-venue"));
      const i = 2;
      fireEvent.change(screen.getByTestId(`venue-id-${i}`), { target: { value: "tz-2" } });
      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "tradezero" } });
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "id" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "secret" } });

      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect(screen.getByTestId(`venue-test-result-${i}`)).toBeTruthy());

      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });
  });

  describe("Test connection", () => {
    it("a successful Test on a tradezero venue fills the read-only env/account-id display and enables Save; Save is disabled before Test", async () => {
      const commands = makeCommands([baseSetup()], {
        TestConnection: testAck({ ok: true, env: "live", accountId: "2TZ001", accountType: "margin", message: "connected", accounts: [] }),
      });
      wrap(commands);
      const i = 1; // tradezero-live
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());

      // tradezero is a testable broker: no manual env select / account-id
      // input, just the auto-detected read-only display.
      expect(screen.queryByTestId(`venue-env-${i}`)).toBeNull();
      expect(screen.queryByTestId(`venue-account-${i}`)).toBeNull();
      expect(screen.getByTestId(`venue-account-detected-${i}`).textContent).toBe("TZ456"); // pre-existing value

      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "new-key" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "new-secret" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      expect((screen.getByTestId(`venue-test-${i}`) as HTMLButtonElement).disabled).toBe(true); // testing state

      await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
      expect(screen.getByTestId(`venue-env-detected-${i}`).textContent).toBe("LIVE");
      expect(screen.getByTestId(`venue-account-detected-${i}`).textContent).toBe("2TZ001");
      // On success the UI builds its own env/accountId summary rather than
      // echoing the probe's raw message.
      expect(screen.getByTestId(`venue-test-result-${i}`).textContent).toBe("✓ Connected · LIVE · 2TZ001");
    });

    it("a TestConnectionResult with more than one account renders a picker; selecting an account sets accountId", async () => {
      const result: TestConnectionResult = {
        ok: true, env: "live", accountId: "", accountType: "", message: "connected",
        accounts: [
          { accountId: "2TZ001", accountType: "margin", env: "live" },
          { accountId: "2TZ002", accountType: "cash", env: "live" },
        ],
      };
      const commands = makeCommands([baseSetup()], { TestConnection: testAck(result) });
      wrap(commands);
      const i = 1;
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "k" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "s" } });
      fireEvent.click(screen.getByTestId(`venue-test-${i}`));

      await waitFor(() => expect(screen.getByTestId(`venue-account-select-${i}`)).toBeTruthy());
      expect(screen.queryByTestId(`venue-account-detected-${i}`)).toBeNull(); // picker replaces the plain-text display

      fireEvent.change(screen.getByTestId(`venue-account-select-${i}`), { target: { value: "2TZ002" } });
      expect((screen.getByTestId(`venue-account-select-${i}`) as HTMLSelectElement).value).toBe("2TZ002");
    });

    it("a failing Test result (ok:false) shows the failure message and leaves Save disabled", async () => {
      const commands = makeCommands([baseSetup()], {
        TestConnection: testAck({ ok: false, env: "", accountId: "", accountType: "", message: "invalid API key", accounts: [] }),
      });
      wrap(commands);
      const i = 1;
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "bad" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "worse" } });

      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect(screen.getByTestId(`venue-test-result-${i}`).textContent).toContain("invalid API key"));
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("a rejected/thrown TestConnection is treated as a failure, same as an ok:false result", async () => {
      const commands = makeCommands([baseSetup()], {
        TestConnection: { kind: "ack", corrId: "c", status: "blocked", reason: "malformed args" },
      });
      wrap(commands);
      const i = 1;
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "k" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "s" } });

      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect(screen.getByTestId(`venue-test-result-${i}`).textContent).toContain("malformed args"));
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("no row (tradezero/alpaca/moomoo/sim) shows an env dropdown; moomoo shows a static LIVE chip instead", async () => {
      // moomoo is loaded with env: "paper" on disk (an older build's manual
      // dropdown could have saved this) — refresh()'s load-path
      // normalization (mirroring the sim case) forces it to "live" in draft,
      // so the static chip below reads LIVE regardless.
      const withMoomooAndSim: VenueSetup = baseSetup({
        file: {
          ...runningConfig,
          venues: [
            ...runningConfig.venues,
            { id: "moomoo-1", broker: "moomoo", env: "paper", credentials: "moomoo", accountId: "MM1", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 },
            { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 },
          ],
        },
        credKeys: ["alpaca", "tradeZero", "moomoo"],
      });
      const commands = makeCommands([withMoomooAndSim]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-id-3")).toBeTruthy());

      expect(screen.queryByTestId("venue-env-0")).toBeNull();       // alpaca
      expect(screen.queryByTestId("venue-account-0")).toBeNull();   // alpaca (no account id at all)
      expect(screen.queryByTestId("venue-env-1")).toBeNull();       // tradezero
      expect(screen.queryByTestId("venue-account-1")).toBeNull();   // tradezero (manual input gone)
      expect(screen.getByTestId("venue-account-detected-1")).toBeTruthy(); // tradezero read-only display

      expect(screen.queryByTestId("venue-env-2")).toBeNull();       // moomoo: no dropdown
      expect(screen.getByTestId("venue-env-live-2").textContent).toBe("LIVE"); // moomoo: static LIVE chip
      expect(screen.getByTestId("venue-account-2")).toBeTruthy();   // moomoo: manual account-id input
      expect(screen.queryByTestId("venue-env-3")).toBeNull();       // sim: no env field at all
      expect(screen.getByTestId("venue-account-3")).toBeTruthy();   // sim keeps manual account-id
    });

    it("switching a row's broker to moomoo forces env: live in the draft", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      const i = 0; // alpaca-paper, env: paper
      await waitFor(() => expect(screen.getByTestId(`venue-id-${i}`)).toBeTruthy());

      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "moomoo" } });

      expect(screen.getByTestId(`venue-env-live-${i}`).textContent).toBe("LIVE");
      const header = screen.getByTestId(`venue-remove-${i}`).parentElement!;
      expect(header.textContent).toContain("LIVE");
    });

    it("normalizes a moomoo venue loaded with env: \"paper\" on disk (saved by an older build's manual dropdown) to live on load, so it never strands as paper", async () => {
      const legacyMoomooPaper: VenueSetup = baseSetup({
        file: { ...runningConfig, venues: [...runningConfig.venues, { id: "moomoo-legacy", broker: "moomoo", env: "paper", credentials: "moomoo", accountId: "MM1", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] },
        credKeys: ["alpaca", "tradeZero", "moomoo"],
      });
      const commands = makeCommands([legacyMoomooPaper]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-id-2")).toBeTruthy());

      const header = screen.getByTestId("venue-remove-2").parentElement!;
      expect(header.textContent).toContain("LIVE");
      expect(screen.getByTestId("venue-env-live-2").textContent).toBe("LIVE");
    });

    it("editing an already-verified row's secret resets its test status; Save becomes disabled again without a fresh Test", async () => {
      const commands = makeCommands([baseSetup()], {
        TestConnection: testAck({ ok: true, env: "live", accountId: "2TZ001", accountType: "margin", message: "connected", accounts: [] }),
      });
      wrap(commands);
      const i = 1;
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "k" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "s" } });
      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "s-changed" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("switching a row's broker invalidates a prior Test result for that row", async () => {
      const commands = makeCommands([baseSetup()], {
        TestConnection: testAck({ ok: true, env: "live", accountId: "2TZ001", accountType: "margin", message: "connected", accounts: [] }),
      });
      wrap(commands);
      const i = 1;
      await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "k" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "s" } });
      fireEvent.click(screen.getByTestId(`venue-test-${i}`));
      await waitFor(() => expect(screen.getByTestId(`venue-test-result-${i}`)).toBeTruthy());

      // Switch away and back — a prior "ok" result for a different broker
      // selection must not still read as verified for the new selection.
      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "moomoo" } });
      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "tradezero" } });
      expect(screen.queryByTestId(`venue-test-result-${i}`)).toBeNull();
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });
  });
});
