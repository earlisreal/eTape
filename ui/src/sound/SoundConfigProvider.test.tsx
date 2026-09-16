// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { SoundConfigProvider, useSoundConfig } from "./SoundConfigProvider";
import { DEFAULT_SOUND_CONFIG } from "./SoundConfig";
import { soundEngine } from "./SoundEngine";

function Probe({ id = "one" }: { id?: string }) {
  const { config, loaded, save } = useSoundConfig();
  return (
    <div>
      <span data-testid={`loaded-${id}`}>{String(loaded)}</span>
      <span data-testid={`fill-${id}`}>{config.fillSound}</span>
      <button data-testid={`save-${id}`} onClick={() => save({ ...config, fillSound: "marimba" })}>save</button>
    </div>
  );
}

afterEach(() => { vi.restoreAllMocks(); });

describe("SoundConfigProvider", () => {
  it("loads config from GetConfig and defaults on a malformed value", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: { fillSound: "marimba", volume: 0.5, enabled: true, placeClick: true, rejectSound: "buzz", scannerSound: "chirp" } })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded-one").textContent).toBe("true"));
    expect(screen.getByTestId("fill-one").textContent).toBe("marimba");
  });

  it("save() writes SetConfig and pushes the config into the engine", async () => {
    const setSpy = vi.spyOn(soundEngine, "setConfig");
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded-one").textContent).toBe("true"));
    act(() => { fireEvent.click(screen.getByTestId("save-one")); });
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
    expect(setSpy).toHaveBeenLastCalledWith(expect.objectContaining({ fillSound: "marimba" }));
  });

  it("reloads a sound change made by another window", async () => {
    let stored = { ...DEFAULT_SOUND_CONFIG };
    const makeCommands = () => ({
      sendCommand: vi.fn(async (name: string, args: unknown) => {
        if (name === "SetConfig") stored = (args as { value: typeof stored }).value;
        return { kind: "ack", corrId: "c", status: "accepted", value: name === "GetConfig" ? stored : undefined };
      }),
    });
    const first = makeCommands();
    const second = makeCommands();
    render(
      <>
        <SoundConfigProvider commands={first as never}><Probe id="one" /></SoundConfigProvider>
        <SoundConfigProvider commands={second as never}><Probe id="two" /></SoundConfigProvider>
      </>,
    );
    await waitFor(() => expect(screen.getByTestId("loaded-two").textContent).toBe("true"));
    fireEvent.click(screen.getByTestId("save-one"));
    await waitFor(() => expect(screen.getByTestId("fill-two").textContent).toBe("marimba"));
  });
});
