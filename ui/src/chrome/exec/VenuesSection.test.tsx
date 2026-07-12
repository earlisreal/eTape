// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { VenuesSection } from "./VenuesSection";
import { HealthStore } from "../../data/HealthStore";
import { ExecStore } from "../../data/ExecStore";
import { SessionStore } from "../../data/SessionStore";
import type { AckMsg, Gate, Venue, VenueConfig, VenueSetup, TestConnectionResult, ExecStatus, DeltaMsg, SysEvent } from "../../wire/contract";

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
    seed: { moomooAttempted: false },
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

function sessionWith(mode: "pending" | "live" | "replay" | "demo"): SessionStore {
  const s = new SessionStore();
  if (mode !== "pending") s.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } } as never);
  return s;
}

function wrap(commands: { sendCommand: (name: string, args: unknown) => Promise<AckMsg> }, opts: { engineState?: "connecting" | "open" | "reconnecting"; health?: HealthStore; exec?: ExecStore; session?: SessionStore } = {}) {
  return render(
    <ThemeProvider>
      <ToastProvider>
        <VenuesSection commands={commands} engineState={opts.engineState} health={opts.health} exec={opts.exec} session={opts.session} />
      </ToastProvider>
    </ThemeProvider>,
  );
}

function execStatusFor(venues: ExecStatus["venues"]): ExecStatus {
  return { masterArmed: false, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues };
}

describe("VenuesSection — Simulator card", () => {
  it("shows the unconfigured body + Add simulator when no sim venue exists, and appends a default sim venue on click", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("sim-add")).toBeTruthy());

    fireEvent.click(screen.getByTestId("sim-add"));
    await waitFor(() => expect(screen.getByTestId("sim-startingbalance")).toBeTruthy());
    expect((screen.getByTestId("sim-startingbalance") as HTMLInputElement).value).toBe("100000");

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));
    const venues = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { venues: Venue[] }).venues;
    expect(venues.some((v) => v.id === "sim" && v.broker === "sim")).toBe(true);
  });

  it("shows starting balance / slippage / fill latency for a configured sim venue, and Reset balance only when running", async () => {
    const simVenue: Venue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 2, fillLatencyMs: 10 };
    const withRunningSim = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      running: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
    });
    const commands = makeCommands([withRunningSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("sim-startingbalance")).toBeTruthy());

    expect((screen.getByTestId("sim-startingbalance") as HTMLInputElement).value).toBe("50000");
    expect((screen.getByTestId("sim-slippage") as HTMLInputElement).value).toBe("2");
    expect((screen.getByTestId("sim-filllatency") as HTMLInputElement).value).toBe("10");
    expect(screen.getByTestId("sim-reset")).toBeTruthy();
  });

  it("two-click confirm sends ResetBalance for the sim venue and toasts success", async () => {
    const simVenue: Venue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const withRunningSim = baseSetup({
      file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
      running: { ...runningConfig, venues: [...runningConfig.venues, simVenue] },
    });
    const commands = makeCommands([withRunningSim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("sim-reset")).toBeTruthy());

    fireEvent.click(screen.getByTestId("sim-reset"));
    expect(commands.sent.some((s) => s.name === "ResetBalance")).toBe(false);
    fireEvent.click(screen.getByTestId("sim-reset"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "ResetBalance")).toBe(true));
    expect(commands.sent.find((s) => s.name === "ResetBalance")?.args).toEqual({ venue: "sim-1" });
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("sim-1"));
  });

  it("hides Reset balance for a sim venue that only exists in the draft, not yet running", async () => {
    const simVenue: Venue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 50000, slippageBps: 0, fillLatencyMs: 0 };
    const draftOnlySim = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, simVenue] } });
    const commands = makeCommands([draftOnlySim]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("sim-startingbalance")).toBeTruthy());
    expect(screen.queryByTestId("sim-reset")).toBeNull();
  });
});

