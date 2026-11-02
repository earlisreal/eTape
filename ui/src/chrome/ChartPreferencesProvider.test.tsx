// @vitest-environment jsdom
import { it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { ChartPreferencesProvider, useChartPreferences } from "./ChartPreferencesProvider";

function Probe({ id }: { id: string }) {
  const { config, save } = useChartPreferences();
  return <button onClick={() => save({ volumeOverlap: !config.volumeOverlap })}>
    {id}:{String(config.volumeOverlap)}
  </button>;
}

it("loads and shares the saved chart default across workspaces", async () => {
  let stored: unknown = { volumeOverlap: false };
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown) => {
    if (name === "SetConfig") stored = (args as { value: unknown }).value;
    return { status: "accepted", value: stored };
  }) };
  render(<>
    <ChartPreferencesProvider commands={commands}><Probe id="one" /></ChartPreferencesProvider>
    <ChartPreferencesProvider commands={commands}><Probe id="two" /></ChartPreferencesProvider>
  </>);
  await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledTimes(2));
  fireEvent.click(screen.getByText("one:false"));
  await waitFor(() => expect(screen.getByText("two:true")).toBeTruthy());
  expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "chartPreferences", value: { volumeOverlap: true } });
});

it.each([undefined, null, [], { volumeOverlap: "true" }])("defaults invalid config %j to off", async (value) => {
  const commands = { sendCommand: vi.fn(async () => ({ status: "accepted", value })) };
  render(<ChartPreferencesProvider commands={commands}><Probe id="invalid" /></ChartPreferencesProvider>);
  await act(async () => {});
  expect(screen.getByText("invalid:false")).toBeTruthy();
});

it("does not let a delayed initial load undo a new choice", async () => {
  let finish!: (ack: { status: string; value: unknown }) => void;
  const commands = { sendCommand: vi.fn((name: string) => name === "GetConfig"
    ? new Promise<{ status: string; value: unknown }>((resolve) => { finish = resolve; })
    : Promise.resolve({ status: "accepted", value: undefined })) };
  render(<ChartPreferencesProvider commands={commands}><Probe id="late" /></ChartPreferencesProvider>);
  fireEvent.click(screen.getByText("late:false"));
  await act(async () => { finish({ status: "accepted", value: { volumeOverlap: false } }); });
  expect(screen.getByText("late:true")).toBeTruthy();
});
