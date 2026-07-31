// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { PracticeLauncherModal } from "./PracticeLauncherModal";
import { ThemeProvider } from "./ThemeProvider";
import type { AckMsg } from "../wire/contract";

function fakeCommands(opts: { ack?: Partial<AckMsg>; reject?: string } = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = {
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      sent.push({ name, args });
      if (opts.reject) throw new Error(opts.reject);
      return { kind: "ack", corrId: "c1", status: "accepted", ...opts.ack } as AckMsg;
    }),
  };
  return { sent, commands };
}

function Wrapped({ open, onClose, commands }: { open: boolean; onClose: () => void; commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> } }) {
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

  it("renders synthetic demo launcher", () => {
    const { commands } = fakeCommands();
    render(<Wrapped open onClose={vi.fn()} commands={commands} />);
    expect(screen.getByTestId("replay-launcher")).toBeTruthy();
    expect(screen.getByText("Synthetic demo market")).toBeTruthy();
    expect(screen.getByTestId("demo-start")).toBeTruthy();
  });

  it("sends StartDemo with empty args and closes on accepted ack", async () => {
    const { sent, commands } = fakeCommands();
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(sent).toEqual([{ name: "StartDemo", args: {} }]);
  });

  it("StartDemo rejection keeps modal open with inline error", async () => {
    const { commands } = fakeCommands({ ack: { status: "blocked", reason: "demo unavailable" } });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(screen.getByText("demo unavailable")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("transport failure shows inline error", async () => {
    const { commands } = fakeCommands({ reject: "socket down" });
    const onClose = vi.fn();
    render(<Wrapped open onClose={onClose} commands={commands} />);
    fireEvent.click(screen.getByTestId("demo-start"));
    await waitFor(() => expect(screen.getByText("socket down")).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });
});