describe("VenuesSection — moomoo card (state machine)", () => {
  it("waiting: no venue, unattempted, link down — no probe button", async () => {
    const commands = makeCommands([baseSetup()]);
    const health = new HealthStore();
    health.apply({ kind: "snapshot", topic: "sys.health", payload: { links: [{ link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" }] } });
    wrap(commands, { health });
    await waitFor(() => expect(screen.getByTestId("moomoo-body")).toBeTruthy());
    expect(screen.getByTestId("moomoo-body").textContent).toContain("Waiting for OpenD");
    expect(screen.queryByTestId("moomoo-probe")).toBeNull();
  });

  it("probe-ready: no venue, unattempted, link up — probe button shown", async () => {
    const commands = makeCommands([baseSetup()]);
    const health = new HealthStore();
    health.apply({ kind: "snapshot", topic: "sys.health", payload: { links: [{ link: "engine-moomoo", ms: 20, min: 20, avg: 20, max: 20, status: "ok" }] } });
    wrap(commands, { health });
    await waitFor(() => expect(screen.getByTestId("moomoo-probe")).toBeTruthy());
    expect(screen.getByTestId("moomoo-body").textContent).toContain("Waiting for OpenD");
  });

  it("declined: no venue, attempted — shows the no-account copy + re-probe button", async () => {
    const commands = makeCommands([baseSetup({ seed: { moomooAttempted: true } })]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("moomoo-probe")).toBeTruthy());
    expect(screen.getByTestId("moomoo-body").textContent).toContain("No live US-authorized account");
  });

  it("picker: a manual probe with >1 eligible account renders a select + Enable, which adds the venue to the draft", async () => {
    const result: TestConnectionResult = {
      ok: true, env: "live", accountId: "", accountType: "", message: "ok",
      accounts: [{ accountId: "1001", accountType: "margin", env: "live" }, { accountId: "1002", accountType: "cash", env: "live" }],
    };
    const commands = makeCommands([baseSetup({ seed: { moomooAttempted: true } })], { TestConnection: testAck(result) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("moomoo-probe")).toBeTruthy());
    fireEvent.click(screen.getByTestId("moomoo-probe"));
    await waitFor(() => expect(screen.getByTestId("moomoo-account-select")).toBeTruthy());

    fireEvent.change(screen.getByTestId("moomoo-account-select"), { target: { value: "1002" } });
    fireEvent.click(screen.getByTestId("moomoo-enable"));
    await waitFor(() => expect(screen.getByTestId("moomoo-account")).toBeTruthy());
    expect(screen.getByTestId("moomoo-account").textContent).toBe("1002");
    // Only in-draft — the explicit Save button still has to be clicked to persist.
    expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(false);
  });

  it("a manual probe with exactly one eligible account pre-fills the account but still requires the explicit Enable click", async () => {
    const result: TestConnectionResult = { ok: true, env: "live", accountId: "2001", accountType: "margin", message: "ok", accounts: [{ accountId: "2001", accountType: "margin", env: "live" }] };
    const commands = makeCommands([baseSetup({ seed: { moomooAttempted: true } })], { TestConnection: testAck(result) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("moomoo-probe")).toBeTruthy());
    fireEvent.click(screen.getByTestId("moomoo-probe"));
    await waitFor(() => expect(screen.getByTestId("moomoo-enable")).toBeTruthy());
    expect(screen.queryByTestId("moomoo-account-select")).toBeNull(); // no picker needed for a single account
    expect(screen.queryByTestId("moomoo-account")).toBeNull(); // not yet enabled

    fireEvent.click(screen.getByTestId("moomoo-enable"));
    await waitFor(() => expect(screen.getByTestId("moomoo-account").textContent).toBe("2001"));
  });

  it("pending restart: file venue exists, not running — shows the auto-configured badge, no connection chip", async () => {
    const moomooVenue: Venue = { id: "moomoo", broker: "moomoo", env: "live", credentials: "", accountId: "3001", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 };
    const setup = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, moomooVenue] }, seed: { moomooAttempted: true } });
    const commands = makeCommands([setup]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("moomoo-account")).toBeTruthy());
    expect(screen.getByTestId("moomoo-account").textContent).toBe("3001");
    expect(screen.getByTestId("moomoo-badge-pending")).toBeTruthy();
    expect(screen.queryByTestId("moomoo-connection-chip")).toBeNull();
    expect(screen.getByTestId("moomoo-caveat")).toBeTruthy();
  });

  it("configured: venue running — shows the connection chip from VenueStatus.connected, and the note on disconnect", async () => {
    const moomooVenue: Venue = { id: "moomoo", broker: "moomoo", env: "live", credentials: "", accountId: "3001", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 };
    const cfg = { ...runningConfig, venues: [...runningConfig.venues, moomooVenue] };
    const setup = baseSetup({ file: cfg, running: cfg, seed: { moomooAttempted: true } });
    const commands = makeCommands([setup]);
    const exec = new ExecStore();
    exec.apply({ kind: "snapshot", topic: "exec.status", payload: execStatusFor([{ venue: "moomoo", broker: "moomoo", connected: false, reconcilePending: false, note: "OpenD unreachable", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }]) });
    wrap(commands, { exec });
    await waitFor(() => expect(screen.getByTestId("moomoo-connection-chip")).toBeTruthy());
    expect(screen.getByTestId("moomoo-connection-chip").textContent).toBe("Disconnected");
    expect(screen.getByText("OpenD unreachable")).toBeTruthy();
    expect(screen.queryByTestId("moomoo-badge-pending")).toBeNull();
  });

  it("Remove is a two-click confirm and drops the venue back to the declined state (marker persists)", async () => {
    const moomooVenue: Venue = { id: "moomoo", broker: "moomoo", env: "live", credentials: "", accountId: "3001", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 };
    const setup = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, moomooVenue] }, seed: { moomooAttempted: true } });
    const commands = makeCommands([setup]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("moomoo-remove")).toBeTruthy());

    fireEvent.click(screen.getByTestId("moomoo-remove"));
    expect(screen.getByTestId("moomoo-account")).toBeTruthy(); // still configured after one click
    fireEvent.click(screen.getByTestId("moomoo-remove"));
    await waitFor(() => expect(screen.queryByTestId("moomoo-account")).toBeNull());
    expect(screen.getByTestId("moomoo-body").textContent).toContain("No live US-authorized account");
  });
});

