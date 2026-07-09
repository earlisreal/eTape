// Shared order-entry config: one provider, one source of truth. The settings
// modal (rendered by the order ticket) edits this config, and the hotkey engine
// (mounted once in AppShell) reads it to resolve hotkey→template bindings. Both
// call sites must see the same state, so this is a context provider rather than
// per-call-site useState — a local copy in each would leave the hotkey engine on
// a stale config after an edit made through the settings modal.
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import type { AckMsg, VenueID } from "../../wire/contract";
import { DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, normalizeOrderConfig, type OrderConfig } from "./actionTemplate";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface OrderConfigApi { config: OrderConfig; loaded: boolean; save(next: OrderConfig): void; setActiveVenue(v: VenueID): void }

const Ctx = createContext<OrderConfigApi | null>(null);

export function OrderConfigProvider({ commands, children }: { commands: Cmd; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState<OrderConfig>(() => normalizeOrderConfig(DEFAULT_ORDER_CONFIG));
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let live = true;
    void commands.sendCommand("GetConfig", { key: ORDER_CONFIG_KEY }).then((ack) => {
      if (!live) return;
      if (ack.status === "accepted" && ack.value && typeof ack.value === "object") setConfig(normalizeOrderConfig(ack.value as OrderConfig));
      setLoaded(true);
    });
    return () => { live = false; };
  }, [commands]);

  const save = useCallback((next: OrderConfig) => {
    setConfig(next);
    void commands.sendCommand("SetConfig", { key: ORDER_CONFIG_KEY, value: next });
  }, [commands]);
  const setActiveVenue = useCallback((v: VenueID) => setConfig((c) => { const next = { ...c, activeVenue: v }; void commands.sendCommand("SetConfig", { key: ORDER_CONFIG_KEY, value: next }); return next; }), [commands]);

  return <Ctx.Provider value={{ config, loaded, save, setActiveVenue }}>{children}</Ctx.Provider>;
}

export function useOrderConfig(): OrderConfigApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useOrderConfig must be used within an OrderConfigProvider");
  return ctx;
}
