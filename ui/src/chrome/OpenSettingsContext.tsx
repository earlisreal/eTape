// AppShell provides shortcuts to specific sections of the Settings dialog.
import { createContext, useContext } from "react";

export interface OpenSettingsApi {
  openOrderSettings: () => void;
  openVenueSettings: () => void;
}

const Ctx = createContext<OpenSettingsApi | null>(null);

export const OpenSettingsProvider = Ctx.Provider;

export function useOpenSettings(): OpenSettingsApi | null {
  return useContext(Ctx);
}
