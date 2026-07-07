// Task 11: lets the order ticket's gear icon open the unified Settings modal
// (owned by AppShell) to the Orders section, without threading a new prop through
// PanelProps/PanelFrame for every panel type (only the order-ticket panel needs
// this). AppShell provides the value once, wrapping the whole shell; the ticket
// panel is the only consumer. If no provider is present (e.g. a panel rendered
// standalone in a unit test), the hook resolves to null and the gear is a no-op —
// callers should optional-chain the call.
import { createContext, useContext } from "react";

export interface OpenSettingsApi {
  openOrderSettings: () => void;
}

const Ctx = createContext<OpenSettingsApi | null>(null);

export const OpenSettingsProvider = Ctx.Provider;

export function useOpenSettings(): OpenSettingsApi | null {
  return useContext(Ctx);
}
