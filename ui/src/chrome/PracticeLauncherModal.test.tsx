// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { PracticeLauncherModal } from "./PracticeLauncherModal";
import { ThemeProvider } from "./ThemeProvider";
import type { ReplayCommandAdapter } from "./exec/useReplayCommands";
import type { AckMsg } from "../wire/contract";

function fakeCommands(opts: { days?: string[]; ack?: Partial<AckMsg>; reject?: string } = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands: ReplayCommandAdapter = {
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      sent.push({ name, args });
      if (opts.reject) throw new Error(opts.reject);
      return { kind: "ack", corrId: "c1", status: "accepted", ...opts.ack } as AckMsg;
    }),
    sendQuery: vi.fn(async () => opts.days ?? ["2026-01-02", "2026-01-05"]),
  };
  return { sent, commands };
}

function Wrapped({ open, onClose, commands }: { open: boolean; onClose: () => void; commands: ReplayCommandAdapter }) {
  return (
    <ThemeProvider>
      <PracticeLauncherModal open={open} onClose={onClose} commands={commands} />
    </ThemeProvider>
  );
}

describe("PracticeLauncherModal", () => {
  it("renders nothing when closed", () => {
    const { commands } = fakeCommands();
    render(<Wrapped open={false} onClose={vi.fn()} commands={commands} />);
    expect(screen.queryByTestId("replay-launcher")).toBeNull();
  });

  it("renders both the synthetic demo market and replay-a-recorded-day options", async () => {
    const { commands } = fakeCommands();
    render(<Wrapped open onClose={vi.fn()} commands={commands} />);
    expect(screen.getByTestId("replay-launcher")).toBeTruthy();
    expect(screen.getByText("Synthetic demo market")).toBeTruthy();
    expect(screen.getByTestId("demo-start")).toBeTruthy();
    expect(screen.getByText("Replay a recorded day")).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId("replay-day").textContent).toContain("2026-01-02"));
    expect(screen.getByTestId("replay-speed")).toBeTruthy();
    expect(screen.getByTestId("replay-start")).toBeTruthy();
  });

  it("shows 'No recorded days yet' for replay specifically, while the demo option stays available", async () => {
    const { commands } = fakeCommands({ days: [] });
    render(<Wrapped open onClose={vi.fn()} commands={commands} />);
    await waitFor(() => expect(screen.getByText("No recorded days yet.")).toBeTruthy());
    expect(screen.queryByTestId("replay-day")).toBeNull(); // form hidden, no day to pick
    // The demo path never depends on recorded days at all.
    const demoBtn = screen.getByTestId("demo-start") as HTMLButtonElement;
    expect(demoBtn.disabled).toBe(false);
  });

  it("selecting 'Synthetic demo market' sends StartDemo with empty args and closes on an accepted ack", async () => {
    const { sent, commands } = fakeCommands();
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(sent).toEqual([{ name: "StartDemo", args: {} }]);
  });

  it("StartDemo rejection keeps the modal open with an inline error instead of assuming success", async () => {
    const { commands } = fakeCommands({ ack: { status: "blocked", reason: "demo unavailable" } });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(screen.getByText("demo unavailable")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("a transport failure on StartDemo shows an inline error rather than silently closing", async () => {
    const { commands } = fakeCommands({ reject: "socket down" });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(screen.getByText("socket down")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  // Pre-existing replay flow, preserved unchanged by the unification.
  it("selecting 'Replay a recorded day' still sends StartReplay with the chosen day/speed and closes on accept", async () => {
    const { sent, commands } = fakeCommands();
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    await waitFor(() => expect(screen.getByTestId("replay-day")).toBeTruthy());
    fireEvent.change(screen.getByTestId("replay-day"), { target: { value: "2026-01-05" } });
    fireEvent.change(screen.getByTestId("replay-speed"), { target: { value: "4" } });
    fireEvent.click(screen.getByTestId("replay-start"));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(sent).toEqual([{ name: "StartReplay", args: { day: "2026-01-05", speed: 4 } }]);
  });

  it("StartReplay rejection keeps the modal open with an inline error instead of assuming success", async () => {
    const { commands } = fakeCommands({ ack: { status: "blocked", reason: "already replaying" } });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    await waitFor(() => expect(screen.getByTestId("replay-day")).toBeTruthy());
    fireEvent.click(screen.getByTestId("replay-start"));
    await waitFor(() => expect(screen.getByText("already replaying")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("a transport failure on StartReplay shows an inline error rather than silently closing", async () => {
    const { commands } = fakeCommands({ reject: "socket down" });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    await waitFor(() => expect(screen.getByTestId("replay-day")).toBeTruthy());
    fireEvent.click(screen.getByTestId("replay-start"));
    await waitFor(() => expect(screen.getByText("socket down")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("Start demo market pending blocks Start replay from firing a concurrent command (and vice versa isn't needed to prove the guard)", async () => {
    // Reviewer-flagged race: pending used to be section-scoped, so a click on
    // one section's button while the OTHER section's request was still
    // outstanding was allowed — letting two self-restart-triggering commands
    // (StartDemo/StartReplay) fire concurrently. Reproduce by holding
    // StartDemo's ack unresolved and confirming the replay button goes (and
    // stays) disabled, and that a click on it never reaches sendCommand.
    let resolveDemo!: (ack: AckMsg) => void;
    const sent: Array<{ name: string; args: unknown }> = [];
    const commands: ReplayCommandAdapter = {
      sendCommand: vi.fn((name: string, args: unknown): Promise<AckMsg> => {
        sent.push({ name, args });
        if (name === "StartDemo") {
          return new Promise<AckMsg>((resolve) => { resolveDemo = resolve; });
        }
        return Promise.resolve({ kind: "ack", corrId: "c1", status: "accepted" } as AckMsg);
      }),
      sendQuery: vi.fn(async () => ["2026-01-02", "2026-01-05"]),
    };
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    await waitFor(() => expect(screen.getByTestId("replay-day")).toBeTruthy());

    fireEvent.click(screen.getByTestId("demo-start"));
    // Demo's ack is deliberately left unresolved — pending === "demo" now.
    await waitFor(() => expect((screen.getByTestId("demo-start") as HTMLButtonElement).disabled).toBe(true));

    const replayBtn = screen.getByTestId("replay-start") as HTMLButtonElement;
    expect(replayBtn.disabled).toBe(true); // fixed bug: used to only disable on pending === "replay"
    fireEvent.click(replayBtn);
    expect(sent).toEqual([{ name: "StartDemo", args: {} }]); // StartReplay never sent — button ate the click

    // Resolving the outstanding demo request completes normally and doesn't
    // get clobbered by a second in-flight request that never should've started.
    resolveDemo({ kind: "ack", corrId: "c1", status: "accepted" } as AckMsg);
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(sent).toEqual([{ name: "StartDemo", args: {} }]);
  });

  it("a stale error from a previous attempt does not bleed into the next time the modal opens", async () => {
    const { commands } = fakeCommands({ ack: { status: "blocked", reason: "demo unavailable" } });
    const onClose = vi.fn();
    const { rerender } = render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(screen.getByText("demo unavailable")).toBeTruthy());
    rerender(<Wrapped open={false} onClose={onClose} commands={commands} />);
    rerender(<Wrapped open onClose={onClose} commands={commands} />);
    // listDays() re-fires on reopen (a fresh microtask) — wait for it to
    // settle so the state update lands inside act() before asserting.
    await waitFor(() => expect(screen.getByTestId("replay-day")).toBeTruthy());
    expect(screen.queryByText("demo unavailable")).toBeNull();
  });
});