describe("VenuesSection — Alpaca card (two independent slots)", () => {
  it("Paper and Live are independent: filling Paper's key doesn't touch Live, and vice versa", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "paper-id" } });
    expect((screen.getByTestId("alpaca-live-keyid") as HTMLInputElement).value).toBe("");
  });

  it("filling both slots and passing Test on each creates BOTH venues on Save", async () => {
    const alpacaFreeConfig = { ...runningConfig, venues: runningConfig.venues.filter((v) => v.broker !== "alpaca") };
    const emptyAlpaca = baseSetup({ file: alpacaFreeConfig, running: alpacaFreeConfig });
    const sent: Array<{ name: string; args: unknown }> = [];
    const sendCommand = vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      sent.push({ name, args });
      if (name === "GetVenueSetup") return { kind: "ack", corrId: "c", status: "accepted", value: emptyAlpaca };
      if (name === "TestConnection") {
        const env = (args as { env: string }).env;
        return testAck(okResult({ env, accountId: env === "paper" ? "PA999" : "LA111" }));
      }
      return { kind: "ack", corrId: "c", status: "accepted" };
    });
    const commands = { sendCommand, sent };
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "paper-id" } });
    fireEvent.change(screen.getByTestId("alpaca-paper-secret"), { target: { value: "paper-secret" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-test-result")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-live-keyid"), { target: { value: "live-id" } });
    fireEvent.change(screen.getByTestId("alpaca-live-secret"), { target: { value: "live-secret" } });
    fireEvent.click(screen.getByTestId("alpaca-live-test"));
    await waitFor(() => expect(screen.getByTestId("alpaca-live-test-result")).toBeTruthy());

    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const venues = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { venues: Venue[] }).venues;
    expect(venues.find((v) => v.id === "alpaca")?.broker).toBe("alpaca");
    expect(venues.find((v) => v.id === "alpaca-live")?.broker).toBe("alpaca");
    const puts = commands.sent.filter((s) => s.name === "PutCredential").map((s) => s.args as { keyId: string });
    expect(puts.some((p) => p.keyId === "paper-id")).toBe(true);
    expect(puts.some((p) => p.keyId === "live-id")).toBe(true);
  });

  it("a live key typed into the paper slot is rejected with the cross-slot mismatch error", async () => {
    const emptyAlpaca = baseSetup({ file: { ...runningConfig, venues: runningConfig.venues.filter((v) => v.broker !== "alpaca") } });
    const commands = makeCommands([emptyAlpaca], { TestConnection: testAck(okResult({ env: "live", accountId: "L1" })) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "live-id" } });
    fireEvent.change(screen.getByTestId("alpaca-paper-secret"), { target: { value: "live-secret" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-test-result").textContent).toContain("belongs to a live account"));
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows chip-set 'Key saved' for a configured slot, and Remove is a two-click confirm", async () => {
    const commands = makeCommands([baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-chip")).toBeTruthy());
    expect(screen.getByTestId("alpaca-paper-chip").textContent).toBe("Key saved");

    fireEvent.click(screen.getByTestId("alpaca-paper-remove"));
    expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy();
    fireEvent.click(screen.getByTestId("alpaca-paper-remove"));
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-chip").textContent).toBe("no key"));
  });
});

describe("VenuesSection — TradeZero card", () => {
  it("a successful Test fills the read-only account display and enables Save", async () => {
    const commands = makeCommands([baseSetup()], { TestConnection: testAck({ ok: true, env: "live", accountId: "2TZ001", accountType: "margin", message: "connected", accounts: [] }) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("tz-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("tz-keyid"), { target: { value: "new-key" } });
    fireEvent.change(screen.getByTestId("tz-secret"), { target: { value: "new-secret" } });
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByTestId("tz-test"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
    expect(screen.getByTestId("tz-account-detected").textContent).toBe("2TZ001");
  });

  it("a multi-account result renders a picker; selecting an account sets accountId", async () => {
    const result: TestConnectionResult = {
      ok: true, env: "live", accountId: "", accountType: "", message: "connected",
      accounts: [{ accountId: "2TZ001", accountType: "margin", env: "live" }, { accountId: "2TZ002", accountType: "cash", env: "live" }],
    };
    const commands = makeCommands([baseSetup()], { TestConnection: testAck(result) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("tz-keyid")).toBeTruthy());
    fireEvent.change(screen.getByTestId("tz-keyid"), { target: { value: "k" } });
    fireEvent.change(screen.getByTestId("tz-secret"), { target: { value: "s" } });
    fireEvent.click(screen.getByTestId("tz-test"));

    await waitFor(() => expect(screen.getByTestId("tz-account-select")).toBeTruthy());
    fireEvent.change(screen.getByTestId("tz-account-select"), { target: { value: "2TZ002" } });
    expect((screen.getByTestId("tz-account-select") as HTMLSelectElement).value).toBe("2TZ002");
  });

  it("blocks a fresh (pending) tradezero slot whose successful test comes back with no account id", async () => {
    const noTz = baseSetup({ file: { ...runningConfig, venues: runningConfig.venues.filter((v) => v.broker !== "tradezero") } });
    const commands = makeCommands([noTz], { TestConnection: testAck({ ok: true, env: "live", accountId: "", accountType: "", message: "connected", accounts: [] }) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("tz-keyid")).toBeTruthy());
    fireEvent.change(screen.getByTestId("tz-keyid"), { target: { value: "k" } });
    fireEvent.change(screen.getByTestId("tz-secret"), { target: { value: "s" } });
    fireEvent.click(screen.getByTestId("tz-test"));
    await waitFor(() => expect(screen.getByTestId("tz-test-result")).toBeTruthy());
    expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("VenuesSection — legacy overflow (launch blocker)", () => {
  it("claims the first venue per (broker, env) slot and renders extra venues read-only in Other venues, round-tripping them byte-for-byte on Save", async () => {
    const firstSim: Venue = { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 100000, slippageBps: 0, fillLatencyMs: 0 };
    const legacyTz2: Venue = { id: "tz-backup", broker: "tradezero", env: "paper", credentials: "tz-backup-cred", accountId: "9999", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 };
    const legacySim2: Venue = { id: "sim-legacy", broker: "sim", env: "live", credentials: "", accountId: "", startingBalance: 7777, slippageBps: 3, fillLatencyMs: 5 };
    const cfg: VenueConfig = {
      ...runningConfig,
      venues: [...runningConfig.venues, firstSim, legacyTz2, legacySim2],
      gate: { ...runningConfig.gate, venue: { "tz-backup": { maxOrderValue: 111, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } } },
    };
    const setup = baseSetup({ file: cfg, credKeys: ["alpaca", "tradeZero", "tz-backup-cred"] });
    const commands = makeCommands([setup]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("other-venues")).toBeTruthy());

    // The first sim venue (sim-1) and first tradezero venue (tradezero-live)
    // are claimed by their cards; tz-backup and the second (legacy, live-env)
    // sim venue are overflow.
    expect(screen.getByTestId("other-venue-3")).toBeTruthy();
    expect(screen.getByTestId("other-venue-4")).toBeTruthy();
    expect(screen.getByTestId("other-venue-3").textContent).toContain("tz-backup");
    expect(screen.getByTestId("other-venue-4").textContent).toContain("sim-legacy");
    // The overflow sim venue's env is NOT force-normalized to paper — only
    // the roster-claimed slot gets that treatment; nothing has a card for
    // this second sim venue, so its literal disk value must survive as-is.
    expect(screen.getByTestId("other-venue-4").textContent).toContain("LIVE");

    // Edit the roster-claimed TradeZero slot's risk limits without touching
    // the overflow venues at all, then Save — the overflow venues (and their
    // gate entries) must come back byte-for-byte.
    fireEvent.click(screen.getByTestId("tz-limits-toggle"));
    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const args = commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { venues: Venue[]; gate: Gate };
    expect(args.venues.find((v) => v.id === "tz-backup")).toEqual(legacyTz2);
    expect(args.venues.find((v) => v.id === "sim-legacy")).toEqual(legacySim2);
    expect(args.gate.venue["tz-backup"]).toEqual({ maxOrderValue: 111, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 });
  });

  it("removing an overflow venue only removes that one, leaving the roster and other overflow venues untouched", async () => {
    const legacy: Venue = { id: "old-thing", broker: "tradezero", env: "paper", credentials: "old-cred", accountId: "1", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 };
    const setup = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, legacy] }, credKeys: ["alpaca", "tradeZero", "old-cred"] });
    const commands = makeCommands([setup]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("other-venue-remove-2")).toBeTruthy());

    fireEvent.click(screen.getByTestId("other-venue-remove-2"));
    fireEvent.click(screen.getByTestId("other-venue-remove-2"));
    await waitFor(() => expect(screen.queryByTestId("other-venues")).toBeNull());
    expect(screen.getByTestId("tz-account-detected").textContent).toBe("TZ456"); // roster slot untouched
  });
});

describe("VenuesSection — restart banner / restart flow (unchanged mechanics)", () => {
  it("hides the restart banner when file == running, and shows it after a save whose re-fetch reports drift", async () => {
    const drifted = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] } });
    const commands = makeCommands([baseSetup(), drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());
    expect(screen.queryByTestId("restart-banner")).toBeNull();

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());
  });

  it("suppresses the restart banner while in demo mode, even though file != running by construction", async () => {
    // Demo mode boots a synthetic in-memory venue config (see main.go's -demo
    // override) and never touches config.toml, so file/running diverge on
    // every demo boot regardless of whether the user edited Settings. The
    // banner would otherwise be permanently (and wrongly) stuck on.
    const drifted = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] } });
    const commands = makeCommands([drifted]);
    wrap(commands, { session: sessionWith("demo") });
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());
    expect(screen.queryByTestId("restart-banner")).toBeNull();
  });

  it("restart button requires a second click to confirm, and the ~3s confirm timeout backs out without sending RestartEngine", async () => {
    const drifted = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] } });
    const commands = makeCommands([drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    expect(restartBtn().textContent).toBe("Restart now");

    vi.useFakeTimers();
    try {
      fireEvent.click(restartBtn());
      expect(restartBtn().textContent).toBe("Confirm restart");
      expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(false);
      act(() => { vi.advanceTimersByTime(3000); });
      expect(restartBtn().textContent).toBe("Restart now");
    } finally {
      vi.useRealTimers();
    }
    expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(false);
  });

  it("sends RestartEngine only after confirming, and disables the button while restarting", async () => {
    const drifted = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] } });
    const commands = makeCommands([drifted]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    fireEvent.click(restartBtn());
    fireEvent.click(restartBtn());

    await waitFor(() => expect(commands.sent.some((s) => s.name === "RestartEngine")).toBe(true));
    await waitFor(() => expect(restartBtn().disabled).toBe(true));
    expect(restartBtn().textContent).toBe("Restarting…");
  });

  it("reloads the page once the engine drops and reconnects, without any user action", async () => {
    const drifted = baseSetup({ file: { ...runningConfig, venues: [...runningConfig.venues, { id: "sim-1", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }] } });
    const commands = makeCommands([drifted]);
    const reload = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", { value: { ...originalLocation, reload }, writable: true, configurable: true });
    const { rerender } = wrap(commands, { engineState: "open" });
    await waitFor(() => expect(screen.getByTestId("restart-banner")).toBeTruthy());

    const restartBtn = () => screen.getByTestId("restart-engine") as HTMLButtonElement;
    fireEvent.click(restartBtn());
    fireEvent.click(restartBtn());
    await waitFor(() => expect(restartBtn().disabled).toBe(true));

    rerender(<ThemeProvider><ToastProvider><VenuesSection commands={commands} engineState="reconnecting" /></ToastProvider></ThemeProvider>);
    expect(reload).not.toHaveBeenCalled();
    rerender(<ThemeProvider><ToastProvider><VenuesSection commands={commands} engineState="open" /></ToastProvider></ThemeProvider>);

    try {
      await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
    } finally {
      Object.defineProperty(window, "location", { value: originalLocation, writable: true, configurable: true });
    }
  });
});

