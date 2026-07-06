// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { SoundConfigProvider, useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";

function Probe() {
  const { config, loaded, save } = useSoundConfig();
  return (
    <div>
      <span data-testid="loaded">{String(loaded)}</span>
      <span data-testid="fill">{config.fillSound}</span>
      <button data-testid="save" onClick={() => save({ ...config, fillSound: "marimba" })}>save</button>
    </div>
  );
}

afterEach(() => { vi.restoreAllMocks(); });

describe("SoundConfigProvider", () => {
  it("loads config from GetConfig and defaults on a malformed value", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: { fillSound: "marimba", volume: 0.5, enabled: true, placeClick: true, rejectSound: "buzz", scannerSound: "chirp" } })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded").textContent).toBe("true"));
    expect(screen.getByTestId("fill").textContent).toBe("marimba");
  });

  it("save() writes SetConfig and pushes the config into the engine", async () => {
    const setSpy = vi.spyOn(soundEngine, "setConfig");
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded").textContent).toBe("true"));
    act(() => { fireEvent.click(screen.getByTestId("save")); });
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
    expect(setSpy).toHaveBeenLastCalledWith(expect.objectContaining({ fillSound: "marimba" }));
  });
});
