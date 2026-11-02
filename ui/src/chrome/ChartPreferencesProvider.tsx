import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { openConfigSync, type ConfigSync } from "../configSync";

interface ChartPreferences { volumeOverlap: boolean }
interface Commands { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }
interface ChartPreferencesApi { config: ChartPreferences; save(next: ChartPreferences): void }
const CONFIG_KEY = "chartPreferences";
const DEFAULTS: ChartPreferences = { volumeOverlap: false };
const Ctx = createContext<ChartPreferencesApi | null>(null);

function normalize(raw: unknown): ChartPreferences {
  return { volumeOverlap: typeof raw === "object" && raw !== null && "volumeOverlap" in raw && raw.volumeOverlap === true };
}

export function ChartPreferencesProvider({ commands, children }: { commands?: Commands; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState(DEFAULTS);
  const syncRef = useRef<ConfigSync | null>(null);
  const revision = useRef(0);
  useEffect(() => {
    if (!commands) return;
    let live = true;
    const refresh = () => {
      const request = ++revision.current;
      void commands.sendCommand("GetConfig", { key: CONFIG_KEY }).then((ack) => {
        if (live && request === revision.current && ack.status === "accepted") setConfig(normalize(ack.value));
      }).catch(() => {});
    };
    const sync = openConfigSync(CONFIG_KEY, refresh);
    syncRef.current = sync;
    refresh();
    return () => { live = false; sync.close(); if (syncRef.current === sync) syncRef.current = null; };
  }, [commands]);
  const save = useCallback((next: ChartPreferences) => {
    ++revision.current; // A pending initial read must not undo the user's choice.
    const normalized = normalize(next);
    setConfig(normalized);
    void commands?.sendCommand("SetConfig", { key: CONFIG_KEY, value: normalized }).then((ack) => {
      if (ack.status === "accepted") syncRef.current?.notify();
    }).catch(() => {});
  }, [commands]);
  return <Ctx.Provider value={{ config, save }}>{children}</Ctx.Provider>;
}

export function useChartPreferences(): ChartPreferencesApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useChartPreferences must be used within a ChartPreferencesProvider");
  return ctx;
}
