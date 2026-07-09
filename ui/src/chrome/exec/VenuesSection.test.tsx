// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { VenuesSection } from "./VenuesSection";
import type { AckMsg, VenueConfig, VenueSetup } from "../../wire/contract";

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

function makeCommands(setupSequence: VenueSetup[], setVenueSetupAck?: AckMsg) {
  let getCalls = 0;
  const sent: Array<{ name: string; args: unknown }> = [];
  const sendCommand = vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
    sent.push({ name, args });
    if (name === "GetVenueSetup") {
      const s = setupSequence[Math.min(getCalls, setupSequence.length - 1)];
      getCalls++;
      return { kind: "ack", corrId: "c", status: "accepted", value: s };
    }
    if (name === "SetVenueSetup") {
      return setVenueSetupAck ?? { kind: "ack", corrId: "c", status: "accepted" };
    }
    if (name === "PutCredential" || name === "DeleteCredential") {
      return { kind: "ack", corrId: "c", status: "accepted" };
    }
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
  it("shows a LIVE badge on a live venue and disables its auto-arm toggle", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("venue-id-1")).toBeTruthy());

    const liveRow = screen.getByTestId("venue-id-1").closest("div")!;
    expect(liveRow.textContent).toContain("LIVE");
    const autoArm = screen.getByTestId("venue-autoarm-1") as HTMLInputElement;
    expect(autoArm.disabled).toBe(true);

    // the paper venue's toggle stays enabled
    const paperAutoArm = screen.getByTestId("venue-autoarm-0") as HTMLInputElement;
    expect(paperAutoArm.disabled).toBe(false);
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

  it("disables delete on a referenced credential and enables it for an unreferenced one", async () => {
    const setup = baseSetup({ credKeys: ["alpaca", "tradeZero", "unused"] });
    const commands = makeCommands([setup]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("credential-delete-tradeZero")).toBeTruthy());

    const tzDelete = screen.getByTestId("credential-delete-tradeZero") as HTMLButtonElement;
    expect(tzDelete.disabled).toBe(true);
    const unusedDelete = screen.getByTestId("credential-delete-unused") as HTMLButtonElement;
    expect(unusedDelete.disabled).toBe(false);
  });

  it("clears the masked credential inputs after a save and never renders them pre-filled", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("cred-keyid")).toBeTruthy());

    const keyId = screen.getByTestId("cred-keyid") as HTMLInputElement;
    const secret = screen.getByTestId("cred-secret") as HTMLInputElement;
    const name = screen.getByTestId("cred-name") as HTMLInputElement;
    fireEvent.change(name, { target: { value: "newkey" } });
    fireEvent.change(keyId, { target: { value: "AKIA-secret-id" } });
    fireEvent.change(secret, { target: { value: "super-secret-value" } });
    expect(keyId.value).toBe("AKIA-secret-id");
    expect(secret.value).toBe("super-secret-value");

    fireEvent.click(screen.getByTestId("cred-save"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "PutCredential")).toBe(true));

    await waitFor(() => expect((screen.getByTestId("cred-keyid") as HTMLInputElement).value).toBe(""));
    expect((screen.getByTestId("cred-secret") as HTMLInputElement).value).toBe("");

    // The component has no read-back path for secrets — a subsequent refresh
    // fetch must not resurrect the typed values either.
    expect((screen.getByTestId("cred-keyid") as HTMLInputElement).value).toBe("");
    expect((screen.getByTestId("cred-secret") as HTMLInputElement).value).toBe("");
  });

  it("renders a blocked SetVenueSetup ack's reason inline", async () => {
    const reason = 'venue "tradezero-live": live venues cannot auto-arm';
    const commands = makeCommands([baseSetup()], { kind: "ack", corrId: "c", status: "blocked", reason });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(screen.getByTestId("venues-error")).toBeTruthy());
    expect(screen.getByTestId("venues-error").textContent).toBe(reason);
  });
});