describe("VenuesSection — save mechanics", () => {
  it("save order: typing an existing venue's key+secret fires PutCredential (named after its credentials key), then SetVenueSetup", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()], { TestConnection: testAck(okResult()) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "AKIA-new-id" } });
    fireEvent.change(screen.getByTestId("alpaca-paper-secret"), { target: { value: "super-secret-value" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
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
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "x" } });
    fireEvent.change(screen.getByTestId("alpaca-paper-secret"), { target: { value: "y" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByTestId("save-venues"));

    await waitFor(() => expect(screen.getByTestId("venues-error").textContent).toBe("bad key format"));
    expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(false);
  });

  it("cleans up (best-effort) the credential of a venue removed from the draft, after a successful save", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("tz-keyid")).toBeTruthy());

    fireEvent.click(screen.getByTestId("tz-remove"));
    fireEvent.click(screen.getByTestId("tz-remove"));
    await waitFor(() => expect(screen.queryByTestId("tz-account-detected")).toBeNull());

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "DeleteCredential")).toBe(true));
    expect(commands.sent.find((s) => s.name === "DeleteCredential")?.args).toEqual({ name: "tradeZero" });
  });

  it("clears the masked credential inputs after a save and never renders them pre-filled from a refresh", async () => {
    const commands = makeCommands([baseSetup(), baseSetup()], { TestConnection: testAck(okResult()) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    const keyId = screen.getByTestId("alpaca-paper-keyid") as HTMLInputElement;
    const secret = screen.getByTestId("alpaca-paper-secret") as HTMLInputElement;
    fireEvent.change(keyId, { target: { value: "AKIA-secret-id" } });
    fireEvent.change(secret, { target: { value: "super-secret-value" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "PutCredential")).toBe(true));

    await waitFor(() => expect((screen.getByTestId("alpaca-paper-keyid") as HTMLInputElement).value).toBe(""));
    expect((screen.getByTestId("alpaca-paper-secret") as HTMLInputElement).value).toBe("");
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

  it("reconciles gate.venue on save: a newly-materialized Alpaca-paper venue gets an all-zero gate entry", async () => {
    const emptyAlpaca = baseSetup({ file: { ...runningConfig, venues: runningConfig.venues.filter((v) => v.broker !== "alpaca") } });
    const commands = makeCommands([emptyAlpaca], { TestConnection: testAck(okResult()) });
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "k" } });
    fireEvent.change(screen.getByTestId("alpaca-paper-secret"), { target: { value: "s" } });
    fireEvent.click(screen.getByTestId("alpaca-paper-test"));
    await waitFor(() => expect((screen.getByTestId("save-venues") as HTMLButtonElement).disabled).toBe(false));

    fireEvent.click(screen.getByTestId("save-venues"));
    await waitFor(() => expect(commands.sent.some((s) => s.name === "SetVenueSetup")).toBe(true));

    const gate = (commands.sent.find((s) => s.name === "SetVenueSetup")!.args as { gate: Gate }).gate;
    expect(gate.venue["alpaca"]).toEqual({ maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 });
  });

  it("collapses per-venue risk limits by default; the toggle reports how many caps are set", async () => {
    const withCaps = baseSetup({
      file: { ...runningConfig, gate: { ...runningConfig.gate, venue: { "tradezero-live": { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } } } },
    });
    const commands = makeCommands([withCaps]);
    wrap(commands);
    await waitFor(() => expect(screen.getByTestId("tz-limits-toggle")).toBeTruthy());

    expect(screen.queryByLabelText("maxOrderValue")).toBeNull();
    expect(screen.getByTestId("tz-limits-toggle").textContent).toContain("Risk limits · 1 set");

    fireEvent.click(screen.getByTestId("tz-limits-toggle"));
    expect((screen.getByLabelText("maxOrderValue") as HTMLInputElement).value).toBe("1000");
    fireEvent.click(screen.getByTestId("tz-limits-toggle"));
    expect(screen.queryByLabelText("maxOrderValue")).toBeNull();
  });
});

