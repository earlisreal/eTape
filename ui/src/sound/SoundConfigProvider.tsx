// Sound settings provider — mirrors chrome/exec/useOrderConfig.tsx, plus an effect
// that pushes every config change into the SoundEngine singleton.
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import type { AckMsg } from "../wire/contract";
import { DEFAULT_SOUND_CONFIG, SOUND_CONFIG_KEY, sanitizeSoundConfig, type SoundConfig } from "./SoundConfig";
import { soundEngine } from "./SoundEngine";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface SoundConfigApi { config: SoundConfig; loaded: boolean; save(next: SoundConfig): void }

const Ctx = createContext<SoundConfigApi | null>(null);

export function SoundConfigProvider({ commands, children }: { commands: Cmd; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState<SoundConfig>(DEFAULT_SOUND_CONFIG);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let live = true;
    void commands.sendCommand("GetConfig", { key: SOUND_CONFIG_KEY }).then((ack) => {
      if (!live) return;
      if (ack.status === "accepted") setConfig(sanitizeSoundConfig(ack.value));
      setLoaded(true);
    });
    return () => { live = false; };
  }, [commands]);

  // Push config into the imperative engine whenever it changes (incl. the initial load).
  useEffect(() => { soundEngine.setConfig(config); }, [config]);

  const save = useCallback((next: SoundConfig) => {
    setConfig(next);
    void commands.sendCommand("SetConfig", { key: SOUND_CONFIG_KEY, value: next });
  }, [commands]);

  return <Ctx.Provider value={{ config, loaded, save }}>{children}</Ctx.Provider>;
}

export function useSoundConfig(): SoundConfigApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useSoundConfig must be used within a SoundConfigProvider");
  return ctx;
}
