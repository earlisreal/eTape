// Sound settings provider — mirrors chrome/exec/useOrderConfig.tsx, plus an effect
// that pushes every config change into the SoundEngine singleton.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { AckMsg } from "../wire/contract";
import { DEFAULT_SOUND_CONFIG, SOUND_CONFIG_KEY, sanitizeSoundConfig, type SoundConfig } from "./SoundConfig";
import { soundEngine } from "./SoundEngine";
import { openConfigSync, type ConfigSync } from "../configSync";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface SoundConfigApi { config: SoundConfig; loaded: boolean; save(next: SoundConfig): void }

const Ctx = createContext<SoundConfigApi | null>(null);

export function SoundConfigProvider({ commands, children }: { commands: Cmd; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState<SoundConfig>(DEFAULT_SOUND_CONFIG);
  const [loaded, setLoaded] = useState(false);
  const syncRef = useRef<ConfigSync | null>(null);

  useEffect(() => {
    let live = true;
    const refresh = () => {
      void commands.sendCommand("GetConfig", { key: SOUND_CONFIG_KEY }).then((ack) => {
        if (!live) return;
        if (ack.status === "accepted") setConfig(sanitizeSoundConfig(ack.value));
        setLoaded(true);
      }).catch(() => { if (live) setLoaded(true); });
    };
    const sync = openConfigSync(SOUND_CONFIG_KEY, refresh);
    syncRef.current = sync;
    refresh();
    return () => {
      live = false;
      sync.close();
      if (syncRef.current === sync) syncRef.current = null;
    };
  }, [commands]);

  // Push config into the imperative engine whenever it changes (incl. the initial load).
  useEffect(() => { soundEngine.setConfig(config); }, [config]);

  const save = useCallback((next: SoundConfig) => {
    setConfig(next);
    void commands.sendCommand("SetConfig", { key: SOUND_CONFIG_KEY, value: next }).then((ack) => {
      if (ack.status === "accepted") syncRef.current?.notify();
    }).catch(() => {});
  }, [commands]);

  return <Ctx.Provider value={{ config, loaded, save }}>{children}</Ctx.Provider>;
}

export function useSoundConfig(): SoundConfigApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useSoundConfig must be used within a SoundConfigProvider");
  return ctx;
}
