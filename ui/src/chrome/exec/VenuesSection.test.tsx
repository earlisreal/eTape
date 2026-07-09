// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { VenuesSection } from "./VenuesSection";
import type { AckMsg, Gate, VenueConfig, VenueSetup } from "../../wire/contract";

const runningConfig: VenueConfig = {
  venues: [
    { id: "alpaca-paper", broker: "alpaca", env: "paper", credentials: "alpaca", accountId: "PA123", autoArm: true },
    { id: "tradezero-live", broker: "tradezero", env: "live", credentials: "tradeZero", accountId: "TZ456", autoArm: false },
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

function wrap(commands: { sendCommand: (name: string, args: unknown) => Promise<AckMsg> }) {
  return render(
    <ThemeProvider>
      <ToastProvider>
        <VenuesSection commands={commands} />
      </ToastProvider>
    </ThemeProvider>,
  );
}

describe("VenuesSection", () => {
  it("shows a LIVE badge on a live venue and disables (and forces off) its auto-arm toggle", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-1")).toBeTruthy());

    const liveHeader = screen.getByTestId("venue-remove-1").parentElement!;
    expect(liveHeader.textContent).toContain("LIVE");
    const liveAutoArm = screen.getByTestId("venue-autoarm-1") as HTMLInputElement;
    expect(liveAutoArm.disabled).toBe(true);
    expect(liveAutoArm.checked).toBe(false);

    // the paper venue's toggle stays enabled and reflects its stored value
    const paperAutoArm = screen.getByTestId("venue-autoarm-0") as HTMLInputElement;
    expect(paperAutoArm.disabled).toBe(false);
    expect(paperAutoArm.checked).toBe(true);
  });

  it("hides the CREDENTIALS group for a sim venue but shows it for tradezero/alpaca/moomoo", async () => {
    const withSim: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", autoArm: false }] },
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
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", autoArm: false }] },
    });
    const commands = makeCommands([withSim, withSim]);
    wrap(commands);
    const i = 2;
    await waitFor(() => expect(screen.getByTestId(`venue-id-${i}`)).toBeTruthy());
    expect(screen.queryByTestId(`venue-cred-keyid-${i}`)).toBeNull(); // sim: no CREDENTIALS group yet

    fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "alpaca" } });
    await waitFor(() => expect(screen.getByTestId(`venue-cred-keyid-${i}`)).toBeTruthy());

    fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "new-id" } });
    fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "new-secret" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const put = commands.sent.find((s) => s.name === "PutCredential" && (s.args as { keyId?: string }).keyId === "new-id");
    expect(put).toBeTruthy();
    expect((put!.args as { name: string }).name).not.toBe("");
  });

  it("hides the restart banner when file == running, and shows it after a save whose re-fetch reports drift", async () => {
    const drifted: VenueSetup = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", autoArm: false }] },
    });
    const commands = makeCommands([baseSetup(), drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-0")).toBeTruthy());

    expect(screen.queryByTestId("restart-banner")).toBeNull();

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());
  });

  it("save order: typing a venue's key+secret fires PutCredential (named after that venue's credentials key), then SetVenueSetup", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "AKIA-new-id" } });
    fireEvent.change(screen.getByTestId("venue-cred-secret-0"), { target: { value: "super-secret-value" } });

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
    const commands = makeCommands([baseSetup()], { PutCredential: { kind: "ack", corrId: "c", status: "blocked", reason: "bad key format" } });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "x" } });
    fireEvent.change(screen.getByTestId("venue-cred-secret-0"), { target: { value: "y" } });
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
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

    const keyId = screen.getByTestId("venue-cred-keyid-0") as HTMLInputElement;
    const secret = screen.getByTestId("venue-cred-secret-0") as HTMLInputElement;
    fireEvent.change(keyId, { target: { value: "AKIA-secret-id" } });
    fireEvent.change(secret, { target: { value: "super-secret-value" } });
    expect(keyId.value).toBe("AKIA-secret-id");
    expect(secret.value).toBe("super-secret-value");

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "PutCredential")).toBe(true));

    await waitFor(() => expect((screen.getByTestId("venue-cred-keyid-0") as HTMLInputElement).value).toBe(""));
    expect((screen.getByTestId("venue-cred-secret-0") as HTMLInputElement).value).toBe("");
  });

  it("renders a blocked SetVenueSetup ack's reason inline", async () => {
    const reason = 'venue "tradezero-live": live venues cannot auto-arm';
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

    it("blocks a new alpaca venue with no credential key set or typed", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("add-venue")).toBeTruthy());

      fireEvent.click(screen.getByTestId("add-venue"));
      const i = 2;
      fireEvent.change(screen.getByTestId(`venue-id-${i}`), { target: { value: "alpaca-2" } });
      fireEvent.change(screen.getByTestId(`venue-broker-${i}`), { target: { value: "alpaca" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

      // typing both key id and secret this session satisfies the rule
      fireEvent.change(screen.getByTestId(`venue-cred-keyid-${i}`), { target: { value: "id" } });
      fireEvent.change(screen.getByTestId(`venue-cred-secret-${i}`), { target: { value: "secret" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false);
    });

    it("blocks on a partially-entered key (key id typed, secret blank)", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-cred-keyid-0")).toBeTruthy());

      fireEvent.change(screen.getByTestId("venue-cred-keyid-0"), { target: { value: "only-id-typed" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });

    it("blocks a tradezero venue with an empty account id", async () => {
      const commands = makeCommands([baseSetup()]);
      wrap(commands);
      await waitFor(() => expect(screen.getByTestId("venue-account-1")).toBeTruthy());

      fireEvent.change(screen.getByTestId("venue-account-1"), { target: { value: "" } });
      expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
    });
  });
});