describe("VenuesSection — stale-draft reload guard", () => {
  function pushVenueSeeded(health: HealthStore, seq: number): void {
    const msg: DeltaMsg = { kind: "delta", topic: "sys.events", payload: { seq, ts: "2026-07-12T00:00:00Z", kind: "venue.seeded", detail: "moomoo venue configured", level: "info" } as SysEvent };
    health.apply(msg);
  }

  it("silently refreshes on a fresh venue.seeded event when there are no unsaved edits", async () => {
    const commands = makeCommands([baseSetup(), baseSetup({ seed: { moomooAttempted: true } })]);
    const health = new HealthStore();
    wrap(commands, { health });
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());
    // Baseline established (no reaction) — GetVenueSetup only called once so far.
    expect(commands.sendCommand).toHaveBeenCalledTimes(1);

    act(() => pushVenueSeeded(health, 1));
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId("stale-draft-banner")).toBeNull();
  });

  it("shows the reload banner instead of silently refreshing when there are unsaved edits", async () => {
    const commands = makeCommands([baseSetup()]);
    const health = new HealthStore();
    wrap(commands, { health });
    await waitFor(() => expect(screen.getByTestId("alpaca-paper-keyid")).toBeTruthy());

    fireEvent.change(screen.getByTestId("alpaca-paper-keyid"), { target: { value: "typed-but-unsaved" } });

    act(() => pushVenueSeeded(health, 1));
    await waitFor(() => expect(screen.getByTestId("stale-draft-banner")).toBeTruthy());
    expect((screen.getByTestId("alpaca-paper-keyid") as HTMLInputElement).value).toBe("typed-but-unsaved"); // edit preserved, not clobbered

    fireEvent.click(screen.getByTestId("stale-draft-reload"));
    await waitFor(() => expect(screen.queryByTestId("stale-draft-banner")).toBeNull());
  });

  it("does not react to replayed history already present at mount", async () => {
    const commands = makeCommands([baseSetup()]);
    const health = new HealthStore();
    // Replay a snapshot BEFORE mount, as the hub would on (re)subscribe.
    health.apply({ kind: "snapshot", topic: "sys.events", payload: [{ seq: 1, ts: "t", kind: "venue.seeded", detail: "old", level: "info" }] });
    wrap(commands, { health });
    await waitFor(() => expect(screen.getByTestId("save-venues")).toBeTruthy());
    expect(commands.sendCommand).toHaveBeenCalledTimes(1); // no extra refresh from the pre-existing history
  });
});
